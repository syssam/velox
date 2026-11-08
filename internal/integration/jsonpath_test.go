package integration

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// TestJSONPaths: Text, Number and HasKey read a value inside a JSON column
// alike on every dialect: a value of another type is NULL, a string is
// compared with case, a JSON null is a key that is there, and a key may hold
// spaces and dots.
func TestJSONPaths(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		attrs := []map[string]any{
			1: {"plan": map[string]any{"tier": "pro", "seats": 5}},
			2: {"plan": map[string]any{"tier": "Pro", "seats": 2.5}},
			3: {"plan": map[string]any{"tier": 5, "seats": "5"}},
			4: {"plan": map[string]any{"tier": nil}},
			5: {"plan": map[string]any{}},
			6: {"a b": map[string]any{"c.d": "y"}},
		}
		for id := int64(1); id <= 6; id++ {
			x := jsonRec(id)
			x.Attrs = attrs[id]
			if err := tg.db.Insert(&x).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		ids := func(p velox.Pred[jrec]) []int64 {
			t.Helper()
			rows, err := tg.db.Query[jrec]().Where(p).OrderBy(JR.ID.Asc()).Unbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var out []int64
			for _, r := range rows {
				out = append(out, r.ID)
			}
			return out
		}
		tier, seats := JR.Attrs.Text("plan", "tier"), JR.Attrs.Number("plan", "seats")
		wantIDs(t, ids(tier.EQ("pro")), []int64{1})
		wantIDs(t, ids(tier.In("pro", "Pro")), []int64{1, 2})
		wantIDs(t, ids(tier.NEQ("pro")), []int64{2})
		wantIDs(t, ids(tier.NotIn("pro")), []int64{2})
		wantIDs(t, ids(tier.EQ("5")), nil)
		wantIDs(t, ids(tier.IsNull()), []int64{3, 4, 5, 6})
		wantIDs(t, ids(tier.NotNull()), []int64{1, 2})
		wantIDs(t, ids(seats.GT(3)), []int64{1})
		wantIDs(t, ids(seats.EQ(5)), []int64{1})
		wantIDs(t, ids(seats.NEQ(5)), []int64{2})
		wantIDs(t, ids(seats.GTE(2.5)), []int64{1, 2})
		wantIDs(t, ids(seats.LT(5)), []int64{2})
		wantIDs(t, ids(seats.LTE(5)), []int64{1, 2})
		wantIDs(t, ids(seats.Between(2, 3)), []int64{2})
		wantIDs(t, ids(seats.IsNull()), []int64{3, 4, 5, 6})
		wantIDs(t, ids(seats.NotNull()), []int64{1, 2})
		wantIDs(t, ids(JR.Attrs.HasKey("plan", "tier")), []int64{1, 2, 3, 4})
		wantIDs(t, ids(velox.Not(JR.Attrs.HasKey("plan", "tier"))), []int64{5, 6})
		wantIDs(t, ids(JR.Attrs.Text("a b", "c.d").EQ("y")), []int64{6})
		wantIDs(t, ids(JR.Nums.HasKey("x")), nil) // an array has no keys

		n, err := tg.db.Update[jrec]().Set(JR.Tags.Set([]string{"paid"})).Where(tier.EQ("pro")).Exec(ctx)
		if err != nil || n != 1 {
			t.Errorf("update: %d, %v", n, err)
		}
		if c, err := tg.db.Query[jrec]().Where(seats.NotNull()).Count(ctx); err != nil || c != 2 {
			t.Errorf("Count: %d, %v", c, err)
		}
		_, err = tg.db.Query[jrec]().Where(JR.Attrs.Text(`a"b`).EQ("x")).Unbounded().All(ctx)
		if err == nil || !strings.Contains(err.Error(), "double quote") {
			t.Errorf("a quote in a key: %v", err)
		}
	})
}
