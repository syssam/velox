package velox_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// asUser returns a context scoped to tenant 1, acting as user ("" for none).
func asUser(user string) context.Context {
	ctx := velox.WithTenant(context.Background(), int64(1))
	if user == "" {
		return ctx
	}
	return testmodel.AsUser(ctx, user)
}

// The policy fixtures: a pnote has a read and a write policy that count
// their calls, and a pbox refers to a pnote and has no policy.
type pnote struct {
	ID        int64
	Owner     string
	Locked    bool
	ParentID  *int64
	InvoiceID int64
	Parent    velox.One[pnote]
	Invoice   velox.One[testmodel.Invoice]
}

var pnoteReads, pnoteWrites atomic.Int64

// pnoteKey's value, a pnoteAnswer, is what pnote's read policy returns.
type pnoteKey struct{}

type pnoteAnswer struct {
	p   velox.Pred[pnote]
	err error
}

func pnoteReading(p velox.Pred[pnote], err error) context.Context {
	return context.WithValue(context.Background(), pnoteKey{}, pnoteAnswer{p, err})
}

func (x *pnote) Schema(s *velox.Schema) {
	s.Table("pnotes")
	s.Policy(velox.Policy[pnote]{
		Read: func(ctx context.Context) (velox.Pred[pnote], error) {
			pnoteReads.Add(1)
			if a, ok := ctx.Value(pnoteKey{}).(pnoteAnswer); ok {
				return a.p, a.err
			}
			return pn.Owner.EQ("ann"), nil
		},
		Write: func(context.Context) (velox.Pred[pnote], error) {
			pnoteWrites.Add(1)
			return pn.Locked.EQ(false), nil
		},
	})
}

var pnoteMeta velox.MetaOnce[pnote]

func (*pnote) VeloxMeta() *velox.Meta[pnote] {
	return pnoteMeta.Get(func() velox.Shape[pnote] {
		return velox.Shape[pnote]{Columns: []string{"ID", "Owner", "Locked", "ParentID", "InvoiceID"},
			Relations: []velox.Relation{velox.RelationTo[pnote]("Parent"), velox.RelationTo[testmodel.Invoice]("Invoice")},
			Addr: func(x *pnote, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.Owner
				case 2:
					return &x.Locked
				case 3:
					return &x.ParentID
				case 4:
					return &x.InvoiceID
				}
				return nil
			}}
	})
}

var pn = struct {
	ID        velox.NumberCol[pnote, int64]
	Owner     velox.StringCol[pnote, string]
	Locked    velox.Col[pnote, bool]
	InvoiceID velox.NumberCol[pnote, int64]
	Parent    velox.Rel[pnote, pnote]
	Invoice   velox.Rel[pnote, testmodel.Invoice]
}{
	ID:        velox.NumberCol[pnote, int64]{I: 0},
	Owner:     velox.StringCol[pnote, string]{I: 1},
	Locked:    velox.Col[pnote, bool]{I: 2},
	InvoiceID: velox.NumberCol[pnote, int64]{I: 4},
	Parent:    velox.Rel[pnote, pnote]{I: 0},
	Invoice:   velox.Rel[pnote, testmodel.Invoice]{I: 1},
}

type pbox struct {
	ID     int64
	NoteID int64
	Note   velox.One[pnote]
}

func (x *pbox) Schema(s *velox.Schema) { s.Table("pboxes") }

var pboxMeta velox.MetaOnce[pbox]

func (*pbox) VeloxMeta() *velox.Meta[pbox] {
	return pboxMeta.Get(func() velox.Shape[pbox] {
		return velox.Shape[pbox]{Columns: []string{"ID", "NoteID"}, Relations: []velox.Relation{velox.RelationTo[pnote]("Note")},
			Addr: func(x *pbox, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.NoteID
				}
				return nil
			}}
	})
}

var pboxNote = velox.Rel[pbox, pnote]{I: 0}

// A pcomment inherits its note's Read policy: its own Read is a Has.
type pcomment struct {
	ID     int64
	NoteID int64
	Note   velox.One[pnote]
}

func (x *pcomment) Schema(s *velox.Schema) {
	s.Table("pcomments")
	s.Policy(velox.Policy[pcomment]{
		Read: func(context.Context) (velox.Pred[pcomment], error) { return pcommentNote.Has(), nil },
	})
}

var pcommentMeta velox.MetaOnce[pcomment]

func (*pcomment) VeloxMeta() *velox.Meta[pcomment] {
	return pcommentMeta.Get(func() velox.Shape[pcomment] {
		return velox.Shape[pcomment]{Columns: []string{"ID", "NoteID"}, Relations: []velox.Relation{velox.RelationTo[pnote]("Note")},
			Addr: func(x *pcomment, i int) any {
				switch i {
				case 0:
					return &x.ID
				case 1:
					return &x.NoteID
				}
				return nil
			}}
	})
}

var pcommentNote = velox.Rel[pcomment, pnote]{I: 0}

func TestPolicyReadPredicate(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	q := db.Query[testmodel.Ticket]().Where(Tk.Done.EQ(false)).Limit(5)

	if _, err := q.All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE "done" = $1 AND "org_id" = $2 AND "owner" = $3 LIMIT $4`, false, int64(1), "ann", 5)

	// The zero Pred adds nothing.
	if _, err := q.All(asUser("admin")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE "done" = $1 AND "org_id" = $2 LIMIT $3`, false, int64(1), 5)

	calls := e.Calls
	_, err := q.All(asUser(""))
	var pe *velox.PolicyError
	if !errors.As(err, &pe) || pe.Op != velox.OpSelect || pe.Entity != "testmodel.Ticket" {
		t.Fatalf("got %#v", err)
	}
	want(t, err, velox.ErrDenied, "velox: testmodel.Ticket: select denied by policy: no user")
	if e.Calls != calls {
		t.Errorf("%d denied statements sent", e.Calls-calls)
	}
}

func TestPolicyOnUpdateAndDelete(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	ann := asUser("ann")

	if _, err := db.Update[testmodel.Ticket]().Set(Tk.Title.Set("x")).Where(Tk.ID.EQ(1)).Exec(ann); err != nil {
		t.Fatal(err)
	}
	// Read's predicate, then Write's: Ticket's are the same.
	if !strings.HasSuffix(e.SQL, ` WHERE "id" = $2 AND "org_id" = $3 AND "owner" = $4 AND "owner" = $5`) || e.Args[3] != "ann" || e.Args[4] != "ann" {
		t.Errorf("update: %s %v", e.SQL, e.Args)
	}

	if _, err := db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(1)).Exec(ann); err != nil {
		t.Fatal(err)
	}
	// Ticket's BeforeDelete hook may add a condition on done.
	if !strings.HasPrefix(e.SQL, `DELETE FROM "tickets" WHERE "id" = $1 AND `) || strings.Count(e.SQL, `"owner" = $`) != 2 || !strings.Contains(e.SQL, `"org_id" = $`) {
		t.Errorf("delete: %s %v", e.SQL, e.Args)
	}

	calls := e.Calls
	_, err := db.Update[testmodel.Ticket]().Set(Tk.Title.Set("x")).Where(Tk.ID.EQ(1)).Exec(asUser(""))
	want(t, err, velox.ErrDenied, "velox: testmodel.Ticket: update denied by policy: no user")
	_, err = db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(1)).Exec(asUser(""))
	var pe *velox.PolicyError
	if !errors.As(err, &pe) || pe.Op != velox.OpDelete {
		t.Errorf("delete: %v", err)
	}
	if e.Calls != calls {
		t.Errorf("%d denied statements sent", e.Calls-calls)
	}
}

func TestPolicyOnCount(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(3)}}}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Ticket]().Count(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT COUNT(*) FROM "tickets" WHERE "org_id" = $1 AND "owner" = $2`, int64(1), "ann")
}

// TestPolicyOnBothPaths: the policy applies with hooks (run) as without
// (plain), after the Rewrite hooks, which see the statement without it.
func TestPolicyOnBothPaths(t *testing.T) {
	var where int
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		where = len(st.Where)
		return nil
	}))
	if _, err := db.Query[testmodel.Ticket]().Where(Tk.Done.EQ(false)).Limit(5).All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE "done" = $1 AND "org_id" = $2 AND "owner" = $3 LIMIT $4`, false, int64(1), "ann", 5)
	if where != 1 {
		t.Errorf("the Rewrite hook saw %d conditions, want the caller's 1", where)
	}
}

func TestPolicyInsideHas(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Ticket]().Where(Tk.Parent.Has()).Limit(1).All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE EXISTS (SELECT 1 FROM "tickets" AS "vx1" WHERE "vx1"."id" = "tickets"."parent_id" AND "org_id" = $1 AND "owner" = $2) AND "org_id" = $3 AND "owner" = $4 LIMIT $5`,
		int64(1), "ann", int64(1), "ann", 1)
}

// TestPolicyInsideHasOfAnEntityWithout: the target's Read policy applies
// though the statement's entity has none, and its denial names both.
func TestPolicyInsideHasOfAnEntityWithout(t *testing.T) {
	var seen velox.Statement
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		seen = *st
		return next(ctx)
	}))
	q := db.Query[pbox]().Where(velox.Not(pboxNote.HasWhere(pn.Locked.EQ(true)))).Limit(1)
	if _, err := q.All(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT "id", "note_id" FROM "pboxes" WHERE NOT (EXISTS (SELECT 1 FROM "pnotes" AS "vx1" WHERE "vx1"."id" = "pboxes"."note_id" AND "locked" = $1 AND "owner" = $2)) LIMIT $3`,
		true, "ann", 1)

	// A delete reads the target: only its Read policy applies.
	if _, err := db.Delete[pbox]().Where(pboxNote.Has()).Exec(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `DELETE FROM "pboxes" WHERE EXISTS (SELECT 1 FROM "pnotes" AS "vx1" WHERE "vx1"."id" = "pboxes"."note_id" AND "owner" = $1)`, "ann")

	calls := e.Calls
	_, err := q.All(pnoteReading(velox.Pred[pnote]{}, velox.Deny("hidden")))
	want(t, err, velox.ErrDenied, "velox: velox_test.pbox: in the EXISTS on velox_test.pnote: select denied by policy: hidden")
	var pe *velox.PolicyError
	if !errors.As(err, &pe) || pe.Entity != "velox_test.pnote" || pe.Op != velox.OpSelect {
		t.Errorf("got %#v", pe)
	}
	if e.Calls != calls {
		t.Errorf("%d denied statements sent", e.Calls-calls)
	}

	if _, err := q.All(velox.SkipPolicy(context.Background(), "support")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SQL, "owner") || seen.PolicyBypass != "support" {
		t.Errorf("SkipPolicy: %s, PolicyBypass %q", e.SQL, seen.PolicyBypass)
	}
}

// TestPolicyCalledOncePerStatement: each policy function runs once per
// statement, though its entity is the statement's and the target of several
// EXISTS.
func TestPolicyCalledOncePerStatement(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	bg := context.Background()
	count := func(name string, reads, writes int64, run func() error) {
		t.Helper()
		pnoteReads.Store(0)
		pnoteWrites.Store(0)
		if err := run(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r, w := pnoteReads.Load(), pnoteWrites.Load(); r != reads || w != writes {
			t.Errorf("%s: Read called %d times, Write %d; want %d and %d", name, r, w, reads, writes)
		}
	}
	nested := pn.Parent.HasWhere(pn.Parent.Has())
	count("select", 1, 0, func() error {
		_, err := db.Query[pnote]().Where(nested, velox.Or(pn.Parent.Has(), pn.ID.EQ(1))).Limit(1).All(bg)
		return err
	})
	if strings.Count(e.SQL, `"owner" = $`) != 4 {
		t.Errorf("select: %s", e.SQL)
	}
	count("update", 1, 1, func() error {
		_, err := db.Update[pnote]().Set(pn.InvoiceID.Set(2)).Where(nested).Exec(bg)
		return err
	})
	if strings.Count(e.SQL, `"owner" = $`) != 3 || strings.Count(e.SQL, `"locked" = $`) != 1 {
		t.Errorf("update: %s", e.SQL)
	}
	count("EXISTS alone", 1, 0, func() error {
		_, err := db.Query[pbox]().Where(pboxNote.Has(), pboxNote.HasWhere(nested)).Limit(1).All(bg)
		return err
	})
	count("skipped", 0, 0, func() error {
		_, err := db.Update[pnote]().Set(pn.InvoiceID.Set(2)).Where(nested).Exec(velox.SkipPolicy(bg, "support"))
		return err
	})
	if strings.Contains(e.SQL, `"owner"`) || strings.Contains(e.SQL, `"locked"`) {
		t.Errorf("skipped: %s", e.SQL)
	}
}

// TestPolicyConditionIsScopedAndChecked: an EXISTS in a policy's condition
// gets the tenant (and the Read policy, see TestPolicyIsInherited); a
// condition holding the zero Pred is refused.
func TestPolicyConditionIsScopedAndChecked(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	q := db.Query[pnote]().Limit(1)
	ctx := pnoteReading(pn.Invoice.HasWhere(testmodel.Invoices.Paid.EQ(true)), nil)
	if _, err := q.All(velox.WithTenant(ctx, int64(7))); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT "id", "owner", "locked", "parent_id", "invoice_id" FROM "pnotes" WHERE EXISTS (SELECT 1 FROM "invoices" AS "vx1" WHERE "vx1"."id" = "pnotes"."invoice_id" AND "paid" = $1 AND "org_id" = $2) LIMIT $3`,
		true, int64(7), 1)
	_, err := q.All(ctx)
	want(t, err, velox.ErrNoTenant, "velox: velox_test.pnote: in the EXISTS on testmodel.Invoice: no tenant in the context")
	// The same condition as an EXISTS target's Read: the error names the
	// innermost target.
	_, err = db.Query[pbox]().Where(pboxNote.Has()).Limit(1).All(ctx)
	want(t, err, velox.ErrNoTenant, "velox: velox_test.pbox: in the EXISTS on testmodel.Invoice: no tenant in the context")
	if _, err := db.Query[pbox]().Where(pboxNote.Has()).Limit(1).All(velox.WithTenant(ctx, int64(7))); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT "id", "note_id" FROM "pboxes" WHERE EXISTS (SELECT 1 FROM "pnotes" AS "vx1" WHERE "vx1"."id" = "pboxes"."note_id" AND EXISTS (SELECT 1 FROM "invoices" AS "vx2" WHERE "vx2"."id" = "vx1"."invoice_id" AND "paid" = $1 AND "org_id" = $2)) LIMIT $3`,
		true, int64(7), 1)

	calls := e.Calls
	_, err = q.All(pnoteReading(velox.Or(pn.Owner.EQ("ann"), velox.Pred[pnote]{}), nil))
	if err == nil || err.Error() != "velox: velox_test.pnote: the Read policy's condition holds the zero Pred" || errors.Is(err, velox.ErrDenied) {
		t.Errorf("a zero Pred in the policy's condition: %v", err)
	}
	if e.Calls != calls {
		t.Errorf("%d refused statements sent", e.Calls-calls)
	}
}

func TestPolicyOnWithLoad(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{ticketRow(2, 1, int64(1))},
		{ticketRow(1, 1, nil)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Ticket]().With(Tk.Parent).Limit(5).All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 2 {
		t.Fatalf("%d statements: %v", len(e.Log), e.Log)
	}
	if !strings.Contains(e.Log[1], `WHERE "id" IN ($1) AND "org_id" = $2 AND "owner" = $3 `) || e.Args[2] != "ann" {
		t.Errorf("the parent load ignores the policy: %s %v", e.Log[1], e.Args)
	}
}

func TestSkipPolicy(t *testing.T) {
	var seen []velox.Statement
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		seen = append(seen, *st)
		return next(ctx)
	}))
	support := velox.SkipPolicy(asUser(""), "support")
	if _, err := db.Query[testmodel.Ticket]().Where(Tk.Done.EQ(false)).Limit(5).All(support); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE "done" = $1 AND "org_id" = $2 LIMIT $3`, false, int64(1), 5)
	if _, err := db.Query[testmodel.Ticket]().Limit(5).All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	// An entity with no policy skips none.
	if _, err := db.Query[testmodel.Invoice]().Limit(5).All(velox.SkipPolicy(tenant(int64(1)), "support")); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 3 {
		t.Fatalf("the Around hook saw %d statements", len(seen))
	}
	for i, want := range []string{"support", "", ""} {
		if seen[i].PolicyBypass != want {
			t.Errorf("statement %d: PolicyBypass %q, want %q", i, seen[i].PolicyBypass, want)
		}
	}
}

func TestSaveOfAHiddenRow(t *testing.T) {
	e := &velox.FakeExec{Affected: 0}
	db := velox.OpenFake(velox.Postgres, e)
	tk := testmodel.Ticket{ID: 1, OrgID: 1, Owner: "bob", Title: "T-1"}
	err := db.Save(asUser("ann"), &tk, Tk.Title)
	if !errors.Is(err, velox.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if strings.Count(e.SQL, `"owner" = $`) != 2 || e.Calls != 1 {
		t.Errorf("save: %s %v", e.SQL, e.Args)
	}
}

// TestPolicyIsInherited: an EXISTS in a policy's condition gets its target's
// Read policy, so a comment is hidden with its note.
func TestPolicyIsInherited(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	bg := context.Background()
	pnoteReads.Store(0)
	if _, err := db.Query[pcomment]().Where(pcommentNote.HasWhere(pn.Locked.EQ(true))).Limit(1).All(bg); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT "id", "note_id" FROM "pcomments" WHERE EXISTS (SELECT 1 FROM "pnotes" AS "vx1" WHERE "vx1"."id" = "pcomments"."note_id" AND "locked" = $1 AND "owner" = $2) AND EXISTS (SELECT 1 FROM "pnotes" AS "vx1" WHERE "vx1"."id" = "pcomments"."note_id" AND "owner" = $3) LIMIT $4`,
		true, "ann", "ann", 1)
	if n := pnoteReads.Load(); n != 1 {
		t.Errorf("pnote's Read called %d times", n)
	}

	calls := e.Calls
	_, err := db.Query[pcomment]().Limit(1).All(pnoteReading(velox.Pred[pnote]{}, velox.Deny("hidden")))
	want(t, err, velox.ErrDenied, "velox: velox_test.pcomment: in the EXISTS on velox_test.pnote: select denied by policy: hidden")
	if e.Calls != calls {
		t.Errorf("%d denied statements sent", e.Calls-calls)
	}
}

// TestPolicyCycleIsRefused: a Read policy that reaches its own Read through
// an EXISTS, directly or from another entity's policy, cannot be applied;
// the statement is refused, and it is not a denial.
func TestPolicyCycleIsRefused(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	self := pnoteReading(pn.Parent.HasWhere(pn.Locked.EQ(false)), nil)
	for name, c := range map[string]struct {
		run  func() error
		want string
	}{
		"direct": {func() error { _, err := db.Query[pnote]().Limit(1).All(self); return err },
			"velox: velox_test.pnote: the Read policy of velox_test.pnote reaches its own Read through an EXISTS; such a policy cannot be applied"},
		"from an EXISTS": {func() error { _, err := db.Query[pbox]().Where(pboxNote.Has()).Limit(1).All(self); return err },
			"velox: velox_test.pbox: the Read policy of velox_test.pnote reaches its own Read through an EXISTS; such a policy cannot be applied"},
		"from another policy": {func() error { _, err := db.Query[pcomment]().Limit(1).All(self); return err },
			"velox: velox_test.pcomment: the Read policy of velox_test.pnote reaches its own Read through an EXISTS; such a policy cannot be applied"},
		"on an update": {func() error {
			_, err := db.Update[pnote]().Set(pn.InvoiceID.Set(2)).Where(pn.ID.EQ(1)).Exec(self)
			return err
		}, "velox: velox_test.pnote: the Read policy of velox_test.pnote reaches its own Read through an EXISTS; such a policy cannot be applied"},
	} {
		err := c.run()
		if err == nil || err.Error() != c.want || errors.Is(err, velox.ErrDenied) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	// Under SkipPolicy no policy runs, so there is no cycle.
	if _, err := db.Query[pnote]().Limit(1).All(velox.SkipPolicy(self, "support")); err != nil {
		t.Error(err)
	}
}

// pcycA's Read is that its B is visible, and pcycB's that its A is: each
// reaches its own Read through the other's.
type pcycA struct {
	ID  int64
	BID int64
	B   velox.One[pcycB]
}

type pcycB struct {
	ID  int64
	AID int64
	A   velox.One[pcycA]
}

var (
	pcycAB = velox.Rel[pcycA, pcycB]{I: 0}
	pcycBA = velox.Rel[pcycB, pcycA]{I: 0}
)

func (x *pcycA) Schema(s *velox.Schema) {
	s.Table("pcyc_as")
	s.Policy(velox.Policy[pcycA]{Read: func(context.Context) (velox.Pred[pcycA], error) { return pcycAB.Has(), nil }})
}

func (x *pcycB) Schema(s *velox.Schema) {
	s.Table("pcyc_bs")
	s.Policy(velox.Policy[pcycB]{Read: func(context.Context) (velox.Pred[pcycB], error) { return pcycBA.Has(), nil }})
}

var (
	pcycAMeta velox.MetaOnce[pcycA]
	pcycBMeta velox.MetaOnce[pcycB]
)

func (*pcycA) VeloxMeta() *velox.Meta[pcycA] {
	return pcycAMeta.Get(func() velox.Shape[pcycA] {
		return velox.Shape[pcycA]{Columns: []string{"ID", "BID"}, Relations: []velox.Relation{velox.RelationTo[pcycB]("B")},
			Addr: func(x *pcycA, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.BID
			}}
	})
}

func (*pcycB) VeloxMeta() *velox.Meta[pcycB] {
	return pcycBMeta.Get(func() velox.Shape[pcycB] {
		return velox.Shape[pcycB]{Columns: []string{"ID", "AID"}, Relations: []velox.Relation{velox.RelationTo[pcycA]("A")},
			Addr: func(x *pcycB, i int) any {
				if i == 0 {
					return &x.ID
				}
				return &x.AID
			}}
	})
}

// TestPolicyMutualCycleIsRefused: two Read policies that reach each other's
// through EXISTS are a cycle, from either side.
func TestPolicyMutualCycleIsRefused(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	bg := context.Background()
	_, err := db.Query[pcycA]().Limit(1).All(bg)
	if err == nil || err.Error() != "velox: velox_test.pcycA: the Read policy of velox_test.pcycA reaches its own Read through an EXISTS; such a policy cannot be applied" || errors.Is(err, velox.ErrDenied) {
		t.Errorf("A: %v", err)
	}
	_, err = db.Query[pcycB]().Limit(1).All(bg)
	if err == nil || err.Error() != "velox: velox_test.pcycB: the Read policy of velox_test.pcycB reaches its own Read through an EXISTS; such a policy cannot be applied" {
		t.Errorf("B: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}
