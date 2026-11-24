package integration

import (
	"strconv"
	"testing"

	"github.com/syssam/velox"
)

// TestJSONArrayHasFieldKey: HasFieldKey finds an object element with a
// member whose value is not a JSON null alike on every dialect (ADR 0160):
// a null member is absent, but 0, "" and false are present; a scalar or
// array element, an object without the member, a document that is no
// array, an empty array and a missing path match the negation; a NULL
// column matches neither.
func TestJSONArrayHasFieldKey(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		tags := []string{
			1:  `[{"employee_id": 7}, {"team_id": 3}]`,
			2:  `[{"team_id": 3}]`,
			3:  `[{"employee_id": null}]`,
			4:  `[]`,
			5:  `[7, "employee_id", null, [{"employee_id": 7}]]`,
			6:  `{"employee_id": 7}`,
			7:  `[{"employee_id": null}, {"employee_id": 5}]`,
			8:  `[{"employee_id": 0}]`,
			9:  `[{"employee_id": ""}, {"x": 1}]`,
			10: `[{"employee_id": false}]`,
			11: `[{"employee_id": {"x": 1}}]`,
			12: `[{"employee_id": []}]`,
			13: `[{"EMPLOYEE_ID": 7}]`,
		}
		attrs := map[int]string{
			1: `{"t": {"a": [{"employee_id": 7}]}}`,
			2: `{"t": {"a": [{"employee_id": null}]}}`,
			3: `{"t": [{"a": [{"employee_id": 7}]}]}`,
			4: `{}`,
			5: `{"t": {"a": [{"x": 1}, {"employee_id": 8}]}}`,
			6: `{"t": {"a": {"employee_id": 7}}}`,
			7: `{"t": {"a": []}}`,
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
		tg.exec(t, `UPDATE json_recs SET nums = '[{"k": null}]' WHERE id = 1`)
		tg.exec(t, `UPDATE json_recs SET nums = '[{"k": 5}]' WHERE id = 3`)
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
		has, hasNot := []int64{1, 7, 8, 9, 10, 11, 12}, []int64{2, 3, 4, 5, 6, 13}
		wantIDs(t, ids(T.HasFieldKey("employee_id")), has)
		wantIDs(t, ids(velox.Not(T.HasFieldKey("employee_id"))), hasNot)
		wantIDs(t, ids(T.HasFieldKey("team_id")), []int64{1, 2})
		wantIDs(t, ids(velox.Not(T.HasFieldKey("team_id"))), []int64{3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13})
		wantIDs(t, ids(T.HasFieldKey("EMPLOYEE_ID")), []int64{13})
		wantIDs(t, ids(T.HasFieldKey("missing")), nil)
		wantIDs(t, ids(velox.And(T.HasFieldKey("employee_id"), T.HasFieldKey("team_id"))), []int64{1})
		wantIDs(t, ids(velox.Or(T.HasFieldKey("team_id"), JR.ID.EQ(4))), []int64{1, 2, 4})

		// At a path: row 3's array is inside an array, row 4's path is
		// missing, row 6's is an object, row 7's array is empty.
		A := JR.Attrs.Array("t", "a")
		wantIDs(t, ids(A.HasFieldKey("employee_id")), []int64{1, 5})
		wantIDs(t, ids(velox.Not(A.HasFieldKey("employee_id"))), []int64{2, 3, 4, 6, 7, 8, 9, 10, 11, 12, 13})

		// A nullable column: row 2's is NULL, row 1's holds [{"k": null}].
		N := JR.Nums.Array()
		wantIDs(t, ids(N.HasFieldKey("k")), []int64{3})
		wantIDs(t, ids(velox.Not(N.HasFieldKey("k"))), []int64{1, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13})
	})
}
