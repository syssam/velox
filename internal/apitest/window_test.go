package apitest

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestWindowCountOverGroupByLimit: COUNT(*) OVER () beside a GroupBy and a
// Limit counts every group, not only the page, on a real SQLite database:
// a page of a grouped report and its total in one statement. The Read
// policy, scope and Where still decide the rows, as for any projection.
func TestWindowCountOverGroupByLimit(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	if _, err := velox.Pool(db).Exec(`INSERT INTO invoices (id, org_id, number, total, paid, note, data) VALUES (4, 2, 'd', 0, false, '', x''), (5, 3, 'e', 0, true, '', x'')`); err != nil {
		t.Fatal(err)
	}
	type row struct{ org, n, groups int64 }
	read := func(q velox.Query[inv]) []row {
		t.Helper()
		var got []row
		for r, err := range q.Rows(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			var x row
			if err := r.Scan(&x.org, &x.n, &x.groups); err != nil {
				t.Fatal(err)
			}
			got = append(got, x)
		}
		return got
	}
	page := db.Query[inv]().
		Select(I.OrgID, velox.CountAll[inv](), velox.Calc[inv]("COUNT(*) OVER ()")).
		GroupBy(I.OrgID).OrderBy(I.OrgID.Asc()).Limit(2)
	if got := read(page); len(got) != 2 || got[0] != (row{1, 3, 3}) || got[1] != (row{2, 1, 3}) {
		t.Errorf("got %v, want [{1 3 3} {2 1 3}]", got)
	}
	// Where narrows the rows before the window counts the groups.
	if got := read(page.Where(I.Paid.EQ(false))); len(got) != 2 || got[0].groups != 2 {
		t.Errorf("with Where: got %v, want 2 groups", got)
	}

	// SelectRaw with PARTITION BY and ORDER BY: each row's rank in its org.
	var ranks []int64
	q := db.Query[inv]().Select(I.ID).
		SelectRaw("ROW_NUMBER() OVER (PARTITION BY ? ORDER BY ? DESC)", I.OrgID, I.ID).
		OrderBy(I.ID.Asc()).Limit(10)
	for r, err := range q.Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		var id, rank int64
		if err := r.Scan(&id, &rank); err != nil {
			t.Fatal(err)
		}
		ranks = append(ranks, rank)
	}
	if want := []int64{3, 2, 1, 1, 1}; !slices.Equal(ranks, want) {
		t.Errorf("ranks %v, want %v", ranks, want)
	}
}

// TestWindowSQL: a window function in a Calc is written as its text, in
// parentheses, in each dialect, and ordering by one is allowed.
func TestWindowSQL(t *testing.T) {
	for _, tc := range []struct {
		d    velox.Dialect
		want string
	}{
		{velox.Postgres, `SELECT "org_id", (COUNT(*) OVER ()), (RANK() OVER (ORDER BY SUM("total") DESC)) FROM "invoices" GROUP BY "org_id" ORDER BY (RANK() OVER (ORDER BY SUM("total") DESC)) LIMIT 5`},
		{velox.MySQL, "SELECT `org_id`, (COUNT(*) OVER ()), (RANK() OVER (ORDER BY SUM(`total`) DESC)) FROM `invoices` GROUP BY `org_id` ORDER BY (RANK() OVER (ORDER BY SUM(`total`) DESC)) IS NULL, (RANK() OVER (ORDER BY SUM(`total`) DESC)) LIMIT 5"},
		{velox.SQLite, `SELECT "org_id", (COUNT(*) OVER ()), (RANK() OVER (ORDER BY SUM("total") DESC)) FROM "invoices" GROUP BY "org_id" ORDER BY (RANK() OVER (ORDER BY SUM("total") DESC)) IS NULL, (RANK() OVER (ORDER BY SUM("total") DESC)) LIMIT 5`},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		rank := velox.Calc[inv]("RANK() OVER (ORDER BY ? DESC)", I.Total.Sum())
		got, _, err := db.Query[inv]().
			Select(I.OrgID, velox.Calc[inv]("COUNT(*) OVER ()"), rank).
			GroupBy(I.OrgID).OrderBy(rank.Asc()).Limit(5).SQL(ctx)
		if err != nil || got != tc.want {
			t.Errorf("%v:\n got %s %v\nwant %s", tc.d, got, err, tc.want)
		}
	}
}

// TestWindowRefused: a window function in a condition (Where, Having), in
// a GroupBy, under an aggregate or in an UPDATE's SetExpr fails before
// anything is sent, as SQL refuses each; inside OVER, a subquery and a
// refused function stay refused.
func TestWindowRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	win := velox.Calc[inv]("COUNT(*) OVER ()")
	sel := func() velox.Query[inv] { return db.Query[inv]().Select(I.OrgID).GroupBy(I.OrgID).Limit(5) }
	for _, tc := range []struct {
		name string
		run  func() error
		want string
	}{
		{"Where text", func() error {
			_, err := db.Query[inv]().Where(velox.Expr[inv]("COUNT(*) OVER () > ?", 1)).Limit(1).All(ctx)
			return err
		}, "window function"},
		{"Where term", func() error {
			_, err := db.Query[inv]().Where(velox.Expr[inv]("? > ?", win, 1)).Limit(1).All(ctx)
			return err
		}, "window function"},
		{"Having", func() error { return rowsErr(sel().Having(velox.Expr[inv]("? > ?", win, 1))) }, "window function"},
		{"GroupBy", func() error { return rowsErr(db.Query[inv]().Select(win).GroupBy(win).Limit(5)) }, "GroupBy holds a window function"},
		{"aggregate of a window", func() error { return rowsErr(sel().Select(win.Sum())) }, "aggregate of a window function"},
		{"SetExpr", func() error {
			_, err := db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, "SUM(?) OVER ()", I.Total)).Exec(ctx)
			return err
		}, "window function"},
		{"subquery in OVER", func() error {
			return rowsErr(sel().SelectRaw("COUNT(*) OVER (PARTITION BY (SELECT 1))"))
		}, "the word SELECT is not allowed"},
		{"refused function in OVER", func() error {
			return rowsErr(sel().SelectRaw("ROW_NUMBER() OVER (ORDER BY pg_sleep(1))"))
		}, "the word pg_sleep is not allowed"},
	} {
		err := tc.run()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", tc.name, err, tc.want)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}
