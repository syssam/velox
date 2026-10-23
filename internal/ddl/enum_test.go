package ddl

import (
	"slices"
	"testing"

	"github.com/syssam/velox2"
)

var trickyValues = [][]string{
	{"a"},
	{"a", "b"},
	{"it's", `back\slash`, "a, b", "x)", `"q"`, "naïve", "日本", " pad ", ""},
}

func TestEnumType(t *testing.T) {
	if got := EnumType("", "mood"); got != `"mood"` {
		t.Errorf("EnumType: %s", got)
	}
	if got := EnumType("Bill ing", `od"d`); got != `"Bill ing"."od""d"` {
		t.Errorf("EnumType: %s", got)
	}
}

func TestEnumCheckRoundTrip(t *testing.T) {
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		for _, col := range []string{"state", `we"ird IN (`, "a`b"} {
			for _, vs := range trickyValues {
				expr := EnumCheck(d, col, vs)
				gc, gv, ok := ParseEnumCheck(d, expr)
				if !ok || gc != col || !slices.Equal(gv, vs) {
					t.Errorf("%v: ParseEnumCheck(%s) = %q, %q, %v", d, expr, gc, gv, ok)
				}
			}
		}
	}
	if got := EnumCheck(velox.Postgres, "state", []string{"a", "b"}); got != `"state" IN ('a', 'b')` {
		t.Errorf("EnumCheck: %s", got)
	}
	for _, expr := range []string{`"s" IN ()`, `"s" IN ('a'`, `"s" = 'a'`, `"s" IN ('a' 'b')`, `"s" IN (1, 2)`, `"s IN ('a')`} {
		if _, _, ok := ParseEnumCheck(velox.Postgres, expr); ok {
			t.Errorf("ParseEnumCheck(%s) accepted it", expr)
		}
	}
}

func TestMySQLEnumRoundTrip(t *testing.T) {
	for _, vs := range trickyValues {
		typ := MySQLEnum(vs)
		if got, ok := ParseMySQLEnum(typ); !ok || !slices.Equal(got, vs) {
			t.Errorf("ParseMySQLEnum(%s) = %q, %v", typ, got, ok)
		}
		if got, ok := ParseMySQLEnum(NormalizeType(velox.MySQL, typ)); !ok || !slices.Equal(got, vs) {
			t.Errorf("ParseMySQLEnum(NormalizeType(%s)) = %q, %v", typ, got, ok)
		}
	}
	if got := MySQLEnum([]string{"a", `b'\`}); got != `enum('a','b''\\')` {
		t.Errorf("MySQLEnum: %s", got)
	}
	for _, typ := range []string{"enum()", "enum('a'", "set('a')", "varchar(3)", "enum('a''b)"} {
		if _, ok := ParseMySQLEnum(typ); ok {
			t.Errorf("ParseMySQLEnum(%s) accepted it", typ)
		}
	}
}
