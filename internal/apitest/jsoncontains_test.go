package apitest

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/fakeconn"
)

// TestJSONContainsSQL: Contains is the bare column against the document, as
// one argument of its JSON text: PostgreSQL's @> with the cast, which a GIN
// index serves, and MySQL's JSON_CONTAINS. SQLite has no JSON containment and
// refuses it, naming the dialect.
func TestJSONContainsSQL(t *testing.T) {
	J := fixture.JDocs
	where := J.Attrs.Contains(map[string]any{"a": "x", "b": []int{1, 2}})
	const doc = `{"a":"x","b":[1,2]}`
	for _, tc := range []struct {
		d    velox.Dialect
		sql  string
		args []any
	}{
		{velox.Postgres, `SELECT "id" FROM "j_docs" WHERE "attrs" @> $1::jsonb LIMIT 1`, []any{doc}},
		{velox.MySQL, "SELECT `id` FROM `j_docs` WHERE JSON_CONTAINS(`attrs`, ?) LIMIT 1", []any{doc}},
	} {
		db := openFake(tc.d, &fakeconn.Exec{})
		text, args, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(where).Limit(1).SQL(ctx)
		if err != nil || text != tc.sql || !reflect.DeepEqual(args, tc.args) {
			t.Errorf("%v:\n got %s %v %v\nwant %s %v", tc.d, text, args, err, tc.sql, tc.args)
		}
	}
	db := openFake(velox.SQLite, &fakeconn.Exec{})
	_, _, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(where).Limit(1).SQL(ctx)
	if err == nil || !strings.Contains(err.Error(), "SQLite has no JSON containment") {
		t.Errorf("SQLite: got %v, want a refusal naming SQLite", err)
	}

	// A nullable column has it too, and an Or of two binds two arguments.
	pg := openFake(velox.Postgres, &fakeconn.Exec{})
	text, args, err := pg.Query[fixture.JDoc]().Columns(J.ID).Where(velox.Or(J.Attrs.Contains(1), J.Nums.Contains([]int{2}))).Limit(1).SQL(ctx)
	if want := `SELECT "id" FROM "j_docs" WHERE ("attrs" @> $1::jsonb OR "nums" @> $2::jsonb) LIMIT 1`; err != nil || text != want || !reflect.DeepEqual(args, []any{"1", "[2]"}) {
		t.Errorf("Or: got %s %v %v", text, args, err)
	}
}

// TestJSONContainsRefusals: a document that does not encode fails the
// statement, in an Or too, before anything is sent.
func TestJSONContainsRefusals(t *testing.T) {
	J := fixture.JDocs
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	for name, p := range map[string]velox.Pred[fixture.JDoc]{
		"chan":     J.Attrs.Contains(make(chan int)),
		"NaN":      J.Attrs.Contains(math.NaN()),
		"in an Or": velox.Or(J.Attrs.Contains(func() {}), J.Attrs.Contains(1)),
	} {
		_, _, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
		if err == nil || !strings.Contains(err.Error(), "does not encode") {
			t.Errorf("%s: got %v, want one naming the encoding", name, err)
		}
	}
}

// TestJSONContainsRenderCache: the document is an argument, so any two
// documents render to the same text, and Contains shares none with Has.
func TestJSONContainsRenderCache(t *testing.T) {
	J := fixture.JDocs
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	sqlOf := func(p velox.Pred[fixture.JDoc]) string {
		t.Helper()
		text, _, err := db.Query[fixture.JDoc]().Columns(J.ID).Where(p).Limit(1).SQL(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return text
	}
	if a, b := sqlOf(J.Attrs.Contains(map[string]any{"a": 1})), sqlOf(J.Attrs.Contains("s")); a != b || a == sqlOf(J.Attrs.Has("a")) {
		t.Errorf("texts:\n%s\n%s", sqlOf(J.Attrs.Contains(1)), sqlOf(J.Attrs.Has("a")))
	}
}
