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

// TestProject runs Project, GroupBy, Having and an ordering by an aggregate
// or an expression on every dialect. The invoices are (id, org, total, paid):
// (1, 1, 10, yes), (2, 1, 20, no), (3, 1, 30, yes), (4, 1, 40, no),
// (5, 2, 50, yes).
func TestProject(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type inv = testmodel.Invoice
		q := func() velox.Query[inv] { return tg.db.Query[inv]().Limit(100) }
		total := velox.Pick(I.Total)
		org := velox.Pick(I.OrgID)

		// One group key, a sum and a count.
		wantRows(t, "per org", rowsOf(t, ctx, q().
			Project(org, total.Sum(), velox.CountAll[inv]()).
			GroupBy(org).OrderBy(I.OrgID.Asc()), 3),
			[]string{"1 100 4", "2 50 1"})

		// Two group keys, and a filter before the grouping.
		wantRows(t, "per org and paid", rowsOf(t, ctx, q().
			Project(org, velox.Pick(I.Paid), total.Sum()).
			GroupBy(org, velox.Pick(I.Paid)).
			OrderBy(I.OrgID.Asc(), I.Paid.Asc()), 3),
			[]string{"1 0 60", "1 1 40", "2 1 50"})
		wantRows(t, "where first", rowsOf(t, ctx, q().
			Where(I.Paid.EQ(true)).
			Project(org, total.Sum()).GroupBy(org).OrderBy(I.OrgID.Asc()), 2),
			[]string{"1 40", "2 50"})

		// HAVING on an aggregate, written as an Expr that holds the term.
		wantRows(t, "having", rowsOf(t, ctx, q().
			Project(org, total.Sum()).GroupBy(org).
			Having(velox.Expr[inv]("? > ?", total.Sum(), 60)), 2),
			[]string{"1 100"})

		// Order by an aggregate, then by an expression of the columns.
		wantRows(t, "order by sum", rowsOf(t, ctx, q().
			Project(org).GroupBy(org).OrderBy(total.Sum().Asc()), 1),
			[]string{"2", "1"})
		wantRows(t, "order by an expression", rowsOf(t, ctx, q().
			Project(velox.Pick(I.ID)).
			OrderBy(velox.Calc[inv]("ABS(? - 28)", I.Total).Asc()), 1),
			[]string{"3", "2", "4", "1", "5"})

		// Distinct counts, minimum and maximum of one column.
		wantRows(t, "distinct", rowsOf(t, ctx, q().
			Project(org.CountDistinct(), total.Min(), total.Max()), 3),
			[]string{"2 10 50"})

		// A value bound in a term.
		wantRows(t, "bound value", rowsOf(t, ctx, q().
			Where(I.ID.EQ(2)).
			Project(velox.Calc[inv]("? * ?", I.Total, 3)), 1),
			[]string{"60"})

		// The projection is read with Limit, like any other read.
		n := 0
		for _, err := range tg.db.Query[inv]().Project(velox.Pick(I.ID)).Rows(ctx) {
			if err == nil {
				n++
			}
		}
		if n != 0 {
			t.Errorf("an unbounded projection read %d rows, want it refused", n)
		}
	})
}

// TestProjectKeepsPolicy: a grouped read of tickets, on a policied DB, counts
// only the caller's own rows, in each org, and not the other caller's.
func TestProjectKeepsPolicy(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		type tk = testmodel.Ticket
		byDone := func(c context.Context) []string {
			return rowsOf(t, c, tg.pdb.Query[tk]().Limit(100).
				Project(velox.Pick(Tk.Done), velox.CountAll[tk]()).
				GroupBy(velox.Pick(Tk.Done)).OrderBy(Tk.Done.Asc()), 2)
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
			Project(velox.Pick(Tk.ID).Sum()), 1), []string{"8"})
	})
}
