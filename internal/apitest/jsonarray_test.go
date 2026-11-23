package apitest

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/untyped"
)

// TestJSONArraySQL: each dialect's form of Has and IsEmpty. The element is an
// argument, as JSON for PostgreSQL and MySQL, which test for an array first and
// keep @> a conjunct (a GIN index serves it), and as the value for SQLite, which
// searches json_each by the element's type.
func TestJSONArraySQL(t *testing.T) {
	J := fixture.JDocs
	where := velox.And(J.Tags.Has("a"), J.Tags.IsEmpty())
	for _, tc := range []struct {
		d    velox.Dialect
		sql  string
		args []any
	}{
		{velox.Postgres,
			`SELECT "id" FROM "j_docs" WHERE ((jsonb_typeof("tags") = 'array' AND "tags" @> $1::jsonb) AND ` +
				`CASE WHEN jsonb_typeof("tags") = 'array' THEN jsonb_array_length("tags") = 0 WHEN "tags" IS NULL THEN NULL ELSE 1 = 0 END) LIMIT 1`,
			[]any{`"a"`}},
		{velox.MySQL,
			"SELECT `id` FROM `j_docs` WHERE ((JSON_TYPE(`tags`) = 'ARRAY' AND JSON_CONTAINS(`tags`, ?)) AND " +
				"CASE WHEN JSON_TYPE(`tags`) = 'ARRAY' THEN JSON_LENGTH(`tags`) = 0 WHEN `tags` IS NULL THEN NULL ELSE 1 = 0 END) LIMIT 1",
			[]any{`"a"`}},
		{velox.SQLite,
			`SELECT "id" FROM "j_docs" WHERE (CASE WHEN json_type("tags") = 'array' THEN EXISTS (SELECT 1 FROM json_each("tags") AS je WHERE je.type = 'text' AND je.value = ?) ` +
				`WHEN "tags" IS NULL THEN NULL ELSE 1 = 0 END AND ` +
				`CASE WHEN json_type("tags") = 'array' THEN json_array_length("tags") = 0 WHEN "tags" IS NULL THEN NULL ELSE 1 = 0 END) LIMIT 1`,
			[]any{"a"}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(where).Limit(1).SQL(ctx)
		if err != nil || text != tc.sql || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%v:\n got %s %v %v\nwant %s %v", tc.d, text, args, err, tc.sql, tc.args)
		}
	}
}

// TestJSONArrayElements: a number, a boolean and a string each have their form;
// SQLite compares the type of a number and says the boolean by its type, so that
// true is not 1; HasSome is an Or and HasEvery an And of Has, and with no values
// they are false and true.
func TestJSONArrayElements(t *testing.T) {
	J := fixture.JDocs
	db := openFake(velox.SQLite, &fakeconn.Exec{})
	sqlOf := func(p velox.Pred[fixture.JDoc]) (string, []any) {
		t.Helper()
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimPrefix(strings.TrimSuffix(text, " LIMIT 1"), `SELECT "id" FROM "j_docs" WHERE `), args
	}
	for _, tc := range []struct {
		name string
		p    velox.Pred[fixture.JDoc]
		sql  string
		args []any
	}{
		{"int", J.Nums.Has(5), "je.type IN ('integer', 'real') AND je.value = ?", []any{int64(5)}},
		{"named int", J.Nums.Has(int8(-3)), "je.type IN ('integer', 'real') AND je.value = ?", []any{int64(-3)}},
		{"uint", J.Nums.Has(uint8(7)), "je.type IN ('integer', 'real') AND je.value = ?", []any{int64(7)}},
		{"float", J.Nums.Has(1.5), "je.type IN ('integer', 'real') AND je.value = ?", []any{1.5}},
		{"float32", J.Nums.Has(float32(0.1)), "je.type IN ('integer', 'real') AND je.value = ?", []any{0.1}},
		{"bool", J.Tags.Has(true), "je.type = ?", []any{"true"}},
		{"string", J.Tags.Has("x"), "je.type = 'text' AND je.value = ?", []any{"x"}},
	} {
		sql, args := sqlOf(tc.p)
		if !strings.Contains(sql, tc.sql) || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%s: got %s %#v, want ...%s %#v", tc.name, sql, args, tc.sql, tc.args)
		}
	}
	if sql, args := sqlOf(J.Tags.HasSome("a", "b")); strings.Count(sql, " OR ") != 1 || len(args) != 2 {
		t.Errorf("HasSome: %s %v", sql, args)
	}
	if sql, args := sqlOf(J.Tags.HasEvery("a", "b")); !strings.Contains(sql, " END AND CASE") || len(args) != 2 {
		t.Errorf("HasEvery: %s %v", sql, args)
	}
	if sql, args := sqlOf(J.Tags.HasSome()); sql != "1 = 0" || len(args) != 0 {
		t.Errorf("HasSome(): %s %v", sql, args)
	}
	if sql, args := sqlOf(J.Tags.HasEvery()); sql != "1 = 1" || len(args) != 0 {
		t.Errorf("HasEvery(): %s %v", sql, args)
	}
}

// TestJSONArrayRefusals: an element that is not a string, a number or a boolean,
// or that has no JSON form every dialect reads alike, fails the statement
// before anything is sent, naming it; so does Has in a HasSome or a HasEvery.
func TestJSONArrayRefusals(t *testing.T) {
	J := fixture.JDocs
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for _, tc := range []struct {
		name string
		p    velox.Pred[fixture.JDoc]
		want string
	}{
		{"nil", J.Tags.Has(nil), "not a <nil>"},
		{"slice", J.Tags.Has([]string{"a"}), "not a []string"},
		{"map", J.Tags.Has(map[string]any{}), "not a map[string]interface {}"},
		{"NaN", J.Nums.Has(math.NaN()), "not a JSON number"},
		{"Inf", J.Nums.Has(math.Inf(1)), "not a JSON number"},
		{"big uint", J.Nums.Has(uint64(math.MaxUint64)), "above the largest integer"},
		{"invalid UTF-8", J.Tags.Has("\xff"), "not valid UTF-8"},
		{"in HasSome", J.Tags.HasSome("a", struct{}{}), "not a struct {}"},
		{"in HasEvery", J.Tags.HasEvery(nil), "not a <nil>"},
	} {
		_, _, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(tc.p).Limit(1).SQL(ctx)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.name, err, tc.want)
		}
	}
}

// TestJSONArrayUntyped: the untyped conditions render the statements the
// handles' do, and refuse what the handles' would: a column that is no JSON
// column, an element of another kind, and a value for IsEmpty.
func TestJSONArrayUntyped(t *testing.T) {
	J := fixture.JDocs
	tbl := untyped.TableOf[fixture.JDoc]()
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		db := openFake(d, &fakeconn.Exec{})
		typed, targs, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(J.Tags.Has("a"), J.Nums.IsEmpty(), J.Nums.Has(2)).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		got, gargs, err := untyped.Query(db, tbl).Columns(0).Where(
			tbl.Pred(int(J.Tags.I), untyped.PredJSONHas, "a"), tbl.Pred(int(J.Nums.I), untyped.PredJSONEmpty, nil),
			tbl.Pred(int(J.Nums.I), untyped.PredJSONHas, 2)).Limit(1).SQL(ctx)
		if err != nil || got != typed || !reflect.DeepEqual(gargs, targs) {
			t.Errorf("%v:\n typed   %s %v\n untyped %s %v %v", d, typed, targs, got, gargs, err)
		}
	}
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for name, p := range map[string]velox.Predicate{
		"Has on a number column":   tbl.Pred(int(J.OwnerID.I), untyped.PredJSONHas, "a"),
		"IsEmpty on a number":      tbl.Pred(int(J.ID.I), untyped.PredJSONEmpty, nil),
		"Has of a slice":           tbl.Pred(int(J.Tags.I), untyped.PredJSONHas, []string{"a"}),
		"Has of nil":               tbl.Pred(int(J.Tags.I), untyped.PredJSONHas, nil),
		"IsEmpty with a value":     tbl.Pred(int(J.Tags.I), untyped.PredJSONEmpty, "x"),
		"Has on a column too high": tbl.Pred(99, untyped.PredJSONHas, "a"),
	} {
		if _, _, err := untyped.Query(db, tbl).Where(p).Limit(1).SQL(ctx); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

// TestJSONArrayRenderCache: the element is an argument, so another value of the
// same kind renders to the same text; another kind renders apart where the
// text differs (SQLite's), and a Has and an IsEmpty never share a text.
func TestJSONArrayRenderCache(t *testing.T) {
	J := fixture.JDocs
	db := openFake(velox.SQLite, &fakeconn.Exec{})
	sqlOf := func(p velox.Pred[fixture.JDoc]) string {
		t.Helper()
		text, _, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	a, b := sqlOf(J.Tags.Has("a")), sqlOf(J.Tags.Has("b"))
	if a != b {
		t.Errorf("one kind, two texts:\n%s\n%s", a, b)
	}
	if sqlOf(J.Tags.Has(1)) == a || sqlOf(J.Tags.Has(true)) == a || sqlOf(J.Tags.Has(1)) == sqlOf(J.Tags.Has(true)) {
		t.Error("two kinds share a text")
	}
	if sqlOf(J.Tags.IsEmpty()) == a {
		t.Error("Has and IsEmpty share a text")
	}
}
