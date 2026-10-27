package velox_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// ticketInsertCols are the columns an insert of a Ticket with a generated key
// writes.
const ticketInsertCols = `"created_by", "org_id", "owner", "title", "done", "invoice_id", "parent_id"`

// as returns a context acting as user, scoped to tenant org.
func as(user string, org int64) context.Context {
	return testmodel.WithUser(velox.WithTenant(context.Background(), org), user)
}

func wantPolicyError(t *testing.T, err error, op velox.Op) {
	t.Helper()
	var pe *velox.PolicyError
	if !errors.As(err, &pe) || pe.Op != op || !errors.Is(err, velox.ErrDenied) {
		t.Fatalf("got %v, want a *PolicyError for %s", err, op)
	}
}

func TestInsertFillsTheTenant(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(5)}}}
	db := velox.OpenFake(velox.Postgres, e)
	tk := testmodel.Ticket{Owner: "ann", Title: "t"}
	if err := db.Insert(&tk).Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	if tk.OrgID != 7 || tk.CreatedBy != "ann" || tk.ID != 5 {
		t.Errorf("OrgID %d, CreatedBy %q, ID %d", tk.OrgID, tk.CreatedBy, tk.ID)
	}
	wantSent(t, e, `INSERT INTO "tickets" (`+ticketInsertCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING "id"`,
		"ann", int64(7), "ann", "t", false, int64(0), (*int64)(nil))
}

func TestInsertTenantMismatch(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(5)}}}
	db := velox.OpenFake(velox.Postgres, e)
	tk := testmodel.Ticket{OrgID: 8, Owner: "ann", Title: "t"}
	err := db.Insert(&tk).Exec(as("ann", 7))
	if !errors.Is(err, velox.ErrTenantMismatch) || !strings.HasPrefix(err.Error(), "velox: testmodel.Ticket: tenant does not match") {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	// An int tenant is converted to the column's int64, as for a query.
	tk.OrgID = 0
	if err := db.Insert(&tk).Exec(testmodel.WithUser(velox.WithTenant(context.Background(), 7), "ann")); err != nil || tk.OrgID != 7 {
		t.Errorf("an int tenant: %v, OrgID %d", err, tk.OrgID)
	}
}

func TestInsertAllTenants(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(5)}}}
	db := velox.OpenFake(velox.Postgres, e)
	all := testmodel.WithUser(velox.WithAllTenants(context.Background(), "import"), "ann")
	tk := testmodel.Ticket{Owner: "ann", Title: "t"}
	err := db.Insert(&tk).Exec(all)
	if !errors.Is(err, velox.ErrNoTenant) || !strings.HasPrefix(err.Error(), "velox: testmodel.Ticket: no tenant in the context") {
		t.Fatalf("a zero tenant under WithAllTenants: %v", err)
	}
	if err := db.Insert(&tk).Exec(testmodel.WithUser(context.Background(), "ann")); !errors.Is(err, velox.ErrNoTenant) {
		t.Errorf("no tenant at all: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	tk.OrgID = 3
	if err := db.Insert(&tk).Exec(all); err != nil {
		t.Fatal(err)
	}
	if tk.ID != 5 || e.Args[1] != int64(3) {
		t.Errorf("ID %d, args %v", tk.ID, e.Args)
	}
}

func TestInsertHookRefusesAnEmptyTitle(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(5)}}}
	db := velox.OpenFake(velox.Postgres, e)
	tk := testmodel.Ticket{Owner: "ann"}
	err := db.Insert(&tk).Exec(as("ann", 7))
	if err == nil || err.Error() != "velox: testmodel.Ticket: hook: a ticket needs a title" {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

func TestInsertPolicy(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(5)}}}
	db := velox.OpenFake(velox.Postgres, e)
	tk := testmodel.Ticket{Owner: "ann", Title: "t"}
	err := db.Insert(&tk).Exec(as("bob", 7))
	wantPolicyError(t, err, velox.OpInsert)
	if want := "velox: testmodel.Ticket: insert denied by policy: a ticket must be created by its owner"; err.Error() != want {
		t.Errorf("got %q, want %q", err, want)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	// WithoutPolicy lets it through, and the hooks see the reason.
	var bypass string
	var tenant any
	db = velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		bypass, tenant = st.PolicyBypass, st.Tenant
		return next(ctx)
	}))
	if err := db.Insert(&tk).Exec(velox.WithoutPolicy(as("bob", 7), "migration")); err != nil || bypass != "migration" || tenant != int64(7) {
		t.Errorf("WithoutPolicy: %v, PolicyBypass %q, Tenant %v", err, bypass, tenant)
	}
}

func TestInsertAllPreparesEveryRow(t *testing.T) {
	e := &velox.FakeExec{Affected: 3}
	db := velox.OpenFake(velox.Postgres, e)
	tks := []testmodel.Ticket{{Owner: "ann", Title: "a"}, {Owner: "ann", Title: "b"}, {Owner: "ann", Title: "c"}}
	if err := db.InsertAll(tks).Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	for i, tk := range tks {
		if tk.OrgID != 7 || tk.CreatedBy != "ann" {
			t.Errorf("row %d: OrgID %d, CreatedBy %q", i, tk.OrgID, tk.CreatedBy)
		}
	}
	// A failing row stops the insert before anything is sent.
	e.Calls = 0
	bad := []testmodel.Ticket{{Owner: "ann", Title: "a"}, {Owner: "ann"}}
	if err := db.InsertAll(bad).Exec(as("ann", 7)); err == nil || !strings.Contains(err.Error(), ": hook: ") || e.Calls != 0 {
		t.Errorf("%v, %d statements sent", err, e.Calls)
	}
	if err := db.InsertAll([]testmodel.Ticket{{Owner: "ann", Title: "a"}, {Owner: "bob", Title: "b"}}).Exec(as("ann", 7)); err == nil {
		t.Error("the second row's policy was not checked")
	} else {
		wantPolicyError(t, err, velox.OpInsert)
	}
}

func TestUpsertMustNameTheTenant(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite} {
		e := &velox.FakeExec{Rows: [][]any{{int64(5)}}}
		db := velox.OpenFake(d, e)
		tk := testmodel.Ticket{Owner: "ann", Title: "t"}
		err := db.Insert(&tk).OnConflict(Tk.ID).Update(Tk.Title).Exec(as("ann", 7))
		if !errors.Is(err, velox.ErrTenantUpsert) || !strings.Contains(err.Error(), "name the tenant column in OnConflict") ||
			!strings.HasPrefix(err.Error(), "velox: testmodel.Ticket: upsert could cross tenants") {
			t.Errorf("%s: %v", d, err)
		}
		if e.Calls != 0 {
			t.Errorf("%s: %d statements sent", d, e.Calls)
		}
	}
}

func TestUpsertGetsThePolicy(t *testing.T) {
	for _, c := range []struct {
		d     velox.Dialect
		where string
	}{
		{velox.Postgres, `($1, $2, $3, $4, $5, $6, $7) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" WHERE "tickets"."owner" = $8 AND "tickets"."owner" = $9 RETURNING "id"`},
		{velox.SQLite, `(?, ?, ?, ?, ?, ?, ?) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" WHERE "tickets"."owner" = ? AND "tickets"."owner" = ? RETURNING "id"`},
	} {
		// Twice: the second statement's text comes from the render cache.
		for range 2 {
			e := &velox.FakeExec{Rows: [][]any{{int64(5)}}}
			db := velox.OpenFake(c.d, e)
			tk := testmodel.Ticket{Owner: "ann", Title: "t"}
			if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
				t.Fatal(err)
			}
			wantSent(t, e, `INSERT INTO "tickets" (`+ticketInsertCols+`) VALUES `+c.where,
				"ann", int64(7), "ann", "t", false, int64(0), (*int64)(nil), "ann", "ann")
		}
	}
}

func TestUpsertPolicyAndCache(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e)
	q := db.Insert(&testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done)
	// The admin is unrestricted: no condition, and the shape differs.
	if err := q.Exec(as("admin", 7)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(e.SQL, "WHERE") {
		t.Errorf("admin: %s", e.SQL)
	}
	if err := q.Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `WHERE "tickets"."owner" = $9 AND "tickets"."owner" = $10`) {
		t.Errorf("ann: %s", e.SQL)
	}
	if err := q.Exec(as("admin", 7)); err != nil || strings.Contains(e.SQL, "WHERE") {
		t.Errorf("admin again: %v %s", err, e.SQL)
	}
	// A policy that denies denies the upsert, as an update.
	err := q.Exec(velox.WithTenant(context.Background(), int64(7)))
	wantPolicyError(t, err, velox.OpInsert) // the Insert policy runs first
	// WithoutPolicy: no condition.
	if err := q.Exec(velox.WithoutPolicy(as("ann", 7), "sync")); err != nil || strings.Contains(e.SQL, "WHERE") {
		t.Errorf("WithoutPolicy: %v %s", err, e.SQL)
	}
}

// TestUpsertDeniedRead: a Read policy that denies is the upsert's denial, as
// an update's.
func TestUpsertDeniedRead(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(5)}}}
	db := velox.OpenFake(velox.Postgres, e)
	// Ticket's Insert policy denies whoever its Read denies, so the fixture
	// pnote, which has no Insert policy, shows Read's denial.
	n := pnote{Owner: "ann"}
	err := db.Insert(&n).OnConflict(pn.ID).Update(pn.Owner).Exec(pnoteReading(velox.Pred[pnote]{}, velox.Deny("no")))
	wantPolicyError(t, err, velox.OpUpdate)
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

func TestUpsertOnMySQL(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.MySQL, e)
	tk := testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}
	q := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done)
	err := q.Exec(as("ann", 7))
	if !errors.Is(err, velox.ErrTenantUpsert) || !strings.Contains(err.Error(), "primary key (id)") {
		t.Errorf("tenant 7: %v", err)
	}
	all := testmodel.WithUser(velox.WithAllTenants(context.Background(), "sync"), "ann")
	tk.OrgID = 7
	if err := q.Exec(all); !errors.Is(err, velox.ErrPolicyUpsert) ||
		!strings.HasPrefix(err.Error(), "velox: testmodel.Ticket: upsert cannot apply the policy") {
		t.Errorf("WithAllTenants: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	if err := q.Exec(velox.WithoutPolicy(all, "sync")); err != nil {
		t.Errorf("WithAllTenants and WithoutPolicy: %v", err)
	}
	if e.Calls != 1 || strings.Contains(e.SQL, "WHERE") {
		t.Errorf("%d statements sent: %s", e.Calls, e.SQL)
	}
}

func TestUpsertDoNothingIsAllowed(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite, velox.MySQL} {
		e := &velox.FakeExec{}
		db := velox.OpenFake(d, e)
		tk := testmodel.Ticket{Owner: "ann", Title: "t"}
		if err := db.Insert(&tk).OnConflict(Tk.ID).DoNothing().Exec(as("ann", 7)); err != nil {
			t.Errorf("%s: %v", d, err)
		}
		if strings.Contains(e.SQL, "WHERE") {
			t.Errorf("%s: %s", d, e.SQL)
		}
	}
}

func TestUpsertOfAHiddenRow(t *testing.T) {
	// One row, no generated key: the affected count tells.
	e := &velox.FakeExec{Affected: 0}
	db := velox.OpenFake(velox.Postgres, e)
	tk := testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}
	err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7))
	wantPolicyError(t, err, velox.OpUpdate)
	if want := "velox: testmodel.Ticket: update denied by policy: the existing row is hidden from this upsert"; err.Error() != want {
		t.Errorf("got %q", err)
	}
	e.Affected = 1
	if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
		t.Errorf("affected 1: %v", err)
	}
	// With RETURNING: no row back.
	e.Rows = nil
	tk = testmodel.Ticket{Owner: "ann", Title: "t"}
	wantPolicyError(t, db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)), velox.OpUpdate)
	// InsertAll: two rows sent, one affected.
	e.Affected = 1
	two := []testmodel.Ticket{{Owner: "ann", Title: "a"}, {Owner: "ann", Title: "b"}}
	wantPolicyError(t, db.InsertAll(two).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)), velox.OpUpdate)
	// No condition was added for the admin: fewer rows is not an error.
	e.Affected = 0
	if err := db.InsertAll(two).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("admin", 7)); err != nil {
		t.Errorf("admin: %v", err)
	}
	// Nor is a DoNothing that kept the existing rows.
	if err := db.InsertAll(two).OnConflict(Tk.OrgID, Tk.Title).DoNothing().Exec(as("ann", 7)); err != nil {
		t.Errorf("DoNothing: %v", err)
	}
}

// TestUpsertNeverChangesTheQuery: the condition goes on a copy of the
// clause, on both paths, so the query stays reusable by another user.
func TestUpsertNeverChangesTheQuery(t *testing.T) {
	around := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		return next(ctx)
	})
	for _, opts := range [][]velox.Option{nil, {around}} {
		e := &velox.FakeExec{Affected: 1}
		db := velox.OpenFake(velox.Postgres, e, opts...)
		q := db.Insert(&testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done)
		for range 2 {
			if err := q.Exec(as("ann", 7)); err != nil {
				t.Fatal(err)
			}
			if strings.Count(e.SQL, "WHERE") != 1 || len(e.Args) != 10 {
				t.Errorf("hooks %d: %s %v", len(opts), e.SQL, e.Args)
			}
		}
		if err := q.Exec(as("admin", 7)); err != nil || strings.Contains(e.SQL, "WHERE") {
			t.Errorf("hooks %d: admin after ann: %v %s", len(opts), err, e.SQL)
		}
	}
}

// TestInsertRowsAreCheckedAfterTheHooks: a Rewrite hook that moves a row to
// another tenant is refused.
func TestInsertRowsAreCheckedAfterTheHooks(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(5)}}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Op == velox.OpInsert {
			st.Rows[0][1] = int64(8) // org_id
		}
		return nil
	}))
	tk := testmodel.Ticket{Owner: "ann", Title: "t"}
	if err := db.Insert(&tk).Exec(as("ann", 7)); !errors.Is(err, velox.ErrTenantMismatch) {
		t.Errorf("got %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestUpsertConditionFromAHook: a Rewrite hook's upsert condition is checked,
// and refused on MySQL, which cannot render one.
func TestUpsertConditionFromAHook(t *testing.T) {
	cond := func(p velox.Predicate) velox.Option {
		return velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Conflict != nil {
				st.Conflict.Where = append(st.Conflict.Where, p)
			}
			return nil
		})
	}
	ctx := velox.WithoutPolicy(as("ann", 7), "hooks")
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.MySQL, e, cond(velox.ColumnEQ(3, "ann")))
	tk := testmodel.Ticket{ID: 1, OrgID: 7, Owner: "ann", Title: "t"}
	all := velox.WithoutPolicy(velox.WithAllTenants(context.Background(), "sync"), "sync")
	if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(all); err == nil || !strings.Contains(err.Error(), "takes none") {
		t.Errorf("MySQL: %v", err)
	}
	db = velox.OpenFake(velox.Postgres, e, cond(velox.ColumnEQ(99, "x")))
	if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(ctx); err == nil || !strings.Contains(err.Error(), "column 99") {
		t.Errorf("a bad column: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	// On PostgreSQL it is rendered, and a hidden row is no policy's denial.
	e.Affected = 0
	db = velox.OpenFake(velox.Postgres, e, cond(velox.ColumnEQ(3, "ann")))
	if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(ctx); err != nil || !strings.HasSuffix(e.SQL, `WHERE "tickets"."owner" = $9`) {
		t.Errorf("%v %s", err, e.SQL)
	}
}

// TestUpsertConditionFromAHookIsScoped: an EXISTS a Rewrite hook puts in the
// update branch's condition gets its target's tenant, as one in a Where does.
func TestUpsertConditionFromAHookIsScoped(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Conflict != nil {
			st.Conflict.Where = append(st.Conflict.Where, velox.Predicate(Tk.Invoice.Has()))
		}
		return nil
	}))
	tk := testmodel.Ticket{ID: 1, Owner: "ann", Title: "t", InvoiceID: 3}
	if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `INSERT INTO "tickets" (`+ticketCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" WHERE EXISTS (SELECT 1 FROM "invoices" AS "vx1" WHERE "vx1"."id" = "tickets"."invoice_id" AND "org_id" = $9) AND "tickets"."owner" = $10 AND "tickets"."owner" = $11`,
		int64(1), "ann", int64(7), "ann", "t", false, int64(3), (*int64)(nil), int64(7), "ann", "ann")
}

// TestUpsertConditionThatWouldBeDropped: a Rewrite hook's condition on an
// upsert that keeps the existing row, which renders no update branch, is an
// error rather than silently dropped.
func TestUpsertConditionThatWouldBeDropped(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Conflict != nil {
			st.Conflict.Where = append(st.Conflict.Where, velox.Predicate(Tk.Owner.EQ("ann")))
		}
		return nil
	}))
	ctx := velox.WithoutPolicy(as("ann", 7), "hooks")
	tk := testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}
	for name, q := range map[string]velox.InsertQuery[testmodel.Ticket]{
		"DoNothing":    db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).DoNothing(),
		"empty Update": db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(),
	} {
		err := q.Exec(ctx)
		if err == nil || err.Error() != "velox: testmodel.Ticket: a Rewrite hook gave the upsert a condition, but the upsert keeps the existing row and has no update branch to apply it to" {
			t.Errorf("%s: %v", name, err)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestUpsertAllOfAHiddenRowRollsBack: a policy-conditioned InsertAll of
// several rows runs in a transaction, so the hidden-row denial also undoes
// the rows it wrote; SQL(ctx) says so with InTx.
func TestUpsertAllOfAHiddenRowRollsBack(t *testing.T) {
	var inTx []bool
	e := &velox.FakeExec{Affected: 2}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		inTx = append(inTx, st.InTx)
		return nil
	}))
	three := []testmodel.Ticket{{ID: 1, Owner: "ann", Title: "a"}, {ID: 2, Owner: "ann", Title: "b"}, {ID: 3, Owner: "ann", Title: "c"}}
	q := db.InsertAll(three).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done)
	text, _, err := q.SQL(as("ann", 7))
	if err != nil {
		t.Fatal(err)
	}
	wantPolicyError(t, q.Exec(as("ann", 7)), velox.OpUpdate)
	if want := []string{"BEGIN", text, "ROLLBACK"}; !reflect.DeepEqual(e.Log, want) {
		t.Errorf("log %q, want %q", e.Log, want)
	}
	if !reflect.DeepEqual(inTx, []bool{true, true}) {
		t.Errorf("InTx %v, want true under SQL(ctx) and Exec", inTx)
	}

	// No transaction when nothing conditions the upsert: one row, DoNothing,
	// WithoutPolicy, or an entity with no Read or Write policy.
	for name, q := range map[string]velox.InsertQuery[testmodel.Ticket]{
		"one row":   db.InsertAll(three[:1]).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done),
		"DoNothing": db.InsertAll(three).OnConflict(Tk.OrgID, Tk.Title).DoNothing(),
	} {
		e.Log, e.Affected = nil, 3
		if err := q.Exec(as("ann", 7)); err != nil || len(e.Log) != 1 {
			t.Errorf("%s: %v, log %q", name, err, e.Log)
		}
	}
	e.Log = nil
	if err := q.Exec(velox.WithoutPolicy(as("ann", 7), "sync")); err != nil || len(e.Log) != 1 {
		t.Errorf("WithoutPolicy: %v, log %q", err, e.Log)
	}
	e.Log = nil
	invs := []testmodel.Invoice{{ID: 1, Number: "a"}, {ID: 2, Number: "b"}}
	if err := db.InsertAll(invs).OnConflict(I.OrgID, I.Number).Update(I.Paid).Exec(velox.WithTenant(context.Background(), int64(7))); err != nil || len(e.Log) != 1 {
		t.Errorf("no policy: %v, log %q", err, e.Log)
	}
}

// TestUpsertOnMySQLNamesTheUniqueIndex: a primary key that holds the tenant
// is not enough on MySQL; a unique index without it is named.
func TestUpsertOnMySQLNamesTheUniqueIndex(t *testing.T) {
	e := &velox.FakeExec{Affected: 1}
	db := velox.OpenFake(velox.MySQL, e)
	x := tunique{ID: 1, Code: "c"}
	err := db.Insert(&x).OnConflict(tu.OrgID, tu.ID).Update(tu.Code).Exec(velox.WithTenant(context.Background(), int64(7)))
	want(t, err, velox.ErrTenantUpsert, "velox: velox_test.tunique: upsert could cross tenants: MySQL's ON DUPLICATE KEY UPDATE resolves a conflict on any unique key, and the unique index (code) does not hold the tenant column org_id")
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// ticketColumn is the index of Ticket's column name.
func ticketColumn(t *testing.T, st *velox.Statement, name string) int {
	t.Helper()
	for i, c := range st.Entity.Columns {
		if c.Name == name {
			return i
		}
	}
	t.Fatalf("no column %s", name)
	return -1
}

// TestUpsertPolicyFlagIsTheStatements: whether the scope conditioned the
// upsert on the policy is the Statement's, not the clause's, so an Around
// hook that replaces the clause with its own copy keeps the hidden-row check.
func TestUpsertPolicyFlagIsTheStatements(t *testing.T) {
	cf := reflect.TypeFor[velox.ConflictClause]()
	if cf.NumField() != 4 {
		t.Errorf("ConflictClause has %d fields, want 4", cf.NumField())
	}
	for f := range cf.Fields() {
		if !f.IsExported() {
			t.Errorf("ConflictClause has the unexported field %s", f.Name)
		}
	}
	e := &velox.FakeExec{Affected: 0}
	db := velox.OpenFake(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		c := st.Conflict
		st.Conflict = &velox.ConflictClause{Columns: slices.Clone(c.Columns), Update: slices.Clone(c.Update), Where: slices.Clone(c.Where)}
		return next(ctx)
	}))
	tk := testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}
	err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7))
	wantPolicyError(t, err, velox.OpUpdate)
}

// TestHookCannotConditionAMultiRowUpsert: a Rewrite hook that gives an
// InsertAll's DoNothing upsert, outside a transaction, an update branch the
// policy conditions is refused before anything is sent: a hidden row would
// fail the statement after the visible rows were written. Claiming the
// transaction with InTx does not get it through.
func TestHookCannotConditionAMultiRowUpsert(t *testing.T) {
	for _, c := range []struct {
		inTx bool
		want string
	}{
		{false, "velox: testmodel.Ticket: a Rewrite hook gave a multi-row upsert outside a transaction an update branch the policy conditions; a row the policy hides would fail it after the others were written"},
		{true, "velox: testmodel.Ticket: a Rewrite hook changed the statement's InTx"},
	} {
		e := &velox.FakeExec{Affected: 2}
		db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			st.Conflict.DoNothing = false
			st.Conflict.Update = []int{ticketColumn(t, st, "done")}
			if c.inTx {
				st.InTx = true
			}
			return nil
		}))
		two := []testmodel.Ticket{{ID: 1, Owner: "ann", Title: "a"}, {ID: 2, Owner: "ann", Title: "b"}}
		err := db.InsertAll(two).OnConflict(Tk.OrgID, Tk.Title).DoNothing().Exec(as("ann", 7))
		if err == nil || err.Error() != c.want {
			t.Errorf("InTx %v: got %v, want %q", c.inTx, err, c.want)
		}
		for _, s := range e.Log {
			if s == "BEGIN" || strings.HasPrefix(s, "INSERT") {
				t.Errorf("InTx %v: sent %q", c.inTx, s)
			}
		}
		if e.Calls != 0 {
			t.Errorf("InTx %v: %d statements sent", c.inTx, e.Calls)
		}
	}
}

// TestAroundHookCannotUnguardAnUpsert: the hidden-row check of a
// policy-conditioned upsert reads what velox sent, not what an Around hook
// left in the Statement: neither dropping Returning nor Rows skips it.
func TestAroundHookCannotUnguardAnUpsert(t *testing.T) {
	around := func(edit func(*velox.Statement)) velox.Option {
		return velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			edit(st)
			return next(ctx)
		})
	}
	noReturning := around(func(st *velox.Statement) { st.Returning = nil })
	noRows := around(func(st *velox.Statement) { st.Rows = nil })
	// Returning dropped: no row came back.
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{}, noReturning)
	tk := testmodel.Ticket{Owner: "ann", Title: "t"}
	wantPolicyError(t, db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)), velox.OpUpdate)
	// Rows dropped: one row sent, none affected.
	db = velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 0}, noRows)
	tk = testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}
	wantPolicyError(t, db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)), velox.OpUpdate)
	// And an InsertAll's: two rows sent, one affected.
	db = velox.OpenFake(velox.Postgres, &velox.FakeExec{Affected: 1}, noRows)
	two := []testmodel.Ticket{{ID: 1, Owner: "ann", Title: "a"}, {ID: 2, Owner: "ann", Title: "b"}}
	wantPolicyError(t, db.InsertAll(two).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)), velox.OpUpdate)
}

// TestHookCannotDropUpsertReturning: a Rewrite hook may not drop the
// RETURNING of a policy-conditioned upsert: the row read back is how a row
// the policy hides is found.
func TestHookCannotDropUpsertReturning(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{{int64(5)}}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Returning = nil
		return nil
	}))
	tk := testmodel.Ticket{Owner: "ann", Title: "t"}
	err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7))
	if want := "velox: testmodel.Ticket: a Rewrite hook dropped the RETURNING of an upsert whose update branch the policy conditions; velox reads the row back to find a row the policy hides"; err == nil || err.Error() != want {
		t.Errorf("got %v, want %q", err, want)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}
