package apitest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestJSONPathSQL: each dialect's form of Text, Number and HasKey, with the
// keys, or the quoted path, as arguments before the value.
func TestJSONPathSQL(t *testing.T) {
	J := fixture.JDocs
	where := velox.And(J.Attrs.Text("plan", "tier").EQ("pro"), J.Attrs.Number("seats").GT(3), J.Attrs.HasKey("x"))
	for _, tc := range []struct {
		d    velox.Dialect
		sql  string
		args []any
	}{
		{velox.Postgres,
			`SELECT "id" FROM "j_docs" WHERE (CASE WHEN jsonb_typeof(jsonb_extract_path("attrs", $1, $2)) = 'string' THEN jsonb_extract_path_text("attrs", $3, $4) END = $5 AND ` +
				`CASE WHEN jsonb_typeof(jsonb_extract_path("attrs", $6)) = 'number' THEN (jsonb_extract_path_text("attrs", $7))::numeric END > $8 AND ` +
				`jsonb_extract_path("attrs", $9) IS NOT NULL) LIMIT 1`,
			[]any{"plan", "tier", "plan", "tier", "pro", "seats", "seats", 3.0, "x"}},
		{velox.MySQL,
			"SELECT `id` FROM `j_docs` WHERE (CASE WHEN JSON_TYPE(JSON_EXTRACT(`attrs`, ?)) = 'STRING' THEN JSON_UNQUOTE(JSON_EXTRACT(`attrs`, ?)) END = ? AND " +
				"CASE WHEN JSON_TYPE(JSON_EXTRACT(`attrs`, ?)) IN ('INTEGER', 'UNSIGNED INTEGER', 'DOUBLE', 'DECIMAL') THEN CAST(JSON_EXTRACT(`attrs`, ?) AS DOUBLE) END > ? AND " +
				"JSON_EXTRACT(`attrs`, ?) IS NOT NULL) LIMIT 1",
			[]any{`$."plan"."tier"`, `$."plan"."tier"`, "pro", `$."seats"`, `$."seats"`, 3.0, `$."x"`}},
		{velox.SQLite,
			`SELECT "id" FROM "j_docs" WHERE (CASE WHEN json_type("attrs", ?) = 'text' THEN json_extract("attrs", ?) END = ? AND ` +
				`CASE WHEN json_type("attrs", ?) IN ('integer', 'real') THEN json_extract("attrs", ?) END > ? AND ` +
				`json_type("attrs", ?) IS NOT NULL) LIMIT 1`,
			[]any{`$."plan"."tier"`, `$."plan"."tier"`, "pro", `$."seats"`, `$."seats"`, 3.0, `$."x"`}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(where).Limit(1).SQL(ctx)
		if err != nil || text != tc.sql || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%v:\n got %s %v %v\nwant %s %v", tc.d, text, args, err, tc.sql, tc.args)
		}
	}
}

// TestJSONPathRenderCache: paths of another kind or length render apart;
// the same shape with other keys renders alike, its keys being arguments.
func TestJSONPathRenderCache(t *testing.T) {
	J := fixture.JDocs
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	sqlOf := func(p velox.Pred[fixture.JDoc]) (string, []any) {
		t.Helper()
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return text, args
	}
	textA, _ := sqlOf(J.Attrs.Text("a").IsNull())
	numA, _ := sqlOf(J.Attrs.Number("a").IsNull())
	textAB, _ := sqlOf(J.Attrs.Text("a", "b").IsNull())
	hasA, _ := sqlOf(J.Attrs.HasKey("a"))
	boolA, _ := sqlOf(J.Attrs.Bool("a").IsNull())
	textB, argsB := sqlOf(J.Attrs.Text("b").IsNull())
	seen := map[string]bool{}
	for _, s := range []string{textA, numA, textAB, hasA, boolA} {
		if seen[s] {
			t.Errorf("two shapes render alike: %s", s)
		}
		seen[s] = true
	}
	if textB != textA || argsB[0] != "b" {
		t.Errorf("other keys: %s %v, want %s with b", textB, argsB, textA)
	}
	other, _ := sqlOf(J.Shape.Text("a").IsNull())
	if !strings.Contains(other, `"shape"`) {
		t.Errorf("another column: %s", other)
	}
}

// TestJSONPathRefused: a path no dialect writes alike, and a path into a
// column that is not JSON, fail the statement.
func TestJSONPathRefused(t *testing.T) {
	J := fixture.JDocs
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	notJSON := velox.JSONCol[fixture.JDoc, int64]{I: 1} // owner_id
	for name, tc := range map[string]struct {
		p    velox.Pred[fixture.JDoc]
		want string
	}{
		"no key":        {J.Attrs.HasKey(), "at least one key"},
		"empty key":     {J.Attrs.Text("").EQ("x"), "may not be empty"},
		"quote":         {J.Attrs.Text(`a"b`).EQ("x"), "double quote"},
		"backslash":     {J.Attrs.Number(`a\b`).GT(1), "double quote, a backslash"},
		"control":       {J.Nums.HasKey("a\nb"), "control character"},
		"not JSON":      {notJSON.Text("a").EQ("x"), "owner_id is not a JSON column"},
		"inside an Or":  {velox.Or(J.ID.EQ(1), J.Attrs.Text(`"`).EQ("x")), "double quote"},
		"whole compare": {velox.Col[fixture.JDoc, map[string]any]{I: 3}.EQ(nil), ""},
	} {
		_, err := db.Query[fixture.JDoc]().Where(tc.p).Limit(1).All(ctx)
		if tc.want == "" {
			continue // EQ(nil) is IS NULL, which an encoded column allows
		}
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", name, err, tc.want)
		}
	}
	// The caller's keys are copied: changing them later changes nothing.
	keys := []string{"a"}
	p := J.Attrs.Text(keys...).EQ("x")
	keys[0] = `"`
	if _, err := db.Query[fixture.JDoc]().Where(p).Limit(1).All(ctx); err != nil {
		t.Errorf("the keys were not copied: %v", err)
	}
}

// TestJSONBoolSQL: each dialect's form of Bool, which reads a boolean only.
func TestJSONBoolSQL(t *testing.T) {
	J := fixture.JDocs
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: `CASE WHEN jsonb_typeof(jsonb_extract_path("attrs", $1)) = 'boolean' THEN (jsonb_extract_path_text("attrs", $2))::boolean END = $3`,
		velox.MySQL:    "CASE WHEN JSON_TYPE(JSON_EXTRACT(`attrs`, ?)) = 'BOOLEAN' THEN JSON_EXTRACT(`attrs`, ?) = CAST('true' AS JSON) END = ?",
		velox.SQLite:   `CASE WHEN json_type("attrs", ?) IN ('true', 'false') THEN json_extract("attrs", ?) END = ?`,
	} {
		text, args, err := openFake(d, &fakeconn.Exec{}).Query[fixture.JDoc]().Where(J.Attrs.Bool("trial").EQ(true)).Limit(1).SQL(ctx)
		// The arguments are the path twice and the value.
		if err != nil || !strings.Contains(text, want) || len(args) != 3 || args[2] != true {
			t.Errorf("%v:\n got %s %v %v\nwant %s", d, text, args, err, want)
		}
	}
}
