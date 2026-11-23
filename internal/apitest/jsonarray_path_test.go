package apitest

import (
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestJSONArrayAtPathSQL: Array(keys...) tests the array at a path of
// object keys (ADR 0141). PostgreSQL asks for an array at the path and keeps
// a bare @> of the column against a document holding the element there, so a
// GIN index serves it and a missing path is false; MySQL and SQLite are a
// CASE, false where the path holds no array, NULL for a NULL column.
func TestJSONArrayAtPathSQL(t *testing.T) {
	J := fixture.JDocs
	where := velox.And(J.Attrs.Array("receipt", "ids").Has("r1"), J.Attrs.Array("tags").IsEmpty())
	for _, tc := range []struct {
		d    velox.Dialect
		sql  string
		args []any
	}{
		{velox.Postgres,
			`SELECT "id" FROM "j_docs" WHERE ((jsonb_typeof(jsonb_extract_path("attrs", $1, $2)) = 'array' AND "attrs" @> $3::jsonb) AND ` +
				`CASE WHEN jsonb_typeof(jsonb_extract_path("attrs", $4)) = 'array' THEN jsonb_array_length(jsonb_extract_path("attrs", $5)) = 0 WHEN "attrs" IS NULL THEN NULL ELSE 1 = 0 END) LIMIT 1`,
			[]any{"receipt", "ids", `{"receipt":{"ids":["r1"]}}`, "tags", "tags"}},
		{velox.MySQL,
			"SELECT `id` FROM `j_docs` WHERE (CASE WHEN JSON_TYPE(JSON_EXTRACT(`attrs`, ?)) = 'ARRAY' THEN JSON_CONTAINS(`attrs`, ?, ?) WHEN `attrs` IS NULL THEN NULL ELSE 1 = 0 END AND " +
				"CASE WHEN JSON_TYPE(JSON_EXTRACT(`attrs`, ?)) = 'ARRAY' THEN JSON_LENGTH(`attrs`, ?) = 0 WHEN `attrs` IS NULL THEN NULL ELSE 1 = 0 END) LIMIT 1",
			[]any{`$."receipt"."ids"`, `"r1"`, `$."receipt"."ids"`, `$."tags"`, `$."tags"`}},
		{velox.SQLite,
			`SELECT "id" FROM "j_docs" WHERE (CASE WHEN json_type("attrs", ?) = 'array' THEN EXISTS (SELECT 1 FROM json_each("attrs", ?) AS je WHERE je.type = 'text' AND je.value = ?) ` +
				`WHEN "attrs" IS NULL THEN NULL ELSE 1 = 0 END AND ` +
				`CASE WHEN json_type("attrs", ?) = 'array' THEN json_array_length("attrs", ?) = 0 WHEN "attrs" IS NULL THEN NULL ELSE 1 = 0 END) LIMIT 1`,
			[]any{`$."receipt"."ids"`, `$."receipt"."ids"`, "r1", `$."tags"`, `$."tags"`}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(where).Limit(1).SQL(ctx)
		if err != nil || text != tc.sql || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%v:\n got %s %#v %v\nwant %s %#v", tc.d, text, args, err, tc.sql, tc.args)
		}
	}
}

// TestJSONArrayAtPathForms: Array() with no key is the handle's own form;
// HasSome and HasEvery are an Or and an And of Has at the path, false and
// true with no values; a number element is JSON in PostgreSQL's document; a
// bad key or element fails the statement; the render cache keys the path's
// length where it shapes the text.
func TestJSONArrayAtPathForms(t *testing.T) {
	J := fixture.JDocs
	sqlOf := func(d velox.Dialect, p velox.Pred[fixture.JDoc]) (string, []any, error) {
		db := openFake(d, &fakeconn.Exec{})
		return db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
	}
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		own, ownArgs, _ := sqlOf(d, J.Tags.Has("a"))
		at, atArgs, _ := sqlOf(d, J.Tags.Array().Has("a"))
		if own != at || !reflect.DeepEqual(ownArgs, atArgs) {
			t.Errorf("%v: Array().Has is not Has:\n%s\n%s", d, own, at)
		}
		own, _, _ = sqlOf(d, J.Tags.IsEmpty())
		at, _, _ = sqlOf(d, J.Tags.Array().IsEmpty())
		if own != at {
			t.Errorf("%v: Array().IsEmpty is not IsEmpty:\n%s\n%s", d, own, at)
		}
	}
	_, args, err := sqlOf(velox.Postgres, J.Shape.Array("ids").Has(7))
	if err != nil || len(args) != 2 || args[1] != `{"ids":[7]}` {
		t.Errorf("number: %v %v", args, err)
	}
	if s, args, _ := sqlOf(velox.SQLite, J.Shape.Array("ids").HasSome(1, 2)); strings.Count(s, " OR ") != 1 || len(args) != 6 {
		t.Errorf("HasSome: %s %v", s, args)
	}
	if s, _, _ := sqlOf(velox.SQLite, J.Shape.Array("ids").HasEvery(1, 2)); !strings.Contains(s, " END AND CASE") {
		t.Errorf("HasEvery: %s", s)
	}
	if s, _, _ := sqlOf(velox.SQLite, J.Shape.Array("ids").HasSome()); !strings.HasSuffix(s, "WHERE 1 = 0 LIMIT 1") {
		t.Errorf("HasSome(): %s", s)
	}
	if s, _, _ := sqlOf(velox.SQLite, J.Shape.Array("ids").HasEvery()); !strings.HasSuffix(s, "WHERE 1 = 1 LIMIT 1") {
		t.Errorf("HasEvery(): %s", s)
	}
	for _, tc := range []struct {
		p    velox.Pred[fixture.JDoc]
		want string
	}{
		{J.Attrs.Array("a\"b").Has(1), "may not be empty or hold a double quote"},
		{J.Attrs.Array("").IsEmpty(), "may not be empty"},
		{J.Attrs.Array("a").Has([]int{1}), "not a []int"},
		{J.Attrs.Array("a").HasSome(1, nil), "not a <nil>"},
		{J.Nums.Array("a").HasEvery(struct{}{}), "not a struct {}"},
	} {
		if _, _, err := sqlOf(velox.Postgres, tc.p); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("got %v, want %q", err, tc.want)
		}
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	q := func(p velox.Pred[fixture.JDoc]) string {
		s, _, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	one, oneAgain, two := q(J.Attrs.Array("x").Has("v")), q(J.Attrs.Array("y").Has("w")), q(J.Attrs.Array("x", "y").Has("v"))
	if one != oneAgain || one == two {
		t.Errorf("PostgreSQL texts by path length:\n%s\n%s\n%s", one, oneAgain, two)
	}
	if e1, e2 := q(J.Attrs.Array("x").IsEmpty()), q(J.Attrs.Array("x", "y").IsEmpty()); e1 == e2 {
		t.Errorf("IsEmpty: a path of two keys wrote the text of one: %s", e2)
	}
}
