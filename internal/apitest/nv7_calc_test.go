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
		got, _, err := db.Query[inv]().Select(I.OrgID, s).GroupBy(I.OrgID).OrderBy(s.Desc()).Limit(1).SQL(ctx)
		if err != nil || strings.Contains(got, "ANY_VALUE") {
			t.Errorf("%s: %s %v", text, got, err)
		}
	}
	for _, text := range []string{"ABS(?)", "MAX(?, 0)", "SUM(?)"} {
		s := velox.Calc[inv](text, I.Total)
		got, _, err := db.Query[inv]().Select(I.OrgID, s).GroupBy(I.OrgID, s).OrderBy(s.Desc()).Limit(1).SQL(ctx)
		if err != nil || !strings.Contains(got, "ANY_VALUE") {
			t.Errorf("%s: %s %v", text, got, err)
		}
	}
	// A marked Calc is refused where an aggregate is: inside an aggregate
	// and as a GroupBy key.
	agg := velox.Agg(velox.Calc[inv]("my_agg(?)", I.Total))
	if _, _, err := db.Query[inv]().Select(I.OrgID, agg.Sum()).GroupBy(I.OrgID).Limit(1).SQL(ctx); err == nil {
		t.Error("Sum of a marked Calc: no error")
	}
	if _, _, err := db.Query[inv]().Select(agg).GroupBy(agg).Limit(1).SQL(ctx); err == nil {
		t.Error("GroupBy of a marked Calc: no error")
	}
	if _, _, err := db.Query[inv]().Select(velox.Agg(velox.CountAll[inv]())).Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), "Agg") {
		t.Errorf("Agg of an aggregate Term: %v", err)
	}
	if _, _, err := db.Query[inv]().Select(velox.Agg(velox.Calc[inv]("COUNT(*) OVER ()"))).Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), "window") {
		t.Errorf("Agg of a window function: %v", err)
	}
}

// TestUntypedAgg: untyped.Agg marks a Calc as velox.Term's Agg does: a sum
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
