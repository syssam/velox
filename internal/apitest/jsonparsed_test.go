package apitest

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestJSONTextParsedSQL: a JSONText's Number reads a number or a guarded
// numeric string, and Time a guarded RFC 3339 string as an instant, on each
// dialect; every value at the path binds the path again, and a time is
// bound in UTC.
func TestJSONTextParsedSQL(t *testing.T) {
	J := fixture.JDocs
	at := time.Date(2026, 3, 1, 10, 0, 0, 0, time.FixedZone("east", 8*3600))
	utc := time.Date(2026, 3, 1, 2, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		d                     velox.Dialect
		number, time          string
		numberArgs, timeArgs  int
		numberPath, timePaths []any
	}{
		{velox.Postgres, pgNumber, pgTime, 10, 4, []any{"a", "b"}, []any{"t"}},
		{velox.MySQL, myNumber, myTime, 7, 12, []any{`$."a"."b"`}, []any{`$."t"`}},
		{velox.SQLite, sqNumber, sqTime, 8, 18, []any{`$."a"."b"`}, []any{`$."t"`}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(J.Attrs.Text("a", "b").Number().GT(1.5)).Limit(1).SQL(ctx)
		if err != nil || text != tc.number {
			t.Errorf("%v Number:\n got %s %v\nwant %s", tc.d, text, err, tc.number)
		}
		want := append(slices.Repeat(tc.numberPath, tc.numberArgs/len(tc.numberPath)), 1.5)
		if !reflect.DeepEqual(args, want) {
			t.Errorf("%v Number: args %v, want %v", tc.d, args, want)
		}
		text, args, err = db.Query[fixture.JDoc]().Columns(J.ID).Where(J.Attrs.Text("t").Time().EQ(at)).Limit(1).SQL(ctx)
		if err != nil || text != tc.time {
			t.Errorf("%v Time:\n got %s %v\nwant %s", tc.d, text, err, tc.time)
		}
		want = append(slices.Repeat(tc.timePaths, tc.timeArgs), any(utc))
		if !reflect.DeepEqual(args, want) {
			t.Errorf("%v Time: args %v, want %v", tc.d, args, want)
		}
	}
}

// TestJSONTextParsedShapes: Text, its Number and its Time at the same path
// render apart through one DB's render cache, and each orders a query, is a
// result column, and stands in Not, Or and Between.
func TestJSONTextParsedShapes(t *testing.T) {
	J := fixture.JDocs
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	v := J.Attrs.Text("v")
	seen := map[string]bool{}
	for _, p := range []velox.Pred[fixture.JDoc]{v.IsNull(), v.Number().IsNull(), v.Time().IsNull(), J.Attrs.Number("v").IsNull()} {
		text, _, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
		if err != nil || seen[text] {
			t.Errorf("rendered alike or failed: %s %v", text, err)
		}
		seen[text] = true
	}
	lo, hi := time.Unix(0, 0), time.Unix(60, 0)
	text, args, err := db.Query[fixture.JDoc]().Select(J.ID, v.Number().Term().As("n"), v.Time().Term().As("t")).
		Where(velox.Or(velox.Not(v.Number().LTE(2)), v.Time().Between(lo, hi), v.Time().NotNull())).
		OrderBy(v.Time().Desc(), v.Number().Asc()).Limit(1).SQL(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{
		`END AS "n", CASE WHEN jsonb_typeof(`,
		`THEN CAST(jsonb_extract_path_text("attrs", $9) AS timestamptz) END AS "t" FROM`,
		`WHERE (NOT (CASE jsonb_typeof(`,
		`END <= $15) OR CASE WHEN`,
		`AS timestamptz) END BETWEEN $20 AND $21 OR`,
		`AS timestamptz) END IS NOT NULL)`,
		`AS timestamptz) END DESC, CASE jsonb_typeof(`,
	} {
		if !strings.Contains(text, frag) {
			t.Errorf("no %q in %s", frag, text)
		}
	}
	if args[19] != lo.UTC() || args[20] != hi.UTC() {
		t.Errorf("Between's times %v %v, want them in UTC", args[19], args[20])
	}
}
