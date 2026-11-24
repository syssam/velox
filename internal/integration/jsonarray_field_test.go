package integration

import (
	"strconv"
	"testing"

	"github.com/syssam/velox"
)

// TestJSONArrayHasField: HasField finds an object element by one member's
// value alike on every dialect (ADR 0159): a number matches the same number
// written otherwise, a string only itself, true is not 1; a scalar element,
// an object without the member or with an object there do not match; a
// document that is no array, an empty array and a missing path match the
// negation; a NULL column matches neither. Only MySQL also matches a member
// that is an array holding the value, as its JSON_CONTAINS does.
func TestJSONArrayHasField(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		tags := []string{
			1:  `[{"employee_id": 7, "role": "owner"}, {"employee_id": 8}]`,
			2:  `[{"employee_id": 7.0}]`,
			3:  `[{"employee_id": "7"}]`,
			4:  `[{"employee_id": true}, {"role": "x"}]`,
			5:  `[]`,
			6:  `[7, "employee_id", {"other": 7}, null]`,
			7:  `{"employee_id": 7}`,
			8:  `[{"employee_id": false, "role": "owner"}]`,
			9:  `[{"employee_id": {"x": 7}}]`,
			10: `[{"employee_id": [7]}]`,
		}
		attrs := map[int]string{
			1: `{"t": {"a": [{"employee_id": 7}]}}`,
			2: `{"t": {"a": []}}`,
			3: `{"t": [{"a": [{"employee_id": 7}]}]}`,
			4: `{}`,
			5: `{"t": {"a": [{"employee_id": 8}, {"employee_id": 7}]}}`,
		}
		for id := int64(1); id < int64(len(tags)); id++ {
			x := jsonRec(id)
			if id == 2 {
				x.Nums = nil
			}
			if err := tg.db.Insert(&x).Exec(ctx); err != nil {
				t.Fatal(err)
			}
			n := strconv.FormatInt(id, 10)
			tg.exec(t, "UPDATE json_recs SET tags = '"+tags[id]+"' WHERE id = "+n)
			if a, ok := attrs[int(id)]; ok {
				tg.exec(t, "UPDATE json_recs SET attrs = '"+a+"' WHERE id = "+n)
			}
		}
		tg.exec(t, `UPDATE json_recs SET nums = '[{"k": 5}]' WHERE id = 1`)
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
		T := JR.Tags.Array()
		seven, notSeven := []int64{1, 2}, []int64{3, 4, 5, 6, 7, 8, 9, 10}
		if tg.d == velox.MySQL {
			seven, notSeven = []int64{1, 2, 10}, []int64{3, 4, 5, 6, 7, 8, 9}
		}
		wantIDs(t, ids(T.HasField("employee_id", 7)), seven)
		wantIDs(t, ids(T.HasField("employee_id", 7.0)), seven)
		wantIDs(t, ids(T.HasField("employee_id", uint8(7))), seven)
		wantIDs(t, ids(velox.Not(T.HasField("employee_id", 7))), notSeven)
		wantIDs(t, ids(T.HasField("employee_id", "7")), []int64{3})
		wantIDs(t, ids(T.HasField("employee_id", true)), []int64{4})
		wantIDs(t, ids(T.HasField("employee_id", false)), []int64{8})
		wantIDs(t, ids(T.HasField("employee_id", 1)), nil)
		wantIDs(t, ids(T.HasField("employee_id", 9)), nil)
		wantIDs(t, ids(T.HasField("role", "owner")), []int64{1, 8})
		wantIDs(t, ids(T.HasField("other", 7)), []int64{6})
		wantIDs(t, ids(velox.And(T.HasField("employee_id", 8), T.HasField("role", "owner"))), []int64{1})
		wantIDs(t, ids(velox.Or(T.HasField("employee_id", "7"), JR.ID.EQ(5))), []int64{3, 5})
		wantIDs(t, ids(T.IsEmpty()), []int64{5})

		// At a path: row 3's array is inside an array, row 4's path is missing.
		A := JR.Attrs.Array("t", "a")
		wantIDs(t, ids(A.HasField("employee_id", 7)), []int64{1, 5})
		wantIDs(t, ids(velox.Not(A.HasField("employee_id", 7))), []int64{2, 3, 4, 6, 7, 8, 9, 10})
		wantIDs(t, ids(A.HasField("employee_id", 8)), []int64{5})

		// A nullable column: row 2's is NULL, row 1's holds [{"k": 5}].
		N := JR.Nums.Array()
		wantIDs(t, ids(N.HasField("k", 5)), []int64{1})
		wantIDs(t, ids(velox.Not(N.HasField("k", 5))), []int64{3, 4, 5, 6, 7, 8, 9, 10})
	})
}
