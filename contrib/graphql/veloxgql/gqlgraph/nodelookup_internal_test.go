package gqlgraph

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/untyped"
)

// lookupParts is n parts of k keys each, of tables t0, t1, ….
func lookupParts(n, k int) []part {
	ps := make([]part, n)
	for i := range ps {
		ps[i] = part{typ: i, name: tableName{table: "t" + string(rune('0'+i%10)), key: "id"}, keys: make([]any, k)}
	}
	return ps
}

// TestRenderLookup: each dialect's quoting and placeholders, a schema, and
// = for one key.
func TestRenderLookup(t *testing.T) {
	ps := []part{
		{typ: 0, name: tableName{schema: "s", table: "a", key: "id"}, keys: []any{int64(1), int64(2)}},
		{typ: 3, name: tableName{table: `b"c`, key: "k`ey"}, keys: []any{int64(1)}},
	}
	for d, want := range map[velox.Dialect]string{
		velox.Postgres: `SELECT 0 FROM "s"."a" WHERE "id" IN ($1, $2) UNION ALL SELECT 3 FROM "b""c" WHERE "k` + "`" + `ey" = $3`,
		velox.MySQL:    "SELECT 0 FROM `s`.`a` WHERE `id` IN (?, ?) UNION ALL SELECT 3 FROM `b\"c` WHERE `k``ey` = ?",
		velox.SQLite:   `SELECT 0 FROM "s"."a" WHERE "id" IN (?, ?) UNION ALL SELECT 3 FROM "b""c" WHERE "k` + "`" + `ey" = ?`,
	} {
		cs := render(d, ps)
		if len(cs) != 1 || cs[0].sql != want || cs[0].lo != 0 || cs[0].hi != 2 {
			t.Errorf("%v: %+v, want %q", d, cs, want)
		}
	}
}

// TestRenderLookupChunks: a statement holds at most maxCompound SELECTs on
// SQLite, and binds at most maxFilterNodes parameters on every dialect;
// PostgreSQL's placeholders start again at $1 in each.
func TestRenderLookupChunks(t *testing.T) {
	cs := render(velox.SQLite, lookupParts(1001, 1))
	if len(cs) != 3 || cs[0].hi != 500 || cs[1].lo != 500 || cs[1].hi != 1000 || cs[2].hi != 1001 {
		t.Fatalf("SQLite, 1001 types: %d statements %v", len(cs), bounds(cs))
	}
	if n := strings.Count(cs[0].sql, "UNION ALL"); n != 499 {
		t.Errorf("SQLite: %d UNION ALL in the first statement, want 499", n)
	}
	if cs := render(velox.Postgres, lookupParts(1001, 1)); len(cs) != 1 {
		t.Errorf("PostgreSQL, 1001 types of one key: %v, want one statement", bounds(cs))
	}
	cs = render(velox.Postgres, lookupParts(7, maxFilterNodes/3))
	if len(cs) != 3 || cs[0].hi != 3 || cs[1].hi != 6 || cs[2].hi != 7 {
		t.Fatalf("PostgreSQL, 7 types of %d keys: %v", maxFilterNodes/3, bounds(cs))
	}
	if !strings.Contains(cs[1].sql, "IN ($1, $2,") || strings.Contains(cs[1].sql, "$30001") {
		t.Errorf("PostgreSQL: the second statement does not number from $1")
	}
}

func bounds(cs []chunk) [][2]int {
	out := make([][2]int, len(cs))
	for i, c := range cs {
		out[i] = [2]int{c.lo, c.hi}
	}
	return out
}

type lookupRow struct{ ID uint64 }

var lookupRowMeta velox.MetaOnce[lookupRow]

func (*lookupRow) VeloxMeta() *velox.Meta[lookupRow] {
	return lookupRowMeta.Load(func() velox.Descriptor[lookupRow] {
		return velox.Descriptor[lookupRow]{
			Table: "wide", Columns: []velox.ColumnDescriptor{{Field: "ID", Name: "id"}}, PrimaryKey: []int{0},
			FieldPtr: func(x *lookupRow, _ int) any { return &x.ID },
		}
	})
}

// TestNodePlan: a list's plan is cached and its statements with it; a
// list changed in place is planned again; a uint64 key and a broken Table
// are read whatever the lookup finds.
func TestNodePlan(t *testing.T) {
	key := []Column{{Name: "id", Index: 0}}
	types := []*Type{
		{Name: "Wide", Table: untyped.TableOf[lookupRow], Key: key},
		{Name: "None", Key: key},
		{Name: "Bad", Table: untyped.TableOf[lookupRow], Key: []Column{{Name: "x", Index: 7}}},
	}
	p, err := planFor(types)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.look) != 0 || !p.always[0] || !p.always[1] || !p.always[2] {
		t.Errorf("look %v, always %v; want none looked up", p.look, p.always)
	}
	if q, _ := planFor(types); q != p {
		t.Error("the same list is planned again")
	}
	types[1] = types[0]
	if q, _ := planFor(types); q == p {
		t.Error("a list changed in place keeps its old plan")
	}
	if _, err := planFor([]*Type{{Name: "Pair", Key: append(key, key...)}}); err == nil {
		t.Error("a type of two key columns: no error")
	}

	p = &nodePlan{}
	ps := lookupParts(2, 3)
	a := p.statements(velox.SQLite, ps)
	b := p.statements(velox.SQLite, ps)
	if len(p.sql) != 1 || &a[0] != &b[0] {
		t.Errorf("statements are rendered again: %d cached", len(p.sql))
	}
	if c := p.statements(velox.Postgres, ps); c[0].sql == a[0].sql || len(p.sql) != 2 {
		t.Errorf("one statement for two dialects")
	}
}

// TestBindKey: a key binds as an int64 or a string, whatever its own type.
func TestBindKey(t *testing.T) {
	type id int32
	type name string
	for in, want := range map[any]any{id(3): int64(3), uint16(4): int64(4), name("x"): "x", int64(5): int64(5)} {
		if got := bindKey(in); got != want {
			t.Errorf("bindKey(%#v) = %#v, want %#v", in, got, want)
		}
	}
}
