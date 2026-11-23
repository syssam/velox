package apitest

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestCalcAggregateText: an expression whose text calls an aggregate
// function is an aggregate, as an aggregate Term is (ADR 0142). MySQL then
// orders a grouped projection by it as written, not by ANY_VALUE of it,
// which would nest an aggregate; SetExpr refuses it. A name inside a
// literal, a longer name and a plain function are not aggregates.
func TestCalcAggregateText(t *testing.T) {
	db := openFake(velox.MySQL, &fakeconn.Exec{})
	for _, text := range []string{"SUM(?)", "sum (?)", "COALESCE(MAX(?), 0)", "group_concat(?)"} {
		s := velox.Calc[inv](text, I.Total)
		got, _, err := db.Query[inv]().Select(I.OrgID, s).GroupBy(I.OrgID).OrderBy(s.Desc()).Limit(1).SQL(ctx)
		if err != nil || strings.Contains(got, "ANY_VALUE") {
			t.Errorf("%s: %s %v", text, got, err)
		}
		_, err = db.Update[inv]().Where(I.ID.EQ(1)).Set(velox.SetExpr(I.Total, text, I.Total)).Exec(ctx)
		if err == nil || !strings.Contains(err.Error(), "holds an aggregate") {
			t.Errorf("SetExpr %s: %v", text, err)
		}
	}
	for _, text := range []string{"ABS(?)", "SUMMARY(?)", "LENGTH(? || 'SUM(')", "? + 1"} {
		s := velox.Calc[inv](text, I.Total)
		got, _, err := db.Query[inv]().Select(I.OrgID, s).GroupBy(I.OrgID, s).OrderBy(s.Desc()).Limit(1).SQL(ctx)
		if err != nil || !strings.Contains(got, "ANY_VALUE") {
			t.Errorf("%s: %s %v", text, got, err)
		}
	}
}
