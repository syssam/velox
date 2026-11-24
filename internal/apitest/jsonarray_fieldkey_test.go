package apitest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestJSONArrayHasFieldKeySQL: each dialect's form of HasFieldKey (ADR
// 0160). PostgreSQL asks jsonb_path_exists of a strict path, MySQL reads the
// members of every element and asks whether they are all null, SQLite
// searches json_each for a member whose JSON type is not null.
func TestJSONArrayHasFieldKeySQL(t *testing.T) {
	J := fixture.JDocs
	where := velox.And(J.Tags.Array().HasFieldKey("employee_id"), J.Attrs.Array("t", "a").HasFieldKey("team_id"))
	for _, tc := range []struct {
		d    velox.Dialect
		sql  string
		args []any
	}{
		{velox.Postgres,
			`SELECT "id" FROM "j_docs" WHERE (jsonb_path_exists("tags", $1::jsonpath) AND jsonb_path_exists("attrs", $2::jsonpath)) LIMIT 1`,
			[]any{`strict $ ? (exists(@[*] ? (@."employee_id" != null)))`, `strict $ ? (exists(@."t"."a"[*] ? (@."team_id" != null)))`}},
		{velox.MySQL,
			"SELECT `id` FROM `j_docs` WHERE (CASE WHEN JSON_TYPE(`tags`) = 'ARRAY' THEN COALESCE(NOT JSON_CONTAINS('[null]', JSON_EXTRACT(`tags`, ?)), 0) WHEN `tags` IS NULL THEN NULL ELSE 1 = 0 END AND " +
				"CASE WHEN JSON_TYPE(JSON_EXTRACT(`attrs`, ?)) = 'ARRAY' THEN COALESCE(NOT JSON_CONTAINS('[null]', JSON_EXTRACT(`attrs`, ?)), 0) WHEN `attrs` IS NULL THEN NULL ELSE 1 = 0 END) LIMIT 1",
			[]any{`$[*]."employee_id"`, `$."t"."a"`, `$."t"."a"[*]."team_id"`}},
		{velox.SQLite,
			`SELECT "id" FROM "j_docs" WHERE (CASE WHEN json_type("tags") = 'array' THEN EXISTS (SELECT 1 FROM json_each("tags") AS je WHERE ` +
				`je.type = 'object' AND json_type(je.value, ?) <> 'null') WHEN "tags" IS NULL THEN NULL ELSE 1 = 0 END AND ` +
				`CASE WHEN json_type("attrs", ?) = 'array' THEN EXISTS (SELECT 1 FROM json_each("attrs", ?) AS je WHERE ` +
				`je.type = 'object' AND json_type(je.value, ?) <> 'null') WHEN "attrs" IS NULL THEN NULL ELSE 1 = 0 END) LIMIT 1`,
			[]any{`$."employee_id"`, `$."t"."a"`, `$."t"."a"`, `$."team_id"`}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(where).Limit(1).SQL(ctx)
		if err != nil || text != tc.sql || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%v:\n got %s %#v %v\nwant %s %#v", tc.d, text, args, err, tc.sql, tc.args)
		}
	}
}

// TestJSONArrayHasFieldKeyForms: a bad key fails the statement; the render
// cache tells HasFieldKey from Has and HasField, and a key from another key.
func TestJSONArrayHasFieldKeyForms(t *testing.T) {
	J := fixture.JDocs
	for _, tc := range []struct {
		p    velox.Pred[fixture.JDoc]
		want string
	}{
		{J.Tags.Array().HasFieldKey(""), "may not be empty"},
		{J.Tags.Array().HasFieldKey(`a"b`), "double quote"},
		{J.Tags.Array().HasFieldKey(`a\b`), "backslash"},
		{J.Tags.Array().HasFieldKey("a\nb"), "control character"},
		{J.Attrs.Array("").HasFieldKey("k"), "may not be empty"},
	} {
		for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
			db := openFake(d, &fakeconn.Exec{})
			if _, _, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(tc.p).Limit(1).SQL(ctx); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%v: got %v, want %q", d, err, tc.want)
			}
		}
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		db := openFake(d, &fakeconn.Exec{})
		q := func(p velox.Pred[fixture.JDoc]) (string, []any) {
			s, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return s, args
		}
		key, keyArgs := q(J.Tags.Array().HasFieldKey("k"))
		field, _ := q(J.Tags.Array().HasField("k", 7))
		has, _ := q(J.Tags.Has(7))
		other, otherArgs := q(J.Tags.Array().HasFieldKey("other"))
		if key == field || key == has || key != other || reflect.DeepEqual(keyArgs, otherArgs) {
			t.Errorf("%v: the cache mixed HasFieldKey with another:\n%s\n%s\n%s", d, key, field, has)
		}
	}
}
