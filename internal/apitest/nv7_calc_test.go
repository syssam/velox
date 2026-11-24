package apitest

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/untyped"
)

// TestCalcAgg: a Calc marked by Agg is an aggregate, as an aggregate Term
// is (ADR 0151). MySQL then orders a grouped projection by it as written,
// not by ANY_VALUE of it, which would nest an aggregate. velox does not read
// the text: an unmarked Calc is a per-row value whatever it calls, so
// SQLite's two-argument MAX(a, b) is a key, and a marked one is an
// aggregate whatever it calls, PERCENTILE_CONT and a user's aggregate too.
func TestCalcAgg(t *testing.T) {
	db := openFake(velox.MySQL, &fakeconn.Exec{})
	for _, text := range []string{"SUM(?)", "COALESCE(MAX(?), 0)", "PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY ?)", "my_agg(?)"} {
		s := velox.Agg(velox.Calc[inv](text, I.Total))
		x := "(" + strings.ReplaceAll(text, "?", "`total`") + ")"
		want := "SELECT `org_id`, " + x + " FROM `invoices` GROUP BY `org_id` ORDER BY " + x + " IS NULL DESC, " + x + " DESC LIMIT 1"
		got, _, err := db.Query[inv]().Select(I.OrgID, s).GroupBy(I.OrgID).OrderBy(s.Desc()).Limit(1).SQL(ctx)
		if err != nil || got != want {
			t.Errorf("marked %s:\n got %s %v\nwant %s", text, got, err, want)
		}
	}
	for _, text := range []string{"ABS(?)", "MAX(?, 0)", "SUM(?)"} {
		s := velox.Calc[inv](text, I.Total)
		x := "(" + strings.ReplaceAll(text, "?", "`total`") + ")"
		want := "SELECT `org_id`, " + x + " FROM `invoices` GROUP BY `org_id`, " + x + " ORDER BY ANY_VALUE(" + x + ") IS NULL DESC, " + x + " DESC LIMIT 1"
		got, _, err := db.Query[inv]().Select(I.OrgID, s).GroupBy(I.OrgID, s).OrderBy(s.Desc()).Limit(1).SQL(ctx)
		if err != nil || got != want {
			t.Errorf("unmarked %s:\n got %s %v\nwant %s", text, got, err, want)
		}
	}
	// A marked Calc is refused where an aggregate is: inside an aggregate,
	// as a GroupBy key and in a Rollup. Selected beside a Rollup it is the
	// subtotal row's aggregate, as a Sum is.
	agg := velox.Agg(velox.Calc[inv]("my_agg(?)", I.Total))
	refused := func(name string, q velox.Query[inv], want string) {
		t.Helper()
		if _, _, err := q.Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", name, err, want)
		}
	}
	refused("Sum of a marked Calc", db.Query[inv]().Select(I.OrgID, agg.Sum()).GroupBy(I.OrgID), "an aggregate of an aggregate")
	refused("GroupBy of a marked Calc", db.Query[inv]().Select(agg).GroupBy(agg), "GroupBy holds an aggregate")
	refused("Rollup of a marked Calc", db.Query[inv]().Select(agg).GroupBy(velox.Rollup[inv](agg)), "Rollup of an aggregate")
	refused("Agg of an aggregate Term", db.Query[inv]().Select(velox.Agg(velox.CountAll[inv]())), "Agg marks an expression")
	refused("Agg of a window function", db.Query[inv]().Select(velox.Agg(velox.Calc[inv]("COUNT(*) OVER ()"))), "window function")
	refused("an aggregate inside a marked Calc", db.Query[inv]().Select(velox.Agg(velox.Calc[inv]("SUM(?)", I.Total.Sum()))), "an aggregate of an aggregate")
	got, _, err := db.Query[inv]().Select(I.OrgID, agg).GroupBy(velox.Rollup[inv](I.OrgID)).Limit(1).SQL(ctx)
	if want := "SELECT `org_id`, (my_agg(`total`)) FROM `invoices` GROUP BY `org_id` WITH ROLLUP LIMIT 1"; err != nil || got != want {
		t.Errorf("selected beside a Rollup:\n got %s %v\nwant %s", got, err, want)
	}
	// SetExpr takes text, which velox does not read: an aggregate in it is
	// sent, and the database refuses it. An aggregate Term argument is
	// refused before (TestSetExprRefused).
	got, _, err = db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, "SUM(?)", I.Total)).SQL(ctx)
	if want := "UPDATE `invoices` SET `total` = (SUM(`total`)), `updated_at` = ? WHERE `id` = ?"; err != nil || got != want {
		t.Errorf("SetExpr with aggregate text:\n got %s %v\nwant %s", got, err, want)
	}
}

// TestUntypedAgg: untyped.Agg marks a Calc as velox.Agg does: a sum
// of it is an aggregate of an aggregate, refused.
func TestUntypedAgg(t *testing.T) {
	db := openFake(velox.MySQL, &fakeconn.Exec{})
	x := untyped.Calc("my_agg(?)", untyped.Col(int(I.Total.I)))
	q := func(t untyped.Term) error {
		_, _, err := untyped.Query(db, untyped.TableOf[inv]()).Select(untyped.Sum(t)).Limit(1).SQL(ctx)
		return err
	}
	if err := q(x); err != nil {
		t.Errorf("Sum of an unmarked Calc: %v", err)
	}
	if err := q(untyped.Agg(x)); err == nil || !strings.Contains(err.Error(), "aggregate of an aggregate") {
		t.Errorf("Sum of a marked Calc: %v", err)
	}
}
