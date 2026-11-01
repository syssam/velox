package ddl

import (
	"testing"

	"github.com/syssam/velox"
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

// TestNormalizeTypeOneForm: what a schema states and what the server reports read
// as one form, on every dialect.
func TestNormalizeTypeOneForm(t *testing.T) {
	for _, c := range []struct {
		d        velox.Dialect
		stated   string
		reported string
	}{
		{velox.Postgres, "timestamp", "timestamp without time zone"},
		{velox.Postgres, "TIMESTAMP(6)", "timestamp(6) without time zone"},
		{velox.Postgres, "timestamp[]", "timestamp without time zone[]"},
		{velox.Postgres, "timestamp(3)[]", "timestamp(3) without time zone[]"},
		{velox.Postgres, "timestamptz", "timestamp with time zone"},
		{velox.Postgres, "timestamptz(3)", "timestamp(3) with time zone"},
		{velox.Postgres, "time", "time without time zone"},
		{velox.Postgres, "time(3)", "time(3) without time zone"},
		{velox.Postgres, "timetz", "time with time zone"},
		{velox.Postgres, "timetz(3)", "time(3) with time zone"},
		{velox.Postgres, "int2", "smallint"},
		{velox.Postgres, "float4", "real"},
		{velox.Postgres, "float", "double precision"},
		{velox.Postgres, "varbit(4)", "bit varying(4)"},
		{velox.Postgres, "char", "character(1)"},
		{velox.Postgres, "char(3)", "character(3)"},
		{velox.Postgres, "numeric(10, 2)", "numeric(10,2)"},
		{velox.Postgres, "varchar(5)[]", "character varying(5)[]"},
		{velox.MySQL, "integer", "int"},
		{velox.MySQL, "INTEGER(11) UNSIGNED", "int unsigned"},
		{velox.MySQL, "numeric(12,4)", "decimal(12,4)"},
		{velox.MySQL, "dec(4,1)", "decimal(4,1)"},
		{velox.MySQL, "decimal", "decimal(10,0)"},
		{velox.MySQL, "bool", "tinyint(1)"},
		{velox.MySQL, "boolean", "tinyint(1)"},
		{velox.MySQL, "real", "double"},
		{velox.MySQL, "double precision", "double"},
		{velox.MySQL, "character varying(9)", "varchar(9)"},
		{velox.MySQL, "character(2)", "char(2)"},
		{velox.MySQL, "char", "char(1)"},
		{velox.MySQL, "binary", "binary(1)"},
		{velox.MySQL, "timestamp(0)", "timestamp"},
		{velox.MySQL, "datetime(0)", "datetime"},
		{velox.MySQL, "time(0)", "time"},
		{velox.MySQL, "timestamp(6)", "timestamp(6)"},
		{velox.MySQL, "time", "time"},
		{velox.SQLite, "TIMESTAMP", "timestamp"},
		{velox.SQLite, "Varchar( 10 )", "varchar(10)"},
	} {
		if got := NormalizeType(c.d, c.stated); got != c.reported {
			t.Errorf("%v: NormalizeType(%q) = %q, want %q", c.d, c.stated, got, c.reported)
		}
		if got := NormalizeType(c.d, c.reported); got != c.reported {
			t.Errorf("%v: the server's %q reads as %q", c.d, c.reported, got)
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
