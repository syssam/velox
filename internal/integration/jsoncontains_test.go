package integration

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// TestJSONContains: Contains is the database's JSON containment on PostgreSQL
// and MySQL: an object contains an object whose every key and value it
// contains, a value of another type is not the one it looks like, and an Or of
// two Contains reads each. SQLite has no JSON containment and refuses it.
func TestJSONContains(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		docs := []map[string]any{
			1: {"a": "x", "b": true},
			2: {"a": "y"},
			3: {"a": "x", "n": map[string]any{"k": 1, "l": []any{1, 2, 3}}},
		}
		for id := int64(1); id < int64(len(docs)); id++ {
			x := jsonRec(id)
			x.Attrs = docs[id]
			if err := tg.db.Insert(&x).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		A := JR.Attrs
		query := func(p velox.Pred[jrec]) ([]int64, error) {
			rs, err := tg.db.Query[jrec]().Columns(JR.ID).Where(p).OrderBy(JR.ID.Asc()).AllowUnbounded().All(ctx)
			var out []int64
			for _, r := range rs {
				out = append(out, r.ID)
			}
			return out, err
		}
		if tg.d == velox.SQLite {
			_, err := query(A.Contains(map[string]any{"a": "x"}))
			if err == nil || !strings.Contains(err.Error(), "SQLite has no JSON containment") {
				t.Errorf("SQLite: got %v, want a refusal naming SQLite", err)
			}
			return
		}
		ids := func(p velox.Pred[jrec]) []int64 {
			t.Helper()
			out, err := query(p)
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		wantIDs(t, ids(A.Contains(map[string]any{"a": "x"})), []int64{1, 3})
		wantIDs(t, ids(A.Contains(map[string]any{"b": true})), []int64{1})
		wantIDs(t, ids(A.Contains(map[string]any{"a": "x", "b": true})), []int64{1})
		wantIDs(t, ids(A.Contains(map[string]any{"a": "x", "b": false})), nil)
		wantIDs(t, ids(A.Contains(map[string]any{"a": "z"})), nil)
		wantIDs(t, ids(A.Contains(map[string]any{})), []int64{1, 2, 3}) // every object contains {}
		// Nested objects and arrays, by the database's rule.
		wantIDs(t, ids(A.Contains(map[string]any{"n": map[string]any{"k": 1}})), []int64{3})
		wantIDs(t, ids(A.Contains(map[string]any{"n": map[string]any{"l": []int{3, 1}}})), []int64{3})
		wantIDs(t, ids(A.Contains(map[string]any{"n": map[string]any{"l": []int{4}}})), nil)
		// Or of two, and a negation.
		wantIDs(t, ids(velox.Or(A.Contains(map[string]any{"b": true}), A.Contains(map[string]any{"a": "y"}))), []int64{1, 2})
		wantIDs(t, ids(velox.Not(A.Contains(map[string]any{"a": "x"}))), []int64{2})
		// A struct is encoded as encoding/json does.
		wantIDs(t, ids(JR.Shape.Contains(jShape{W: 3, H: 4, N: "sq"})), []int64{1, 2, 3})
		wantIDs(t, ids(JR.Shape.Contains(map[string]any{"w": 4})), nil)
		// In a write's condition.
		n, err := tg.db.Update[jrec]().Set(JR.Tags.Set([]string{"done"})).Where(A.Contains(map[string]any{"a": "y"})).Exec(ctx)
		if err != nil || n != 1 {
			t.Errorf("update: %d, %v", n, err)
		}
		wantIDs(t, ids(JR.Tags.Has("done")), []int64{2})
	})
}
