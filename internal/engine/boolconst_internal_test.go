package engine

import (
	"strings"
	"testing"
)

// flagInfo has a bool column, a nullable bool column and a string column.
var flagInfo = &EntityInfo{Name: "engine.flag", Table: "flags", PrimaryKey: []int{0}, Columns: []ColumnInfo{
	{Name: "id", fast: fastInt64}, {Name: "active", fast: fastBool},
	{Name: "seen", Nullable: true, fast: fastBool | fastPtr}, {Name: "name", fast: fastString},
}}

func flagStmt(n Node) *Statement {
	return &Statement{Op: OpSelect, Entity: flagInfo, Columns: []int{0}, Where: []Predicate{{n}}}
}

// TestBoolConstRenders: a bool column compared with a constant bool is
// written without an argument: on PostgreSQL as the column or NOT the
// column, on MySQL and SQLite as a comparison with the literal 1 or 0.
func TestBoolConstRenders(t *testing.T) {
	eq := func(col int32, v any) Node { return Node{Op: OpEQ, Col: col, V: v} }
	neq := func(col int32, v any) Node { return Node{Op: OpNEQ, Col: col, V: v} }
	for _, tc := range []struct {
		name   string
		n      Node
		pg     string
		my, sq string
		args   int
	}{
		{"eq true", eq(1, true), `"active"`, "`active` = 1", `"active" = 1`, 0},
		{"eq false", eq(1, false), `NOT "active"`, "`active` = 0", `"active" = 0`, 0},
		{"neq true", neq(1, true), `NOT "active"`, "`active` <> 1", `"active" <> 1`, 0},
		{"neq false", neq(1, false), `"active"`, "`active` <> 0", `"active" <> 0`, 0},
		{"nullable", eq(2, false), `NOT "seen"`, "`seen` = 0", `"seen" = 0`, 0},
		{"nil stays IS NULL", eq(2, nil), `"seen" IS NULL`, "`seen` IS NULL", `"seen" IS NULL`, 0},
		{"in a not", Group(OpNot, []Node{eq(1, false)}), `NOT (NOT "active")`, "NOT (`active` = 0)", `NOT ("active" = 0)`, 0},
		{"in an or", Group(OpOr, []Node{eq(1, false), eq(3, "x")}), `(NOT "active" OR "name" = $1)`,
			"(`active` = 0 OR `name` = ?)", `("active" = 0 OR "name" = ?)`, 1},
		{"a string is bound", eq(3, "true"), `"name" = $1`, "`name` = ?", `"name" = ?`, 1},
		{"a bool on another column is bound", eq(3, true), `"name" = $1`, "`name` = ?", `"name" = ?`, 1},
		{"gt is bound", Node{Op: OpGT, Col: 1, V: false}, `"active" > $1`, "`active` > ?", `"active" > ?`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, d := range []struct {
				d    Dialect
				want string
			}{{Postgres, tc.pg}, {MySQL, tc.my}, {SQLite, tc.sq}} {
				text, args, err := RenderStmt(d.d, flagStmt(tc.n))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasSuffix(text, "WHERE "+d.want) {
					t.Errorf("%v: %s, want WHERE %s", d.d, text, d.want)
				}
				if len(args) != tc.args {
					t.Errorf("%v: args %v, want %d", d.d, args, tc.args)
				}
			}
		})
	}
}

// TestBoolConstIsShape: the constant shapes the text, so the cache keeps
// true, false and nil apart, and a hit binds no argument for it.
func TestBoolConstIsShape(t *testing.T) {
	var c RenderCache
	for range 2 { // the second round hits
		for _, v := range []any{true, false, nil} {
			e, args, err := c.Get(Postgres, flagStmt(Node{Op: OpEQ, Col: 2, V: v}))
			if err != nil {
				t.Fatal(err)
			}
			want, _, _ := RenderStmt(Postgres, flagStmt(Node{Op: OpEQ, Col: 2, V: v}))
			if e.Text != want || len(args) != 0 {
				t.Errorf("%v: %s %v, want %s and no argument", v, e.Text, args, want)
			}
		}
	}
	if c.Size() != 3 {
		t.Errorf("%d shapes, want 3", c.Size())
	}
}
