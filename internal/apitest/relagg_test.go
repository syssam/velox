package apitest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// relAggRows reads every owner's id and its terms, by id.
func relAggRows(t *testing.T, ctx context.Context, db *velox.DB, terms ...velox.Selectable[fixture.IOwner]) map[int64][]any {
	t.Helper()
	q := db.Query[fixture.IOwner]().Select(append([]velox.Selectable[fixture.IOwner]{fixture.IOwners.ID}, terms...)...).
		OrderBy(fixture.IOwners.ID.Asc()).AllowUnbounded()
	out := map[int64][]any{}
	for r, err := range q.Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		vals := make([]any, len(terms)+1)
		ptrs := make([]any, len(vals))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := r.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out[vals[0].(int64)] = vals[1:]
	}
	return out
}

// TestRelAggregateSQL: a relation's Count and Max are subqueries correlated
// with the owner's key, holding the target's interceptor and Read policy;
// through a join entity, the target's rows the join links to the owner. A
// count is never NULL, so it orders without an IS NULL term.
func TestRelAggregateSQL(t *testing.T) {
	O, N := fixture.IOwners, fixture.INotes
	for _, tc := range []struct {
		d   velox.Dialect
		sql string
	}{
		{velox.Postgres, `SELECT "id", (SELECT COUNT(*) FROM "notes" AS "vx1" WHERE "vx1"."owner_id" = "owners"."id" AND "body" <> $1 AND NOT "hidden" AND "body" <> $2), ` +
			`(SELECT COUNT(*) FROM "notes" AS "vx1" WHERE EXISTS (SELECT 1 FROM "owner_notes" AS "vx2" WHERE "vx2"."note_id" = "vx1"."id" AND "vx2"."owner_id" = "owners"."id") AND NOT "hidden" AND "body" <> $3), ` +
			`(SELECT MAX("id") FROM "notes" AS "vx1" WHERE "vx1"."owner_id" = "owners"."id" AND NOT "hidden" AND "body" <> $4) FROM "owners" ` +
			`ORDER BY (SELECT COUNT(*) FROM "notes" AS "vx1" WHERE "vx1"."owner_id" = "owners"."id" AND NOT "hidden" AND "body" <> $5) DESC, (SELECT MAX("id") FROM "notes" AS "vx1" WHERE "vx1"."owner_id" = "owners"."id" AND NOT "hidden" AND "body" <> $6), "id" LIMIT 5`},
		{velox.MySQL, "SELECT `id`, (SELECT COUNT(*) FROM `notes` AS `vx1` WHERE `vx1`.`owner_id` = `owners`.`id` AND `body` <> ? AND `hidden` = 0 AND `body` <> ?), " +
			"(SELECT COUNT(*) FROM `notes` AS `vx1` WHERE EXISTS (SELECT 1 FROM `owner_notes` AS `vx2` WHERE `vx2`.`note_id` = `vx1`.`id` AND `vx2`.`owner_id` = `owners`.`id`) AND `hidden` = 0 AND `body` <> ?), " +
			"(SELECT MAX(`id`) FROM `notes` AS `vx1` WHERE `vx1`.`owner_id` = `owners`.`id` AND `hidden` = 0 AND `body` <> ?) FROM `owners` " +
			"ORDER BY (SELECT COUNT(*) FROM `notes` AS `vx1` WHERE `vx1`.`owner_id` = `owners`.`id` AND `hidden` = 0 AND `body` <> ?) DESC, (SELECT MAX(`id`) FROM `notes` AS `vx1` WHERE `vx1`.`owner_id` = `owners`.`id` AND `hidden` = 0 AND `body` <> ?) IS NULL, (SELECT MAX(`id`) FROM `notes` AS `vx1` WHERE `vx1`.`owner_id` = `owners`.`id` AND `hidden` = 0 AND `body` <> ?), `id` LIMIT 5"},
	} {
		db := openPolicied(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[fixture.IOwner]().
			Select(O.ID, O.Notes.Where(N.Body.NEQ("x")).Count(), O.Tagged.Count(), O.Notes.Max(N.ID)).
			OrderBy(O.Notes.Count().Desc(), O.Notes.Max(N.ID).Asc(), O.ID.Asc()).Limit(5).SQL(context.Background())
		if err != nil || text != tc.sql {
			t.Errorf("%v:\n got %s %v\nwant %s", tc.d, text, err, tc.sql)
		}
		if len(args) == 0 || args[0] != "x" {
			t.Errorf("%v: args %v", tc.d, args)
		}
	}
}

// TestRelAggregateHidesRows: a relation's aggregates count only the rows a
// query of the target reads: the interceptor hides note 2, the Read policy
// note 3, and a WithFilter filter note 1. WithoutPolicy lifts the policy
// and keeps the interceptor. Through the join entity, the notes linked to
// each owner count; with none, SUM is NULL and COUNT 0.
func TestRelAggregateHidesRows(t *testing.T) {
	O, N := fixture.IOwners, fixture.INotes
	ctx := context.Background()
	db := interceptorDB(t)
	terms := []velox.Selectable[fixture.IOwner]{O.Notes.Count(), O.Tagged.Count(), O.Notes.Sum(N.ID), O.Tagged.Sum(N.ID), O.Notes.Where(N.Body.EQ("a")).Count()}
	got := relAggRows(t, ctx, db, terms...)
	want := map[int64][]any{1: {int64(1), int64(1), int64(1), int64(1), int64(1)}, 2: {int64(1), int64(0), int64(4), nil, int64(0)}}
	for id, w := range want {
		if !equalAny(got[id], w) {
			t.Errorf("owner %d: %v, want %v", id, got[id], w)
		}
	}
	// WithoutPolicy: note 3 counts, note 2 still hidden.
	got = relAggRows(t, ctx, db.WithoutPolicy("test"), O.Notes.Count())
	if !equalAny(got[1], []any{int64(2)}) {
		t.Errorf("owner 1 without policy: %v, want [2]", got[1])
	}
	var log allFilterLog
	fdb := interceptorDB(t, velox.WithFilter(allFilter(&log)))
	got = relAggRows(t, ctx, fdb, O.Notes.Count(), O.Tagged.Count())
	if !equalAny(got[1], []any{int64(0), int64(0)}) || !equalAny(got[2], []any{int64(1), int64(0)}) {
		t.Errorf("under the filter: %v", got)
	}
	// In an expression, selected and ordered by.
	tenfold := velox.Calc[fixture.IOwner]("? * 10", O.Tagged.Count())
	got = relAggRows(t, ctx, db, tenfold)
	if !equalAny(got[1], []any{int64(10)}) || !equalAny(got[2], []any{int64(0)}) {
		t.Errorf("Calc of a count: %v", got)
	}
	os, err := db.Query[fixture.IOwner]().OrderBy(tenfold.Desc(), O.ID.Desc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "ordered by Calc of a count", ownerIDs(os), 1, 2)
	// A filter's error fails the statement, from an ordering, a result
	// column, an expression and a condition.
	for name, q := range map[string]velox.Query[fixture.IOwner]{
		"order":      fdb.Query[fixture.IOwner]().OrderBy(O.Notes.Count().Asc()),
		"select":     fdb.Query[fixture.IOwner]().Select(O.Notes.Count()),
		"expression": fdb.Query[fixture.IOwner]().OrderBy(velox.Calc[fixture.IOwner]("? + 1", O.Notes.Count()).Asc()),
		"where":      fdb.Query[fixture.IOwner]().Where(velox.Expr[fixture.IOwner]("? > 0", O.Notes.Count())),
	} {
		if _, _, err := q.AllowUnbounded().SQL(allMode(allFail)); !errors.Is(err, errAllFilter) {
			t.Errorf("a failing filter, %s: %v", name, err)
		}
	}
	// In a Where, through Expr.
	os, err = db.Query[fixture.IOwner]().Where(velox.Expr[fixture.IOwner]("? > ?", O.Tagged.Count(), 0)).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "Tagged.Count() > 0", ownerIDs(os), 1)
	os, err = db.Query[fixture.IOwner]().Where(velox.Or(O.ID.EQ(9), velox.Expr[fixture.IOwner]("? = ?", O.Notes.Count(), 1))).OrderBy(O.ID.Asc()).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, "inside Or", ownerIDs(os), 1, 2)
}

// equalAny reports whether a and b hold equal values.
func equalAny(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRelAggregateSoftDelete: a soft-deleted row is not counted, and the
// target's scope applies inside the count.
func TestRelAggregateSoftDelete(t *testing.T) {
	db := openPolicied(velox.SQLite, &fakeconn.Exec{})
	ctx := testmodel.Org.With(context.Background(), int64(1))
	text, _, err := db.Query[fixture.SOwner]().OrderBy(fixture.SOwners.Docs.Count().Desc(), fixture.SOwners.ID.Asc()).Limit(5).SQL(ctx)
	want := `SELECT "id", "name", "pinned_id" FROM "s_owners" ORDER BY (SELECT COUNT(*) FROM "s_docs" AS "vx1" WHERE "vx1"."owner_id" = "s_owners"."id" AND "deleted_at" IS NULL AND "org_id" = ?) DESC, "id" LIMIT 5`
	if err != nil || text != want {
		t.Errorf("\n got %s %v\nwant %s", text, err, want)
	}
	// Without the scope's key, the target's policy denies the statement.
	if _, _, err := db.Query[fixture.SOwner]().OrderBy(fixture.SOwners.Docs.Count().Desc()).Limit(5).SQL(context.Background()); err == nil {
		t.Error("a count over a scoped target without its key: no error")
	}
}

// TestRelAggregateAfter: After pages over a relation's count with ties,
// broken by the key, both ways; the After row's count is the subquery bound
// to its key.
func TestRelAggregateAfter(t *testing.T) {
	O := fixture.IOwners
	ctx := context.Background()
	db := interceptorDB(t)
	// Visible notes: owner 1 has 1, owner 2 has 1: a tie, broken by id.
	for _, tc := range []struct {
		order []velox.Order[fixture.IOwner]
		want  []int64
	}{
		{[]velox.Order[fixture.IOwner]{O.Notes.Count().Desc(), O.ID.Asc()}, []int64{1, 2}},
		{[]velox.Order[fixture.IOwner]{O.Notes.Count().Asc(), O.ID.Desc()}, []int64{2, 1}},
		{[]velox.Order[fixture.IOwner]{O.Tagged.Count().Asc(), O.ID.Asc()}, []int64{2, 1}},
		{[]velox.Order[fixture.IOwner]{O.Tagged.Count().Desc(), O.ID.Asc()}, []int64{1, 2}},
		{[]velox.Order[fixture.IOwner]{O.Tagged.Sum(fixture.INotes.ID).Asc(), O.ID.Asc()}, []int64{1, 2}},  // NULL last
		{[]velox.Order[fixture.IOwner]{O.Tagged.Sum(fixture.INotes.ID).Desc(), O.ID.Asc()}, []int64{2, 1}}, // NULL first
	} {
		q := db.Query[fixture.IOwner]().OrderBy(tc.order...)
		var got []int64
		page, err := q.Limit(1).All(ctx)
		for ; err == nil && len(page) > 0 && len(got) < 5; page, err = q.After(page[0]).Limit(1).All(ctx) {
			got = append(got, page[0].ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, "paged", got, tc.want...)
	}
	// An ordering by an expression of a count still cannot be paged.
	x := velox.Calc[fixture.IOwner]("? + 1", O.Notes.Count())
	if _, err := db.Query[fixture.IOwner]().OrderBy(x.Asc(), O.ID.Asc()).After(&fixture.IOwner{ID: 1}).Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "expression") {
		t.Errorf("After an expression of a count: %v", err)
	}
	// With Columns, the relation's key must be read.
	if _, err := db.Query[fixture.IOwner]().Columns(O.Name).OrderBy(O.Notes.Count().Asc(), O.ID.Asc()).After(&fixture.IOwner{ID: 1}).Limit(1).All(ctx); err == nil {
		t.Error("After without the relation's key selected: no error")
	}
}

// TestRelAggregateRefusals: a relation's aggregate is a value of each row:
// it cannot be grouped by, selected beside an aggregate or GroupBy,
// aggregated, assigned, or put in a Having; a relation's Where error fails
// the statement.
func TestRelAggregateRefusals(t *testing.T) {
	O, N := fixture.IOwners, fixture.INotes
	ctx := context.Background()
	db := openPolicied(velox.Postgres, &fakeconn.Exec{})
	q := db.Query[fixture.IOwner]().Limit(5)
	cases := map[string]velox.Query[fixture.IOwner]{
		"group by":     q.Select(O.Notes.Count()).GroupBy(O.Notes.Count()),
		"beside group": q.Select(O.Name, O.Notes.Count()).GroupBy(O.Name),
		"beside agg":   q.Select(velox.CountAll[fixture.IOwner](), O.Notes.Count()),
		"aggregated":   q.Select(O.ID).OrderBy(velox.Calc[fixture.IOwner]("?", O.Notes.Count()).Sum().Asc()),
		"having":       q.Select(O.Name).GroupBy(O.Name).Having(velox.Expr[fixture.IOwner]("? > 0", O.Notes.Count())),
		"bad where":    q.OrderBy(O.Notes.Where(velox.Pred[fixture.INote]{}).Count().Asc()),
		"zero sum":     q.OrderBy(O.Notes.Sum(nil).Asc()),
		"foreign col":  q.OrderBy(O.Notes.Sum(O.ID).Asc()),
		"by name":      q.OrderBy(O.Notes.Where(velox.Pred[fixture.INote](velox.FieldEQ("body", "a"))).Count().Asc()),
	}
	for name, c := range cases {
		if _, _, err := c.SQL(ctx); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	_ = N
	inv := db.Update[testmodel.Invoice]().Set(velox.SetExpr(I.Total, "?", I.Items.Count())).Where(I.ID.EQ(1))
	if _, _, err := inv.SQL(ctx); err == nil || !strings.Contains(err.Error(), "relation's aggregate") {
		t.Errorf("SetExpr of a count: %v", err)
	}
}

// TestRelAggregateCacheKey: the render cache tells apart counts that differ
// only in their conditions, the scope's included.
func TestRelAggregateCacheKey(t *testing.T) {
	O, N := fixture.IOwners, fixture.INotes
	var log allFilterLog
	db := openPolicied(velox.Postgres, &fakeconn.Exec{}, velox.WithFilter(allFilter(&log)))
	q := db.Query[fixture.IOwner]().Limit(5)
	seen := map[string]string{}
	for name, run := range map[string]func() (string, []any, error){
		"plain": func() (string, []any, error) { return q.OrderBy(O.Notes.Count().Asc()).SQL(context.Background()) },
		"where": func() (string, []any, error) {
			return q.OrderBy(O.Notes.Where(N.Body.EQ("a")).Count().Asc()).SQL(context.Background())
		},
		"allow":      func() (string, []any, error) { return q.OrderBy(O.Notes.Count().Asc()).SQL(allMode(allNone)) },
		"sum":        func() (string, []any, error) { return q.OrderBy(O.Notes.Sum(N.ID).Asc()).SQL(context.Background()) },
		"other rel":  func() (string, []any, error) { return q.OrderBy(O.Tagged.Count().Asc()).SQL(context.Background()) },
		"projection": func() (string, []any, error) { return q.Select(O.Notes.Count()).SQL(context.Background()) },
	} {
		for range 2 { // the second run is a hit
			text, _, err := run()
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if prev, ok := seen[text]; ok && prev != name {
				t.Errorf("%s and %s render alike: %s", name, prev, text)
			}
			seen[text] = name
		}
	}
	if len(seen) != 6 {
		t.Errorf("%d texts, want 6", len(seen))
	}
}

// TestRelAggregateUntyped: Table.RelAgg is Rel's aggregates untyped: an
// ordering by it pages with After, and a condition by name is refused.
func TestRelAggregateUntyped(t *testing.T) {
	ctx := context.Background()
	db := interceptorDB(t)
	own, notes := untyped.TableOf[fixture.IOwner](), untyped.TableOf[fixture.INote]()
	tagged := own.RelAgg(2, notes, untyped.CountAll())
	q := untyped.Query(db, own).OrderBy(untyped.OrderByTerm(tagged, false), velox.OrderTerm{Column: 0, Desc: true})
	rows, err := q.Limit(1).All(ctx)
	if err != nil || rows.Len() != 1 {
		t.Fatal(rows, err)
	}
	first := rows.At(0).(*fixture.IOwner)
	rows, err = q.After(first).Limit(5).All(ctx)
	if err != nil || rows.Len() != 1 || first.ID != 2 || rows.At(0).(*fixture.IOwner).ID != 1 {
		t.Errorf("paged: %v then %v, %v", first.ID, rows, err)
	}
	byName := own.RelAgg(0, notes, untyped.CountAll(), velox.FieldEQ("body", "a"))
	if _, err := untyped.Query(db, own).OrderBy(untyped.OrderByTerm(byName, false)).AllowUnbounded().All(ctx); err == nil {
		t.Error("a condition by name: no error")
	}
}
