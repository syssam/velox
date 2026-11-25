package engine

import (
	"math"
	"reflect"
	"strings"
	"testing"
)

// containInfos are a doc entity with a JSON column and an item entity whose
// rows name a doc and hold a JSON column too.
func containInfos() (doc, item *EntityInfo) {
	doc = &EntityInfo{Name: "Doc", Table: "doc", PrimaryKey: []int{0}, Columns: []ColumnInfo{
		{Name: "id"}, {Name: "data", Codec: true},
	}}
	item = &EntityInfo{Name: "Item", Table: "item", PrimaryKey: []int{0}, Columns: []ColumnInfo{
		{Name: "id"}, {Name: "doc_id"}, {Name: "meta", Codec: true},
	}}
	return doc, item
}

func textEQ(keys []string, v string) Node {
	return JSONNode(1, JSONPath{Kind: JSONKindText, Keys: keys}, Node{Op: OpEQ, V: v})
}

func notNode(n Node) Node { return Node{Op: OpNot, Kids: []Node{n}} }

// TestJSONContainSQL: on PostgreSQL, Text's, Number's and Bool's EQ, Text's
// In and ValueIsNull are containment in a filter under an even number of
// NOTs, and keep the typed form under an odd number, in a projection, an
// ordering and a conflict target's WHERE, and on a path with a key that
// reads as an array index. MySQL and SQLite keep the typed form.
func TestJSONContainSQL(t *testing.T) {
	doc, item := containInfos()
	const typedTier = `CASE WHEN jsonb_typeof(jsonb_extract_path("data", $1, $2)) = 'string' THEN jsonb_extract_path_text("data", $3, $4) END = $5`
	tier := textEQ([]string{"plan", "tier"}, "pro")
	sel := func(where ...Node) *Statement {
		ps := make([]Predicate, len(where))
		for i, n := range where {
			ps[i] = NewPredicate(n)
		}
		return &Statement{Op: OpSelect, Entity: doc, Columns: []int{0}, Where: ps}
	}
	for _, tc := range []struct {
		name string
		st   *Statement
		sql  string
		args []any
	}{
		{"text EQ", sel(tier),
			`SELECT "id" FROM "doc" WHERE "data" @> $1::jsonb`, []any{`{"plan":{"tier":"pro"}}`}},
		{"number EQ", sel(JSONNode(1, JSONPath{Kind: JSONKindNumber, Keys: []string{"n"}}, Node{Op: OpEQ, V: 1.5})),
			`SELECT "id" FROM "doc" WHERE "data" @> $1::jsonb`, []any{`{"n":1.5}`}},
		{"big number", sel(JSONNode(1, JSONPath{Kind: JSONKindNumber, Keys: []string{"n"}}, Node{Op: OpEQ, V: float64(1 << 60)})),
			`SELECT "id" FROM "doc" WHERE "data" @> $1::jsonb`, []any{`{"n":1.152921504606847e+18}`}},
		{"small number", sel(JSONNode(1, JSONPath{Kind: JSONKindNumber, Keys: []string{"n"}}, Node{Op: OpEQ, V: 1e-7})),
			`SELECT "id" FROM "doc" WHERE "data" @> $1::jsonb`, []any{`{"n":1e-07}`}},
		{"NaN", sel(JSONNode(1, JSONPath{Kind: JSONKindNumber, Keys: []string{"n"}}, Node{Op: OpEQ, V: math.NaN()})),
			`SELECT "id" FROM "doc" WHERE "data" @> $1::jsonb`, []any{nil}},
		{"bool EQ", sel(JSONNode(1, JSONPath{Kind: JSONKindBool, Keys: []string{"on"}}, Node{Op: OpEQ, V: false})),
			`SELECT "id" FROM "doc" WHERE "data" @> $1::jsonb`, []any{`{"on":false}`}},
		{"value is null", sel(JSONNode(1, JSONPath{Kind: JSONKindNull, Keys: []string{"a", "b"}}, Node{Op: OpNotNull})),
			`SELECT "id" FROM "doc" WHERE "data" @> $1::jsonb`, []any{`{"a":{"b":null}}`}},
		{"escaped text", sel(textEQ([]string{"k"}, "<a>\u00e9\n")),
			`SELECT "id" FROM "doc" WHERE "data" @> $1::jsonb`, []any{`{"k":"\u003ca\u003eé\n"}`}},
		{"in, padded", sel(JSONNode(1, JSONPath{Kind: JSONKindText, Keys: []string{"s"}}, Node{Op: OpIn, V: []any{"a", "b", "c"}})),
			`SELECT "id" FROM "doc" WHERE ("data" @> $1::jsonb OR "data" @> $2::jsonb OR "data" @> $3::jsonb OR "data" @> $4::jsonb)`,
			[]any{`{"s":"a"}`, `{"s":"b"}`, `{"s":"c"}`, `{"s":"c"}`}},
		{"empty in", sel(JSONNode(1, JSONPath{Kind: JSONKindText, Keys: []string{"s"}}, Node{Op: OpIn, V: []any{}})),
			`SELECT "id" FROM "doc" WHERE 1 = 0`, []any{}},
		{"not", sel(notNode(tier)),
			`SELECT "id" FROM "doc" WHERE NOT (` + typedTier + `)`, []any{"plan", "tier", "plan", "tier", "pro"}},
		{"not not", sel(notNode(notNode(tier))),
			`SELECT "id" FROM "doc" WHERE NOT (NOT ("data" @> $1::jsonb))`, []any{`{"plan":{"tier":"pro"}}`}},
		{"or with a negated branch", sel(Group(OpOr, []Node{textEQ([]string{"a"}, "x"), notNode(Group(OpAnd, []Node{{Op: OpEQ, Col: 0, V: 1}, textEQ([]string{"b"}, "y")}))})),
			`SELECT "id" FROM "doc" WHERE ("data" @> $1::jsonb OR NOT (("id" = $2 AND CASE WHEN jsonb_typeof(jsonb_extract_path("data", $3)) = 'string' THEN jsonb_extract_path_text("data", $4) END = $5)))`,
			[]any{`{"a":"x"}`, 1, "b", "b", "y"}},
		{"index key", sel(textEQ([]string{"a", "0"}, "x")),
			`SELECT "id" FROM "doc" WHERE CASE WHEN jsonb_typeof(jsonb_extract_path("data", $1, $2)) = 'string' THEN jsonb_extract_path_text("data", $3, $4) END = $5`,
			[]any{"a", "0", "a", "0", "x"}},
		{"neq stays typed", sel(JSONNode(1, JSONPath{Kind: JSONKindText, Keys: []string{"a"}}, Node{Op: OpNEQ, V: "x"})),
			`SELECT "id" FROM "doc" WHERE CASE WHEN jsonb_typeof(jsonb_extract_path("data", $1)) = 'string' THEN jsonb_extract_path_text("data", $2) END <> $3`,
			[]any{"a", "a", "x"}},
		{"exists under not", &Statement{Op: OpSelect, Entity: doc, Columns: []int{0}, Where: []Predicate{NewPredicate(notNode(Node{Op: OpExists, V: Link{Target: item, OwnerCol: 0, TargetCol: 1},
			Kids: []Node{JSONNode(2, JSONPath{Kind: JSONKindText, Keys: []string{"c"}}, Node{Op: OpEQ, V: "red"})}}))}},
			`SELECT "id" FROM "doc" WHERE NOT (EXISTS (SELECT 1 FROM "item" AS "vx1" WHERE "vx1"."doc_id" = "doc"."id" AND "meta" @> $1::jsonb))`,
			[]any{`{"c":"red"}`}},
		{"delete", &Statement{Op: OpDelete, Entity: doc, Where: []Predicate{NewPredicate(tier)}},
			`DELETE FROM "doc" WHERE "data" @> $1::jsonb`, []any{`{"plan":{"tier":"pro"}}`}},
		{"projection, ordering and having", &Statement{Op: OpSelect, Entity: doc, Where: []Predicate{NewPredicate(tier)},
			Project: &Projection{Terms: []Term{JSONTerm(1, JSONPath{Kind: JSONKindText, Keys: []string{"s"}})}, GroupBy: []Term{ColumnTerm(0)},
				Having: []Predicate{NewPredicate(textEQ([]string{"h"}, "v"))}},
			OrderBy: []OrderTerm{{Expr: JSONTerm(1, JSONPath{Kind: JSONKindBool, Keys: []string{"o"}})}}},
			`SELECT CASE WHEN jsonb_typeof(jsonb_extract_path("data", $1)) = 'string' THEN jsonb_extract_path_text("data", $2) END FROM "doc" WHERE "data" @> $3::jsonb GROUP BY "id" HAVING "data" @> $4::jsonb ` +
				`ORDER BY CASE WHEN jsonb_typeof(jsonb_extract_path("data", $5)) = 'boolean' THEN (jsonb_extract_path_text("data", $6))::boolean END`,
			[]any{"s", "s", `{"plan":{"tier":"pro"}}`, `{"h":"v"}`, "o", "o"}},
	} {
		got, args, err := RenderStmt(Postgres, tc.st)
		sameArgs := func(a, b []any) bool { return len(a)+len(b) == 0 || reflect.DeepEqual(a, b) }
		if err != nil || got != tc.sql || !sameArgs(args, tc.args) {
			t.Errorf("%s:\n got %s %#v %v\nwant %s %#v", tc.name, got, args, err, tc.sql, tc.args)
		}
		var c RenderCache
		if _, _, err := c.Get(Postgres, tc.st); err != nil {
			t.Fatal(err)
		}
		if e, hit, err := c.Get(Postgres, tc.st); err != nil || e.Text != got || !sameArgs(hit, args) {
			t.Errorf("%s cached: %s %v %v", tc.name, e.Text, hit, err)
		}
	}
	for _, d := range []Dialect{MySQL, SQLite} {
		got, _, err := RenderStmt(d, sel(tier))
		if err != nil || strings.Contains(got, "@>") || !strings.Contains(got, "CASE WHEN") {
			t.Errorf("%v: %s %v", d, got, err)
		}
	}
}

// TestJSONContainConflictTarget: an upsert's conflict target keeps the typed
// form, as PostgreSQL infers the partial index by its predicate's form; its
// update branch's WHERE, a filter, is containment.
func TestJSONContainConflictTarget(t *testing.T) {
	doc, _ := containInfos()
	tier := NewPredicate(textEQ([]string{"t"}, "x"))
	st := &Statement{Op: OpInsert, Entity: doc, Columns: []int{0}, Rows: [][]any{{int64(1)}},
		Conflict: &ConflictClause{Columns: []int{0}, Update: []int{0}, TargetWhere: []Predicate{tier}, Where: []Predicate{tier}}}
	got, _, err := RenderStmt(Postgres, st)
	if err != nil {
		t.Fatal(err)
	}
	target, update, ok := strings.Cut(got, " DO UPDATE ")
	if !ok || strings.Contains(target, "@>") || !strings.Contains(target, "CASE WHEN") || !strings.Contains(update, `"doc"."data" @> `) {
		t.Errorf("conflict:\n%s", got)
	}
}

// TestJSONContainCacheKey: the same condition renders as containment and,
// under a NOT, typed; a path with an index key renders typed. Each is its
// own shape in the render cache, and a hit binds its own arguments.
func TestJSONContainCacheKey(t *testing.T) {
	doc, _ := containInfos()
	sel := func(n Node) *Statement {
		return &Statement{Op: OpSelect, Entity: doc, Columns: []int{0}, Where: []Predicate{NewPredicate(n)}}
	}
	var c RenderCache
	for _, st := range []*Statement{
		sel(textEQ([]string{"a"}, "x")), sel(notNode(textEQ([]string{"a"}, "x"))),
		sel(textEQ([]string{"1"}, "x")), sel(notNode(textEQ([]string{"1"}, "x"))),
		sel(textEQ([]string{"b"}, "y")), sel(notNode(textEQ([]string{"b"}, "y"))),
		sel(textEQ([]string{"-2"}, "y")), sel(notNode(notNode(textEQ([]string{"b"}, "y")))),
	} {
		want, wantArgs, err := RenderStmt(Postgres, st)
		if err != nil {
			t.Fatal(err)
		}
		e, args, err := c.Get(Postgres, st)
		if err != nil || e.Text != want || !reflect.DeepEqual(args, wantArgs) {
			t.Errorf("cached %s %v %v\nwant %s %v", e.Text, args, err, want, wantArgs)
		}
	}
	if c.n != 5 {
		t.Errorf("%d shapes, want 5: contain, not, index key, not index key, not not", c.n)
	}
}

// TestIndexKey: the keys jsonb_extract_path may read as an array index.
func TestIndexKey(t *testing.T) {
	for k, want := range map[string]bool{
		"0": true, "12": true, "-1": true, "+3": true, "  4": true, "99999999999999999999": true,
		"a": false, "1a": false, "1 ": false, "-": false, "+": false, " ": false, "1.0": false, "١": false,
	} {
		if indexKey(k) != want {
			t.Errorf("indexKey(%q) = %v, want %v", k, !want, want)
		}
	}
}

// TestJSONContainRefused: a text that is not valid UTF-8 fails the
// statement where it would be containment.
func TestJSONContainRefused(t *testing.T) {
	doc, _ := containInfos()
	st := &Statement{Op: OpSelect, Entity: doc, Columns: []int{0}, Where: []Predicate{NewPredicate(textEQ([]string{"a"}, "\xff"))}}
	if _, _, err := RenderStmt(Postgres, st); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("invalid text: %v", err)
	}
	st.Where = []Predicate{NewPredicate(textEQ([]string{"\xff"}, "a"))}
	if _, _, err := RenderStmt(Postgres, st); err == nil || !strings.Contains(err.Error(), "UTF-8") {
		t.Errorf("invalid key: %v", err)
	}
}
