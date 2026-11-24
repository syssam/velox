package apitest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestJSONArrayHasFieldSQL: each dialect's form of HasField (ADR 0159).
// PostgreSQL is a bare @> of the column against [{"key": v}], at the path,
// which a GIN index serves; MySQL is Has's form with the object for the
// element; SQLite searches json_each for an object whose member has the
// value's JSON type and value, its path an argument.
func TestJSONArrayHasFieldSQL(t *testing.T) {
	J := fixture.JDocs
	where := velox.And(J.Tags.Array().HasField("employee_id", 7), J.Attrs.Array("t", "a").HasField("role", "owner"))
	for _, tc := range []struct {
		d    velox.Dialect
		sql  string
		args []any
	}{
		{velox.Postgres,
			`SELECT "id" FROM "j_docs" WHERE ("tags" @> $1::jsonb AND "attrs" @> $2::jsonb) LIMIT 1`,
			[]any{`[{"employee_id":7}]`, `{"t":{"a":[{"role":"owner"}]}}`}},
		{velox.MySQL,
			"SELECT `id` FROM `j_docs` WHERE ((JSON_TYPE(`tags`) = 'ARRAY' AND JSON_CONTAINS(`tags`, ?)) AND " +
				"CASE WHEN JSON_TYPE(JSON_EXTRACT(`attrs`, ?)) = 'ARRAY' THEN JSON_CONTAINS(`attrs`, ?, ?) WHEN `attrs` IS NULL THEN NULL ELSE 1 = 0 END) LIMIT 1",
			[]any{`{"employee_id":7}`, `$."t"."a"`, `{"role":"owner"}`, `$."t"."a"`}},
		{velox.SQLite,
			`SELECT "id" FROM "j_docs" WHERE (CASE WHEN json_type("tags") = 'array' THEN EXISTS (SELECT 1 FROM json_each("tags") AS je WHERE ` +
				`je.type = 'object' AND json_type(je.value, ?) IN ('integer', 'real') AND json_extract(je.value, ?) = ?) WHEN "tags" IS NULL THEN NULL ELSE 1 = 0 END AND ` +
				`CASE WHEN json_type("attrs", ?) = 'array' THEN EXISTS (SELECT 1 FROM json_each("attrs", ?) AS je WHERE ` +
				`je.type = 'object' AND json_type(je.value, ?) = 'text' AND json_extract(je.value, ?) = ?) WHEN "attrs" IS NULL THEN NULL ELSE 1 = 0 END) LIMIT 1`,
			[]any{`$."employee_id"`, `$."employee_id"`, int64(7), `$."t"."a"`, `$."t"."a"`, `$."role"`, `$."role"`, "owner"}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(where).Limit(1).SQL(ctx)
		if err != nil || text != tc.sql || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%v:\n got %s %#v %v\nwant %s %#v", tc.d, text, args, err, tc.sql, tc.args)
		}
	}
}

// TestJSONArrayHasFieldForms: a boolean is SQLite's member type, not a
// value; a float and a key with characters JSON escapes encode in the
// document; a bad key or value fails the statement; the render cache tells
// HasField from Has where their texts differ.
func TestJSONArrayHasFieldForms(t *testing.T) {
	J := fixture.JDocs
	sqlOf := func(d velox.Dialect, p velox.Pred[fixture.JDoc]) (string, []any, error) {
		db := openFake(d, &fakeconn.Exec{})
		return db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
	}
	s, args, err := sqlOf(velox.SQLite, J.Tags.Array().HasField("ok", true))
	if err != nil || !strings.Contains(s, "je.type = 'object' AND json_type(je.value, ?) = ?)") ||
		!reflect.DeepEqual(args, []any{`$."ok"`, "true"}) {
		t.Errorf("bool: %s %#v %v", s, args, err)
	}
	if _, args, err := sqlOf(velox.Postgres, J.Tags.Array().HasField("é <k>", 1.5)); err != nil || args[0] != `[{"é \u003ck\u003e":1.5}]` {
		t.Errorf("float: %#v %v", args, err)
	}
	if _, args, err := sqlOf(velox.MySQL, J.Tags.Array().HasField("k", uint8(3))); err != nil || args[0] != `{"k":3}` {
		t.Errorf("uint8: %#v %v", args, err)
	}
	for _, tc := range []struct {
		p    velox.Pred[fixture.JDoc]
		want string
	}{
		{J.Tags.Array().HasField("", 1), "may not be empty"},
		{J.Tags.Array().HasField(`a"b`, 1), "double quote"},
		{J.Tags.Array().HasField(`a\b`, 1), "backslash"},
		{J.Tags.Array().HasField("k", nil), "not a <nil>"},
		{J.Tags.Array().HasField("k", []int{1}), "not a []int"},
		{J.Attrs.Array("").HasField("k", 1), "may not be empty"},
	} {
		for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
			if _, _, err := sqlOf(d, tc.p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v: got %v, want %q", d, err, tc.want)
			}
		}
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		db := openFake(d, &fakeconn.Exec{})
		q := func(p velox.Pred[fixture.JDoc]) string {
			s, _, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return s
		}
		has, field := q(J.Tags.Has(7)), q(J.Tags.Array().HasField("k", 7))
		// MySQL's text is Has's, with another argument.
		if (has == field) != (d == velox.MySQL) || field != q(J.Tags.Array().HasField("other", 8)) {
			t.Errorf("%v: the cache mixed Has and HasField:\n%s\n%s", d, has, field)
		}
	}
}
