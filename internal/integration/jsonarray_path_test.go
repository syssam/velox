package integration

import (
	"testing"

	"github.com/syssam/velox"
)

// TestJSONArrayAtPath: a JSON handle's Array(keys...) tests the elements of
// the array at a path of object keys alike on every dialect (ADR 0141): a
// missing path, a scalar or an object at the path, and an array on the way
// hold no element and are not empty, so the negation matches them; a NULL
// column matches neither the condition nor its negation; elements compare by
// JSON type as the column's own Has does.
func TestJSONArrayAtPath(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		attrs := []map[string]any{
			1: {"r": map[string]any{"ids": []any{"a", "b"}}},
			2: {"r": map[string]any{"ids": []any{}}},
			3: {"r": map[string]any{"ids": "a"}},
			4: {"r": "a"},
			5: {},
			6: {"r": []any{map[string]any{"ids": []any{"a"}}}},
			7: {"r": map[string]any{"ids": []any{true, 1, "1"}}},
			8: {"r": map[string]any{"ids": []any{"c"}}},
		}
		for id := int64(1); id < int64(len(attrs)); id++ {
			x := jsonRec(id)
			x.Attrs = attrs[id]
			if id == 8 {
				x.Nums = nil
			}
			if err := tg.db.Insert(&x).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		tg.exec(t, `UPDATE json_recs SET nums = '{"k": [5]}' WHERE id = 1`)
		ids := func(p velox.Pred[jrec]) []int64 {
			t.Helper()
			rs, err := tg.db.Query[jrec]().Columns(JR.ID).Where(p).OrderBy(JR.ID.Asc()).AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var out []int64
			for _, r := range rs {
				out = append(out, r.ID)
			}
			return out
		}
		A := JR.Attrs.Array("r", "ids")
		wantIDs(t, ids(A.Has("a")), []int64{1})
		wantIDs(t, ids(velox.Not(A.Has("a"))), []int64{2, 3, 4, 5, 6, 7, 8})
		wantIDs(t, ids(A.Has(true)), []int64{7})
		wantIDs(t, ids(A.Has(1)), []int64{7})
		wantIDs(t, ids(A.Has(1.0)), []int64{7})
		wantIDs(t, ids(A.Has("1")), []int64{7})
		wantIDs(t, ids(A.HasSome("b", "c")), []int64{1, 8})
		wantIDs(t, ids(A.HasEvery("a", "b")), []int64{1})
		wantIDs(t, ids(A.IsEmpty()), []int64{2})
		wantIDs(t, ids(velox.Not(A.IsEmpty())), []int64{1, 3, 4, 5, 6, 7, 8})
		wantIDs(t, ids(JR.Attrs.Array("r").Has("a")), nil) // an object, a string, an array of objects
		wantIDs(t, ids(velox.Or(JR.Attrs.Array("r", "ids").Has("c"), JR.ID.EQ(2))), []int64{2, 8})

		// A nullable column: row 8's is NULL; row 1's holds {"k": [5]}.
		N := JR.Nums.Array("k")
		wantIDs(t, ids(N.Has(5)), []int64{1})
		wantIDs(t, ids(velox.Not(N.Has(5))), []int64{2, 3, 4, 5, 6, 7})
		wantIDs(t, ids(velox.Not(N.IsEmpty())), []int64{1, 2, 3, 4, 5, 6, 7})
	})
}
