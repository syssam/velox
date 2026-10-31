package engine_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/engine"
	"github.com/syssam/velox2/internal/fakeconn"
	"github.com/syssam/velox2/internal/testmodel"
)

// scopeGen builds Ticket conditions from fuzz input: each byte read picks the
// next node, and input that runs out reads as zeros.
type scopeGen struct {
	b []byte
	s string
}

func (g *scopeGen) next() byte {
	if len(g.b) == 0 {
		return 0
	}
	c := g.b[0]
	g.b = g.b[1:]
	return c
}

// pred returns a condition at depth: EQ, In, Has(Parent) or Has(Invoice),
// or, at a depth under 3, And, Or, Not or HasWhere(Parent, …) of deeper ones.
func (g *scopeGen) pred(depth int) velox.Pred[testmodel.Ticket] {
	c := g.next()
	k := c % 8
	if depth >= 3 {
		k %= 4 // leaves only
	}
	switch k {
	case 0:
		return Tk.Title.EQ(g.s)
	case 1:
		return Tk.ID.In(int64(c), 2)
	case 2:
		return Tk.Parent.Has()
	case 3:
		return Tk.Invoice.Has()
	case 4:
		return velox.And(g.preds(depth + 1)...)
	case 5:
		return velox.Or(g.preds(depth + 1)...)
	case 6:
		return velox.Not(g.pred(depth + 1))
	default:
		return Tk.Parent.HasWhere(g.preds(depth + 1)...)
	}
}

// preds returns zero to three conditions at depth.
func (g *scopeGen) preds(depth int) []velox.Pred[testmodel.Ticket] {
	ps := make([]velox.Pred[testmodel.Ticket], g.next()%4)
	for i := range ps {
		ps[i] = g.pred(depth)
	}
	return ps
}

// scopeCase is a Ticket statement built from fuzz input, and the Rewrite
// hooks that edit it before the scope.
func scopeCase(ops []byte, s string) (*velox.Statement, []velox.Option) {
	g := &scopeGen{b: ops, s: s}
	head := g.next()
	st := &velox.Statement{Entity: infoOf[testmodel.Ticket](), Op: velox.OpSelect, Limit: 10}
	switch head % 4 {
	case 1:
		st.Count, st.Limit = true, 0
	case 2:
		st.Op, st.Limit, st.AllRows = velox.OpUpdate, 0, true
		st.Set = []velox.Assignment{{Column: int(Tk.Title.I), Op: velox.AssignSet, Value: "t" + s}}
	case 3:
		st.Op, st.Limit, st.AllRows = velox.OpDelete, 0, true
	}
	for _, p := range g.preds(0) {
		st.Where = append(st.Where, velox.Predicate(p))
	}
	st.Where = slices.Clip(st.Where) // as a query gives it
	var opts []velox.Option
	hooks := head >> 2
	if hooks&1 != 0 { // append a condition
		opts = append(opts, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			st.Where = append(st.Where, velox.ColumnEQ(int(Tk.Title.I), "rw"))
			return nil
		}))
	}
	if hooks&2 != 0 { // change a condition in place
		opts = append(opts, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if len(st.Where) > 0 {
				st.Where[0] = velox.Predicate(velox.Not(velox.Pred[testmodel.Ticket](st.Where[0])))
			}
			return nil
		}))
	}
	if hooks&4 != 0 { // add an EXISTS of the hook's own
		opts = append(opts, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			st.Where = append(st.Where, velox.Predicate(Tk.Parent.HasWhere(Tk.Title.EQ("p"), Tk.Parent.Has())))
			return nil
		}))
	}
	if hooks&8 != 0 { // fold every condition into one And
		opts = append(opts, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			ps := make([]velox.Pred[testmodel.Ticket], len(st.Where))
			for i, p := range st.Where {
				ps[i] = velox.Pred[testmodel.Ticket](p)
			}
			st.Where = []velox.Predicate{velox.Predicate(velox.And(ps...))}
			return nil
		}))
	}
	return st, opts
}

// FuzzScope (Review Focus 1): whatever the conditions and the Rewrite hooks,
// a Ticket statement as ann under tenant n gets the tenant condition and the
// owner condition as conditions of its own, every EXISTS on Ticket gets both
// among its conditions, at every depth, and every EXISTS on Invoice its
// tenant condition; and the caller's conditions are left as they were.
func FuzzScope(f *testing.F) {
	f.Add(int64(7), []byte{}, "")
	f.Add(int64(7), []byte{0, 3, 2, 3, 0}, "a")                  // select: Has(Parent), Has(Invoice), Title
	f.Add(int64(-1), []byte{1, 1, 7, 2, 7, 1, 2, 3}, "b")        // count: HasWhere(Parent, HasWhere(Parent, Has(Parent)), Has(Invoice))
	f.Add(int64(3), []byte{2, 2, 4, 2, 6, 7, 1, 2, 5, 1, 3}, "") // update: And(Not(HasWhere(Parent, Has(Parent))), Or(Has(Invoice))), Title
	f.Add(int64(0), []byte{2, 2, 4, 2, 6, 7, 1, 2, 5, 1, 3}, "") // the same under the zero tenant, which is refused
	f.Add(int64(9), []byte{3 | 15<<2, 2, 7, 1, 7, 1, 2, 1}, "c") // delete with every hook: three EXISTS deep
	f.Fuzz(func(t *testing.T, n int64, ops []byte, s string) {
		if len(ops) > 64 {
			ops = ops[:64]
		}
		st, opts := scopeCase(ops, s)
		want, _ := scopeCase(ops, s) // the same conditions, built again: a copy no run touches
		in := st.Where
		db := openFake(velox.Postgres, &fakeconn.Exec{}, opts...)
		ctx := testmodel.WithUser(velox.WithTenant(context.Background(), n), "ann")
		err := prepare(ctx, db, st)
		if n == 0 {
			// The zero tenant scopes nothing: the statement fails closed.
			if !errors.Is(err, velox.ErrNoTenant) {
				t.Fatalf("tenant 0: %v", err)
			}
			return
		}
		if err != nil {
			t.Fatalf("%v", err)
		}

		ticket, invoice := st.Entity, infoOf[testmodel.Invoice]()
		owner := velox.Predicate(Tk.Owner.EQ("ann"))
		ticketTenant := velox.ColumnEQ(int(Tk.OrgID.I), n)
		invoiceTenant := velox.ColumnEQ(int(testmodel.Invoices.OrgID.I), n)
		has := func(ps []velox.Predicate, p velox.Predicate) bool {
			return slices.ContainsFunc(ps, func(q velox.Predicate) bool { return reflect.DeepEqual(q, p) })
		}
		if !has(st.Where, ticketTenant) || !has(st.Where, owner) {
			t.Fatalf("the statement's own conditions lack the tenant or the owner condition: %#v", st.Where)
		}
		var walk func(p velox.Predicate, depth int)
		walk = func(p velox.Predicate, depth int) {
			kids := kids(p)
			switch existsOn(p) {
			case ticket:
				if !has(kids, ticketTenant) || !has(kids, owner) {
					t.Fatalf("an EXISTS on Ticket at depth %d lacks the tenant or the owner condition: %#v", depth, kids)
				}
			case invoice:
				if !has(kids, invoiceTenant) {
					t.Fatalf("an EXISTS on Invoice at depth %d lacks the tenant condition: %#v", depth, kids)
				}
			}
			for _, k := range kids {
				walk(k, depth+1)
			}
		}
		for _, p := range st.Where {
			walk(p, 0)
		}

		if !samePreds(in, want.Where) {
			t.Fatalf("the run changed the caller's conditions:\n got %#v\nwant %#v", in, want.Where)
		}
	})
}

// prepare runs db's stages before execution on st, as run does: guard,
// Rewrite hooks, scope, row cap and rendering. It goes through DryRun, which
// also routes st and checks its parameters.
func prepare(ctx context.Context, db *velox.DB, st *velox.Statement) error {
	_, _, err := engineOf(db).DryRun(ctx, st)
	return err
}

// kids returns p's sub-conditions: the operands of an And, Or or Not, or the
// conditions an EXISTS puts on its target.
func kids(p velox.Predicate) []velox.Predicate {
	out := make([]velox.Predicate, len(engine.PredNode(p).Kids))
	for i, k := range engine.PredNode(p).Kids {
		out[i] = engine.NewPredicate(k)
	}
	return out
}

// existsOn returns the target of an EXISTS; nil for any other condition.
func existsOn(p velox.Predicate) *velox.EntityInfo {
	if engine.PredNode(p).Op != engine.OpExists {
		return nil
	}
	l, ok := engine.PredNode(p).V.(engine.Link)
	if !ok {
		return nil
	}
	return l.Target
}

// samePreds reports whether a and b are the same conditions. A relation not
// yet resolved is a RelRef, which holds a function: reflect.DeepEqual never
// finds two equal, so relRefs are compared by their relation index.
func samePreds(a, b []velox.Predicate) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameNode(engine.PredNode(a[i]), engine.PredNode(b[i])) {
			return false
		}
	}
	return true
}

func sameNode(a, b engine.Node) bool {
	if a.Op != b.Op || a.Col != b.Col || len(a.Kids) != len(b.Kids) || !reflect.DeepEqual(a.W, b.W) {
		return false
	}
	ra, aok := a.V.(engine.RelRef)
	rb, bok := b.V.(engine.RelRef)
	switch {
	case aok != bok || aok && ra.I != rb.I:
		return false
	case !aok && !reflect.DeepEqual(a.V, b.V):
		return false
	}
	for i := range a.Kids {
		if !sameNode(a.Kids[i], b.Kids[i]) {
			return false
		}
	}
	return true
}
