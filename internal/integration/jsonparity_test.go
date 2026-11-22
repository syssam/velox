package integration

import (
	"testing"

	"github.com/syssam/velox"
)

// jsonParitySetup inserts three records whose attrs differ in their name,
// list and n, and whose tags and nums are arrays of 2, 0 and 1 elements (nums
// of the second is NULL).
func jsonParitySetup(t *testing.T, tg target) {
	t.Helper()
	jsonSetup(t, tg)
	for _, r := range []jrec{
		{ID: 1, Tags: []string{"a", "b"}, Attrs: map[string]any{"name": "alpha*Beta", "list": []any{1, 2, 3}, "n": 5}, Nums: ints(5, 6)},
		{ID: 2, Tags: []string{}, Attrs: map[string]any{"name": "beta?x", "list": []any{}, "n": 2}},
		{ID: 3, Tags: []string{"c"}, Attrs: map[string]any{"name": "gamma[1]%_", "list": map[string]any{"k": 1}, "n": 10.5}, Nums: &[]int{}},
	} {
		if err := tg.db.Insert(&r).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

func jsonIDs(t *testing.T, tg target, p velox.Pred[jrec], order ...velox.Order[jrec]) []int64 {
	t.Helper()
	if len(order) == 0 {
		order = []velox.Order[jrec]{JR.ID.Asc()}
	}
	rs, err := tg.db.Query[jrec]().Columns(JR.ID).Where(p).OrderBy(order...).AllowUnbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []int64
	for _, r := range rs {
		out = append(out, r.ID)
	}
	return out
}

// TestJSONLenAndPatterns: Len is the length of an array, NULL for an object
// or a missing path; Contains, HasPrefix and HasSuffix take the string
// literally, with case, wildcards of LIKE and GLOB included; Asc and Desc
// order by a number or a string at a path, on every dialect.
func TestJSONLenAndPatterns(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonParitySetup(t, tg)
		all := velox.And[jrec]()
		name, A := JR.Attrs.Text("name"), JR.Attrs

		wantIDs(t, jsonIDs(t, tg, JR.Tags.Len().EQ(2)), []int64{1})
		wantIDs(t, jsonIDs(t, tg, JR.Tags.Len().LT(2)), []int64{2, 3})
		wantIDs(t, jsonIDs(t, tg, JR.Nums.Len().IsNull()), []int64{2})
		wantIDs(t, jsonIDs(t, tg, A.Len("list").GT(0)), []int64{1})
		wantIDs(t, jsonIDs(t, tg, A.Len("list").EQ(0)), []int64{2})
		wantIDs(t, jsonIDs(t, tg, A.Len("list").IsNull()), []int64{3}) // an object has no length
		wantIDs(t, jsonIDs(t, tg, A.Len("none").IsNull()), []int64{1, 2, 3})
		wantIDs(t, jsonIDs(t, tg, all, JR.Tags.Len().Desc(), JR.ID.Asc()), []int64{1, 3, 2})

		wantIDs(t, jsonIDs(t, tg, name.Contains("*B")), []int64{1})
		wantIDs(t, jsonIDs(t, tg, name.Contains("*b")), nil) // case
		wantIDs(t, jsonIDs(t, tg, name.Contains("?")), []int64{2})
		wantIDs(t, jsonIDs(t, tg, name.Contains("%_")), []int64{3})
		wantIDs(t, jsonIDs(t, tg, name.Contains("_")), []int64{3})
		wantIDs(t, jsonIDs(t, tg, name.Contains("")), []int64{1, 2, 3})
		wantIDs(t, jsonIDs(t, tg, name.HasPrefix("alpha*")), []int64{1})
		wantIDs(t, jsonIDs(t, tg, name.HasPrefix("ALPHA")), nil)
		wantIDs(t, jsonIDs(t, tg, name.HasSuffix("[1]%_")), []int64{3})
		wantIDs(t, jsonIDs(t, tg, name.HasSuffix("1]")), nil)
		wantIDs(t, jsonIDs(t, tg, A.Text("n").Contains("5")), nil) // a number is no string

		wantIDs(t, jsonIDs(t, tg, all, A.Number("n").Desc()), []int64{3, 1, 2})
		wantIDs(t, jsonIDs(t, tg, all, A.Number("n").Asc()), []int64{2, 1, 3})
		wantIDs(t, jsonIDs(t, tg, all, name.Desc()), []int64{3, 2, 1})
		wantIDs(t, jsonIDs(t, tg, all, A.Text("none").Asc(), JR.ID.Desc()), []int64{3, 2, 1})
	})
}

// TestJSONAppend: Append adds elements after the array's own, escapes and
// all; a NULL column and the JSON null become the elements; a document that
// is no array becomes the first element; an empty slice changes no array.
func TestJSONAppend(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonParitySetup(t, tg)
		upd := func(id int64, s velox.Setter[jrec]) {
			t.Helper()
			if _, err := tg.db.Update[jrec]().Set(s).Where(JR.ID.EQ(id)).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		upd(1, JR.Tags.Append([]string{"x", "é\"\\😀"}))
		upd(2, JR.Tags.Append([]string{"z"}))
		upd(2, JR.Nums.Append([]int{7}))
		upd(1, JR.Nums.Append(nil))
		upd(3, JR.Nums.Append([]int{1, 2}))
		wantRaw(t, tg, "tags", 1, `["a", "b", "x", "é\"\\😀"]`)
		wantRaw(t, tg, "tags", 2, `["z"]`)
		wantRaw(t, tg, "nums", 2, `[7]`)
		wantRaw(t, tg, "nums", 1, `[5, 6]`)
		wantRaw(t, tg, "nums", 3, `[1, 2]`)

		// The entity reads back what was appended.
		x, err := tg.db.Query[jrec]().Where(JR.ID.EQ(1)).One(ctx)
		if err != nil || len(x.Tags) != 4 || x.Tags[3] != "é\"\\😀" {
			t.Errorf("read back: %+v %v", x, err)
		}

		// The JSON null and a document that is no array.
		tg.exec(t, "UPDATE json_recs SET tags = 'null' WHERE id = 2")
		tg.exec(t, `UPDATE json_recs SET tags = '{"k": "a"}' WHERE id = 3`)
		upd(2, JR.Tags.Append([]string{"n"}))
		upd(3, JR.Tags.Append([]string{"q"}))
		wantRaw(t, tg, "tags", 2, `["n"]`)
		wantRaw(t, tg, "tags", 3, `[{"k": "a"}, "q"]`)
	})
}
