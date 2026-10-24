package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

func TestNormalizeDefault(t *testing.T) {
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
	ctx := context.Background()
	db := openDB(t, "sqlite", ":memory:")
	var e *migrate.Error
	if _, err := migrate.Inspect(ctx, db, velox.Dialect(99), nil); !errors.As(err, &e) || e.Code != "dialect" {
		t.Errorf("unknown dialect: %v", err)
	}
	if _, err := migrate.Inspect(ctx, nil, velox.SQLite, nil); !errors.As(err, &e) || e.Code != "nil" {
		t.Errorf("no database: %v", err)
	}
	if _, err := migrate.Drift(ctx, db, nil); !errors.As(err, &e) || e.Code != "nil" {
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
	} {
		if got := migrate.MySQLDefault(c.typ, c.expr, c.def); got != c.want {
			t.Errorf("%s %v %s: %q, want %q", c.typ, c.expr, c.def, got, c.want)
		}
	}
}

// TestDriftNumberDefaults: a number default on a number column is compared
// by value on every dialect; another value is a change, and on a text
// column the text is the value.
func TestDriftNumberDefaults(t *testing.T) {
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
