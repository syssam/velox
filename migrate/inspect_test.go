package migrate_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

func TestNormalizeDefault(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{`'x'::text`, `'x'`},
		{`'it''s'::character varying`, `'it''s'`},
		{`'a\b'::text`, `'a\b'`},
		{`'-1'::integer`, `-1`},
		{`'-1.5'::numeric`, `-1.5`},
		{`'0'::numeric`, `0`},
		{`'3000000000'::bigint`, `3000000000`},
		{`'1.5'::double precision`, `1.5`},
		{`'1'::text`, `'1'`},
		{`'viewer'::users_role`, `'viewer'`},
		{`'open'::billing.invoices_state`, `'open'`},
		{`'a'::"Mood"`, `'a'`},
		{`'{}'::jsonb`, `'{}'`},
		{`'2020-01-01'::date`, `'2020-01-01'`},
		{`'x'::character varying(10)`, `'x'`},
		{`nextval('users_id_seq'::regclass)`, `nextval('users_id_seq')`},
		{`nextval('billing.invoices_id_seq'::regclass)`, `nextval('billing.invoices_id_seq')`},
		{`now()`, `now()`},
		{`CURRENT_TIMESTAMP`, `CURRENT_TIMESTAMP`},
		{`true`, `true`},
		{`1.5`, `1.5`},
		{`0`, `0`},
		{`('x'::text || 'y'::text)`, `('x'::text || 'y'::text)`},
		{`'a'::text::character varying`, `'a'`},
		{`''::text`, `''`},
		{`NULL::character varying`, ``},
		{`NULL`, ``},
		// Date and time literals keep their casts, for drift to compare by
		// value.
		{`'2020-01-01 08:00:00+00'::timestamp with time zone`, `'2020-01-01 08:00:00+00'::timestamp with time zone`},
		{`'2020-01-01 10:00:00'::timestamp without time zone`, `'2020-01-01 10:00:00'::timestamp without time zone`},
		{`'10:00:00'::time without time zone`, `'10:00:00'::time without time zone`},
		{``, ``},
	} {
		if got := migrate.NormalizeDefault(velox.Postgres, c.in); got != c.want {
			t.Errorf("%s: %s, want %s", c.in, got, c.want)
		}
	}
}

func TestInspectRefuses(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openDB(t, "sqlite", ":memory:")
	_, err := migrate.Inspect(ctx, db, velox.Dialect(99), nil)
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "dialect" {
		t.Errorf("unknown dialect: %v", err)
	}
	_, err = migrate.Inspect(ctx, nil, velox.SQLite, nil)
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "nil" {
		t.Errorf("no database: %v", err)
	}
	_, err = migrate.Drift(ctx, db, nil)
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "nil" {
		t.Errorf("Drift of nil: %v", err)
	}
	// A database that does not answer as PostgreSQL or MySQL: the
	// driver's error, wrapped.
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL} {
		_, err := migrate.Inspect(ctx, db, d, nil)
		if err == nil || !strings.HasPrefix(err.Error(), "migrate: inspect: ") || errors.Unwrap(err) == nil {
			t.Errorf("SQLite as %v: %v", d, err)
		}
	}
}

// TestDriftEnumCasts: PostgreSQL casts a literal compared with an enum
// column to the column's type; drift matches that cast to the literal
// written, for the enum types the schemas have, and only there.
func TestDriftEnumCasts(t *testing.T) {
	t.Parallel()
	schema := func(where, check string, enums ...migrate.Enum) *migrate.Schema {
		return &migrate.Schema{Dialect: velox.Postgres, Enums: enums, Tables: []migrate.Table{{
			Name:    "users",
			Columns: []migrate.Column{{Name: "id", Type: "bigint"}, {Name: "role", Type: `"users_role"`}},
			Indexes: []migrate.Index{{Name: "users_admin", Columns: []string{"id"}, Where: where}},
			Checks:  []migrate.Check{{Name: "users_role_known", Expr: check}},
		}}}
	}
	role := migrate.Enum{Name: "users_role", Values: []string{"admin", "viewer"}}
	billing := migrate.Enum{Schema: "billing", Name: "users_role", Values: []string{"admin", "viewer"}}
	for _, c := range []struct {
		printed, written, check, writtenCheck string
		enums                                 []migrate.Enum
		changes                               int
	}{
		{`(role = 'admin'::users_role)`, `role = 'admin'`,
			`(role = ANY (ARRAY['admin'::users_role, 'viewer'::users_role]))`, `"role" IN ('admin', 'viewer')`, []migrate.Enum{role}, 0},
		{`(role = 'admin'::billing.users_role)`, `role = 'admin'`,
			`(role <> ALL (ARRAY['admin'::billing.users_role]))`, `"role" NOT IN ('admin')`, []migrate.Enum{billing}, 0},
		// Another label, or a type that is no enum type of the schemas.
		{`(role = 'viewer'::users_role)`, `role = 'admin'`,
			`(role = ANY (ARRAY['admin'::users_role, 'banned'::users_role]))`, `"role" IN ('admin', 'viewer')`, []migrate.Enum{role}, 4},
		{`(role = 'admin'::users_role)`, `role = 'admin'`,
			`(role = ANY (ARRAY['admin'::users_role, 'viewer'::users_role]))`, `"role" IN ('admin', 'viewer')`, nil, 4},
		// An enum type of another schema prints qualified.
		{`(role = 'admin'::users_role)`, `role = 'admin'`,
			`(role = ANY (ARRAY['admin'::users_role, 'viewer'::users_role]))`, `"role" IN ('admin', 'viewer')`, []migrate.Enum{billing}, 4},
		// A function's argument keeps its cast.
		{`(f('admin'::users_role) = 1)`, `f('admin') = 1`, `(role IS NOT NULL)`, `role IS NOT NULL`, []migrate.Enum{role}, 2},
	} {
		r, err := migrate.DriftOf(schema(c.printed, c.check, c.enums...), schema(c.written, c.writtenCheck, c.enums...))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Changes) != c.changes || len(r.Warnings) != 0 {
			t.Errorf("%s, %s: changes %v, warnings %v; want %d changes", c.printed, c.check, r.Changes, r.Warnings, c.changes)
		}
	}
}

// TestDriftTimeDefaults: a date or time default PostgreSQL prints in its own
// form is compared by value; another value is a change.
func TestDriftTimeDefaults(t *testing.T) {
	t.Parallel()
	schema := func(def string) *migrate.Schema {
		return &migrate.Schema{Dialect: velox.Postgres, Tables: []migrate.Table{{
			Name:    "t",
			Columns: []migrate.Column{{Name: "at", Type: "timestamp with time zone", Default: def}},
		}}}
	}
	for _, c := range []struct {
		printed, written string
		changes          int
	}{
		{`'2020-01-01 08:00:00+00'::timestamp with time zone`, `'2020-01-01 10:00:00+02'`, 0},
		{`'2020-01-01 08:00:00+00'::timestamp with time zone`, `'2020-01-01 08:00:00+00'`, 0},
		{`'2020-01-01 08:00:00+00'::timestamp with time zone`, `'2020-01-01 10:00:00+00'`, 1},
		{`'2020-01-01 00:00:00'::timestamp without time zone`, `'2020-01-01'`, 0},
	} {
		r, err := migrate.DriftOf(schema(c.printed), schema(c.written))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Changes) != c.changes || len(r.Warnings) != 0 {
			t.Errorf("%s, %s: changes %v, warnings %v", c.printed, c.written, r.Changes, r.Warnings)
		}
	}
}

// TestDriftMySQLTimeDefaults: a date or time default MySQL prints padded is
// compared by value; another value is a change.
func TestDriftMySQLTimeDefaults(t *testing.T) {
	t.Parallel()
	schema := func(typ, def string) *migrate.Schema {
		return &migrate.Schema{Dialect: velox.MySQL, Tables: []migrate.Table{{
			Name: "t", Collation: "utf8mb4_bin",
			Columns: []migrate.Column{{Name: "at", Type: typ, Default: def}},
		}}}
	}
	for _, c := range []struct {
		typ, printed, written string
		changes               int
	}{
		{"datetime(6)", `'2020-01-01 00:00:00.000000'`, `'2020-01-01 00:00:00'`, 0},
		{"datetime(6)", `'2020-01-01 00:00:00.000000'`, `'2020-01-01'`, 0},
		{"timestamp(3)", `'2020-01-01 10:00:00.500'`, `'2020-01-01 10:00:00.5'`, 0},
		{"date", `'2020-01-01'`, `'2020-01-01'`, 0},
		{"time", `'10:00:00'`, `'10:00'`, 0},
		{"datetime(6)", `'2020-01-01 00:00:00.000000'`, `'2020-01-01 00:00:01'`, 1},
		{"time", `'10:00:00'`, `'10:01'`, 1},
		// Not a time type: the text is the value.
		{"varchar(32)", `'2020-01-01 00:00:00.000000'`, `'2020-01-01 00:00:00'`, 1},
	} {
		r, err := migrate.DriftOf(schema(c.typ, c.printed), schema(c.typ, c.written))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Changes) != c.changes || len(r.Warnings) != 0 {
			t.Errorf("%s %s, %s: changes %v, warnings %v", c.typ, c.printed, c.written, r.Changes, r.Warnings)
		}
	}
}

// TestUnescapeMySQL: information_schema's escape level is read, and the
// escapes of the literal under it are kept for the normaliser.
func TestUnescapeMySQL(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ in, want string }{
		{`_utf8mb4\'x\'`, `_utf8mb4'x'`},
		{`_utf8mb4\'a\\nb\'`, `_utf8mb4'a\nb'`},
		{`_utf8mb4\'a\\0b\'`, `_utf8mb4'a\0b'`},
		{`_utf8mb4\'it\\\'s\'`, `_utf8mb4'it\'s'`},
		{`(_utf8mb4\'c\\\\\\\'d\')`, `(_utf8mb4'c\\\'d')`},
		{`CURRENT_TIMESTAMP(6)`, `CURRENT_TIMESTAMP(6)`},
	} {
		if got := migrate.UnescapeMySQL(c.in); got != c.want {
			t.Errorf("%s: %s, want %s", c.in, got, c.want)
		}
	}
}

// TestMySQLDefault: a COLUMN_DEFAULT in the model's spelling.
func TestMySQLDefault(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		typ  string
		expr bool
		def  string
		want string
	}{
		{"text", true, `_utf8mb4\'a\\nb\'`, "('a\nb')"},
		{"text", true, `_utf8mb4\'a\\0b\'`, "('a\x00b')"},
		{"text", true, `_utf8mb4\'it\\\'s\'`, `('it''s')`},
		{"int", true, `42`, `(42)`},
		{"int", true, `1e2`, `(1e2)`},
		{"tinyint(1)", true, `true`, `(true)`},
		{"int", true, `NULL`, `(NULL)`},
		{"int", true, `-(1)`, `-(1)`},
		{"int", true, `(1 + 1)`, `(1 + 1)`},
		{"datetime(6)", true, `CURRENT_TIMESTAMP(6)`, `CURRENT_TIMESTAMP(6)`},
		{"varchar(8)", false, `CURRENT_TIMESTAMP`, `'CURRENT_TIMESTAMP'`},
		{"int", false, `0`, `0`},
		// A binary type's expression: _binary'…' and 0x… of printable
		// ASCII are the model's literal; other bytes are as printed.
		{"blob", true, `_binary\'a\'`, `('a')`},
		{"blob", true, `0x61`, `('a')`},
		{"tinyblob", true, `0x61`, `('a')`},
		{"blob", true, `0x00ff`, `0x00ff`},
		{"varbinary(8)", true, `_binary\'a\'`, `('a')`},
		// _latin1 and _ascii drop on an ASCII literal only.
		{"text", true, `_latin1\'a\'`, `('a')`},
		{"text", true, `_ascii\'a\'`, `('a')`},
		{"text", true, `_latin1\'Ã©\'`, `_latin1'Ã©'`},
		// Guards: _binary on text, a varchar's text, and bit are untouched.
		{"text", true, `_binary\'a\'`, `_binary'a'`},
		{"varchar(8)", false, `0x61`, `'0x61'`},
		{"bit(8)", false, `b'101'`, `b'101'`},
	} {
		if got := migrate.MySQLDefault(c.typ, c.expr, c.def); got != c.want {
			t.Errorf("%s %v %s: %q, want %q", c.typ, c.expr, c.def, got, c.want)
		}
	}
}

// TestMySQLBinaryDefault: a binary or varbinary column's default, as SHOW
// CREATE TABLE prints it, in the model's spelling: printable ASCII is the
// model's literal, binary's padding dropped; other bytes are as printed.
func TestMySQLBinaryDefault(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ typ, shown, want string }{
		{"varbinary(8)", `'a'`, `'a'`},
		{"varbinary(8)", `0x6100FF`, `0x6100FF`},
		{"varbinary(8)", `'a\0b'`, `'a\0b'`},
		{"varbinary(8)", `'\0'`, `'\0'`},
		{"binary(3)", `'\0a\0'`, `'\0a\0'`},
		{"binary(2)", `'a\0'`, `'a'`},
		{"varbinary(8)", `0x4A4B`, `'JK'`},
		{"varbinary(8)", `0x5C`, `'\\'`},
		{"varbinary(8)", `'it''s'`, `'it''s'`},
		{"varbinary(8)", `'it\'s'`, `'it''s'`},
		{"varbinary(8)", `0x00FF`, `0x00FF`},
		{"varbinary(8)", `_binary'a'`, `'a'`},
		{"varbinary(8)", `_binary'\0'`, `_binary'\0'`},
	} {
		if got := migrate.MySQLBinaryDefault(c.typ, c.shown); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.typ, c.shown, got, c.want)
		}
	}
}

// TestMyShownDefault: one column's DEFAULT literal in SHOW CREATE TABLE's
// text, a name or literal holding DEFAULT not misread.
func TestMyShownDefault(t *testing.T) {
	t.Parallel()
	create := "CREATE TABLE `bins` (\n  `id` bigint NOT NULL AUTO_INCREMENT,\n  `code` varbinary(8) NOT NULL DEFAULT 'a',\n" +
		"  `raw` varbinary(8) NOT NULL DEFAULT 0x6100FF,\n  `nul` varbinary(8) DEFAULT 'a\\0b',\n" +
		"  `c DEFAULT d` varbinary(8) NOT NULL DEFAULT 'x'' DEFAULT y',\n  PRIMARY KEY (`id`)\n) ENGINE=InnoDB"
	for _, c := range []struct {
		create, col, want string
		ok                bool
	}{
		{create, "code", `'a'`, true},
		{create, "raw", `0x6100FF`, true},
		{create, "nul", `'a\0b'`, true},
		{create, "c DEFAULT d", `'x'' DEFAULT y'`, true},
		{create, "id", "", false},
		{create, "missing", "", false},
		// A backquote doubled in a name, DEFAULT after other attributes
		// and a comment holding DEFAULT.
		{"CREATE TABLE `t` (\n  `a``b` varbinary(8) NOT NULL DEFAULT 'q',\n  `a` int\n)", "a`b", `'q'`, true},
		{"CREATE TABLE `t` (\n  `a``b` varbinary(8) NOT NULL DEFAULT 'q',\n  `a` int\n)", "a", "", false},
		{"CREATE TABLE `t` (\n  `c` varbinary(8) NOT NULL COMMENT 'no DEFAULT 0x00 here' DEFAULT 0x01\n)", "c", `0x01`, true},
		{"CREATE TABLE `t` (\n  `c` varbinary(8) COMMENT 'DEFAULT ''z''',\n  `d` int DEFAULT '1'\n)", "c", "", false},
		{"CREATE TABLE `t` (\n  `c` varbinary(8) COMMENT 'it\\'s DEFAULT 1' DEFAULT 'x,y'\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4", "c", `'x,y'`, true},
		// MySQL's own order: DEFAULT, then COMMENT.
		{"CREATE TABLE `t` (\n  `c` varbinary(8) NOT NULL DEFAULT 'a' COMMENT 'DEFAULT ''b''',\n  `d` int\n)", "c", `'a'`, true},
		// ANSI_QUOTES quotes names with ", and sql_quote_show_create=OFF
		// leaves them bare.
		{"CREATE TABLE \"t\" (\n  \"c\" varbinary(8) DEFAULT 'a',\n  \"d\"\"e\" varbinary(8) DEFAULT 'b'\n)", "c", `'a'`, true},
		{"CREATE TABLE \"t\" (\n  \"c\" varbinary(8) DEFAULT 'a',\n  \"d\"\"e\" varbinary(8) DEFAULT 'b'\n)", `d"e`, `'b'`, true},
		{"CREATE TABLE t (\n  c varbinary(8) DEFAULT 'a',\n  cc varbinary(8) DEFAULT 'b'\n)", "cc", `'b'`, true},
		{"CREATE TABLE t (\n  c varbinary(8) DEFAULT 'a',\n  cc varbinary(8) DEFAULT 'b'\n)", "c", `'a'`, true},
		// A name holding quotes or a space is never read bare.
		{"CREATE TABLE `t` (\n  `c` varbinary(8) DEFAULT 'a',\n  ```c``` varbinary(8) DEFAULT 'b'\n)", "`c`", `'b'`, true},
		{"CREATE TABLE `t` (\n  `c` varbinary(8) DEFAULT 'a'\n)", "`c`", "", false},
		{"CREATE TABLE t (\n  my varbinary(8) DEFAULT 'a'\n)", "my col", "", false},
		{"CREATE TABLE t (\n  c varbinary(8) DEFAULT 'a'\n)", `"c"`, "", false},
		// Under ANSI_QUOTES a backslash in a name escapes nothing.
		{"CREATE TABLE \"t\" (\n  \"a\\\" varbinary(8) DEFAULT 'q',\n  \"c\" varbinary(8) DEFAULT 'r'\n)", `a\`, `'q'`, true},
		{"CREATE TABLE \"t\" (\n  \"a\\\" varbinary(8) DEFAULT 'q',\n  \"c\" varbinary(8) DEFAULT 'r'\n)", "c", `'r'`, true},
	} {
		got, ok := migrate.MyShownDefault(c.create, c.col)
		if got != c.want || ok != c.ok {
			t.Errorf("%q in %q: %q %v, want %q %v", c.col, c.create, got, ok, c.want, c.ok)
		}
	}
}

// TestDriftMySQLBinaryDefaults: on MySQL a binary or varbinary default is
// compared by its bytes, both sides decoded alike: the same bytes are no
// difference, other bytes a change and never only a warning.
func TestDriftMySQLBinaryDefaults(t *testing.T) {
	t.Parallel()
	schema := func(typ, def string) *migrate.Schema {
		return &migrate.Schema{Dialect: velox.MySQL, Tables: []migrate.Table{{
			Name:      "t",
			Columns:   []migrate.Column{{Name: "c", Type: typ, Default: def}},
			Collation: "utf8mb4_bin",
		}}}
	}
	change := []string{"alter-column t.c (default)"}
	for _, c := range []struct {
		typ, db, model string
		changes        []string
	}{
		{"varbinary(8)", `'a'`, `0x61`, nil},
		{"varbinary(8)", `0x00FF`, `0x00ff`, nil},
		{"varbinary(8)", `'\0'`, `0x00`, nil},
		{"varbinary(8)", `_binary'a'`, `'a'`, nil},
		{"varbinary(8)", `'it\'s'`, `'it''s'`, nil},
		{"varbinary(8)", `'\n\r\t\b\Z\\'`, "0x0A0D09081A5C", nil},
		{"varbinary(8)", `'a\0b'`, `'a\0c'`, change},
		{"varbinary(8)", `'a\0b'`, "'a\x00b'", nil},
		{"varbinary(8)", `'a\0b'`, "'a\x00c'", change},
		{"binary(2)", `'a'`, `'a\0'`, nil},
		{"varbinary(8)", `'a'`, `'a\0'`, change},
		{"varbinary(8)", `0x6100FF`, `'a'`, change},
		{"varbinary(8)", `0x061`, `0x61`, change},
		{"varbinary(8)", `0x061`, `0x061`, nil},
		{"varbinary(8)", `'a'`, `X'61'`, nil},
		{"varbinary(8)", `'ab'`, `x'6162'`, nil},
		{"varbinary(8)", `'b'`, `X'61'`, change},
		{"varbinary(8)", `0x61`, `X'061'`, change},
		// An expression is compared as one, not by bytes.
		{"binary(16)", `uuid_to_bin(uuid())`, `(uuid_to_bin(uuid()))`, nil},
		{"binary(16)", `uuid_to_bin(uuid())`, `(UUID_TO_BIN(UUID()))`, nil},
		{"varbinary(8)", `uuid_to_bin(uuid())`, `(UUID_TO_BIN(UUID()))`, nil},
		{"varbinary(8)", `concat(_utf8mb4'a',_utf8mb4'b')`, `(concat('a','b'))`, nil},
	} {
		r, err := migrate.DriftOf(schema(c.typ, c.db), schema(c.typ, c.model))
		if err != nil {
			t.Fatal(err)
		}
		var changes []string
		for _, ch := range r.Changes {
			changes = append(changes, ch.String())
		}
		if !slices.Equal(changes, c.changes) || len(r.Warnings) > 0 {
			t.Errorf("%s: database %s, model %s: changes %q, warnings %v; want %q", c.typ, c.db, c.model, changes, r.Warnings, c.changes)
		}
	}
	// An expression that differs is a warning, as on any other column.
	r, err := migrate.DriftOf(schema("binary(16)", "uuid_to_bin(uuid(),1)"), schema("binary(16)", "(uuid_to_bin(uuid()))"))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) > 0 || len(r.Warnings) != 1 || r.Warnings[0].Object != "t.c" {
		t.Errorf("a differing expression: changes %v, warnings %v; want one warning on t.c", r.Changes, r.Warnings)
	}
}

// TestDriftNumberDefaults: a number default on a number column is compared
// by value on every dialect; another value is a change, and on a text
// column the text is the value.
func TestDriftNumberDefaults(t *testing.T) {
	t.Parallel()
	schema := func(d velox.Dialect, typ, def string) *migrate.Schema {
		s := &migrate.Schema{Dialect: d, Tables: []migrate.Table{{
			Name:    "t",
			Columns: []migrate.Column{{Name: "n", Type: typ, Default: def}},
		}}}
		if d == velox.MySQL {
			s.Tables[0].Collation = "utf8mb4_bin"
		}
		return s
	}
	for _, c := range []struct {
		d                 velox.Dialect
		typ, printed, src string
		changes           int
	}{
		{velox.Postgres, "numeric(10,2)", "0.00", "0", 0},
		{velox.Postgres, "numeric(10,2)", "0", "0e0", 0},
		{velox.Postgres, "double precision", "1.5", "1.50", 0},
		{velox.Postgres, "integer", "100", "1e2", 0},
		{velox.Postgres, "double precision", "1.5", "1.50001", 1},
		{velox.MySQL, "double", "1.5", "1.50", 0},
		{velox.MySQL, "decimal(5,2)", "-0.50", "-.5", 0},
		{velox.MySQL, "double", "1.5", "1.50001", 1},
		{velox.MySQL, "tinyint(1)", "1", "1.0", 0},
		{velox.SQLite, "real", "0.0", "0", 0},
		{velox.SQLite, "integer", "1", "2", 1},
		{velox.Postgres, "text", "'0.00'", "'0'", 1},
	} {
		r, err := migrate.DriftOf(schema(c.d, c.typ, c.printed), schema(c.d, c.typ, c.src))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Changes) != c.changes || len(r.Warnings) != 0 {
			t.Errorf("%v %s %s, %s: changes %v, warnings %v", c.d, c.typ, c.printed, c.src, r.Changes, r.Warnings)
		}
	}
}

// TestDriftMySQLPrimaryKeyName: MySQL names every primary key PRIMARY, so
// a snapshot naming it otherwise is no rename and no warning.
func TestDriftMySQLPrimaryKeyName(t *testing.T) {
	t.Parallel()
	schema := func(name string) *migrate.Schema {
		return &migrate.Schema{Dialect: velox.MySQL, Tables: []migrate.Table{{
			Name: "t", Collation: "utf8mb4_bin",
			Columns:    []migrate.Column{{Name: "id", Type: "bigint"}},
			PrimaryKey: []string{"id"}, PrimaryKeyName: name,
		}}}
	}
	for _, name := range []string{"t_pkey", ""} {
		r, err := migrate.DriftOf(schema("PRIMARY"), schema(name))
		if err != nil {
			t.Fatal(err)
		}
		if len(r.Changes) != 0 || len(r.Warnings) != 0 {
			t.Errorf("%q: changes %v, warnings %v", name, r.Changes, r.Warnings)
		}
	}
}
