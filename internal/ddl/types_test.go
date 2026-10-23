package ddl

import (
	"testing"

	"github.com/syssam/velox2"
)

func TestNormalizeType(t *testing.T) {
	for _, c := range []struct {
		d       velox.Dialect
		in, out string
	}{
		{velox.Postgres, "VARCHAR(20)", "character varying(20)"},
		{velox.Postgres, "int4", "integer"},
		{velox.Postgres, "int8", "bigint"},
		{velox.Postgres, "float8", "double precision"},
		{velox.Postgres, "bool", "boolean"},
		{velox.Postgres, "timestamptz", "timestamp with time zone"},
		{velox.Postgres, "decimal(10,2)", "numeric(10,2)"},
		{velox.Postgres, "int", "integer"},
		{velox.Postgres, " Character  Varying(5) ", "character varying(5)"},
		{velox.MySQL, "int(11)", "int"},
		{velox.MySQL, "bigint(20) unsigned", "bigint unsigned"},
		{velox.MySQL, "tinyint(1)", "tinyint(1)"},
		{velox.SQLite, "TEXT", "text"},
	} {
		if got := NormalizeType(c.d, c.in); got != c.out {
			t.Errorf("%v: NormalizeType(%q) = %q, want %q", c.d, c.in, got, c.out)
		}
	}
}

func TestStringType(t *testing.T) {
	for _, c := range []struct {
		d    velox.Dialect
		t    string
		want bool
	}{
		{velox.Postgres, "text", true},
		{velox.Postgres, "varchar(9)", true},
		{velox.Postgres, "char(3)", true},
		{velox.MySQL, "varchar(9)", true},
		{velox.MySQL, "tinytext", true},
		{velox.MySQL, "longtext", true},
		{velox.SQLite, "text", true},
		{velox.Postgres, "citext", false},
		{velox.Postgres, "integer", false},
		{velox.Postgres, "status", false},
		{velox.MySQL, "json", false},
		{velox.MySQL, "blob", false},
	} {
		if got := StringType(c.d, c.t); got != c.want {
			t.Errorf("%v: StringType(%q) = %v, want %v", c.d, c.t, got, c.want)
		}
	}
}
