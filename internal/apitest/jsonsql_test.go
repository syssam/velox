package apitest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestJSONAppendSQL: Append is PostgreSQL's ||, MySQL's JSON_MERGE_PRESERVE
// and SQLite's json_insert with a json(?) per element, each taking a NULL
// column or the JSON null as the empty array and a document that is no array
// as the array of it.
func TestJSONAppendSQL(t *testing.T) {
	J := fixture.JDocs
	for _, tc := range []struct {
		d    velox.Dialect
		sql  string
		args []any
	}{
		{velox.Postgres,
			`UPDATE "j_docs" SET "tags" = CASE WHEN "tags" IS NULL OR jsonb_typeof("tags") = 'null' THEN '[]'::jsonb ELSE "tags" END || $1::jsonb WHERE "id" = $2`,
			[]any{`["x","y\""]`, int64(1)}},
		{velox.MySQL,
			"UPDATE `j_docs` SET `tags` = JSON_MERGE_PRESERVE(CASE WHEN `tags` IS NULL OR JSON_TYPE(`tags`) = 'NULL' THEN JSON_ARRAY() ELSE `tags` END, ?) WHERE `id` = ?",
			[]any{`["x","y\""]`, int64(1)}},
		{velox.SQLite,
			`UPDATE "j_docs" SET "tags" = json_insert(CASE WHEN "tags" IS NULL OR json_type("tags") = 'null' THEN '[]' WHEN json_type("tags") = 'array' THEN "tags" ELSE json_array(json("tags")) END, ` +
				`'$[#]', json(?), '$[#]', json(?)) WHERE "id" = ?`,
			[]any{`"x"`, `"y\""`, int64(1)}},
	} {
		e := &fakeconn.Exec{}
		db := openFake(tc.d, e)
		if _, err := db.Update[fixture.JDoc]().Set(J.Tags.Append([]string{"x", `y"`})).Where(J.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatalf("%v: %v", tc.d, err)
		}
		if e.SQL != tc.sql || !reflect.DeepEqual(e.Args, tc.args) {
			t.Errorf("%v:\n got %s %v\nwant %s %v", tc.d, e.SQL, e.Args, tc.sql, tc.args)
		}
	}
}

// TestJSONAppendNothing: a nil or empty slice appends nothing, and on SQLite
// renders without json_insert; a nullable column's Append takes the element
// type's slice.
func TestJSONAppendNothing(t *testing.T) {
	J := fixture.JDocs
	e := &fakeconn.Exec{}
	db := openFake(velox.SQLite, e)
	for _, s := range []velox.Setter[fixture.JDoc]{J.Tags.Append(nil), J.Tags.Append([]string{}), J.Nums.Append(nil)} {
		if _, err := db.Update[fixture.JDoc]().Set(s).Where(J.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(e.SQL, `= json(CASE WHEN`) || strings.Contains(e.SQL, "json_insert") || len(e.Args) != 1 {
			t.Errorf("an empty Append: %s %v", e.SQL, e.Args)
		}
	}
	pg := &fakeconn.Exec{}
	if _, err := openFake(velox.Postgres, pg).Update[fixture.JDoc]().Set(J.Nums.Append([]int{7})).Where(J.ID.EQ(1)).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pg.SQL, `"nums" = CASE WHEN "nums" IS NULL`) || pg.Args[0] != "[7]" {
		t.Errorf("a nullable column: %s %v", pg.SQL, pg.Args)
	}
}

// TestJSONAppendRenderCache: SQLite's text has a json(?) per element, so
// each length is a shape of its own; PostgreSQL's has one argument.
func TestJSONAppendRenderCache(t *testing.T) {
	J := fixture.JDocs
	run := func(d velox.Dialect, db *velox.DB, e *fakeconn.Exec, v []string) string {
		t.Helper()
		if _, err := db.Update[fixture.JDoc]().Set(J.Tags.Append(v)).Where(J.ID.EQ(1)).Exec(ctx); err != nil {
			t.Fatalf("%v: %v", d, err)
		}
		return e.SQL
	}
	e := &fakeconn.Exec{}
	db := openFake(velox.SQLite, e)
	one, two, oneAgain := run(velox.SQLite, db, e, []string{"a"}), run(velox.SQLite, db, e, []string{"a", "b"}), run(velox.SQLite, db, e, []string{"z"})
	if one == two || one != oneAgain || e.Args[0] != `"z"` {
		t.Errorf("SQLite: %s\n%s\n%s %v", one, two, oneAgain, e.Args)
	}
	pe := &fakeconn.Exec{}
	pdb := openFake(velox.Postgres, pe)
	if a, b := run(velox.Postgres, pdb, pe, []string{"a"}), run(velox.Postgres, pdb, pe, []string{"a", "b"}); a != b || pe.Args[0] != `["a","b"]` {
		t.Errorf("PostgreSQL: %s\n%s %v", a, b, pe.Args)
	}
}

// TestJSONAppendRefused: an insert refuses Append, as it refuses Add; a
// value that encodes as no array fails the statement before anything is
// sent.
func TestJSONAppendRefused(t *testing.T) {
	J := fixture.JDocs
	e := &fakeconn.Exec{}
	db := openFake(velox.SQLite, e)
	x := fixture.JDoc{ID: 1}
	if err := db.Insert(&x).Set(J.Tags.Append([]string{"a"})).Exec(ctx); err == nil || !strings.Contains(err.Error(), "Add, Sub or Append") {
		t.Errorf("insert: %v", err)
	}
	if _, err := db.Update[fixture.JDoc]().Set(J.Shape.Append(fixture.JShape{W: 1})).Where(J.ID.EQ(1)).Exec(ctx); err == nil || !strings.Contains(err.Error(), "encodes as a JSON array") {
		t.Errorf("an object: %v", err)
	}
	if e.Calls != 0 {
		t.Errorf("%d statements sent", e.Calls)
	}
}

// TestJSONLenSQL: Len is the length of an array, NULL for anything else,
// at a path or of the column itself: numeric on PostgreSQL, as Number is.
func TestJSONLenSQL(t *testing.T) {
	J := fixture.JDocs
	where := velox.And(J.Tags.Len().GT(1), J.Attrs.Len("a", "b").EQ(2))
	for _, tc := range []struct {
		d    velox.Dialect
		sql  string
		args []any
	}{
		{velox.Postgres,
			`SELECT "id" FROM "j_docs" WHERE (CASE WHEN jsonb_typeof("tags") = 'array' THEN (jsonb_array_length("tags"))::numeric END > $1 AND ` +
				`CASE WHEN jsonb_typeof(jsonb_extract_path("attrs", $2, $3)) = 'array' THEN (jsonb_array_length(jsonb_extract_path("attrs", $4, $5)))::numeric END = $6) LIMIT $7`,
			[]any{1.0, "a", "b", "a", "b", 2.0, 1}},
		{velox.MySQL,
			"SELECT `id` FROM `j_docs` WHERE (CASE WHEN JSON_TYPE(JSON_EXTRACT(`tags`, ?)) = 'ARRAY' THEN JSON_LENGTH(`tags`, ?) END > ? AND " +
				"CASE WHEN JSON_TYPE(JSON_EXTRACT(`attrs`, ?)) = 'ARRAY' THEN JSON_LENGTH(`attrs`, ?) END = ?) LIMIT ?",
			[]any{"$", "$", 1.0, `$."a"."b"`, `$."a"."b"`, 2.0, 1}},
		{velox.SQLite,
			`SELECT "id" FROM "j_docs" WHERE (CASE WHEN json_type("tags", ?) = 'array' THEN json_array_length("tags", ?) END > ? AND ` +
				`CASE WHEN json_type("attrs", ?) = 'array' THEN json_array_length("attrs", ?) END = ?) LIMIT 1`,
			[]any{"$", "$", 1.0, `$."a"."b"`, `$."a"."b"`, 2.0}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(where).Limit(1).SQL(ctx)
		if err != nil || text != tc.sql || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%v:\n got %s %v %v\nwant %s %v", tc.d, text, args, err, tc.sql, tc.args)
		}
	}
	// A Len shape is not a Number shape of as many keys.
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	a, _, _ := db.Query[fixture.JDoc]().Columns(J.ID).Where(J.Attrs.Len("a").IsNull()).Limit(1).SQL(ctx)
	b, _, _ := db.Query[fixture.JDoc]().Columns(J.ID).Where(J.Attrs.Number("a").IsNull()).Limit(1).SQL(ctx)
	if a == b {
		t.Errorf("Len and Number render alike: %s", a)
	}
	// Only Len takes no key.
	if _, _, err := db.Query[fixture.JDoc]().Where(J.Attrs.Number().IsNull()).Limit(1).SQL(ctx); err == nil {
		t.Error("Number of no key was not refused")
	}
}

// TestJSONTextPatternsSQL: Contains, HasPrefix and HasSuffix are LIKE with
// the ! escape on PostgreSQL and MySQL, which keep case there, and a GLOB on
// SQLite, whose LIKE ignores it; the pattern is an argument, escaped for each.
func TestJSONTextPatternsSQL(t *testing.T) {
	J := fixture.JDocs
	name := J.Attrs.Text("name")
	where := velox.And(name.Contains("a%_!*?["), name.HasPrefix("p"), name.HasSuffix("s"))
	for _, tc := range []struct {
		d    velox.Dialect
		frag string
		pats []any
	}{
		{velox.Postgres, `THEN jsonb_extract_path_text("attrs", $2) END LIKE $3 ESCAPE '!'`, []any{"%a!%!_!!*?[%", "p%", "%s"}},
		{velox.MySQL, "THEN JSON_UNQUOTE(JSON_EXTRACT(`attrs`, ?)) END LIKE ? ESCAPE '!'", []any{"%a!%!_!!*?[%", "p%", "%s"}},
		{velox.SQLite, `THEN json_extract("attrs", ?) END GLOB ?`, []any{"*a%_![*][?][[]*", "p*", "*s"}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(where).Limit(1).SQL(ctx)
		if err != nil || !strings.Contains(text, tc.frag) {
			t.Errorf("%v: %s %v, want %s", tc.d, text, err, tc.frag)
			continue
		}
		var pats []any
		for i := 2; i < len(args); i += 3 {
			pats = append(pats, args[i])
		}
		if !reflect.DeepEqual(pats, tc.pats) {
			t.Errorf("%v: patterns %q, want %q (args %q)", tc.d, pats, tc.pats, args)
		}
	}
}

// TestJSONOrderAndSelectSQL: a JSON path orders a query and is a result
// column, named by As, of a projection; a JSON column is read whole as a
// result column.
func TestJSONOrderAndSelectSQL(t *testing.T) {
	J := fixture.JDocs
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	text, _, err := db.Query[fixture.JDoc]().Columns(J.ID).OrderBy(J.Attrs.Number("n").Desc(), J.Attrs.Text("s").Asc()).Limit(1).SQL(ctx)
	want := `SELECT "id" FROM "j_docs" ORDER BY CASE WHEN jsonb_typeof(jsonb_extract_path("attrs", $1)) = 'number' THEN (jsonb_extract_path_text("attrs", $2))::numeric END DESC, ` +
		`CASE WHEN jsonb_typeof(jsonb_extract_path("attrs", $3)) = 'string' THEN jsonb_extract_path_text("attrs", $4) END LIMIT $5`
	if err != nil || text != want {
		t.Errorf("order:\n got %s %v\nwant %s", text, err, want)
	}
	sq := openFake(velox.SQLite, &fakeconn.Exec{})
	text, _, err = sq.Query[fixture.JDoc]().Columns(J.ID).OrderBy(J.Tags.Len().Asc()).Limit(1).SQL(ctx)
	want = `SELECT "id" FROM "j_docs" ORDER BY CASE WHEN json_type("tags", ?) = 'array' THEN json_array_length("tags", ?) END IS NULL, CASE WHEN json_type("tags", ?) = 'array' THEN json_array_length("tags", ?) END LIMIT 1`
	if err != nil || text != want {
		t.Errorf("order by Len on SQLite:\n got %s %v\nwant %s", text, err, want)
	}
	text, _, err = sq.Query[fixture.JDoc]().Select(J.ID, J.Tags, J.Attrs.Text("s").Term().As("s"), J.Attrs.Number("n").Term().Sum().As("total")).
		GroupBy(J.ID).Limit(1).SQL(ctx)
	want = `SELECT "id", "tags", CASE WHEN json_type("attrs", ?) = 'text' THEN json_extract("attrs", ?) END AS "s", ` +
		`SUM(CASE WHEN json_type("attrs", ?) IN ('integer', 'real') THEN json_extract("attrs", ?) END) AS "total" FROM "j_docs" GROUP BY "id" LIMIT 1`
	if err != nil || text != want {
		t.Errorf("select:\n got %s %v\nwant %s", text, err, want)
	}
	// A JSON column is still no group key, ordering or DISTINCT result.
	for name, q := range map[string]velox.Query[fixture.JDoc]{
		"group":    sq.Query[fixture.JDoc]().Select(J.Tags).GroupBy(J.Tags).Limit(1),
		"distinct": sq.Query[fixture.JDoc]().Select(J.Tags).Distinct().Limit(1),
	} {
		if _, _, err := q.SQL(ctx); err == nil || !strings.Contains(err.Error(), "encoded column") {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A bad name fails the statement.
	if _, _, err := sq.Query[fixture.JDoc]().Select(velox.CountAll[fixture.JDoc]().As("a b")).Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), "term name") {
		t.Errorf("bad name: %v", err)
	}
}
