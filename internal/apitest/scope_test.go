package apitest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/scope"
)

var Tk = testmodel.Tickets

const ticketCols = `"id", "created_by", "org_id", "owner", "title", "done", "invoice_id", "parent_id"`

// scoped returns a context in org id, acting as "admin", whom Ticket's own rules
// let through: the scope tests check the scope alone.
func scoped(id int64) context.Context {
	return testmodel.WithUser(testmodel.Org.With(context.Background(), id), "admin")
}

func ticketRow(id, org int64, parent any) []any {
	return []any{id, "ann", org, "ann", fmt.Sprintf("T-%d", id), false, int64(1), parent}
}

func wantSent(t *testing.T, e *fakeconn.Exec, sql string, args ...any) {
	t.Helper()
	if e.SQL != sql {
		t.Errorf("SQL\n got %s\nwant %s", e.SQL, sql)
	}
	if !reflect.DeepEqual(e.Args, args) {
		t.Errorf("args %#v, want %#v", e.Args, args)
	}
}

func TestScopeIsAdded(t *testing.T) {
	bg := context.Background()
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openPolicied(velox.Postgres, e)
	if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5).All(testmodel.Org.With(bg, int64(7))); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+allCols+` FROM "invoices" WHERE "paid" AND "org_id" = $1 LIMIT 5`, int64(7))
}

func TestScopeOnEveryOperation(t *testing.T) {
	t7 := testmodel.Org.With(context.Background(), int64(7))
	e := &fakeconn.Exec{Rows: [][]any{{int64(3)}}, Affected: 1}
	db := openPolicied(velox.Postgres, e)

	if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Count(t7); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT COUNT(*) FROM "invoices" WHERE "paid" AND "org_id" = $1`, int64(7))

	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(t7); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.SQL, ` WHERE "id" = $3 AND "org_id" = $4`) || e.Args[3] != int64(7) {
		t.Errorf("update: %s %v", e.SQL, e.Args)
	}

	if _, err := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).All().Exec(t7); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.SQL, ` WHERE "org_id" = $3`) || e.Args[2] != int64(7) {
		t.Errorf("update of all rows: %s %v", e.SQL, e.Args)
	}

	if _, err := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(t7); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `DELETE FROM "invoices" WHERE "id" = $1 AND "org_id" = $2`, int64(1), int64(7))

	if _, err := db.Delete[testmodel.Invoice]().All().Exec(t7); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `DELETE FROM "invoices" WHERE "org_id" = $1`, int64(7))

	inv := testmodel.Invoice{ID: 1, OrgID: 7, Number: "INV-1"}
	if err := db.Save(&inv, I.Number).Exec(t7); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.SQL, ` WHERE "id" = $3 AND "org_id" = $4`) || e.Args[3] != int64(7) {
		t.Errorf("save: %s %v", e.SQL, e.Args)
	}
}

// TestScopeOnBothPaths: the scope applies with hooks (run) as without
// (plain), after the Rewrite hooks, so they see the statement unscoped.
func TestScopeOnBothPaths(t *testing.T) {
	var where int
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openPolicied(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		where = len(st.Where)
		return nil
	}))
	if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5).All(scoped(7)); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+allCols+` FROM "invoices" WHERE "paid" AND "org_id" = $1 LIMIT 5`, int64(7))
	if where != 1 {
		t.Errorf("the Rewrite hook saw %d conditions, want the caller's 1", where)
	}
}

// TestNoScopeValueFailsEveryOperation: with neither a scope value nor WithoutPolicy,
// every operation on a scoped entity fails, on both paths, and sends nothing.
func TestNoScopeValueFailsEveryOperation(t *testing.T) {
	bg := context.Background()
	around := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		return next(ctx)
	})
	for _, opts := range [][]velox.Option{nil, {around}} {
		e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}, Affected: 1}
		db := openPolicied(velox.Postgres, e, opts...)
		inv := testmodel.Invoice{ID: 1, OrgID: 7}
		var iterErr error
		for _, err := range db.Query[testmodel.Invoice]().Limit(5).Iter(bg) {
			iterErr = err
		}
		_, allErr := db.Query[testmodel.Invoice]().Limit(5).All(bg)
		_, oneErr := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(bg)
		_, countErr := db.Query[testmodel.Invoice]().Count(bg)
		_, updErr := db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(bg)
		_, delErr := db.Delete[testmodel.Invoice]().Where(I.ID.EQ(1)).Exec(bg)
		saveErr := db.Save(&inv, I.Number).Exec(bg)
		for name, err := range map[string]error{"All": allErr, "One": oneErr, "Iter": iterErr, "Count": countErr,
			"Update": updErr, "Delete": delErr, "Save": saveErr} {
			if !errors.Is(err, scope.ErrNoScope) || !strings.HasSuffix(err.Error(), "velox/scope: the context has no scope value: org") {
				t.Errorf("%s (hooks %d): %v", name, len(opts), err)
			}
		}
		if e.Calls != 0 {
			t.Errorf("hooks %d: %d statements sent", len(opts), e.Calls)
		}
	}
}

func TestWithoutPolicyAddsNothing(t *testing.T) {
	var seen []velox.Statement
	var mu sync.Mutex
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openPolicied(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		mu.Lock()
		seen = append(seen, *st)
		mu.Unlock()
		return next(ctx)
	}))
	if _, err := db.WithoutPolicy("reason").Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5).All(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+allCols+` FROM "invoices" WHERE "paid" LIMIT 5`)
	if _, err := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).Limit(5).All(scoped(7)); err != nil {
		t.Fatal(err)
	}
	e.Rows = nil
	if _, err := db.Query[testmodel.Item]().Limit(5).All(scoped(7)); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("the Around hook saw %d statements", len(seen))
	}
	if seen[0].PolicyBypass != "reason" {
		t.Errorf("WithoutPolicy: PolicyBypass %q", seen[0].PolicyBypass)
	}
	if seen[1].PolicyBypass != "" || seen[2].PolicyBypass != "" {
		t.Errorf("PolicyBypass %q and %q, want none", seen[1].PolicyBypass, seen[2].PolicyBypass)
	}
}

// TestScopeColumnAssignments: the scope column is Immutable in the schema, so no
// update assigns it, to the context's value or to another, with the scope or
// without it.
func TestScopeColumnAssignments(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openPolicied(velox.Postgres, e)
	t7 := scoped(7)
	lifted := db.WithoutPolicy("move an invoice")
	for _, c := range []struct {
		db  *velox.DB
		ctx context.Context
	}{{db, t7}, {lifted, context.Background()}} {
		db, ctx := c.db, c.ctx
		upd := func(s velox.Setter[testmodel.Invoice]) velox.UpdateQuery[testmodel.Invoice] {
			return db.Update[testmodel.Invoice]().Set(s).Where(I.ID.EQ(1))
		}
		for name, s := range map[string]velox.Setter[testmodel.Invoice]{"Set(7)": I.OrgID.Set(7), "Set(8)": I.OrgID.Set(8), "Add(1)": I.OrgID.Add(1), "Sub(1)": I.OrgID.Sub(1)} {
			if _, err := upd(s).Exec(ctx); !errors.Is(err, velox.ErrImmutable) {
				t.Errorf("%s: %v", name, err)
			}
		}
		inv := testmodel.Invoice{ID: 1, OrgID: 8}
		if err := db.Save(&inv, I.OrgID).Exec(ctx); !errors.Is(err, velox.ErrImmutable) {
			t.Errorf("Save of the column: %v", err)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d refused statements sent", e.Calls)
	}
}

func TestHasOnAScopedTarget(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e)
	if _, err := db.Query[testmodel.Ticket]().Where(Tk.Invoice.Has()).Limit(1).All(scoped(7)); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE EXISTS (SELECT 1 FROM "invoices" AS "vx1" WHERE "vx1"."id" = "tickets"."invoice_id" AND "org_id" = $1) AND "org_id" = $2 LIMIT 1`,
		int64(7), int64(7))
}

// TestHasOnAScopedTargetFromAnUnscopedEntity: the target's scope applies
// though the statement's own entity has none.
func TestHasOnAScopedTargetFromAnUnscopedEntity(t *testing.T) {
	var seen velox.Statement
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		seen = *st
		return next(ctx)
	}))
	q := db.Query[testmodel.Item]().Where(It.Qty.GT(0), velox.Not(It.Invoice.HasWith(I.Paid.EQ(true)))).Limit(1)
	if _, err := q.All(scoped(7)); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT "id", "invoice_id", "sku", "qty" FROM "items" WHERE "qty" > $1 AND NOT (EXISTS (SELECT 1 FROM "invoices" AS "vx1" WHERE "vx1"."id" = "items"."invoice_id" AND "paid" AND "org_id" = $2)) LIMIT 1`,
		int64(0), int64(7))

	_, err := q.All(context.Background())
	wantNoScope(t, err, "velox: testmodel.Item: in the EXISTS on testmodel.Invoice: ")
	if _, err := db.WithoutPolicy("reason").Query[testmodel.Item]().Where(It.Qty.GT(0), velox.Not(It.Invoice.HasWith(I.Paid.EQ(true)))).Limit(1).All(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SQL, "org_id") || seen.PolicyBypass != "reason" {
		t.Errorf("WithoutPolicy: %s, PolicyBypass %q", e.SQL, seen.PolicyBypass)
	}

	// An unscoped entity whose conditions reach no scoped target needs no
	// org.
	if _, err := db.Query[testmodel.Item]().Where(It.Qty.GT(0)).Limit(1).All(context.Background()); err != nil {
		t.Errorf("unscoped: %v", err)
	}
}

// TestNestedHasIsScopedAtEveryLevel: a HasWith inside a HasWith scopes
// each target.
func TestNestedHasIsScopedAtEveryLevel(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e)
	q := db.Query[testmodel.Ticket]().Where(Tk.Parent.HasWith(Tk.Invoice.Has())).Limit(1)
	if _, err := q.All(scoped(7)); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE EXISTS (SELECT 1 FROM "tickets" AS "vx1" WHERE "vx1"."id" = "tickets"."parent_id" AND EXISTS (SELECT 1 FROM "invoices" AS "vx2" WHERE "vx2"."id" = "vx1"."invoice_id" AND "org_id" = $1) AND "org_id" = $2) AND "org_id" = $3 LIMIT 1`,
		int64(7), int64(7), int64(7))
}

// TestScopeNeverChangesASharedQuery is Review Focus 1: the scope copies the
// conditions it changes, so a query and its predicates can be run under many
// orgs at once.
func TestScopeNeverChangesASharedQuery(t *testing.T) {
	has := Tk.Invoice.HasWith(I.Paid.EQ(true))
	cond := velox.Or(Tk.Done.EQ(true), velox.And(Tk.Title.NEQ(""), has))

	e0 := &fakeconn.Exec{}
	db0 := openPolicied(velox.Postgres, e0)
	base := db0.Query[testmodel.Ticket]().Where(cond, has).Limit(1)
	sql0, args0 := sqlOK(t)(base.SQL(scoped(1)))
	run0 := func() (string, []any) {
		t.Helper()
		e0.Log = nil
		if _, err := base.All(scoped(1)); err != nil {
			t.Fatal(err)
		}
		return e0.Log[0], e0.Args
	}
	before, beforeArgs := run0()

	// dbH has an Around hook, so its statements take run's path, where
	// prepare clones Where; db0 stays plain to keep the plain path covered.
	// The hook checks, for each statement, that it holds the statement's
	// org three times and no other goroutine's.
	var hookBad atomic.Pointer[string]
	eH := &fakeconn.Exec{}
	dbH := openPolicied(velox.Postgres, eH, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		org, _ := testmodel.Org.From(ctx)
		n := 0
		for _, a := range st.Args {
			if a == org {
				n++
			}
		}
		if n != 3 || slices.ContainsFunc(st.Args, func(a any) bool { v, ok := a.(int64); return ok && a != org && v >= 100 }) {
			s := fmt.Sprintf("org %v: args %v", org, st.Args)
			hookBad.CompareAndSwap(nil, &s)
		}
		return next(ctx)
	}))
	baseH := dbH.Query[testmodel.Ticket]().Where(cond, has).Limit(1)
	sqlH0, argsH0 := sqlOK(t)(baseH.SQL(scoped(1)))

	var wg sync.WaitGroup
	for i := range 50 {
		id := int64(i + 100)
		wg.Go(func() {
			e := &fakeconn.Exec{}
			db := openPolicied(velox.Postgres, e)
			if _, err := db.Query[testmodel.Ticket]().Where(cond, has).Limit(1).All(scoped(id)); err != nil {
				t.Error(err)
				return
			}
			n := 0
			for _, a := range e.Args {
				if a == id {
					n++
				}
			}
			// The ticket's own org, and the invoice's in each EXISTS.
			if n != 3 || slices.ContainsFunc(e.Args, func(a any) bool { v, ok := a.(int64); return ok && v != id && v >= 100 }) {
				t.Errorf("org %d: args %v", id, e.Args)
			}
		})
		wg.Go(func() {
			// The shared query itself, on the plain path: its Where must be
			// copied before an EXISTS in it is replaced.
			if _, err := base.All(scoped(id)); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			// The same on the hooked path, run's.
			if _, err := baseH.All(scoped(id)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if s := hookBad.Load(); s != nil {
		t.Errorf("hooked path, %s", *s)
	}
	if sql, args := sqlOK(t)(baseH.SQL(scoped(1))); sql != sqlH0 || !reflect.DeepEqual(args, argsH0) {
		t.Errorf("the hooked base query's SQL changed:\n%s %v\n%s %v", sqlH0, argsH0, sql, args)
	}

	if sql, args := sqlOK(t)(base.SQL(scoped(1))); sql != sql0 || !reflect.DeepEqual(args, args0) {
		t.Errorf("the base query's SQL changed:\n%s %v\n%s %v", sql0, args0, sql, args)
	}
	if after, afterArgs := run0(); after != before || !reflect.DeepEqual(afterArgs, beforeArgs) {
		t.Errorf("the base query sends another statement:\n%s %v\n%s %v", before, beforeArgs, after, afterArgs)
	}
}

func TestWithLoadIsScoped(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{ticketRow(2, 7, int64(1))},
		{ticketRow(1, 7, nil)},
	}}
	db := openPolicied(velox.Postgres, e)
	got, err := db.Query[testmodel.Ticket]().With(Tk.Parent).Limit(5).All(scoped(7))
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := got[0].Edges.Parent.Get(); !ok || p == nil || p.ID != 1 {
		t.Fatalf("parent %v, %v", p, ok)
	}
	if len(e.Log) != 2 {
		t.Fatalf("%d statements: %v", len(e.Log), e.Log)
	}
	if !strings.Contains(e.Log[1], `WHERE "id" = ANY($1) AND "org_id" = $2 `) || e.Args[1] != int64(7) {
		t.Errorf("the parent load is unscoped: %s %v", e.Log[1], e.Args)
	}

	// An unscoped read loading a scoped relation needs a org for the load.
	e = &fakeconn.Exec{Results: [][][]any{{{int64(1), int64(1), "SKU", int64(2)}}, {invoiceRow(1)}}}
	db = openPolicied(velox.Postgres, e)
	_, err = db.Query[testmodel.Item]().With(It.Invoice).Limit(5).All(context.Background())
	wantNoScope(t, err, "velox: testmodel.Item: loading invoice: testmodel.Invoice: ")
	if len(e.Log) != 1 {
		t.Errorf("%d statements sent, want the item read alone", len(e.Log))
	}
}

// TestScopeNeverAppendsIntoAHooksArray: a Rewrite hook may point st.Where at
// an array it does not own, with room past its length; the scope's org
// condition goes into a copy, never into that array.
func TestScopeNeverAppendsIntoAHooksArray(t *testing.T) {
	shared := make([]velox.Predicate, 0, 4)
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = shared[:0]
		return nil
	}))
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(scoped(7)); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+allCols+` FROM "invoices" WHERE "org_id" = $1 LIMIT 1`, int64(7))
	if !reflect.ValueOf(shared[:1][0]).IsZero() {
		t.Errorf("the scope appended into the hook's array: %v", shared[:1])
	}
}

// TestScopeWithSharedDefaults is C1 of policy 2's final review: a Rewrite
// hook that sets every statement's Where to one shared slice of defaults,
// with spare capacity, must not let concurrent statements overwrite each
// other's org condition. Run it with -race.
func TestScopeWithSharedDefaults(t *testing.T) {
	defaults := make([]velox.Predicate, 1, 8)
	defaults[0] = velox.Predicate(I.Paid.EQ(true))
	setDefaults := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Where = defaults
		return nil
	})
	var wg sync.WaitGroup
	for g := range 8 {
		id := int64(100 + g)
		wg.Go(func() {
			var wrong int
			var last []any
			db := openPolicied(velox.Postgres, &fakeconn.Exec{}, setDefaults,
				velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
					if len(st.Args) != 1 || st.Args[0] != id {
						wrong, last = wrong+1, st.Args
					}
					return next(ctx)
				}))
			ctx := scoped(id)
			for range 200 {
				if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err != nil {
					t.Error(err)
					return
				}
			}
			if wrong > 0 {
				t.Errorf("org %d: %d of 200 statements carried another org's condition, last with args %v", id, wrong, last)
			}
		})
	}
	wg.Wait()
	if len(defaults) != 1 || !reflect.ValueOf(defaults[:2][1]).IsZero() {
		t.Errorf("the defaults changed: %v", defaults[:2])
	}
}

// TestZeroScopeValueIsRefused: a scope value that is the type's zero value, which
// middleware gets by parsing a missing claim, scopes nothing: every
// statement fails closed, inserts included.
func TestZeroScopeValueIsRefused(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}, Affected: 1}
	db := openPolicied(velox.Postgres, e)
	zero := scoped(0)
	_, err := db.Query[testmodel.Invoice]().Limit(5).All(zero)
	wantNoScope(t, err, "velox: testmodel.Invoice: select denied by policy: ")
	_, err = db.Update[testmodel.Invoice]().Set(I.Paid.Set(true)).Where(I.ID.EQ(1)).Exec(zero)
	wantNoScope(t, err, "velox: testmodel.Invoice: update denied by policy: ")
	_, err = db.Query[testmodel.Item]().Where(It.Invoice.Has()).Limit(5).All(zero)
	wantNoScope(t, err, "velox: testmodel.Item: in the EXISTS on testmodel.Invoice: ")
	inv := testmodel.Invoice{Number: "A"}
	err = db.Insert(&inv).Exec(zero)
	wantNoScope(t, err, "velox: testmodel.Invoice: insert denied by policy: ")
	if inv.OrgID != 0 {
		t.Errorf("OrgID %d", inv.OrgID)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestRowScopeValues: RowScope shows the values a context's scope keys carry,
// so that callers in different scopes are told apart.
func TestRowScopeValues(t *testing.T) {
	bg := context.Background()
	seven, eight := velox.RowScopeFromContext(testmodel.Org.With(bg, 7)), velox.RowScopeFromContext(testmodel.Org.With(bg, 8))
	if seven.Values == "" || seven == eight || seven != velox.RowScopeFromContext(testmodel.Org.With(bg, 7)) {
		t.Errorf("org 7 %+v, org 8 %+v", seven, eight)
	}
	if got := velox.RowScopeFromContext(bg); got != (velox.RowScope{}) {
		t.Errorf("no scope: %+v", got)
	}
}
