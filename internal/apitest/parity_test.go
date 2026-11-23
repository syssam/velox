package apitest

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
)

// TestDistinct: Distinct renders SELECT DISTINCT for a projection and for
// Values on every dialect, keeps the policy's scope, and its shape is apart
// from the same read without it in the render cache.
func TestDistinct(t *testing.T) {
	for _, c := range []struct {
		d               velox.Dialect
		values, project string
	}{
		{velox.Postgres,
			`SELECT DISTINCT "title" FROM "tickets" WHERE "org_id" = $1 AND "owner" = $2 LIMIT 10`,
			`SELECT DISTINCT "title", "done" FROM "tickets" WHERE "org_id" = $1 AND "owner" = $2 LIMIT 10`},
		{velox.MySQL,
			"SELECT DISTINCT `title` FROM `tickets` WHERE `org_id` = ? AND `owner` = ? LIMIT 10",
			"SELECT DISTINCT `title`, `done` FROM `tickets` WHERE `org_id` = ? AND `owner` = ? LIMIT 10"},
		{velox.SQLite,
			`SELECT DISTINCT "title" FROM "tickets" WHERE "org_id" = ? AND "owner" = ? LIMIT 10`,
			`SELECT DISTINCT "title", "done" FROM "tickets" WHERE "org_id" = ? AND "owner" = ? LIMIT 10`},
	} {
		e := &fakeconn.Exec{Rows: [][]any{{"a"}}}
		db := openPolicied(c.d, e)
		q := db.Query[testmodel.Ticket]().Limit(10)
		if _, err := velox.Values(asUser("ann"), q, Tk.Title.Key()); err != nil || strings.Contains(e.SQL, "DISTINCT") {
			t.Fatalf("%v: without Distinct: %s, %v", c.d, e.SQL, err)
		}
		if _, err := velox.Values(asUser("ann"), q.Distinct(), Tk.Title.Key()); err != nil || e.SQL != c.values {
			t.Errorf("%v: Values\n got %s, %v\nwant %s", c.d, e.SQL, err, c.values)
		}
		got, _, err := q.Distinct().Select(Tk.Title, Tk.Done).SQL(asUser("ann"))
		if err != nil || got != c.project {
			t.Errorf("%v: Select\n got %s, %v\nwant %s", c.d, got, err, c.project)
		}
	}
}

// TestDistinctRefusesEntityReads: a read of entities, Count, Exists, an
// aggregate, LoadEach and SQL of such a read fail with Distinct before
// anything is sent; Count names CountDistinct.
func TestDistinctRefusesEntityReads(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	q := db.Query[testmodel.Invoice]().Distinct().Limit(5)
	refused := func(what string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "Distinct applies to a projection") || !strings.Contains(err.Error(), "CountDistinct") {
			t.Errorf("%s: %v", what, err)
		}
	}
	_, err := q.All(ctx)
	refused("All", err)
	_, err = q.First(ctx)
	refused("First", err)
	_, err = q.Count(ctx)
	refused("Count", err)
	_, err = q.Exists(ctx)
	refused("Exists", err)
	_, err = velox.Sum(ctx, q, I.Total)
	refused("Sum", err)
	for _, err := range q.Iter(ctx) {
		refused("Iter", err)
	}
	_, _, err = q.SQL(ctx)
	refused("SQL", err)
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestDistinctOrder: a Distinct read orders only by what it reads, on every
// dialect: a projection by its terms, Values by its column. Any other
// ordering fails before anything is sent, as PostgreSQL and MySQL would
// fail it, where SQLite would order by an arbitrary row of each group.
func TestDistinctOrder(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}}
		db := openFake(d, e)
		q := db.Query[testmodel.Invoice]().Distinct().Limit(5)
		refused := func(what string, err error) {
			t.Helper()
			if err == nil || !strings.Contains(err.Error(), "a Distinct read orders only by what it reads") {
				t.Errorf("%v: %s: %v", d, what, err)
			}
		}
		_, err := velox.Values(ctx, q.OrderBy(I.Number.Asc()), I.OrgID.Key())
		refused("Values by another column", err)
		_, _, err = q.Select(I.OrgID).OrderBy(I.Total.Asc()).SQL(ctx)
		refused("projection by a column it does not read", err)
		_, _, err = q.Select(I.OrgID).OrderBy(I.Total.Sum().Desc()).GroupBy(I.OrgID).SQL(ctx)
		refused("projection by an aggregate it does not read", err)
		if e.Calls != 0 {
			t.Errorf("%v: %d statements sent", d, e.Calls)
		}
		if _, err := velox.Values(ctx, q.OrderBy(I.OrgID.Desc()), I.OrgID.Key()); err != nil {
			t.Errorf("%v: Values by its column: %v", d, err)
		}
		if _, _, err := q.Select(I.OrgID, I.Total.Sum()).GroupBy(I.OrgID).OrderBy(I.Total.Sum().Desc(), I.OrgID.Asc()).SQL(ctx); err != nil {
			t.Errorf("%v: projection by its terms: %v", d, err)
		}
	}
}

// TestDistinctRewriteRefused: a Rewrite hook may not set or clear Distinct.
func TestDistinctRewriteRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
		st.Distinct = !st.Distinct
		return nil
	}))
	if _, err := db.Query[testmodel.Invoice]().Limit(1).All(ctx); err == nil || !strings.Contains(err.Error(), "changed the statement's Distinct") {
		t.Errorf("set: %v", err)
	}
	if _, err := velox.Values(ctx, db.Query[testmodel.Invoice]().Distinct().Limit(1), I.Number.Key()); err == nil || !strings.Contains(err.Error(), "changed the statement's Distinct") {
		t.Errorf("cleared: %v", err)
	}
}

// TestUpsertUpdateAll: UpdateAll's update branch writes every column but
// the key, the conflict columns and the Immutable created_at, updated_at
// among them (its Default's value; its UpdateDefault does not fire), on
// every dialect. A skipped SetPtr's column is left out, and the Version
// column is bumped, not overwritten, as on any upsert; with nothing left the upsert keeps the row (Ignore). Update
// after it replaces it, and UpdateAll after Update replaces that.
func TestUpsertUpdateAll(t *testing.T) {
	for _, c := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, `ON CONFLICT ("org_id", "number") DO UPDATE SET "updated_at" = EXCLUDED."updated_at", "total" = EXCLUDED."total", "paid" = EXCLUDED."paid", "paid_at" = EXCLUDED."paid_at", "note" = EXCLUDED."note", "data" = EXCLUDED."data" RETURNING "id"`},
		{velox.SQLite, `ON CONFLICT ("org_id", "number") DO UPDATE SET "updated_at" = EXCLUDED."updated_at", "total" = EXCLUDED."total", "paid" = EXCLUDED."paid", "paid_at" = EXCLUDED."paid_at", "note" = EXCLUDED."note", "data" = EXCLUDED."data" RETURNING "id"`},
		{velox.MySQL, "AS new ON DUPLICATE KEY UPDATE `updated_at` = new.`updated_at`, `total` = new.`total`, `paid` = new.`paid`, `paid_at` = new.`paid_at`, `note` = new.`note`, `data` = new.`data`"},
	} {
		e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}, Affected: 1, LastID: 1, HasLastID: true}
		db := openFake(c.d, e)
		inv := testmodel.Invoice{OrgID: 1, Number: "N", Total: 3}
		got, _, err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).UpdateAll().SQL(ctx)
		if err != nil || !strings.HasSuffix(got, c.want) {
			t.Errorf("%v:\n got %s, %v\nwant suffix %s", c.d, got, err, c.want)
		}
	}

	// InsertAll writes the same update branch for every row.
	for _, c := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, `($9, $10, $11, $12, $13, $14, NULL, $15, $16) ON CONFLICT ("org_id", "number") DO UPDATE SET "updated_at" = EXCLUDED."updated_at", "total" = EXCLUDED."total", "paid" = EXCLUDED."paid", "paid_at" = EXCLUDED."paid_at", "note" = EXCLUDED."note", "data" = EXCLUDED."data" RETURNING "id"`},
		{velox.SQLite, `ON CONFLICT ("org_id", "number") DO UPDATE SET "updated_at" = EXCLUDED."updated_at", "total" = EXCLUDED."total", "paid" = EXCLUDED."paid", "paid_at" = EXCLUDED."paid_at", "note" = EXCLUDED."note", "data" = EXCLUDED."data" RETURNING "id"`},
		{velox.MySQL, "AS new ON DUPLICATE KEY UPDATE `updated_at` = new.`updated_at`, `total` = new.`total`, `paid` = new.`paid`, `paid_at` = new.`paid_at`, `note` = new.`note`, `data` = new.`data`"},
	} {
		e := &fakeconn.Exec{Rows: [][]any{{int64(1)}, {int64(2)}}, Affected: 2, LastID: 1, HasLastID: true}
		db := openFake(c.d, e)
		two := []testmodel.Invoice{{OrgID: 1, Number: "A", Total: 1}, {OrgID: 1, Number: "B", Total: 2}}
		got, _, err := db.InsertAll(two).OnConflict(I.OrgID, I.Number).UpdateAll().SQL(ctx)
		if err != nil || !strings.HasSuffix(got, c.want) {
			t.Errorf("%v InsertAll:\n got %s, %v\nwant suffix %s", c.d, got, err, c.want)
		}
	}

	e := &fakeconn.Exec{Rows: [][]any{{int64(1)}}}
	db := openFake(velox.Postgres, e)
	inv := testmodel.Invoice{OrgID: 1, Number: "N"}
	var note *string
	got, _, err := db.Insert(&inv).OnConflict(I.OrgID, I.Number).UpdateAll().Set(I.Note.SetPtr(note)).SQL(ctx)
	if want := `DO UPDATE SET "updated_at" = EXCLUDED."updated_at", "total" = EXCLUDED."total", "paid" = EXCLUDED."paid", "paid_at" = EXCLUDED."paid_at", "data" = EXCLUDED."data" RETURNING "id"`; err != nil || !strings.HasSuffix(got, want) {
		t.Errorf("skipped note:\n got %s, %v", got, err)
	}
	got, _, err = db.Insert(&inv).OnConflict(I.OrgID, I.Number).UpdateAll().Update(I.Total).SQL(ctx)
	if want := `DO UPDATE SET "total" = EXCLUDED."total" RETURNING "id"`; err != nil || !strings.HasSuffix(got, want) {
		t.Errorf("Update after UpdateAll: %s, %v", got, err)
	}
	got, _, err = db.Insert(&inv).OnConflict(I.OrgID, I.Number).Update(I.Total).UpdateAll().SQL(ctx)
	if err != nil || !strings.Contains(got, `"note" = EXCLUDED."note"`) {
		t.Errorf("UpdateAll after Update: %s, %v", got, err)
	}
	got, _, err = db.Insert(&inv).OnConflict(I.OrgID, I.Number).UpdateAll().DoNothing().SQL(ctx)
	if err != nil || !strings.HasSuffix(got, `DO NOTHING RETURNING "id"`) {
		t.Errorf("DoNothing after UpdateAll: %s, %v", got, err)
	}
	if err := db.Insert(&inv).UpdateAll().Exec(ctx); err == nil || !strings.Contains(err.Error(), "an upsert needs OnConflict") {
		t.Errorf("without OnConflict: %v", err)
	}

	p := testmodel.Paper{Title: "t"}
	got, _, err = db.Insert(&p).OnConflict(testmodel.Papers.ID).UpdateAll().SQL(ctx)
	if want := `ON CONFLICT ("id") DO UPDATE SET "title" = EXCLUDED."title"`; err != nil || !strings.Contains(got, want) || strings.Contains(got, `EXCLUDED."version"`) {
		t.Errorf("Version left out: %s, %v", got, err)
	}
	got, _, err = db.Insert(&p).OnConflict(testmodel.Papers.Title).UpdateAll().SQL(ctx)
	if want := `ON CONFLICT ("title") DO UPDATE SET "title" = "papers"."title"`; err != nil || !strings.Contains(got, want) {
		t.Errorf("nothing left, Ignore: %s, %v", got, err)
	}
}
