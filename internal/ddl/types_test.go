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
		{velox.MySQL, "ENUM( 'Draft' , 'Published' )", "enum('Draft','Published')"},
		{velox.MySQL, "SET('a, b','C')", "set('a, b','C')"},
		{velox.MySQL, `enum('It''s','a\\'' B')`, `enum('It''s','a\\'' B')`},
		{velox.MySQL, `ENUM('a\' B', 'C')`, `enum('a\' B','C')`},
		{velox.MySQL, "VARCHAR(10)  COLLATE \"en_US\"", "varchar(10) collate \"en_US\""},
		{velox.MySQL, "ENUM(`X  Y`)", "enum(`X  Y`)"},
		{velox.Postgres, `"MyType"`, `"MyType"`},
		{velox.Postgres, `"My  ""Odd"" Type"`, `"My  ""Odd"" Type"`},
		{velox.Postgres, `Character Varying(20) COLLATE "en_US"`, `character varying(20) collate "en_US"`},
		{velox.Postgres, `"Sch"."Typ"`, `"Sch"."Typ"`},
		{velox.Postgres, `'unterminated  X`, `'unterminated  X`},
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
