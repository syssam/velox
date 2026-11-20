package apitest

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/scope"
)

// ticketInsertCols are the columns an insert of a Ticket with a generated key
// writes.
const ticketInsertCols = `"created_by", "org_id", "owner", "title", "done", "invoice_id"`

// as returns a context acting as user, scoped to the org.
func as(user string, org int64) context.Context {
	return testmodel.WithUser(testmodel.Org.With(context.Background(), org), user)
}

func wantPolicyError(t *testing.T, err error, op velox.Op) {
	t.Helper()
	var pe *velox.PolicyError
	if !errors.As(err, &pe) || pe.Op != op || !errors.Is(err, velox.ErrDenied) {
		t.Fatalf("got %v, want a *PolicyError for %s", err, op)
	}
}

func TestInsertFillsTheScope(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}}
	db := openPolicied(velox.Postgres, e)
	tk := testmodel.Ticket{Owner: "ann", Title: "t"}
	if err := db.Insert(&tk).Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	if tk.OrgID != 7 || tk.CreatedBy != "ann" || tk.ID != 5 {
		t.Errorf("OrgID %d, CreatedBy %q, ID %d", tk.OrgID, tk.CreatedBy, tk.ID)
	}
	wantSent(t, e, `INSERT INTO "tickets" (`+ticketInsertCols+`) VALUES ($1, $2, $3, $4, $5, $6) RETURNING "id"`,
		"ann", int64(7), "ann", "t", false, int64(0))
}

func TestInsertScopeMismatch(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}}
	db := openPolicied(velox.Postgres, e)
	tk := testmodel.Ticket{OrgID: 8, Owner: "ann", Title: "t"}
	err := db.Insert(&tk).Exec(as("ann", 7))
	if !errors.Is(err, scope.ErrMismatch) || !strings.HasPrefix(err.Error(), "velox: testmodel.Ticket: insert denied by policy: ") {
		t.Fatalf("got %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	// A zero OrgID is the scope's to fill.
	tk.OrgID = 0
	if err := db.Insert(&tk).Exec(testmodel.WithUser(testmodel.Org.With(context.Background(), 7), "ann")); err != nil || tk.OrgID != 7 {
		t.Errorf("a zero OrgID: %v, OrgID %d", err, tk.OrgID)
	}
}

// TestInsertWithoutPolicy: WithoutPolicy lifts the scope, so an insert names its
// own org, and keeps whatever the row says, a zero included; without a scope
// value and without WithoutPolicy an insert is refused.
func TestInsertWithoutPolicy(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}}
	db := openPolicied(velox.Postgres, e)
	all := testmodel.WithUser(context.Background(), "ann")
	tk := testmodel.Ticket{Owner: "ann", Title: "t"}
	if err := db.Insert(&tk).Exec(testmodel.WithUser(context.Background(), "ann")); !errors.Is(err, scope.ErrNoScope) {
		t.Errorf("no scope value at all: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	tk.OrgID = 3
	if err := db.WithoutPolicy("import").Insert(&tk).Exec(all); err != nil {
		t.Fatal(err)
	}
	if tk.ID != 5 || e.Args[1] != int64(3) {
		t.Errorf("ID %d, args %v", tk.ID, e.Args)
	}
}

func TestInsertHookRefusesAnEmptyTitle(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}}
	db := openPolicied(velox.Postgres, e)
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
	e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}}
	db := openPolicied(velox.Postgres, e)
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
	db = openPolicied(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		bypass = st.PolicyBypass
		return next(ctx)
	}))
	if err := db.WithoutPolicy("migration").Insert(&tk).Exec(as("bob", 7)); err != nil || bypass != "migration" {
		t.Errorf("WithoutPolicy: %v, PolicyBypass %q", err, bypass)
	}
}

func TestInsertAllPreparesEveryRow(t *testing.T) {
	e := &fakeconn.Exec{Affected: 3}
	db := openPolicied(velox.Postgres, e)
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

func TestUpsertGetsThePolicy(t *testing.T) {
	for _, c := range []struct {
		d     velox.Dialect
		where string
	}{
		{velox.Postgres, `($1, $2, $3, $4, $5, $6) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" WHERE "tickets"."org_id" = $7 AND "tickets"."owner" = $8 AND "tickets"."owner" = $9 RETURNING "id"`},
		{velox.SQLite, `(?, ?, ?, ?, ?, ?) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" WHERE "tickets"."org_id" = ? AND "tickets"."owner" = ? AND "tickets"."owner" = ? RETURNING "id"`},
	} {
		// Twice: the second statement's text comes from the render cache.
		for range 2 {
			e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}}
			db := openPolicied(c.d, e)
			tk := testmodel.Ticket{Owner: "ann", Title: "t"}
			if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
				t.Fatal(err)
			}
			wantSent(t, e, `INSERT INTO "tickets" (`+ticketInsertCols+`) VALUES `+c.where,
				"ann", int64(7), "ann", "t", false, int64(0), int64(7), "ann", "ann")
		}
	}
}

func TestUpsertPolicyAndCache(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openPolicied(velox.Postgres, e)
	q := db.Insert(&testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done)
	// The admin is confined to the org alone: no owner condition, and the shape
	// differs.
	if err := q.Exec(as("admin", 7)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(e.SQL, `WHERE "tickets"."org_id" = $8`) {
		t.Errorf("admin: %s", e.SQL)
	}
	if err := q.Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.SQL, `WHERE "tickets"."org_id" = $8 AND "tickets"."owner" = $9 AND "tickets"."owner" = $10`) {
		t.Errorf("ann: %s", e.SQL)
	}
	if err := q.Exec(as("admin", 7)); err != nil || !strings.HasSuffix(e.SQL, `WHERE "tickets"."org_id" = $8`) {
		t.Errorf("admin again: %v %s", err, e.SQL)
	}
	// A policy that denies the insert refuses the upsert: the Insert policy runs
	// before the update branch's conditions.
	err := q.Exec(testmodel.Org.With(context.Background(), int64(7)))
	wantPolicyError(t, err, velox.OpInsert) // the Insert policy runs first
	// WithoutPolicy: no condition.
	if err := db.WithoutPolicy("sync").Insert(&testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil || strings.Contains(e.SQL, "WHERE") {
		t.Errorf("WithoutPolicy: %v %s", err, e.SQL)
	}
}

// TestUpsertDeniedRead: a Read policy that denies is the upsert's denial, as
// an update's.
func TestUpsertDeniedRead(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}}
	db := openPolicied(velox.Postgres, e)
	// Ticket's Insert policy denies whoever its Read denies, so the fixture
	// pnote, which has no Insert policy, shows Read's denial.
	n := fixture.Pnote{Owner: "ann"}
	err := db.Insert(&n).OnConflict(fixture.Pn.ID).Update(fixture.Pn.Owner).Exec(pnoteReading(velox.Predicate{}, velox.Deny("no")))
	wantPolicyError(t, err, velox.OpUpdate)
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

func TestUpsertOnMySQL(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openPolicied(velox.MySQL, e)
	tk := testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}
	q := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done)
	err := q.Exec(as("ann", 7))
	if !errors.Is(err, velox.ErrPolicyUpsert) || !strings.HasPrefix(err.Error(), "velox: testmodel.Ticket: upsert cannot apply the policy") {
		t.Errorf("org 7: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	tk.OrgID = 7
	if err := db.WithoutPolicy("sync").Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
		t.Errorf("WithoutPolicy: %v", err)
	}
	if e.Calls != 1 || strings.Contains(e.SQL, "WHERE") {
		t.Errorf("%d statements sent: %s", e.Calls, e.SQL)
	}
}

func TestUpsertDoNothingIsAllowed(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.SQLite, velox.MySQL} {
		e := &fakeconn.Exec{}
		db := openPolicied(d, e)
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
	e := &fakeconn.Exec{Affected: 0}
	db := openPolicied(velox.Postgres, e)
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
	// WithoutPolicy adds no condition: fewer rows is not an error.
	e.Affected = 0
	if err := db.WithoutPolicy("sync").InsertAll(two).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("admin", 7)); err != nil {
		t.Errorf("WithoutPolicy: %v", err)
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
		e := &fakeconn.Exec{Affected: 1}
		db := openPolicied(velox.Postgres, e, opts...)
		q := db.Insert(&testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done)
		for range 2 {
			if err := q.Exec(as("ann", 7)); err != nil {
				t.Fatal(err)
			}
			if strings.Count(e.SQL, "WHERE") != 1 || len(e.Args) != 10 {
				t.Errorf("hooks %d: %s %v", len(opts), e.SQL, e.Args)
			}
		}
		if err := q.Exec(as("admin", 7)); err != nil || !strings.HasSuffix(e.SQL, `WHERE "tickets"."org_id" = $8`) {
			t.Errorf("hooks %d: admin after ann: %v %s", len(opts), err, e.SQL)
		}
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
	ctx := as("ann", 7)
	e := &fakeconn.Exec{Affected: 1}
	db := openPolicied(velox.MySQL, e, cond(velox.ColumnEQ(3, "ann"))).WithoutPolicy("hooks")
	tk := testmodel.Ticket{ID: 1, OrgID: 7, Owner: "ann", Title: "t"}
	all := context.Background()
	if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(all); err == nil || !strings.Contains(err.Error(), "takes none") {
		t.Errorf("MySQL: %v", err)
	}
	db = openPolicied(velox.Postgres, e, cond(velox.ColumnEQ(99, "x"))).WithoutPolicy("hooks")
	if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(ctx); err == nil || !strings.Contains(err.Error(), "column 99") {
		t.Errorf("a bad column: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	// On PostgreSQL it is rendered, and a hidden row is no policy's denial.
	e.Affected = 0
	db = openPolicied(velox.Postgres, e, cond(velox.ColumnEQ(3, "ann"))).WithoutPolicy("hooks")
	if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(ctx); err != nil || !strings.HasSuffix(e.SQL, `WHERE "tickets"."owner" = $8`) {
		t.Errorf("%v %s", err, e.SQL)
	}
}

// TestUpsertConditionFromAHookIsScoped: an EXISTS a Rewrite hook puts in the
// update branch's condition gets its target's org, as one in a Where does.
func TestUpsertConditionFromAHookIsScoped(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openPolicied(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Conflict != nil {
			st.Conflict.Where = append(st.Conflict.Where, velox.Predicate(Tk.Invoice.Has()))
		}
		return nil
	}))
	tk := testmodel.Ticket{ID: 1, Owner: "ann", Title: "t", InvoiceID: 3}
	if err := db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `INSERT INTO "tickets" ("id", "created_by", "org_id", "owner", "title", "done", "invoice_id") VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" WHERE EXISTS (SELECT 1 FROM "invoices" AS "vx1" WHERE "vx1"."id" = "tickets"."invoice_id" AND "org_id" = $8) AND "tickets"."org_id" = $9 AND "tickets"."owner" = $10 AND "tickets"."owner" = $11`,
		int64(1), "ann", int64(7), "ann", "t", false, int64(3), int64(7), int64(7), "ann", "ann")
}

// TestUpsertConditionThatWouldBeDropped: a Rewrite hook's condition on an
// upsert that keeps the existing row, which renders no update branch, is an
// error rather than silently dropped.
func TestUpsertConditionThatWouldBeDropped(t *testing.T) {
	e := &fakeconn.Exec{Affected: 1}
	db := openPolicied(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		if st.Conflict != nil {
			st.Conflict.Where = append(st.Conflict.Where, velox.Predicate(Tk.Owner.EQ("ann")))
		}
		return nil
	})).WithoutPolicy("hooks")
	ctx := as("ann", 7)
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
	e := &fakeconn.Exec{Affected: 2}
	db := openPolicied(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
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
		if err := q.Exec(as("ann", 7)); err != nil || len(e.Log) != 1 || e.Log[0] == "BEGIN" {
			t.Errorf("%s: %v, log %q", name, err, e.Log)
		}
	}
	e.Log = nil
	if err := db.WithoutPolicy("sync").InsertAll(three).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)); err != nil || len(e.Log) != 1 || e.Log[0] == "BEGIN" {
		t.Errorf("WithoutPolicy: %v, log %q", err, e.Log)
	}
	e.Log = nil
	items := []testmodel.Item{{ID: 1, SKU: "a"}, {ID: 2, SKU: "b"}}
	if err := db.InsertAll(items).OnConflict(It.ID).Update(It.Qty).Exec(context.Background()); err != nil || len(e.Log) != 1 {
		t.Errorf("no policy: %v, log %q", err, e.Log)
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
	e := &fakeconn.Exec{Affected: 0}
	db := openPolicied(velox.Postgres, e, velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
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
		e := &fakeconn.Exec{Affected: 2}
		db := openPolicied(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
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
	db := openPolicied(velox.Postgres, &fakeconn.Exec{}, noReturning)
	tk := testmodel.Ticket{Owner: "ann", Title: "t"}
	wantPolicyError(t, db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)), velox.OpUpdate)
	// Rows dropped: one row sent, none affected.
	db = openPolicied(velox.Postgres, &fakeconn.Exec{Affected: 0}, noRows)
	tk = testmodel.Ticket{ID: 1, Owner: "ann", Title: "t"}
	wantPolicyError(t, db.Insert(&tk).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)), velox.OpUpdate)
	// And an InsertAll's: two rows sent, one affected.
	db = openPolicied(velox.Postgres, &fakeconn.Exec{Affected: 1}, noRows)
	two := []testmodel.Ticket{{ID: 1, Owner: "ann", Title: "a"}, {ID: 2, Owner: "ann", Title: "b"}}
	wantPolicyError(t, db.InsertAll(two).OnConflict(Tk.OrgID, Tk.Title).Update(Tk.Done).Exec(as("ann", 7)), velox.OpUpdate)
}

// TestHookCannotDropUpsertReturning: a Rewrite hook may not drop the
// RETURNING of a policy-conditioned upsert: the row read back is how a row
// the policy hides is found.
func TestHookCannotDropUpsertReturning(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(5)}}}
	db := openPolicied(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
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
