package migrate_test

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

var update = flag.Bool("update", false, "write the golden files")

func lint(t *testing.T, from *migrate.Schema, cs []migrate.Change) []migrate.Finding {
	t.Helper()
	return migrate.Lint(from, cs)
}

// findingLine is a finding in one line: its change, code, class and target,
// the file of its position (the line number would move the goldens whenever
// cases_test.go changes), and the message.
func findingLine(f migrate.Finding) string {
	s := fmt.Sprintf("%d %s %s %s", f.Change, f.Code, f.Class, f.Target)
	if f.Pos != "" {
		file := f.Pos
		if i := strings.LastIndex(file, ":"); i >= 0 {
			file = file[:i]
		}
		s += " [" + filepath.Base(file) + "]"
	}
	return s + ": " + f.Message
}

// codes are the findings as code@target, in order.
func codes(fs []migrate.Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Code + "@" + f.Target
	}
	return out
}

// caseFindings is the findings of the case on d.
func caseFindings(t *testing.T, name string, d velox.Dialect) []migrate.Finding {
	t.Helper()
	c := caseNamed(name)
	from, to := models(t, c, d)
	return lint(t, from, diff(t, from, to, c.opts))
}

func TestLintGolden(t *testing.T) {
	for _, d := range allDialects {
		var b strings.Builder
		for _, c := range cases {
			if !slices.Contains(c.on(), d) {
				continue
			}
			from, to := models(t, c, d)
			fmt.Fprintf(&b, "== %s\n", c.name)
			for _, f := range lint(t, from, diff(t, from, to, c.opts)) {
				b.WriteString(findingLine(f) + "\n")
			}
		}
		path := filepath.Join("testdata", "lint", d.String()+".txt")
		if *update {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(want) != b.String() {
			t.Errorf("%s differs from the findings (run with -update to write it):\n%s", path, b.String())
		}
	}
}

// TestLintCases pins the findings of the cases that matter most, by code and
// target, apart from the goldens, which a regenerated file would accept.
func TestLintCases(t *testing.T) {
	type key struct {
		name string
		d    velox.Dialect
	}
	pgM := velox.Postgres
	my := velox.MySQL
	lite := velox.SQLite
	want := map[key][]string{
		{"create-tables", pgM}:             nil,
		{"create-tables", lite}:            nil,
		{"drop-table", pgM}:                {"drop-table@posts"},
		{"drop-table", my}:                 {"drop-table@posts"},
		{"rename-table", pgM}:              {"rename-table@accounts"},
		{"rename-table", my}:               {"rename-table@accounts"},
		{"rename-column", pgM}:             {"rename-column@users.full_name"},
		{"set-not-null", pgM}:              {"set-not-null@users.bio"},
		{"set-not-null", my}:               {"set-not-null@users.bio"},
		{"set-not-null", lite}:             {"set-not-null@users.bio", "sqlite-rebuild@users"},
		{"drop-not-null", pgM}:             nil,
		{"drop-not-null", lite}:            {"sqlite-rebuild@users"},
		{"change-default", pgM}:            nil,
		{"add-index", pgM}:                 {"add-index@users.users_created_at"},
		{"add-unique", pgM}:                {"add-unique@users.users_name_unique"},
		{"rename-index", pgM}:              nil,
		{"enum-append", pgM}:               nil,
		{"enum-append", my}:                nil,
		{"enum-insert", pgM}:               nil,
		{"enum-insert", my}:                {"reorder-enum@users.role"},
		{"enum-remove", pgM}:               {"drop-enum-value@users_role"},
		{"enum-remove", my}:                {"drop-enum-value@users.role"},
		{"enum-reorder", pgM}:              {"reorder-enum@users_role"},
		{"enum-reorder", my}:               {"reorder-enum@users.role"},
		{"widen-int", pgM}:                 {"widen-type@users.logins"},
		{"widen-int", my}:                  {"widen-type@users.logins"},
		{"widen-varchar", pgM}:             {"widen-type@users.email"},
		{"widen-varchar", my}:              {"widen-type@users.email"},
		{"narrow-varchar", pgM}:            {"alter-type@users.email"},
		{"narrow-varchar", my}:             {"alter-type@users.email"},
		{"collation", my}:                  {"convert-collation@users"},
		{"add-identity", pgM}:              {"add-identity@users.id"},
		{"serial-to-identity", pgM}:        {"add-identity@users.id", "serial-to-identity@users.id"},
		{"add-column-schema-default", pgM}: {"volatile-default@users.seen_at"},
	}
	for k, w := range want {
		t.Run(k.name+"/"+k.d.String(), func(t *testing.T) {
			if got := codes(caseFindings(t, k.name, k.d)); !slices.Equal(got, w) {
				t.Errorf("findings\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(w, "\n"))
			}
		})
	}
}

// TestLintAddColumn: a field added to a populated table is safe when it is
// optional, nillable or has a default, and blocks when it is required.
func TestLintAddColumn(t *testing.T) {
	for _, name := range []string{"add-column-optional", "add-column-nillable", "add-column-default"} {
		for _, d := range allDialects {
			if got := caseFindings(t, name, d); len(got) > 0 {
				t.Errorf("%s on %v: %v", name, d, codes(got))
			}
		}
	}
	c := caseNamed("add-column-required")
	for _, d := range allDialects {
		from, to := models(t, c, d)
		cs := diff(t, from, to, c.opts)
		var got []migrate.Finding
		for _, f := range lint(t, from, cs) {
			if f.Code == "add-required-column" {
				got = append(got, f)
			}
		}
		if len(got) != 1 || got[0].Class != migrate.Destructive || got[0].Target != "users.nickname" {
			t.Errorf("on %v: %+v", d, got)
		}
		// The same change on a table from lacks has no rows to fill.
		bare := migrate.Clone(from)
		bare.Tables = slices.DeleteFunc(bare.Tables, func(tb migrate.Table) bool { return tb.Name == "users" })
		if fs := lint(t, bare, cs); len(fs) > 0 {
			t.Errorf("on %v, users missing from from: %v", d, codes(fs))
		}
	}
}

func TestLintWidenType(t *testing.T) {
	pgM, my, lite := velox.Postgres, velox.MySQL, velox.SQLite
	const (
		widen = "widen-type"
		alter = "alter-type"
	)
	for _, r := range []struct {
		d        velox.Dialect
		from, to string
		code     string
		class    migrate.Class
	}{
		{pgM, "smallint", "integer", widen, migrate.Review},
		{pgM, "integer", "bigint", widen, migrate.Review},
		{pgM, "smallint", "bigint", widen, migrate.Review},
		{pgM, "real", "double precision", widen, migrate.Review},
		{pgM, "character varying(10)", "character varying(20)", widen, migrate.Safe},
		{pgM, "character varying(10)", "text", widen, migrate.Safe},
		{pgM, "character varying(10)", "character varying", widen, migrate.Safe},
		{pgM, "integer", "smallint", alter, migrate.Destructive},
		{pgM, "bigint", "integer", alter, migrate.Destructive},
		{pgM, "double precision", "real", alter, migrate.Destructive},
		{pgM, "character varying(20)", "character varying(10)", alter, migrate.Destructive},
		{pgM, "text", "character varying(10)", alter, migrate.Destructive},
		{pgM, "integer", "text", alter, migrate.Destructive},
		{pgM, "character varying(10)", `"users_role"`, alter, migrate.Destructive},
		{pgM, "bigint", "numeric(20,0)", alter, migrate.Destructive},
		{my, "tinyint", "smallint", widen, migrate.Review},
		{my, "smallint", "int", widen, migrate.Review},
		{my, "int", "bigint", widen, migrate.Review},
		{my, "tinyint", "bigint", widen, migrate.Review},
		{my, "smallint unsigned", "int unsigned", widen, migrate.Review},
		{my, "int unsigned", "bigint unsigned", widen, migrate.Review},
		{my, "int", "bigint unsigned", alter, migrate.Destructive},
		{my, "int unsigned", "bigint", alter, migrate.Destructive},
		{my, "bigint", "int", alter, migrate.Destructive},
		{my, "float", "double", widen, migrate.Review},
		{my, "double", "float", alter, migrate.Destructive},
		{my, "varchar(10)", "varchar(20)", widen, migrate.Review},
		{my, "varchar(20)", "varchar(10)", alter, migrate.Destructive},
		{my, "varchar(20)", "text", alter, migrate.Destructive},
		{my, "tinytext", "text", widen, migrate.Review},
		{my, "text", "mediumtext", widen, migrate.Review},
		{my, "mediumtext", "longtext", widen, migrate.Review},
		{my, "tinytext", "longtext", widen, migrate.Review},
		{my, "text", "tinytext", alter, migrate.Destructive},
		{my, "tinyblob", "blob", widen, migrate.Review},
		{my, "blob", "mediumblob", widen, migrate.Review},
		{my, "mediumblob", "longblob", widen, migrate.Review},
		{my, "longblob", "blob", alter, migrate.Destructive},
		{my, "text", "blob", alter, migrate.Destructive},
		{my, "tinyint(1)", "tinyint", alter, migrate.Destructive},
		{my, "datetime(6)", "datetime", alter, migrate.Destructive},
		{lite, "integer", "real", "", migrate.Safe},
		{lite, "text", "integer", "", migrate.Safe},
	} {
		t.Run(fmt.Sprintf("%v/%s/%s", r.d, r.from, r.to), func(t *testing.T) {
			ref := migrate.TableRef{Name: "t"}
			col := func(typ string) migrate.Column { return migrate.Column{Name: "c", Type: typ} }
			from := &migrate.Schema{Dialect: r.d, Tables: []migrate.Table{{Name: "t", Columns: []migrate.Column{col(r.from)}}}}
			cs := []migrate.Change{migrate.AlterColumn{Table: ref, From: col(r.from), To: col(r.to), What: migrate.ColumnType}}
			var got []migrate.Finding
			for _, f := range lint(t, from, cs) {
				if f.Code == widen || f.Code == alter {
					got = append(got, f)
				}
			}
			if r.code == "" {
				if len(got) > 0 {
					t.Errorf("SQLite gives %v; only the rebuild is a finding", codes(got))
				}
				return
			}
			if len(got) != 1 || got[0].Code != r.code || got[0].Class != r.class || got[0].Target != "t.c" {
				t.Errorf("got %+v, want %s %v", got, r.code, r.class)
			}
		})
	}
}

// TestLintReplacementsAreQuiet: what a type change or a replacement drops and
// adds again leaves no finding of its own.
func TestLintReplacementsAreQuiet(t *testing.T) {
	for _, d := range pgMySQL {
		cs := caseChanges(t, "key-type-change", d)
		if !slices.ContainsFunc(cs, func(c migrate.Change) bool { _, ok := c.(migrate.DropForeignKey); return ok }) {
			t.Fatalf("key-type-change on %v drops no foreign key", d)
		}
		for _, f := range caseFindings(t, "key-type-change", d) {
			if f.Code == "drop-foreign-key" || f.Code == "add-foreign-key" {
				t.Errorf("key-type-change on %v: %s", d, findingLine(f))
			}
		}
	}
	for _, d := range allDialects {
		cs := caseChanges(t, "fk-index-replaced", d)
		if !slices.ContainsFunc(cs, func(c migrate.Change) bool { _, ok := c.(migrate.DropIndex); return ok }) {
			t.Fatalf("fk-index-replaced on %v drops no index", d)
		}
		for _, f := range caseFindings(t, "fk-index-replaced", d) {
			if f.Code == "drop-index" || f.Code == "drop-foreign-key" || f.Code == "add-foreign-key" {
				t.Errorf("fk-index-replaced on %v: %s", d, findingLine(f))
			}
		}
	}
	// The index a replacement drops stays loud when it is not led by the new one.
	for _, d := range allDialects {
		var drops []string
		for _, f := range caseFindings(t, "fk-index-restored", d) {
			if f.Code == "drop-index" {
				drops = append(drops, f.Target)
			}
		}
		if len(drops) != 1 {
			t.Errorf("fk-index-restored on %v: drop-index findings %v", d, drops)
		}
	}
}

// TestLintSQLiteRebuild: a change SQLite can only make by rebuilding the
// table says so, and the drops folded into the rebuild say nothing.
func TestLintSQLiteRebuild(t *testing.T) {
	has := func(fs []migrate.Finding, code string) bool {
		return slices.ContainsFunc(fs, func(f migrate.Finding) bool { return f.Code == code })
	}
	for name, rebuild := range map[string]bool{
		"rebuild-parent":            true,
		"add-foreign-key":           true,
		"drop-foreign-key":          true,
		"set-not-null":              true,
		"change-default":            true,
		"add-column-required":       true,
		"add-column-schema-default": true,
		"drop-column":               true, // its column has a unique index
		"drop-column-in-index":      true,
		"add-column-optional":       false,
		"add-column-default":        false,
		"add-index":                 false,
		"rename-column":             false,
		"rename-index":              false,
		"create-tables":             false,
	} {
		fs := caseFindings(t, name, velox.SQLite)
		if has(fs, "sqlite-rebuild") != rebuild {
			t.Errorf("%s: sqlite-rebuild is %v: %v", name, !rebuild, codes(fs))
		}
	}
	for _, name := range []string{"drop-column", "drop-column-in-index", "drop-foreign-key"} {
		if fs := caseFindings(t, name, velox.SQLite); has(fs, "drop-index") || has(fs, "drop-foreign-key") || has(fs, "drop-check") {
			t.Errorf("%s: a drop folded into the rebuild has a finding: %v", name, codes(fs))
		}
	}
	// Other dialects never rebuild.
	for _, c := range cases {
		for _, d := range c.on() {
			if d == velox.SQLite {
				continue
			}
			from, to := models(t, c, d)
			if has(lint(t, from, diff(t, from, to, c.opts)), "sqlite-rebuild") {
				t.Errorf("%s on %v has sqlite-rebuild", c.name, d)
			}
		}
	}
}

// TestLintEnumChecks: a check-based enum losing a value is destructive, and
// gaining one is only a new check.
func TestLintEnumChecks(t *testing.T) {
	got := codes(caseFindings(t, "enum-remove", velox.SQLite))
	if !slices.Contains(got, "drop-enum-value@users.users_role_check") || !slices.Contains(got, "add-check@users.users_role_check") {
		t.Errorf("enum-remove on SQLite: %v", got)
	}
	got = codes(caseFindings(t, "enum-append", velox.SQLite))
	if slices.ContainsFunc(got, func(s string) bool {
		return strings.HasPrefix(s, "drop-enum-value") || strings.HasPrefix(s, "reorder-enum")
	}) || !slices.Contains(got, "add-check@users.users_role_check") {
		t.Errorf("enum-append on SQLite: %v", got)
	}
}

// TestLintPos: a finding carries the position of the element of the schema it
// is about.
func TestLintPos(t *testing.T) {
	c := caseNamed("add-column-required")
	from, to := models(t, c, velox.Postgres)
	want := column(table(to, "users"), "nickname").Pos
	if want == "" {
		t.Fatal("the column has no position")
	}
	fs := lint(t, from, diff(t, from, to, c.opts))
	if len(fs) != 1 || fs[0].Pos != want {
		t.Errorf("add-required-column Pos %q, want %q: %+v", fs[0].Pos, want, fs)
	}
	c = caseNamed("drop-column")
	from, to = models(t, c, velox.Postgres)
	want = column(table(from, "users"), "email").Pos
	for _, f := range lint(t, from, diff(t, from, to, c.opts)) {
		if f.Code == "drop-column" && f.Pos != want {
			t.Errorf("drop-column Pos %q, want %q", f.Pos, want)
		}
	}
}

func TestParseAllow(t *testing.T) {
	got, err := migrate.ParseAllow("drop-column=users.name,drop-table")
	want := []migrate.Allow{{Code: "drop-column", Target: "users.name"}, {Code: "drop-table"}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("got %v, %v; want %v", got, err, want)
	}
	if got, err = migrate.ParseAllow(" add-unique = users.users_name_unique , set-not-null "); err != nil || len(got) != 2 || got[0].Target != "users.users_name_unique" {
		t.Errorf("with spaces: %v, %v", got, err)
	}
	for _, s := range []string{"rename-table", "", "drop-column=,", ",drop-table", "drop-table,,drop-column", "drop-column=", "nonsense", "widen-type=users.x"} {
		_, err := migrate.ParseAllow(s)
		var e *migrate.Error
		if !errors.As(err, &e) || e.Code != "allow" {
			t.Errorf("ParseAllow(%q) = %v, want an *Error with Code allow", s, err)
		}
	}
}

func TestBlocked(t *testing.T) {
	fs := []migrate.Finding{
		{Code: "drop-column", Class: migrate.Destructive, Change: 0, Target: "users.name"},
		{Code: "rename-table", Class: migrate.Review, Change: 1, Target: "accounts"},
		{Code: "drop-column", Class: migrate.Destructive, Change: 2, Target: "users.email"},
		{Code: "drop-table", Class: migrate.Destructive, Change: 3, Target: "posts"},
		{Code: "widen-type", Class: migrate.Safe, Change: 4, Target: "users.x"},
	}
	for _, r := range []struct {
		allow []migrate.Allow
		want  []int
	}{
		{nil, []int{0, 2, 3}},
		{[]migrate.Allow{{Code: "drop-column", Target: "users.name"}}, []int{2, 3}},
		{[]migrate.Allow{{Code: "drop-column"}}, []int{3}},
		{[]migrate.Allow{{Code: "drop-column"}, {Code: "drop-table", Target: "posts"}}, nil},
		{[]migrate.Allow{{Code: "drop-table", Target: "users"}}, []int{0, 2, 3}},
		{[]migrate.Allow{{Code: "drop-column", Target: "posts"}}, []int{0, 2, 3}},
		{[]migrate.Allow{{Code: "rename-table"}}, []int{0, 2, 3}},
	} {
		var got []int
		for _, f := range migrate.Blocked(fs, r.allow) {
			got = append(got, f.Change)
		}
		if !slices.Equal(got, r.want) {
			t.Errorf("allow %v blocks %v, want %v", r.allow, got, r.want)
		}
	}
}

// TestCaseAllowsCoverTheCases: each case's allow names every destructive
// finding it has, and nothing less does.
func TestCaseAllowsCoverTheCases(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		fs := lint(t, from, diff(t, from, to, c.opts))
		if b := migrate.Blocked(fs, c.allow); len(b) > 0 {
			t.Errorf("allow %v leaves %v", c.allow, codes(b))
		}
		destructive := 0
		for _, f := range fs {
			if f.Class == migrate.Destructive {
				destructive++
			}
		}
		if (destructive > 0) != (len(migrate.Blocked(fs, nil)) > 0) {
			t.Errorf("Blocked(nil) disagrees with the destructive findings")
		}
	})
}

func TestLintDoesNotMutate(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		cs := diff(t, from, to, c.opts)
		f, want := migrate.Clone(from), diff(t, from, to, c.opts)
		lint(t, from, cs)
		if !reflect.DeepEqual(from, f) || !reflect.DeepEqual(cs, want) {
			t.Error("Lint changed its arguments")
		}
	})
}

func TestLintDeterministic(t *testing.T) {
	eachCase(t, func(t *testing.T, c testCase, from, to *migrate.Schema) {
		cs := diff(t, from, to, c.opts)
		a, b := lint(t, from, cs), lint(t, from, cs)
		if !reflect.DeepEqual(a, b) {
			t.Errorf("two calls differ:\n%v\n--\n%v", a, b)
		}
		seen := map[[2]any]bool{}
		for _, f := range a {
			k := [2]any{f.Change, f.Code}
			if seen[k] {
				t.Errorf("change %d has two %s findings", f.Change, f.Code)
			}
			seen[k] = true
			if f.Change < 0 || f.Change >= len(cs) {
				t.Errorf("finding for change %d of %d", f.Change, len(cs))
			}
		}
		if !slices.IsSortedFunc(a, func(x, y migrate.Finding) int { return x.Change - y.Change }) {
			t.Error("findings are not in the order of the changes")
		}
	})
}
