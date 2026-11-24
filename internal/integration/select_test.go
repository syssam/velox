package integration

import (
	"context"
	"database/sql/driver"
	"fmt"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// rowsOf reads every row of q's projection, each as the values of its
// columns formatted with %v, joined by a space, a boolean as 1 or 0.
func rowsOf[E any](t *testing.T, ctx context.Context, q velox.Query[E], cols int) []string {
	t.Helper()
	var out []string
	for r, err := range q.Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		vals := make([]any, cols)
		ptrs := make([]any, cols)
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := r.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		s := ""
		for i, v := range vals {
			if x, ok := v.(driver.Valuer); ok { // pgx's numeric
				if dv, err := x.Value(); err == nil {
					v = dv
				}
			}
			switch x := v.(type) {
			case []byte: // a driver may send a number as text
				v = string(x)
			case bool: // SQLite and MySQL send 1 and 0
				v = map[bool]int{false: 0, true: 1}[x]
			}
			if i > 0 {
				s += " "
			}
			s += fmt.Sprint(v)
		}
		out = append(out, s)
	}
	return out
}

func wantRows(t *testing.T, name string, got, want []string) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("%s: got %q, want %q", name, got, want)
	}
}

// TestSelectRepeatedBoundTerm: a term with a bound value that is selected,
// grouped and ordered by is one expression to the database. PostgreSQL
// matches a GROUP BY expression by its text, so the same term written with
// $1 in the select list and $2 in GROUP BY failed with 42803 ("must appear
// in the GROUP BY clause"); velox writes the repeat with the placeholders of
// the first. On MySQL, whose ? cannot be repeated, the copies failed with
// error 1055 under only_full_group_by; velox writes them as ANY_VALUE.
func TestSelectRepeatedBoundTerm(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type inv = testmodel.Invoice
		bucket := velox.Calc[inv]("? * ?", I.OrgID, 10)
		wantRows(t, "bucket", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(bucket, velox.CountAll[inv]()).GroupBy(bucket).OrderBy(bucket.Desc()).Limit(10), 2),
			[]string{"20 1", "10 4"})
		// Grouped and ordered by, not selected.
		wantRows(t, "unselected", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(velox.CountAll[inv]()).GroupBy(bucket).OrderBy(bucket.Asc()).Limit(10), 1),
			[]string{"4", "1"})
		// The same text with another value is another term.
		other := velox.Calc[inv]("? * ?", I.OrgID, 100)
		wantRows(t, "other value", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(bucket, velox.CountAll[inv]()).GroupBy(bucket, other).OrderBy(bucket.Asc()).Limit(10), 2),
			[]string{"10 4", "20 1"})
		var month velox.Term[inv]
		switch tg.d {
		case velox.Postgres:
			month = velox.Calc[inv]("date_trunc('month', ? AT TIME ZONE ?)", I.CreatedAt, "Asia/Hong_Kong")
		case velox.MySQL:
			month = velox.Calc[inv]("date_format(?, ?)", I.CreatedAt, "%Y-%m")
		default:
			return
		}
		n := 0
		for _, err := range tg.db.Query[inv]().Select(month, velox.CountAll[inv]()).GroupBy(month).OrderBy(month.Asc()).Limit(10).Rows(ctx) {
			if err != nil {
				t.Fatal(err)
			}
			n++
		}
		if n == 0 {
			t.Error("date bucket: no rows")
		}
	})
}

// TestHavingRepeatedTerm: a HAVING that tests a GROUP BY key written as an
// expression runs on every dialect. On PostgreSQL a key with a bound value
// was a new expression in HAVING, its value bound again, and failed with
// 42803; velox writes it with the key's placeholders. MySQL's HAVING names
// no column outside an aggregate unless it is a key column, so a key
// expression there failed with error 1054, bound or not; velox writes it
// as MIN of the key, which is the group's value (decision 0138), but not
// inside an aggregate, marked or not (decision 0151).
func TestHavingRepeatedTerm(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type inv = testmodel.Invoice
		bucket := velox.Calc[inv]("? * ?", I.OrgID, 10)
		wantRows(t, "bound", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(bucket, velox.CountAll[inv]()).GroupBy(bucket).
			Having(velox.Expr[inv]("? > ?", bucket, 15)).OrderBy(bucket.Asc()).Limit(10), 2),
			[]string{"20 1"})
		wantRows(t, "bound, not selected", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(velox.CountAll[inv]()).GroupBy(bucket).
			Having(velox.Expr[inv]("? < ?", bucket, 15)).Limit(10), 1),
			[]string{"4"})
		next := velox.Calc[inv]("? + 1", I.OrgID)
		wantRows(t, "no value", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(next, I.Total.Sum()).GroupBy(next).
			Having(velox.Expr[inv]("? > 2", next)).Limit(10), 2),
			[]string{"3 50"})
		// A key inside a marked aggregate is its argument: MIN of it there
		// was MySQL's error 1111.
		wantRows(t, "key inside a marked aggregate", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(I.Paid, velox.CountAll[inv]()).GroupBy(next, I.Paid).
			Having(velox.Expr[inv]("? > 0", velox.Agg(velox.Calc[inv]("COUNT(DISTINCT ?)", next)))).
			OrderBy(I.Paid.Asc(), velox.CountAll[inv]().Asc()).Limit(10), 2),
			[]string{"0 2", "1 1", "1 2"})
	})
}

// TestDistinctOrderedByTerm: SELECT DISTINCT ordered by a selected
// expression runs on every dialect. MySQL refused the ordering with error
// 3065 or 3066: a copy of a term with a bound value is another expression,
// and so is an expression's IS NULL, which velox writes first; velox writes
// both as ANY_VALUE of the term (decision 0138).
func TestDistinctOrderedByTerm(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type inv = testmodel.Invoice
		bucket := velox.Calc[inv]("? * ?", I.OrgID, 10)
		wantRows(t, "bound", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(bucket).Distinct().OrderBy(bucket.Desc()).Limit(10), 1),
			[]string{"20", "10"})
		next := velox.Calc[inv]("? + 1", I.OrgID)
		wantRows(t, "no value", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(next).Distinct().OrderBy(next.Asc()).Limit(10), 1),
			[]string{"2", "3"})
		wantRows(t, "an aggregate with a value", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(velox.Calc[inv]("? * ?", I.Total, 2).Sum()).GroupBy(I.OrgID).Distinct().
			OrderBy(velox.Calc[inv]("? * ?", I.Total, 2).Sum().Asc()).Limit(10), 1),
			[]string{"100", "200"})
	})
}

// TestSelect runs Select, GroupBy, Having and an ordering by an aggregate
// or an expression on every dialect. The invoices are (id, org, total, paid):
// (1, 1, 10, yes), (2, 1, 20, no), (3, 1, 30, yes), (4, 1, 40, no),
// (5, 2, 50, yes).
func TestSelect(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type inv = testmodel.Invoice
		q := func() velox.Query[inv] { return tg.db.Query[inv]().Limit(100) }
		total := I.Total
		org := I.OrgID

		// One group key, a sum and a count.
		wantRows(t, "per org", rowsOf(t, ctx, q().
			Select(org, total.Sum(), velox.CountAll[inv]()).
			GroupBy(org).OrderBy(I.OrgID.Asc()), 3),
			[]string{"1 100 4", "2 50 1"})

		// Two group keys, and a filter before the grouping.
		wantRows(t, "per org and paid", rowsOf(t, ctx, q().
			Select(org, I.Paid, total.Sum()).
			GroupBy(org, I.Paid).
			OrderBy(I.OrgID.Asc(), I.Paid.Asc()), 3),
			[]string{"1 0 60", "1 1 40", "2 1 50"})
		wantRows(t, "where first", rowsOf(t, ctx, q().
			Where(I.Paid.EQ(true)).
			Select(org, total.Sum()).GroupBy(org).OrderBy(I.OrgID.Asc()), 2),
			[]string{"1 40", "2 50"})

		// HAVING on an aggregate, written as an Expr that holds the term.
		wantRows(t, "having", rowsOf(t, ctx, q().
			Select(org, total.Sum()).GroupBy(org).
			Having(velox.Expr[inv]("? > ?", total.Sum(), 60)), 2),
			[]string{"1 100"})

		// Order by an aggregate, then by an expression of the columns.
		wantRows(t, "order by sum", rowsOf(t, ctx, q().
			Select(org).GroupBy(org).OrderBy(total.Sum().Asc()), 1),
			[]string{"2", "1"})
		wantRows(t, "order by an expression", rowsOf(t, ctx, q().
			Select(I.ID).
			OrderBy(velox.Calc[inv]("ABS(? - 28)", I.Total).Asc()), 1),
			[]string{"3", "2", "4", "1", "5"})

		// Distinct counts, minimum and maximum of one column.
		wantRows(t, "distinct", rowsOf(t, ctx, q().
			Select(org.CountDistinct(), total.Min(), total.Max()), 3),
			[]string{"2 10 50"})

		// A value bound in a term.
		wantRows(t, "bound value", rowsOf(t, ctx, q().
			Where(I.ID.EQ(2)).
			Select(velox.Calc[inv]("? * ?", I.Total, 3)), 1),
			[]string{"60"})

		// SelectRaw is one expression, bound values and all.
		wantRows(t, "SelectRaw", rowsOf(t, ctx, q().
			Where(I.ID.EQ(4)).SelectRaw("? + ?", I.Total, 2), 1),
			[]string{"42"})

		// The projection is read with Limit, like any other read.
		n := 0
		for _, err := range tg.db.Query[inv]().Select(I.ID).Rows(ctx) {
			if err == nil {
				n++
			}
		}
		if n != 0 {
			t.Errorf("an unbounded projection read %d rows, want it refused", n)
		}
	})
}

// TestWindowOnRealDatabases: a window function in a Calc or SelectRaw, on
// every dialect (MySQL 8 and SQLite 3.25 have windows). COUNT(*) OVER ()
// beside GroupBy and Limit counts every group, not only the page's. The
// invoices are TestSelect's.
func TestWindowOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type inv = testmodel.Invoice
		wantRows(t, "count over a page of groups", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(I.OrgID, velox.CountAll[inv](), velox.Calc[inv]("COUNT(*) OVER ()")).
			GroupBy(I.OrgID).OrderBy(I.OrgID.Asc()).Limit(1), 3),
			[]string{"1 4 2"})
		wantRows(t, "row number per org", rowsOf(t, ctx, tg.db.Query[inv]().
			Select(I.ID).
			SelectRaw("ROW_NUMBER() OVER (PARTITION BY ? ORDER BY ? DESC)", I.OrgID, I.Total).
			OrderBy(I.ID.Asc()).Limit(10), 2),
			[]string{"1 4", "2 3", "3 2", "4 1", "5 1"})
	})
}

// TestSelectKeepsPolicy: a grouped read of tickets, on a policied DB, counts
// only the caller's own rows, in each org, and not the other caller's.
func TestSelectKeepsPolicy(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type tk = testmodel.Ticket
		byDone := func(c context.Context) []string {
			return rowsOf(t, c, tg.pdb.Query[tk]().Limit(100).
				Select(Tk.Done, velox.CountAll[tk]()).
				GroupBy(Tk.Done).OrderBy(Tk.Done.Asc()), 2)
		}
		o1 := testmodel.Org.With(context.Background(), int64(1))
		o2 := testmodel.Org.With(context.Background(), int64(2))
		// Org 1: ann has tickets 1, 2 (done) and 5; bob has 3 (done) and 4.
		wantRows(t, "ann in org 1", byDone(testmodel.WithUser(o1, "ann")), []string{"0 2", "1 1"})
		wantRows(t, "bob in org 1", byDone(testmodel.WithUser(o1, "bob")), []string{"0 1", "1 1"})
		// Org 2: ann has tickets 6 and 8 (done); bob has 7.
		wantRows(t, "ann in org 2", byDone(testmodel.WithUser(o2, "ann")), []string{"0 1", "1 1"})
		// An expression and an aggregate see the same rows: the total of all
		// tickets' ids is the caller's tickets' only.
		wantRows(t, "sum of ids", rowsOf(t, testmodel.WithUser(o1, "ann"), tg.pdb.Query[tk]().Limit(100).
			Select(Tk.ID.Sum()), 1), []string{"8"})
	})
}
