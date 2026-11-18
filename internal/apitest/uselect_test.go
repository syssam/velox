package apitest

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/untyped"
)

// uReport is report, written on the untyped API with column indexes.
func uReport(db *velox.DB) untyped.TableQuery {
	total := untyped.Col(int(I.Total.I))
	return untyped.Query(db, untyped.TableOf[inv]()).
		Where(untyped.Expr("? = ?", untyped.Col(int(I.Paid.I)), true)).
		Select(untyped.Col(int(I.OrgID.I)), untyped.Sum(total), untyped.CountAll()).
		GroupBy(untyped.Col(int(I.OrgID.I))).
		Having(untyped.Expr("? > ?", untyped.Sum(total), 100)).
		OrderBy(untyped.OrderByTerm(untyped.Sum(total), true)).
		Limit(10)
}

// TestUntypedSelectSQL: the untyped projection builds the statement the
// typed one does, in each dialect, with the same arguments, except that its
// paid condition is an Expr.
func TestUntypedSelectSQL(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		db := openFake(d, &fakeconn.Exec{})
		got, args, err := uReport(db).SQL(ctx)
		if err != nil {
			t.Fatalf("%v: %v", d, err)
		}
		want, wantArgs, err := db.Query[inv]().Where(velox.Expr[inv]("? = ?", I.Paid, true)).
			Select(I.OrgID, I.Total.Sum(), velox.CountAll[inv]()).
			GroupBy(I.OrgID).
			Having(velox.Expr[inv]("? > ?", I.Total.Sum(), 100)).
			OrderBy(I.Total.Sum().Desc()).Limit(10).SQL(ctx)
		if err != nil || got != want || len(args) != len(wantArgs) {
			t.Errorf("%v:\n got %s %v\nwant %s %v (%v)", d, got, args, want, wantArgs, err)
		}
	}
}

// TestUntypedSelectRows: the rows of an untyped projection come back by
// position on a real SQLite database, with a Calc and an expression
// ordering.
func TestUntypedSelectRows(t *testing.T) {
	db := connectInvoices(t, ":memory:")
	org := untyped.Col(int(I.OrgID.I))
	var got []string
	for r, err := range untyped.Query(db, untyped.TableOf[inv]()).
		Select(org, untyped.CountAll(), untyped.Max(untyped.Col(int(I.Number.I))),
			untyped.Calc("? * ?", untyped.Count(untyped.Col(int(I.ID.I))), 10)).
		GroupBy(org).Limit(10).Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		var o, n int64
		var last string
		var x int64
		if err := r.Scan(&o, &n, &last, &x); err != nil {
			t.Fatal(err)
		}
		got = append(got, strconv.FormatInt(o, 10)+" "+strconv.FormatInt(n, 10)+" "+last+" "+strconv.FormatInt(x, 10))
	}
	if len(got) != 1 || got[0] != "1 3 c 30" {
		t.Errorf("got %q, want [1 3 c 30]", got)
	}
	var ids []int64
	for r, err := range untyped.Query(db, untyped.TableOf[inv]()).
		Select(untyped.Col(int(I.ID.I))).
		OrderBy(untyped.OrderByTerm(untyped.Calc("? * -1", untyped.Col(int(I.ID.I))), false)).
		Limit(10).Rows(ctx) {
		if err != nil {
			t.Fatal(err)
		}
		var id int64
		if err := r.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if len(ids) != 3 || ids[0] != 3 || ids[2] != 1 {
		t.Errorf("ordered by -id: %v", ids)
	}
}

// TestUntypedSelectKeepsPolicy: an untyped projection reads under the Read
// policy and the scope, and a refused caller sends nothing.
func TestUntypedSelectKeepsPolicy(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openPolicied(velox.Postgres, e)
	q := untyped.Query(db, untyped.TableOf[testmodel.Ticket]()).
		Select(untyped.Col(int(Tk.Owner.I)), untyped.CountAll()).
		GroupBy(untyped.Col(int(Tk.Owner.I))).Limit(10)
	for row, err := range q.Rows(asUser("ann")) {
		_, _ = row, err
	}
	wantSent(t, e, `SELECT "owner", COUNT(*) FROM "tickets" WHERE "org_id" = $1 AND "owner" = $2 GROUP BY "owner" LIMIT $3`, int64(1), "ann", 10)
	calls := e.Calls
	var got error
	for _, err := range q.Rows(asUser("")) {
		got = err
	}
	if !errors.Is(got, velox.ErrDenied) || e.Calls != calls {
		t.Errorf("no user: %v, %d statements sent", got, e.Calls-calls)
	}
}

// TestUntypedSelectRefused: an untyped projection is refused by what reads
// entities, and a bad term fails before anything is sent.
func TestUntypedSelectRefused(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	base := func() untyped.TableQuery {
		return untyped.Query(db, untyped.TableOf[inv]()).Select(untyped.Col(int(I.OrgID.I))).Limit(5)
	}
	rowsErr := func(q untyped.TableQuery) error {
		for _, err := range q.Rows(ctx) {
			return err
		}
		return nil
	}
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"All":                 {func() error { _, err := base().All(ctx); return err }(), "Rows"},
		"Count":               {func() error { _, err := base().Count(ctx); return err }(), "Rows"},
		"Exists":              {func() error { _, err := base().Exists(ctx); return err }(), "Rows"},
		"no Select":           {rowsErr(untyped.Query(db, untyped.TableOf[inv]()).Limit(5)), "Select"},
		"GroupBy no Select":   {rowsErr(untyped.Query(db, untyped.TableOf[inv]()).GroupBy(untyped.Col(0)).Limit(5)), "Select"},
		"Having no Select":    {rowsErr(untyped.Query(db, untyped.TableOf[inv]()).Having(untyped.Expr("1 = 1")).Limit(5)), "Select"},
		"a column it lacks":   {rowsErr(untyped.Query(db, untyped.TableOf[inv]()).Select(untyped.Col(99)).Limit(5)), "99"},
		"an aggregate of one": {rowsErr(base().Select(untyped.Sum(untyped.Max(untyped.Col(0))))), "aggregate"},
		"the zero Term":       {rowsErr(base().Select(untyped.Term{})), "Term"},
		"a subquery":          {rowsErr(base().Select(untyped.Calc("(SELECT 1)"))), "SELECT"},
		"no Limit":            {rowsErr(untyped.Query(db, untyped.TableOf[inv]()).Select(untyped.Col(0))), "Limit"},
	} {
		if tc.err == nil || !strings.Contains(tc.err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", name, tc.err, tc.want)
		}
	}
	if e.Calls != 0 {
		t.Errorf("%d statements were sent", e.Calls)
	}
}
