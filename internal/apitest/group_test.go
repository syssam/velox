package apitest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/internal/testmodel/kind"
	"github.com/syssam/velox/scope"
)

func TestGroupedSQL(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1), int64(30)}, {int64(2), int64(50)}}}
	db := openFake(velox.Postgres, e)
	// Everything but the conditions is ignored.
	q := db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Desc()).Limit(5).Offset(2).Columns(I.Number).Unbounded()
	check := func(sel string) {
		t.Helper()
		want := `SELECT "org_id", ` + sel + ` FROM "invoices" WHERE "paid" = $1 GROUP BY "org_id" ORDER BY "org_id"`
		if e.SQL != want || !reflect.DeepEqual(e.Args, []any{true}) {
			t.Errorf("sent %s %v, want %s", e.SQL, e.Args, want)
		}
	}
	key := I.OrgID.Key()
	counts, err := velox.CountBy(ctx, q, key)
	if want := []velox.Group[int64, int64]{{Key: 1, Value: 30}, {Key: 2, Value: 50}}; err != nil || !reflect.DeepEqual(counts, want) {
		t.Errorf("CountBy: %v, %v", counts, err)
	}
	check("COUNT(*)")
	e.Rows = [][]any{{int64(1), 30.0}, {int64(2), 50.0}}
	sums, err := velox.SumBy(ctx, q, key, I.Total)
	if want := []velox.Group[int64, float64]{{Key: 1, Value: 30}, {Key: 2, Value: 50}}; err != nil || !reflect.DeepEqual(sums, want) {
		t.Errorf("SumBy: %v, %v", sums, err)
	}
	check(`SUM("total")`)
	if got, err := velox.AvgBy(ctx, q, key, I.Total); err != nil || len(got) != 2 || got[1].Value != 50 {
		t.Errorf("AvgBy: %v, %v", got, err)
	}
	check(`AVG("total")`)
	if got, err := velox.MinBy(ctx, q, key, I.Total); err != nil || len(got) != 2 || got[0].Value != 30 {
		t.Errorf("MinBy: %v, %v", got, err)
	}
	check(`MIN("total")`)
	if got, err := velox.MaxBy(ctx, q, key, I.Total); err != nil || len(got) != 2 || got[0].Value != 30 {
		t.Errorf("MaxBy: %v, %v", got, err)
	}
	check(`MAX("total")`)

	// No row, no group.
	e.Rows = nil
	if got, err := velox.CountBy(ctx, q, key); err != nil || len(got) != 0 {
		t.Errorf("no row: %v, %v", got, err)
	}
}

// K follows the handle: a named type as itself, a nullable column as a pointer.
func TestGroupKeyTypes(t *testing.T) {
	S := testmodel.Samples
	e := &fakeconn.Exec{Rows: [][]any{{"new", int64(2)}}}
	q := openFake(velox.Postgres, e).Query[testmodel.Sample]().Unbounded()
	if got, err := velox.CountBy(ctx, q, S.Status.Key()); err != nil || !reflect.DeepEqual(got, []velox.Group[kind.Status, int64]{{Key: "new", Value: 2}}) {
		t.Errorf("a named key: %v, %v", got, err)
	}
	e.Rows = [][]any{{nil, int64(1)}, {"new", int64(2)}}
	got, err := velox.CountBy(ctx, q, S.Prev.Key())
	if err != nil || len(got) != 2 || got[0].Key != nil || got[1].Key == nil || *got[1].Key != "new" || got[1].Value != 2 {
		t.Errorf("a nullable key: %v, %v", got, err)
	}
	e.Rows = [][]any{{true, int64(350)}}
	if got, err := velox.SumBy(ctx, q, S.Flag.Key(), S.Price); err != nil || !reflect.DeepEqual(got, []velox.Group[bool, kind.Cents]{{Key: true, Value: 350}}) {
		t.Errorf("a named value: %v, %v", got, err)
	}
}

// A group of only NULLs sums to 0, as Sum does, and has no entry from the
// others, which report no value for it, as Avg, Min and Max do.
func TestGroupOfNulls(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1), nil}, {int64(2), 7.0}}}
	q := openFake(velox.Postgres, e).Query[testmodel.Invoice]().Unbounded()
	key := I.OrgID.Key()
	if got, err := velox.SumBy(ctx, q, key, I.Total); err != nil || !reflect.DeepEqual(got, []velox.Group[int64, float64]{{Key: 1, Value: 0}, {Key: 2, Value: 7}}) {
		t.Errorf("SumBy: %v, %v", got, err)
	}
	want := []velox.Group[int64, float64]{{Key: 2, Value: 7}}
	if got, err := velox.AvgBy(ctx, q, key, I.Total); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("AvgBy: %v, %v", got, err)
	}
	if got, err := velox.MinBy(ctx, q, key, I.Total); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("MinBy: %v, %v", got, err)
	}
	if got, err := velox.MaxBy(ctx, q, key, I.Total); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("MaxBy: %v, %v", got, err)
	}
}

// The groups can be as many as the rows, so a grouped aggregate says Unbounded
// as a read does; a Limit, which it ignores, does not stand in for it.
func TestGroupedNeedsUnbounded(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	for name, q := range map[string]velox.Query[testmodel.Invoice]{"plain": db.Query[testmodel.Invoice](), "limited": db.Query[testmodel.Invoice]().Limit(5)} {
		if _, err := velox.CountBy(ctx, q, I.OrgID.Key()); !errors.Is(err, velox.ErrUnbounded) || !strings.Contains(err.Error(), "grouped") {
			t.Errorf("%s CountBy: %v", name, err)
		}
		if _, err := velox.SumBy(ctx, q, I.OrgID.Key(), I.Total); !errors.Is(err, velox.ErrUnbounded) {
			t.Errorf("%s SumBy: %v", name, err)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// Groups are rows: the cap sends a LIMIT and fails past it, and the query's
// MaxRows overrides the DB's.
func TestGroupsAreRowCapped(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{int64(1), int64(1)}, {int64(2), int64(1)}, {int64(3), int64(1)}}}
	db := openFake(velox.Postgres, e, velox.WithMaxRows(2))
	q := db.Query[testmodel.Invoice]().Unbounded()
	_, err := velox.CountBy(ctx, q, I.OrgID.Key())
	if !errors.Is(err, velox.ErrTooManyRows) {
		t.Errorf("past the cap: %v", err)
	}
	wantSent(t, e, `SELECT "org_id", COUNT(*) FROM "invoices" GROUP BY "org_id" ORDER BY "org_id" LIMIT $1`, 3)
	if _, err := velox.MinBy(ctx, db.Query[testmodel.Item]().Unbounded(), testmodel.Items.InvoiceID.Key(), testmodel.Items.Qty); !errors.Is(err, velox.ErrTooManyRows) {
		t.Errorf("MinBy past the cap: %v", err)
	}
	if got, err := velox.CountBy(ctx, q.MaxRows(10), I.OrgID.Key()); err != nil || len(got) != 3 {
		t.Errorf("MaxRows(10): %v, %v", got, err)
	}
	e.Rows = e.Rows[:2]
	if got, err := velox.CountBy(ctx, q, I.OrgID.Key()); err != nil || len(got) != 2 {
		t.Errorf("at the cap: %v, %v", got, err)
	}
}

// A grouped aggregate takes the scope and the Read policy as Count does.
func TestGroupedIsScoped(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{{"ann", int64(3)}}}
	db := openPolicied(velox.Postgres, e)
	q := db.Query[testmodel.Ticket]().Where(Tk.Done.EQ(false)).Unbounded()
	got, err := velox.SumBy(asUser("ann"), q, Tk.Owner.Key(), Tk.InvoiceID)
	if err != nil || !reflect.DeepEqual(got, []velox.Group[string, int64]{{Key: "ann", Value: 3}}) {
		t.Fatalf("%v, %v", got, err)
	}
	wantSent(t, e, `SELECT "owner", SUM("invoice_id") FROM "tickets" WHERE "done" = $1 AND "org_id" = $2 AND "owner" = $3 GROUP BY "owner" ORDER BY "owner"`, false, int64(1), "ann")
	if _, err := velox.CountBy(asUser("ann"), q, Tk.Owner.Key()); err != nil {
		t.Fatal(err)
	}
	wantSent(t, e, `SELECT "owner", COUNT(*) FROM "tickets" WHERE "done" = $1 AND "org_id" = $2 AND "owner" = $3 GROUP BY "owner" ORDER BY "owner"`, false, int64(1), "ann")

	// No org, no statement; a Read policy that refuses, no statement.
	calls := e.Calls
	if _, err := velox.CountBy(context.Background(), q, Tk.Owner.Key()); !errors.Is(err, scope.ErrNoScope) {
		t.Errorf("no org: %v", err)
	}
	if _, err := velox.CountBy(asUser(""), q, Tk.Owner.Key()); !errors.Is(err, velox.ErrDenied) {
		t.Errorf("no user: %v", err)
	}
	if e.Calls != calls {
		t.Errorf("%d statements sent", e.Calls-calls)
	}
}

func TestGroupedRefusals(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	after := db.Query[testmodel.Invoice]().After(&testmodel.Invoice{ID: 3}).Unbounded()
	if _, err := velox.CountBy(ctx, after, I.OrgID.Key()); err == nil || !strings.Contains(err.Error(), "After needs an OrderBy") {
		t.Errorf("After: %v", err)
	}
	err := db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		_, err := velox.CountBy(ctx, tx.Query[testmodel.Invoice]().ForUpdate().Unbounded(), I.OrgID.Key())
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "cannot lock rows") {
		t.Errorf("a lock: %v", err)
	}
	// An encoded column has no Key; a handle made by hand does not get past
	// the rendering.
	bad := velox.Col[fixture.JDoc, []string]{I: fixture.JDocs.Tags.I}
	_, err = velox.CountBy(ctx, db.Query[fixture.JDoc]().Unbounded(), bad.Key())
	if err == nil || !strings.Contains(err.Error(), "cannot group by") {
		t.Errorf("an encoded key: %v", err)
	}
}

// A Rewrite hook may not change the grouping, in either direction, nor the
// Aggregate or Count beside it.
func TestRewriteMayNotChangeGroupBy(t *testing.T) {
	const want = "velox: testmodel.Invoice: a Rewrite hook changed the statement's GroupBy"
	e := &fakeconn.Exec{Rows: [][]any{{int64(1), int64(1)}}}
	move := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { st.GroupBy = 1; return nil })
	drop := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { st.GroupBy = 0; return nil })
	wipe := velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		*st = velox.Statement{Op: st.Op, Entity: st.Entity}
		return nil
	})
	for name, o := range map[string]velox.Option{"moves": move, "drops": drop} {
		q := openFake(velox.Postgres, e, o).Query[testmodel.Invoice]().Unbounded()
		if _, err := velox.SumBy(ctx, q, I.OrgID.Key(), I.Total); err == nil || err.Error() != want {
			t.Errorf("SumBy, a hook that %s: %v", name, err)
		}
		if _, err := velox.CountBy(ctx, q, I.OrgID.Key()); err == nil || err.Error() != want {
			t.Errorf("CountBy, a hook that %s: %v", name, err)
		}
	}
	// Setting it on a plain read or Count is refused too.
	q := openFake(velox.Postgres, e, move).Query[testmodel.Invoice]()
	if _, err := q.Count(ctx); err == nil || err.Error() != want {
		t.Errorf("Count: %v", err)
	}
	if _, err := q.Limit(1).All(ctx); err == nil || err.Error() != want {
		t.Errorf("All: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
	wq := openFake(velox.Postgres, e, wipe).Query[testmodel.Invoice]().Unbounded()
	if _, err := velox.CountBy(ctx, wq, I.OrgID.Key()); err == nil {
		t.Error("a hook that wipes the statement: no error")
	}
	// An unrelated hook passes, and an Around hook that retries reads anew.
	retry := velox.WithAround(func(c context.Context, _ *velox.Statement, next func(context.Context) error) error {
		if err := next(c); err != nil {
			return err
		}
		return next(c)
	})
	nop := velox.WithRewrite(func(context.Context, *velox.Statement) error { return nil })
	hq := openFake(velox.Postgres, e, nop, retry).Query[testmodel.Invoice]().Unbounded()
	if got, err := velox.CountBy(ctx, hq, I.OrgID.Key()); err != nil || !reflect.DeepEqual(got, []velox.Group[int64, int64]{{Key: 1, Value: 1}}) {
		t.Errorf("hooks: %v, %v", got, err)
	}
	// The hooks see the key, and the summary's readers the plain SELECT.
	var seen velox.Statement
	see := velox.WithAround(func(c context.Context, st *velox.Statement, next func(context.Context) error) error {
		err := next(c)
		seen = *st
		return err
	})
	if _, err := velox.CountBy(ctx, openFake(velox.Postgres, e, see).Query[testmodel.Invoice]().Unbounded(), I.OrgID.Key()); err != nil {
		t.Fatal(err)
	}
	if !seen.Count || seen.GroupBy != int(I.OrgID.I)+1 || seen.Result.Rows != 1 || seen.Op != velox.OpSelect {
		t.Errorf("the hook saw %+v", seen)
	}
}
