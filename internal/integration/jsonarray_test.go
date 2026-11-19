package integration

import (
	"testing"

	"github.com/syssam/velox"
)

// TestJSONArrays: Has, HasSome, HasEvery and IsEmpty read the elements of a JSON
// array alike on every dialect: a string only matches itself, with case and
// escapes; a number matches the same number written otherwise; the boolean
// true is not the number 1; a document that is no array has no element and is
// not empty; and a NULL column is no row of a condition or of its negation.
func TestJSONArrays(t *testing.T) {
	eachJSON(t, func(t *testing.T, tg target) {
		jsonSetup(t, tg)
		type row struct {
			tags []string
			nums *[]int
		}
		rows := []row{
			1: {[]string{"a", "b"}, ints(5, 6)},
			2: {[]string{"b", "c"}, &[]int{}},
			3: {[]string{}, nil},
			4: {[]string{"A", "a b", "é<>&\"\\"}, ints(1)},
			5: {[]string{"x"}, ints(5)}, // tags becomes an object below
			6: {[]string{"x"}, ints(5)}, // tags becomes [true, 1, "1", 1.0, null]
			7: {[]string{"x"}, ints(5)}, // tags becomes [true]
			8: {[]string{"x"}, ints(5)}, // tags becomes [1]
		}
		for id := int64(1); id < int64(len(rows)); id++ {
			x := jsonRec(id)
			x.Tags, x.Nums = rows[id].tags, rows[id].nums
			if err := tg.db.Insert(&x).Exec(ctx); err != nil {
				t.Fatal(err)
			}
		}
		for id, doc := range map[int]string{5: `{"k": "a"}`, 6: `[true, 1, "1", 1.0, null]`, 7: `[true]`, 8: `[1]`} {
			tg.exec(t, "UPDATE json_recs SET tags = '"+doc+"' WHERE id = "+string(rune('0'+id)))
		}
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
		T, N := JR.Tags, JR.Nums

		// Strings: exact, with case and escapes; the object {"k": "a"} holds no element.
		wantIDs(t, ids(T.Has("a")), []int64{1})
		wantIDs(t, ids(T.Has("A")), []int64{4})
		wantIDs(t, ids(T.Has("a b")), []int64{4})
		wantIDs(t, ids(T.Has("é<>&\"\\")), []int64{4})
		wantIDs(t, ids(T.Has("b")), []int64{1, 2})
		wantIDs(t, ids(T.Has("")), nil)
		wantIDs(t, ids(velox.Not(T.Has("a"))), []int64{2, 3, 4, 5, 6, 7, 8})

		// Types: true is not 1, "1" is not 1, and 1.0 is 1.
		wantIDs(t, ids(T.Has(true)), []int64{6, 7})
		wantIDs(t, ids(T.Has(false)), nil)
		wantIDs(t, ids(T.Has(1)), []int64{6, 8})
		wantIDs(t, ids(T.Has(1.0)), []int64{6, 8})
		wantIDs(t, ids(T.Has("1")), []int64{6})
		wantIDs(t, ids(T.Has(2)), nil)

		// Several elements.
		wantIDs(t, ids(T.HasSome("a", "c")), []int64{1, 2})
		wantIDs(t, ids(T.HasSome("zzz")), nil)
		wantIDs(t, ids(T.HasEvery("a", "b")), []int64{1})
		wantIDs(t, ids(T.HasEvery("b")), []int64{1, 2})
		wantIDs(t, ids(T.HasSome()), nil)
		wantIDs(t, ids(T.HasEvery()), []int64{1, 2, 3, 4, 5, 6, 7, 8})

		// IsEmpty: only the empty array; the object is not empty.
		wantIDs(t, ids(T.IsEmpty()), []int64{3})
		wantIDs(t, ids(velox.Not(T.IsEmpty())), []int64{1, 2, 4, 5, 6, 7, 8})
		wantIDs(t, ids(JR.Attrs.IsEmpty()), nil) // every row's attrs is an object
		wantIDs(t, ids(JR.Attrs.Has("n")), nil)

		// A nullable column: row 3's is NULL, which no condition nor its negation matches.
		wantIDs(t, ids(N.Has(5)), []int64{1, 5, 6, 7, 8})
		wantIDs(t, ids(velox.Not(N.Has(5))), []int64{2, 4})
		wantIDs(t, ids(N.IsEmpty()), []int64{2})
		wantIDs(t, ids(velox.Not(N.IsEmpty())), []int64{1, 4, 5, 6, 7, 8})
		wantIDs(t, ids(N.IsNull()), []int64{3})

		// In a write's condition, and with another one.
		n, err := tg.db.Update[jrec]().Set(T.Set([]string{"done"})).Where(velox.And(T.Has("c"), N.IsEmpty())).Exec(ctx)
		if err != nil || n != 1 {
			t.Errorf("update: %d, %v", n, err)
		}
		wantIDs(t, ids(T.Has("done")), []int64{2})
	})
}
