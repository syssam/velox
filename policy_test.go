package velox_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
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
	return testmodel.WithUser(ctx, user)
}

func TestPolicyReadPredicate(t *testing.T) {
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	q := db.Query[testmodel.Ticket]().Where(Tk.Done.EQ(false)).Limit(5)

	if _, err := q.All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE "done" = $1 AND "org_id" = $2 AND "owner" = $3 LIMIT $4`, false, int64(1), "ann", 5)

	// Allow adds nothing.
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
	// Ticket's delete hook may add a condition on done.
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

func TestPolicyOnCountOfAnExists(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(3)}}}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Ticket]().Where(Tk.Parent.Has()).Count(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT COUNT(*) FROM "tickets" WHERE EXISTS (SELECT 1 FROM "tickets" AS "vx1" WHERE "vx1"."id" = "tickets"."parent_id" AND "org_id" = $1 AND "owner" = $2) AND "org_id" = $3 AND "owner" = $4`,
		int64(1), "ann", int64(1), "ann")
}

// TestPolicyDeleteOrder: the caller's conditions come first, then the policy's.
func TestPolicyDeleteOrder(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Delete[testmodel.Ticket]().Where(Tk.Done.EQ(true), Tk.Title.NEQ("x")).Exec(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `DELETE FROM "tickets" WHERE "done" = $1 AND "title" <> $2 AND "done" = $3 AND "org_id" = $4 AND "owner" = $5 AND "owner" = $6`,
		true, "x", true, int64(1), "ann", "ann")
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

	if _, err := q.All(velox.WithoutPolicy(context.Background(), "support")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SQL, "owner") || seen.PolicyBypass != "support" {
		t.Errorf("WithoutPolicy: %s, PolicyBypass %q", e.SQL, seen.PolicyBypass)
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
		_, err := db.Update[pnote]().Set(pn.InvoiceID.Set(2)).Where(nested).Exec(velox.WithoutPolicy(bg, "support"))
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
	// path, from the outermost target in.
	_, err = db.Query[pbox]().Where(pboxNote.Has()).Limit(1).All(ctx)
	want(t, err, velox.ErrNoTenant, "velox: velox_test.pbox: in the EXISTS on velox_test.pnote, then on testmodel.Invoice: no tenant in the context")
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
	support := velox.WithoutPolicy(asUser(""), "support")
	if _, err := db.Query[testmodel.Ticket]().Where(Tk.Done.EQ(false)).Limit(5).All(support); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT `+ticketCols+` FROM "tickets" WHERE "done" = $1 AND "org_id" = $2 LIMIT $3`, false, int64(1), 5)
	if _, err := db.Query[testmodel.Ticket]().Limit(5).All(asUser("ann")); err != nil {
		t.Fatal(err)
	}
	// An entity with no policy skips none.
	if _, err := db.Query[testmodel.Invoice]().Limit(5).All(velox.WithoutPolicy(tenant(int64(1)), "support")); err != nil {
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
	err := db.Save(&tk, Tk.Title).Exec(asUser("ann"))
	if !errors.Is(err, velox.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	if strings.Count(e.SQL, `"owner" = $`) != 2 || e.Calls != 1 {
		t.Errorf("save: %s %v", e.SQL, e.Args)
	}
}

// TestHiddenRowThroughRun: the same as TestSaveOfAHiddenRow, on a DB with a
// pass-through Around hook, so the save takes run's path.
func TestHiddenRowThroughRun(t *testing.T) {
	e := &velox.FakeExec{Affected: 0}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		return next(ctx)
	}))
	tk := testmodel.Ticket{ID: 1, OrgID: 1, Owner: "bob", Title: "T-1"}
	err := db.Save(&tk, Tk.Title).Exec(asUser("ann"))
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
	// Under WithoutPolicy no policy runs, so there is no cycle.
	if _, err := db.Query[pnote]().Limit(1).All(velox.WithoutPolicy(self, "support")); err != nil {
		t.Error(err)
	}
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

// TestPolicyZeroPredFails: a policy written the old way, returning the zero
// Pred for "no restriction", fails the statement; it never lets the caller
// through. Allow is the way to say no restriction.
func TestPolicyZeroPredFails(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	const msg = "the Read policy returned the zero Pred; return velox.Allow[E]() for no restriction"
	const writeMsg = "the Write policy returned the zero Pred; return velox.Allow[E]() for no restriction"
	zeroRead := pnoteReading(velox.Pred[pnote]{}, nil)

	calls := e.Calls
	_, err := db.Query[pnote]().Limit(1).All(zeroRead)
	if err == nil || err.Error() != "velox: velox_test.pnote: "+msg {
		t.Errorf("Read: %v", err)
	}
	if errors.Is(err, velox.ErrDenied) {
		t.Errorf("a zero Pred is a policy bug, not a denial: %v", err)
	}
	_, err = db.Query[pnote]().Count(zeroRead)
	if err == nil || !strings.Contains(err.Error(), msg) {
		t.Errorf("Count: %v", err)
	}
	// The Read condition applies to an update and a delete as well.
	_, err = db.Update[pnote]().Set(pn.Owner.Set("x")).Where(pn.ID.EQ(1)).Exec(zeroRead)
	if err == nil || !strings.Contains(err.Error(), msg) {
		t.Errorf("update with a zero Read: %v", err)
	}

	// An EXISTS reads its target: the error names the statement's entity, then
	// the target.
	_, err = db.Query[pbox]().Where(pboxNote.Has()).Limit(1).All(zeroRead)
	if err == nil || err.Error() != "velox: velox_test.pbox: in the EXISTS on velox_test.pnote: "+msg {
		t.Errorf("EXISTS: %v", err)
	}

	zeroWrite := pnoteWriting(velox.Pred[pnote]{}, nil)
	_, err = db.Update[pnote]().Set(pn.Owner.Set("x")).Where(pn.ID.EQ(1)).Exec(zeroWrite)
	if err == nil || err.Error() != "velox: velox_test.pnote: "+writeMsg {
		t.Errorf("Write on update: %v", err)
	}
	_, err = db.Delete[pnote]().Where(pn.ID.EQ(1)).Exec(zeroWrite)
	if err == nil || !strings.Contains(err.Error(), writeMsg) {
		t.Errorf("Write on delete: %v", err)
	}

	// A With load reads its target under the target's Read policy.
	boxes := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{{int64(1), int64(1)}}})
	_, err = boxes.Query[pbox]().With(pboxNote).Limit(1).All(zeroRead)
	if err == nil || !strings.Contains(err.Error(), msg) {
		t.Errorf("With load: %v", err)
	}

	// An upsert's update branch gets both conditions.
	up := func(ctx context.Context) error {
		return db.Insert(&pnote{ID: 1, Owner: "x"}).OnConflict(pn.ID).Update(pn.Owner).Exec(ctx)
	}
	if err := up(zeroRead); err == nil || !strings.Contains(err.Error(), msg) {
		t.Errorf("upsert with a zero Read: %v", err)
	}
	if err := up(zeroWrite); err == nil || !strings.Contains(err.Error(), writeMsg) {
		t.Errorf("upsert with a zero Write: %v", err)
	}

	// The error comes first: a policy that returns the zero Pred with an error
	// denies the statement with that error.
	boom := errors.New("boom")
	_, err = db.Delete[pnote]().Where(pn.ID.EQ(1)).Exec(pnoteWriting(velox.Pred[pnote]{}, boom))
	if !errors.Is(err, boom) || !errors.Is(err, velox.ErrDenied) || strings.Contains(err.Error(), "zero Pred") {
		t.Errorf("Write (zero, err): %v", err)
	}
	if e.Calls != calls {
		t.Errorf("%d statements sent past a zero policy Pred", e.Calls-calls)
	}

	// Allow, the explicit form, is accepted.
	if _, err := db.Query[pnote]().Limit(1).All(pnoteReading(velox.Allow[pnote](), nil)); err != nil {
		t.Errorf("Allow: %v", err)
	}
}

// TestAllowRendersNothing: a policy that returns Allow adds no condition, so
// the statement is the one an entity without a policy sends.
func TestAllowRendersNothing(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	allow := velox.Allow[pnote]()

	q := db.Query[pnote]().Where(pn.ID.GT(0)).Limit(3)
	if _, err := q.All(velox.WithoutPolicy(context.Background(), "test")); err != nil {
		t.Fatal(err)
	}
	wantSQL, wantArgs := e.SQL, e.Args
	if strings.Contains(wantSQL, `"owner" =`) {
		t.Fatalf("WithoutPolicy sent %s", wantSQL)
	}
	if _, err := q.All(pnoteReading(allow, nil)); err != nil {
		t.Fatal(err)
	}
	if e.SQL != wantSQL || !slices.Equal(e.Args, wantArgs) {
		t.Errorf("select with Allow: %s %v, want %s %v", e.SQL, e.Args, wantSQL, wantArgs)
	}

	// Both of an update's conditions Allow: the statement is the caller's.
	up := db.Update[pnote]().Set(pn.Owner.Set("x")).Where(pn.ID.EQ(1))
	if _, err := up.Exec(velox.WithoutPolicy(context.Background(), "test")); err != nil {
		t.Fatal(err)
	}
	wantSQL, wantArgs = e.SQL, e.Args
	ctx := context.WithValue(pnoteReading(allow, nil), pnoteWriteKey{}, pnoteAnswer{allow, nil})
	if _, err := up.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if e.SQL != wantSQL || !slices.Equal(e.Args, wantArgs) {
		t.Errorf("update with Allow: %s %v, want %s %v", e.SQL, e.Args, wantSQL, wantArgs)
	}

	// And inside an EXISTS: the target's Allow adds nothing.
	bq := db.Query[pbox]().Where(pboxNote.Has()).Limit(1)
	if _, err := bq.All(velox.WithoutPolicy(context.Background(), "test")); err != nil {
		t.Fatal(err)
	}
	wantSQL = e.SQL
	if _, err := bq.All(pnoteReading(allow, nil)); err != nil {
		t.Fatal(err)
	}
	if e.SQL != wantSQL {
		t.Errorf("EXISTS with Allow: %s, want %s", e.SQL, wantSQL)
	}
}

// TestAllowComposes: Or(Allow, x) is Allow, And(Allow, x) is x, and
// Not(Allow) matches nothing.
func TestAllowComposes(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	allow := velox.Allow[pnote]()
	x := pn.Owner.EQ("ann")
	skip := velox.WithoutPolicy(context.Background(), "test")

	sql := func(p velox.Pred[pnote]) (string, []any) {
		t.Helper()
		if _, err := db.Query[pnote]().Where(p).Limit(1).All(skip); err != nil {
			t.Fatal(err)
		}
		return e.SQL, e.Args
	}
	same := func(name string, got, want velox.Pred[pnote]) {
		t.Helper()
		gs, ga := sql(got)
		ws, wa := sql(want)
		if gs != ws || !slices.Equal(ga, wa) {
			t.Errorf("%s: %s %v, want %s %v", name, gs, ga, ws, wa)
		}
	}
	same("And(Allow, x)", velox.And(allow, x), x)
	same("And(x, Allow)", velox.And(x, allow), x)
	same("And(Allow, Allow)", velox.And(allow, allow), allow)
	same("And(Allow, x, y)", velox.And(allow, x, pn.Locked.EQ(true)), velox.And(x, pn.Locked.EQ(true)))
	same("Or(Allow, x)", velox.Or(allow, x), allow)
	same("Or(x, Allow)", velox.Or(x, allow), allow)
	same("And(x, Or(Allow, y))", velox.And(x, velox.Or(allow, pn.Locked.EQ(true))), x)

	if s, _ := sql(velox.Not(allow)); !strings.HasSuffix(s, `WHERE NOT (1 = 1) LIMIT $1`) {
		t.Errorf("Not(Allow): %s", s)
	}

	// Allow in a Where matches every row: an update or delete that holds only
	// it is refused as one that names no row.
	_, err := db.Delete[pnote]().Where(velox.Or(allow, x)).Exec(skip)
	want(t, err, velox.ErrNoWhere, "velox: velox_test.pnote: "+velox.ErrNoWhere.Error()+": its conditions match every row; add a condition, or All to change every row")
	if _, err := db.Delete[pnote]().Where(velox.Not(allow)).Exec(skip); err != nil {
		t.Errorf("Not(Allow) matches nothing, which is a condition: %v", err)
	}

	// An Allow does not hide a zero or invalid kid of Or: the statement still
	// fails on it, as it does without the Allow.
	calls := e.Calls
	_, err = db.Query[pnote]().Where(velox.Or(allow, velox.Pred[pnote]{})).Limit(1).All(skip)
	if err == nil || !strings.Contains(err.Error(), "a condition is the zero Predicate") {
		t.Errorf("Or(Allow, zero): %v", err)
	}
	bad := velox.Rel[pnote, pnote]{I: 9}.Has()
	_, wantErr := db.Query[pnote]().Where(bad).Limit(1).All(skip)
	_, err = db.Query[pnote]().Where(velox.Or(allow, bad)).Limit(1).All(skip)
	if wantErr == nil || err == nil || err.Error() != wantErr.Error() {
		t.Errorf("Or(Allow, invalid): %v, want %v", err, wantErr)
	}
	if e.Calls != calls {
		t.Errorf("%d statements sent past a zero or invalid kid", e.Calls-calls)
	}
	// A Has next to an Allow is kept, to resolve when the statement runs, and
	// the Or still matches every row.
	_, err = db.Delete[pnote]().Where(velox.Or(allow, pn.Parent.Has())).Exec(skip)
	want(t, err, velox.ErrNoWhere, "velox: velox_test.pnote: "+velox.ErrNoWhere.Error()+": its conditions match every row; add a condition, or All to change every row")
	if s, _ := sql(velox.Or(allow, pn.Parent.Has())); !strings.Contains(s, "1 = 1 OR EXISTS") {
		t.Errorf("Or(Allow, Has): %s", s)
	}
}

// TestPolicyWritable: a Writable query gets the Write condition after the
// tenant and Read ones, as an UPDATE does, so a Count checks what an Update
// may change; it is a read still: Write's denial is a select's.
func TestPolicyWritable(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(1)}}}
	db := velox.OpenFake(velox.Postgres, e)
	q := db.Query[pnote]().Where(pn.ID.EQ(1))
	if _, err := q.Writable().Count(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT COUNT(*) FROM "pnotes" WHERE "id" = $1 AND "owner" = $2 AND "locked" = $3`, int64(1), "ann", false)
	// q is a value: Writable left it as it was.
	if _, err := q.Count(context.Background()); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT COUNT(*) FROM "pnotes" WHERE "id" = $1 AND "owner" = $2`, int64(1), "ann")

	calls := e.Calls
	_, err := q.Writable().Count(pnoteWriting(velox.Pred[pnote]{}, velox.Deny("read-only")))
	var pe *velox.PolicyError
	if !errors.As(err, &pe) || pe.Op != velox.OpSelect || !errors.Is(err, velox.ErrDenied) {
		t.Errorf("a denied Write: %v", err)
	}
	if e.Calls != calls {
		t.Errorf("%d denied statements sent", e.Calls-calls)
	}
	// WithoutPolicy skips Write too.
	if _, err := q.Writable().Count(velox.WithoutPolicy(context.Background(), "test")); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT COUNT(*) FROM "pnotes" WHERE "id" = $1`, int64(1))

	// A Rewrite hook cannot drop Writable, which the scope reads, nor set it.
	for name, w := range map[string]bool{"cleared": true, "set": false} {
		re := &velox.FakeExec{Rows: [][]any{{int64(1)}}}
		rdb := velox.OpenFake(velox.Postgres, re, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			st.Writable = !st.Writable
			return nil
		}))
		rq := rdb.Query[pnote]().Where(pn.ID.EQ(1))
		if w {
			rq = rq.Writable()
		}
		_, err := rq.Count(context.Background())
		if err == nil || !strings.Contains(err.Error(), "a Rewrite hook changed the statement's Writable") || re.Calls != 0 {
			t.Errorf("Writable %s by a Rewrite hook: %v, %d sent", name, err, re.Calls)
		}
	}
}

// TestPolicySQLOnEveryDialect: SQL(ctx) applies the policy as execution does,
// on every dialect. Allow leaves the unscoped text; a zero Pred, from Read or
// from Write, fails SQL(ctx) with the message execution gives.
func TestPolicySQLOnEveryDialect(t *testing.T) {
	const (
		readMsg  = "velox: velox_test.pnote: the Read policy returned the zero Pred; return velox.Allow[E]() for no restriction"
		writeMsg = "velox: velox_test.pnote: the Write policy returned the zero Pred; return velox.Allow[E]() for no restriction"
	)
	for _, c := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, `SELECT "id", "owner", "locked", "parent_id", "invoice_id" FROM "pnotes" WHERE "id" > $1 LIMIT $2`},
		{velox.MySQL, "SELECT `id`, `owner`, `locked`, `parent_id`, `invoice_id` FROM `pnotes` WHERE `id` > ? LIMIT ?"},
		{velox.SQLite, `SELECT "id", "owner", "locked", "parent_id", "invoice_id" FROM "pnotes" WHERE "id" > ? LIMIT ?`},
	} {
		db := velox.OpenFake(c.d, &velox.FakeExec{})
		q := db.Query[pnote]().Where(pn.ID.GT(0)).Limit(3)
		unscoped, uargs, err := q.SQL(velox.WithoutPolicy(context.Background(), "test"))
		if err != nil || unscoped != c.want {
			t.Errorf("%v: unscoped\n got %s, %v\nwant %s", c.d, unscoped, err, c.want)
		}
		got, args, err := q.SQL(pnoteReading(velox.Allow[pnote](), nil))
		if err != nil || got != c.want || !slices.Equal(args, uargs) || !slices.Equal(args, []any{int64(0), 3}) {
			t.Errorf("%v: Allow\n got %s %v, %v\nwant %s %v", c.d, got, args, err, c.want, uargs)
		}
		if _, _, err := q.SQL(pnoteReading(velox.Pred[pnote]{}, nil)); err == nil || err.Error() != readMsg {
			t.Errorf("%v: a zero Read: %v", c.d, err)
		}
		u := db.Update[pnote]().Set(pn.Owner.Set("x")).Where(pn.ID.EQ(1))
		if _, _, err := u.SQL(pnoteWriting(velox.Pred[pnote]{}, nil)); err == nil || err.Error() != writeMsg {
			t.Errorf("%v: a zero Write: %v", c.d, err)
		}
	}
}

// TestExistsErrorNamesThePath: an error from an EXISTS nested in another
// names both targets, from the outermost in.
func TestExistsErrorNamesThePath(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
	_, err := db.Query[pbox]().Where(pboxNote.HasWhere(pn.Invoice.Has())).Limit(1).All(context.Background())
	want(t, err, velox.ErrNoTenant, "velox: velox_test.pbox: in the EXISTS on velox_test.pnote, then on testmodel.Invoice: no tenant in the context")
}

// policyDDL is velox-gen migrate sql -dialect sqlite's invoices and tickets
// tables, for the testmodel schema.
var policyDDL = []string{
	`CREATE TABLE "invoices" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_at" datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updated_at" datetime NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "org_id" integer NOT NULL,
  "number" text NOT NULL,
  "total" real NOT NULL,
  "paid" boolean NOT NULL,
  "paid_at" datetime,
  "note" text NOT NULL DEFAULT '',
  "data" blob NOT NULL
)`,
	`CREATE UNIQUE INDEX "invoice_org_id_number" ON "invoices" ("org_id", "number")`,
	`CREATE TABLE "tickets" (
  "id" integer NOT NULL PRIMARY KEY AUTOINCREMENT,
  "created_by" text NOT NULL,
  "org_id" integer NOT NULL,
  "owner" text NOT NULL,
  "title" text NOT NULL,
  "done" boolean NOT NULL,
  "invoice_id" integer NOT NULL,
  "parent_id" integer,
  CONSTRAINT "tickets_invoices_invoice" FOREIGN KEY ("invoice_id") REFERENCES "invoices" ("id"),
  CONSTRAINT "tickets_tickets_children" FOREIGN KEY ("parent_id") REFERENCES "tickets" ("id") ON DELETE SET NULL
)`,
	`CREATE UNIQUE INDEX "ticket_org_id_title" ON "tickets" ("org_id", "title")`,
}

// policyDB opens a SQLite database in a temporary file, with the invoices and
// tickets tables.
func policyDB(t *testing.T) *velox.DB {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = raw.Close() })
	for _, s := range policyDDL {
		if _, err := raw.Exec(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	return velox.Open(velox.SQLite, raw)
}

// TestWritePolicyFiltersToZeroRows: a row Write hides is left alone, and the
// statement that matched only such rows affects 0 rows, without an error.
func TestWritePolicyFiltersToZeroRows(t *testing.T) {
	db := policyDB(t)
	seed := velox.WithoutPolicy(velox.WithAllTenants(context.Background(), "test"), "seed")
	inv := testmodel.Invoice{OrgID: 1, Number: "N1", Data: []byte("d")}
	if err := db.Insert(&inv).Exec(seed); err != nil {
		t.Fatal(err)
	}
	tk := testmodel.Ticket{OrgID: 1, Owner: "bob", Title: "T-1", Done: true, InvoiceID: inv.ID}
	if err := db.Insert(&tk).Exec(seed); err != nil {
		t.Fatal(err)
	}
	ann := testmodel.WithUser(velox.WithAllTenants(context.Background(), "test"), "ann")

	n, err := db.Update[testmodel.Ticket]().Set(Tk.Title.Set("x")).Where(Tk.ID.EQ(tk.ID)).Exec(ann)
	if n != 0 || err != nil {
		t.Errorf("update as ann: %d, %v", n, err)
	}
	n, err = db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(tk.ID)).Exec(ann)
	if n != 0 || err != nil {
		t.Errorf("delete as ann: %d, %v", n, err)
	}
	got, err := db.Query[testmodel.Ticket]().Where(Tk.ID.EQ(tk.ID)).One(seed)
	if err != nil || got.Title != "T-1" {
		t.Fatalf("under WithoutPolicy: %+v, %v", got, err)
	}

	// The row is changeable by its owner: the zero rows were the policy's.
	bob := testmodel.WithUser(velox.WithAllTenants(context.Background(), "test"), "bob")
	n, err = db.Delete[testmodel.Ticket]().Where(Tk.ID.EQ(tk.ID)).Exec(bob)
	if n != 1 || err != nil {
		t.Errorf("delete as bob: %d, %v", n, err)
	}
}

// TestEqualFoldIsASCIIOnSQLite: SQLite's LOWER folds ASCII letters only, so
// EqualFold ignores the case of those alone.
func TestEqualFoldIsASCIIOnSQLite(t *testing.T) {
	db := policyDB(t)
	ctx := velox.WithoutPolicy(velox.WithAllTenants(context.Background(), "test"), "seed")
	if err := db.Insert(&testmodel.Invoice{OrgID: 1, Number: "ÉCOLE", Data: []byte("d")}).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		s    string
		want int64
	}{
		{"ÉcOLE", 1}, // the É is the same; c and O differ in case only
		{"école", 0}, // SQLite leaves É as it is, so it is not é
	} {
		n, err := db.Query[testmodel.Invoice]().Where(I.Number.EqualFold(c.s)).Count(ctx)
		if err != nil || n != c.want {
			t.Errorf("EqualFold(%q): %d, %v; want %d", c.s, n, err, c.want)
		}
	}
}
