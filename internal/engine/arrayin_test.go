package engine_test

import (
	"database/sql/driver"
	"reflect"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/engine"
)

// valuer is a type with a driver.Valuer, whose text form velox does not
// know.
type valuer string

func (v valuer) Value() (driver.Value, error) { return "v:" + string(v), nil }

// code is a named string type without a Valuer, as velox-gen's enums are.
type code string

// level is a named integer type with a String method that is not its
// value: its value is bound, as database/sql and pgx bind it.
type level int16

func (l level) String() string { return "level" }

// flags is a named unsigned integer type.
type flags uint16

// inOn is a SELECT of invInfo whose condition is op over column col with
// the values vs.
func inOn(op int, col int32, vs ...any) *velox.Statement {
	o := engine.OpIn
	if op != 0 {
		o = engine.OpNotIn
	}
	return &velox.Statement{Op: velox.OpSelect, Entity: invInfo, Where: []velox.Predicate{
		engine.NewPredicate(engine.Node{Op: o, Col: col, V: vs}),
	}}
}

// TestPostgresInIsAnArray: on PostgreSQL an In list of integers or strings
// is = ANY of one argument, its array literal, and NotIn is <> ALL; any
// other list, and every list on MySQL and SQLite, is an IN list.
func TestPostgresInIsAnArray(t *testing.T) {
	const pre = `SELECT ` + pgCols + ` FROM "invoices" WHERE `
	for _, c := range []struct {
		name string
		st   *velox.Statement
		sql  string
		args []any
	}{
		{"int64", inOn(0, 0, int64(1), int64(-2), int64(9223372036854775807)), `"id" = ANY($1)`, []any{"{1,-2,9223372036854775807}"}},
		{"not in", inOn(1, 0, int64(1), int64(2)), `"id" <> ALL($1)`, []any{"{1,2}"}},
		{"small integers", inOn(0, 0, int8(-8), int16(16), int32(32), int(64), uint8(8), uint16(16), uint32(4294967295)), `"id" = ANY($1)`, []any{"{-8,16,32,64,8,16,4294967295}"}},
		{"nil", inOn(0, 0, int64(1), nil), `"id" = ANY($1)`, []any{"{1,NULL}"}},
		{"strings", inOn(0, 2, `a"b`, `c\d`, "e,f", "{g}", " h ", "NULL", ""), `"number" = ANY($1)`,
			[]any{`{"a\"b","c\\d","e,f","{g}"," h ","NULL",""}`}},
		{"named types", inOn(0, 2, code("x"), level(3), flags(7)), `"number" = ANY($1)`, []any{`{"x",3,7}`}},
		{"uint64", inOn(0, 0, uint64(1), uint64(2)), `"id" IN ($1, $2)`, []any{uint64(1), uint64(2)}},
		{"uint", inOn(0, 0, uint(1)), `"id" IN ($1)`, []any{uint(1)}},
		{"float", inOn(0, 3, 1.5, 2.5), `"total" IN ($1, $2)`, []any{1.5, 2.5}},
		{"a float among integers", inOn(0, 0, int64(1), 2.5), `"id" IN ($1, $2)`, []any{int64(1), 2.5}},
		{"time", inOn(0, 4, time.Unix(0, 0)), `"paid_at" IN ($1)`, []any{time.Unix(0, 0)}},
		{"bytes", inOn(0, 2, []byte("x")), `"number" IN ($1)`, []any{[]byte("x")}},
		{"valuer", inOn(0, 2, valuer("x")), `"number" IN ($1)`, []any{valuer("x")}},
		{"pointer", inOn(0, 0, new(int64(1))), `"id" IN ($1)`, nil},
		{"empty", inOn(0, 0), `1 = 0`, nil},
		{"empty not in", inOn(1, 0), `1 = 1`, nil},
	} {
		got, args := render(velox.Postgres, c.st)
		if got != pre+c.sql {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, pre+c.sql)
		}
		if c.args != nil && !reflect.DeepEqual(args, c.args) {
			t.Errorf("%s: args %#v, want %#v", c.name, args, c.args)
		}
		// MySQL and SQLite keep the IN list, each value its own argument.
		for _, d := range []velox.Dialect{velox.MySQL, velox.SQLite} {
			if vs := engine.PredNode(c.st.Where[0]).V.([]any); len(vs) > 0 {
				_, args := render(d, c.st)
				if len(args) < len(vs) || !reflect.DeepEqual(args[:len(vs)], vs) {
					t.Errorf("%s, %v: args %#v, want each of %#v", c.name, d, args, vs)
				}
			}
		}
	}
}

// TestPostgresInShapes: on PostgreSQL an array In list is one shape whatever
// its length, apart from NotIn's and from an IN list's; on SQLite the
// lengths 3 and 300 are two shapes.
func TestPostgresInShapes(t *testing.T) {
	list := func(n int) []any {
		vs := make([]any, n)
		for i := range vs {
			vs[i] = int64(i)
		}
		return vs
	}
	key := func(d velox.Dialect, st *velox.Statement) string {
		var buf [64]byte
		return string(engine.ShapeKey(buf[:0], d, st))
	}
	three, many := inOn(0, 0, list(3)...), inOn(0, 0, list(300)...)
	if key(velox.Postgres, three) != key(velox.Postgres, many) {
		t.Error("postgres: In lists of 3 and 300 integers are two shapes")
	}
	var c engine.RenderCache
	a, aArgs := cachedRender(&c, velox.Postgres, three)
	b, bArgs := cachedRender(&c, velox.Postgres, many)
	if a != b || c.Size() != 1 || len(aArgs) != 1 || len(bArgs) != 1 {
		t.Errorf("postgres: %s %d / %s %d, %d shapes", a, len(aArgs), b, len(bArgs), c.Size())
	}
	if key(velox.SQLite, three) == key(velox.SQLite, many) {
		t.Error("sqlite: In lists of 3 and 300 values share a shape")
	}
	for name, other := range map[string]*velox.Statement{
		"not in":   inOn(1, 0, list(3)...),
		"in list":  inOn(0, 0, 1.5, 2.5, 3.5),
		"empty in": inOn(0, 0),
	} {
		if key(velox.Postgres, other) == key(velox.Postgres, three) {
			t.Errorf("postgres: %s shares the array In's shape", name)
		}
	}
}
