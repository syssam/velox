package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/codegen"
	"github.com/syssam/velox/internal/genrun"
	"github.com/syssam/velox/migrate"
)

// sqlSchema is a schema package of User and Post with an enum, a unique
// field, a table comment, an index and an edge, as Go source by file. The
// enum names a PostgreSQL type, which velox-gen makes only then (decision
// 0090).
func sqlSchema(userIndexes string) map[string]string {
	return map[string]string{
		"veloxschema/user.go": `package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
)

type User struct{ schema.Schema }

func (User) Annotations() []schema.Annotation { return []schema.Annotation{schema.Comment("people")} }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Enum("role").Values("admin", "member").SchemaType(map[string]string{field.Postgres: "user_role"}),
		field.String("email").Unique(),
	}
}

func (User) Edges() []schema.Edge { return []schema.Edge{edge.To("posts", Post.Type)} }

func (User) Indexes() []schema.Index { return []schema.Index{` + userIndexes + `} }
`,
		"veloxschema/post.go": `package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
)

type Post struct{ schema.Schema }

func (Post) Edges() []schema.Edge {
	return []schema.Edge{edge.From("author", User.Type).Ref("posts").Unique()}
}
`,
	}
}

// firstFile is the statements of the first file migrate diff -dry-run
// prints for the new directory fresh.
func firstFile(t *testing.T, d velox.Dialect, out string) []string {
	t.Helper()
	_, rest, ok := strings.Cut(out, "== fresh/0001_")
	if !ok {
		t.Fatalf("no first file in:\n%s", out)
	}
	name, body, _ := strings.Cut(rest, "\n")
	body, _, _ = strings.Cut(body, "== ")
	f, err := migrate.ParseFile(d, "0001_"+name, []byte(body))
	if err != nil {
		t.Fatal(err)
	}
	return f.Statements
}

// Review Focus 5: on success stderr is empty and stdout is exactly the
// statements of the first file migrate diff writes.
func TestMigrateSQL(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, sqlSchema(`index.Fields("role")`))
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		t.Run(d.String(), func(t *testing.T) {
			code, out, errs := runCLI(t, dir, "migrate", "sql", "-dialect", d.String())
			if code != 0 || errs != "" {
				t.Fatalf("sql: exit %d\nstderr:\n%s", code, errs)
			}
			code, diff, errs := runCLI(t, dir, "migrate", "diff", "-dialect", d.String(), "-dry-run", "-dir", "fresh")
			if code != 0 {
				t.Fatalf("diff: exit %d\nstderr:\n%s", code, errs)
			}
			if w := strings.Join(firstFile(t, d, diff), ";\n") + ";\n"; out != w {
				t.Errorf("sql:\n%s\nwant:\n%s", out, w)
			}
			if d == velox.Postgres && (!strings.HasPrefix(out, "CREATE TYPE") || !strings.Contains(out, "COMMENT ON")) {
				t.Errorf("sql: no CREATE TYPE first or no COMMENT ON:\n%s", out)
			}
			_, again, _ := runCLI(t, dir, "migrate", "sql", "-dialect", d.String())
			if again != out {
				t.Errorf("a second run differs:\n%s\nwant:\n%s", again, out)
			}
		})
	}

}

// Review Focus 5: a refusal prints nothing on stdout.
func TestMigrateSQLRefusals(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, sqlSchema(`index.Fields("email").Where("email <> ''")`))
	for name, args := range map[string][]string{
		"no dialect":   {},
		"oracle":       {"-dialect", "oracle"},
		"dir":          {"-dialect", "sqlite", "-dir", "x"},
		"two packages": {"-dialect", "sqlite", "./veloxschema", "./other"},
	} {
		code, out, errs := runCLI(t, dir, append([]string{"migrate", "sql"}, args...)...)
		if code != 2 || out != "" {
			t.Errorf("%s: exit %d, want 2\nstdout:\n%s\nstderr:\n%s", name, code, out, errs)
		}
	}
	code, _, errs := runCLI(t, dir, "migrate", "sql")
	if !strings.HasPrefix(errs, "velox-gen: migrate sql needs -dialect postgres, mysql or sqlite\n") {
		t.Errorf("no dialect: exit %d\nstderr:\n%s", code, errs)
	}
	code, out, errs := runCLI(t, dir, "migrate", "sql", "-dialect", "mysql")
	if code != 1 || out != "" || !regexp.MustCompile(`^velox-gen: veloxschema/user\.go:[0-9]+: `).MatchString(errs) {
		t.Errorf("mysql Where: exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}

	// An empty output would be an empty schema to Atlas, which drops every
	// table, so a package with no schemas is refused.
	empty := tempModule(t, map[string]string{"veloxschema/doc.go": "// Package veloxschema has no entities.\npackage veloxschema\n"})
	code, out, errs = runCLI(t, empty, "migrate", "sql", "-dialect", "postgres")
	if code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: no schemas in ") {
		t.Errorf("no schemas: exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
}

// A refusal prints every position, including the ones inside its reason, as
// a path relative to the working directory.
func TestMigrateSQLPositionsAreRelative(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	const head = `package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

`
	// lineOf is the 1-based line of the first line of src that has sub.
	lineOf := func(t *testing.T, src, sub string) int {
		t.Helper()
		for i, l := range strings.Split(src, "\n") {
			if strings.Contains(l, sub) {
				return i + 1
			}
		}
		t.Fatalf("no %q in:\n%s", sub, src)
		return 0
	}

	t.Run("row limit", func(t *testing.T) {
		post := head + `type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field {
	return []schema.Field{
		field.String("body").MaxLen(10000),
		field.String("summary").MaxLen(10000),
	}
}
`
		dir := tempModule(t, map[string]string{"veloxschema/post.go": post})
		code, out, errs := runCLI(t, dir, "migrate", "sql", "-dialect", "mysql")
		want := "velox-gen: veloxschema/post.go: schema Post: MySQL rows hold at most 65535 bytes; these need 80012:\n" +
			fmt.Sprintf("  veloxschema/post.go:%d: field \"body\": varchar(10000), 40002 bytes\n", lineOf(t, post, `"body"`)) +
			fmt.Sprintf("  veloxschema/post.go:%d: field \"summary\": varchar(10000), 40002 bytes\n", lineOf(t, post, `"summary"`)) +
			"  the other columns and the NULL bitmap: 8 bytes\n" +
			"  use Text() or a smaller MaxLen\n"
		if code != 1 || out != "" || errs != want {
			t.Errorf("exit %d, want 1\nstdout:\n%s\nstderr:\n%s\nwant:\n%s", code, out, errs, want)
		}
		if strings.Contains(errs, dir) {
			t.Errorf("an absolute path is printed:\n%s", errs)
		}
	})

	t.Run("shared enum", func(t *testing.T) {
		enum := func(name, fieldName, values string) string {
			return head + "type " + name + " struct{ schema.Schema }\n\n" +
				"func (" + name + ") Fields() []schema.Field {\n\treturn []schema.Field{\n" +
				"\t\tfield.Enum(\"" + fieldName + "\").Values(" + values + ").SchemaType(map[string]string{field.Postgres: \"order_status\"}),\n" +
				"\t}\n}\n"
		}
		invoice := enum("Invoice", "state", `"paid", "new"`)
		dir := tempModule(t, map[string]string{
			"veloxschema/order.go":   enum("Order", "status", `"new", "paid"`),
			"veloxschema/invoice.go": invoice,
		})
		code, out, errs := runCLI(t, dir, "migrate", "sql", "-dialect", "postgres")
		want := fmt.Sprintf("(veloxschema/invoice.go:%d), with other values", lineOf(t, invoice, `"state"`))
		if code != 1 || out != "" || !strings.Contains(errs, want) {
			t.Errorf("exit %d, want 1\nstdout:\n%s\nstderr:\n%s\nwant it to hold %s", code, out, errs, want)
		}
		if strings.Contains(errs, dir) {
			t.Errorf("an absolute path is printed:\n%s", errs)
		}
	})
}

// relErrors leaves alone what is not inside the working directory: a file
// outside it, a path that only ends like it, and an error with no position.
func TestRelErrorsKeepsWhatIsOutside(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "app")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	sep := string(filepath.Separator)
	in := filepath.Join(dir, "veloxschema", "a.go")
	outside := filepath.Join(filepath.Dir(dir), "other", "b.go")
	lookalike := filepath.Join("x", dir, "c.go")
	for _, c := range []struct{ name, pos, reason, wantPos, wantReason string }{
		{"inside", in, "see " + in + ":3", "veloxschema" + sep + "a.go", "see veloxschema" + sep + "a.go:3"},
		{"inside with a line", in + ":2", "see (" + in + ":3)", "veloxschema" + sep + "a.go:2", "see (veloxschema" + sep + "a.go:3)"},
		{"outside", outside, "see " + outside + ":3", outside, "see " + outside + ":3"},
		{"outside with a line", outside + ":2", "see " + outside + ":3", outside + ":2", "see " + outside + ":3"},
		{"repeated", in, in + ":1 and (" + in + ":2) and " + in + ":3", "veloxschema" + sep + "a.go", "veloxschema" + sep + "a.go:1 and (veloxschema" + sep + "a.go:2) and veloxschema" + sep + "a.go:3"},
		{"directory at the end", "", "see " + dir, "", "see " + dir},
		{"sibling", "", "see " + dir + "2" + sep + "c.go", "", "see " + dir + "2" + sep + "c.go"},
		{"lookalike", "", "see " + lookalike, "", "see " + lookalike},
		{"no position", "", "plain", "", "plain"},
		{"relative", "veloxschema/a.go:2", "plain", "veloxschema/a.go:2", "plain"},
	} {
		got, ok := errors.AsType[*codegen.Error](genrun.RelErrors(dir, &codegen.Error{Pos: c.pos, Reason: c.reason}))
		if !ok {
			t.Fatalf("%s: not a *veloxgen.Error", c.name)
		}
		if got.Pos != c.wantPos || got.Reason != c.wantReason {
			t.Errorf("%s: got %q %q, want %q %q", c.name, got.Pos, got.Reason, c.wantPos, c.wantReason)
		}
	}
}

// The filesystem root holds every path, so relErrors cuts nothing from a
// reason whose working directory is the root: a slash is not a prefix.
func TestRelErrorsAtTheRoot(t *testing.T) {
	root := string(filepath.Separator)
	reason := "a " + root + " b and " + root + "usr" + root + "lib"
	if got, ok := errors.AsType[*codegen.Error](genrun.RelErrors(root, &codegen.Error{Reason: reason})); !ok || got.Reason != reason {
		t.Errorf("got %v, want the reason %q as it was", got, reason)
	}
}
