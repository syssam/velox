package migrate_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestParseAllowParenthesizedTarget: a comma inside parentheses is the
// target's, as in an unnamed index's or key's label, and the target round-trips
// through a file's directive.
func TestParseAllowParenthesizedTarget(t *testing.T) {
	t.Parallel()
	got, err := migrate.ParseAllow("unmodeled-loss=t.(a, b),drop-table=x, unmodeled-loss=t.check (a IN (1, 2))")
	want := []migrate.Allow{{Code: "unmodeled-loss", Target: "t.(a, b)"}, {Code: "drop-table", Target: "x"}, {Code: "unmodeled-loss", Target: "t.check (a IN (1, 2))"}}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
	f, err := migrate.ParseFile(velox.SQLite, "0002_x.sql", []byte("-- velox:allow unmodeled-loss \"t.(a, b)\"\nSELECT 1;\n"))
	if err != nil || len(f.Directives.Allow) != 1 || f.Directives.Allow[0].Target != "t.(a, b)" {
		t.Errorf("the directive: %v, %v", f.Directives.Allow, err)
	}
	if _, err := migrate.ParseAllow("drop-table=a),nonsense"); err == nil {
		t.Error("a stray parenthesis swallowed the next item")
	}
}

// TestPostgresLimitedTargetUsing: every spelling of a string target of limited
// length gets the column through the assignment cast, with a character past
// the length on a string over it, so a row over the length fails rather than
// being cut or trimmed; one that fits already goes without USING; a bit target
// from a string goes through an unlimited cast, which the assignment then
// checks.
func TestPostgresLimitedTargetUsing(t *testing.T) {
	t.Parallel()
	over := ` USING CASE WHEN char_length("c") > 3 THEN "c" || '!' ELSE "c" END`
	for _, c := range []struct{ from, to, using string }{
		{"text", "bpchar(3)", over},
		{"text", "pg_catalog.varchar(3)", over},
		{"text", "pg_catalog.bpchar(3)", over},
		{"text", `"pg_catalog"."varchar"(3)`, over},
		{"text", `"varchar"(3)`, over},
		{"text", "nchar(3)", over},
		{"text", "national character varying(3)", over},
		{"text", "char varying(3)", over},
		{"character varying(10)", "character(3)", over},
		{"character", "varchar(3)", ""},
		{"character varying(3)", "character varying(5)", ""},
		{"text[]", "pg_catalog.bpchar(3)[]", ` USING CASE WHEN "c"::text[]::varchar(3)[]::text[] IS DISTINCT FROM "c"::text[] THEN "c"::text[] || ARRAY[repeat('!', 4)] ELSE "c"::text[] END`},
		{"varchar(3)[]", "varchar(5)[]", ""},
		{"text", "bit(3)", ` USING "c"::varbit`},
		{"text", "pg_catalog.bit(3)", ` USING "c"::varbit`},
		{"text", "varbit(3)", ` USING "c"::varbit`},
		{"text", "bit varying(3)", ` USING "c"::varbit`},
		{"bit(10)", "pg_catalog.bit(3)", ""},
		{"integer", "text", ` USING "c"::text`},
		{"text", "pg_catalog.bpchar", ` USING "c"::pg_catalog.bpchar`}, // unlimited
		{"text", "integer", ` USING "c"::integer`},
	} {
		if got := migrate.PgUsing(`"c"`, c.from, c.to); got != c.using {
			t.Errorf("%s to %s: %q; want %q", c.from, c.to, got, c.using)
		}
	}
}

// TestLintPostgresTruncatingTargets: a target velox cannot classify, and a
// number to bit, keep a destructive finding that says what the cast may do.
func TestLintPostgresTruncatingTargets(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	retype := func(from, to string) string {
		t.Helper()
		f, u := oneTable(d, migrate.Column{Name: "c", Type: from}), oneTable(d, migrate.Column{Name: "c", Type: to})
		cs, err := migrate.Diff(f, u, migrate.DiffOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, fd := range migrate.Lint(f, cs) {
			if fd.Code == "alter-type" && fd.Class == migrate.Destructive {
				return fd.Message
			}
		}
		t.Fatalf("%s to %s has no destructive alter-type", from, to)
		return ""
	}
	for _, to := range []string{"d3", `"d3"`, "public.d3", `"char"`} {
		if m := retype("text", to); !strings.Contains(m, "may truncate") {
			t.Errorf("text to %s: %q", to, m)
		}
	}
	for _, from := range []string{"integer", "bigint", "smallint"} {
		if m := retype(from, "bit(3)"); !strings.Contains(m, "low bits") {
			t.Errorf("%s to bit(3): %q", from, m)
		}
	}
	if m := retype("text", "integer"); strings.Contains(m, "may truncate") {
		t.Errorf("text to integer: %q", m)
	}
	// An enum type the plan names is classified.
	f := oneTable(d, migrate.Column{Name: "c", Type: "text"})
	u := oneTable(d, migrate.Column{Name: "c", Type: `"mood"`})
	u.Enums = []migrate.Enum{{Name: "mood", Values: []string{"a"}}}
	cs, err := migrate.Diff(f, u, migrate.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range migrate.Lint(f, cs) {
		if strings.Contains(fd.Message, "may truncate") {
			t.Errorf("an enum: %q", fd.Message)
		}
	}
}

// TestRenderRefusesEnumArray: a swap of an enum type a column holds an array of
// could never run, DROP TYPE failing on the array; the plan is refused, naming
// the column.
func TestRenderRefusesEnumArray(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	schema := func(values ...string) *migrate.Schema {
		s := oneTable(d, migrate.Column{Name: "moods", Type: `"mood"[]`, Nullable: true})
		s.Enums = []migrate.Enum{{Name: "mood", Values: values}}
		return s
	}
	_, err := migrate.NewPlan(schema("a", "b"), schema("b", "a"), allowAll("reorder-enum"))
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "render" || !strings.Contains(err.Error(), "t.moods") || !strings.Contains(err.Error(), "array") {
		t.Errorf("NewPlan: %v", err)
	}
	// Adding a value needs no swap.
	if _, err := migrate.NewPlan(schema("a"), schema("a", "b"), migrate.PlanOptions{}); err != nil {
		t.Errorf("adding a value: %v", err)
	}
}

// TestRenderRefusesForeignCollation: CONVERT TO CHARACTER SET utf8mb4 with a
// collation of another character set always fails, leaving a dirty version, so
// the plan is refused.
func TestRenderRefusesForeignCollation(t *testing.T) {
	t.Parallel()
	d := velox.MySQL
	schema := func(coll string) *migrate.Schema {
		s := oneTable(d)
		s.Tables[0].Collation = coll
		return s
	}
	_, err := migrate.NewPlan(schema("utf8mb4_bin"), schema("latin1_bin"), allowAll("convert-collation"))
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "render" || !strings.Contains(err.Error(), "latin1_bin") || !strings.Contains(err.Error(), "utf8mb4") {
		t.Errorf("NewPlan: %v", err)
	}
	if _, err := migrate.NewPlan(schema("utf8mb4_bin"), schema("utf8mb4_general_ci"), allowAll("convert-collation")); err != nil {
		t.Errorf("a utf8mb4 collation: %v", err)
	}
}

// TestMySQLCreateNamesTheCollationsCharset: CREATE TABLE of a collation of
// another character set, as a baseline can hold, is not refused by MySQL.
func TestMySQLCreateNamesTheCollationsCharset(t *testing.T) {
	t.Parallel()
	d := velox.MySQL
	db, _ := freshDB(t, d)
	s := oneTable(d, migrate.Column{Name: "c", Type: "varchar(10)", Nullable: true})
	s.Tables[0].Collation = "latin1_bin"
	mustUp(t, db, dirOf(t, d, step{to: s, name: "base"}))
}
