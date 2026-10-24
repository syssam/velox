package main

import (
	"regexp"
	"strings"
	"testing"

	velox2 "github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// sqlSchema is a schema package of User and Post with an enum, a unique
// field, a table comment, an index and an edge, as Go source by file.
func sqlSchema(userIndexes string) map[string]string {
	return map[string]string{
		"schema/user.go": `package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/index"
)

type User struct{ schema.Schema }

func (User) Annotations() []schema.Annotation { return []schema.Annotation{schema.Comment("people")} }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.Enum("role").Values("admin", "member"),
		field.String("email").Unique(),
	}
}

func (User) Edges() []schema.Edge { return []schema.Edge{edge.To("posts", Post.Type)} }

func (User) Indexes() []schema.Index { return []schema.Index{` + userIndexes + `} }
`,
		"schema/post.go": `package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
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
func firstFile(t *testing.T, d velox2.Dialect, out string) []string {
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
	for _, d := range []velox2.Dialect{velox2.Postgres, velox2.MySQL, velox2.SQLite} {
		t.Run(d.String(), func(t *testing.T) {
			code, out, errs := velox(t, dir, "migrate", "sql", "-dialect", d.String())
			if code != 0 || errs != "" {
				t.Fatalf("sql: exit %d\nstderr:\n%s", code, errs)
			}
			code, diff, errs := velox(t, dir, "migrate", "diff", "-dialect", d.String(), "-dry-run", "-dir", "fresh")
			if code != 0 {
				t.Fatalf("diff: exit %d\nstderr:\n%s", code, errs)
			}
			if w := strings.Join(firstFile(t, d, diff), ";\n") + ";\n"; out != w {
				t.Errorf("sql:\n%s\nwant:\n%s", out, w)
			}
			if d == velox2.Postgres && (!strings.HasPrefix(out, "CREATE TYPE") || !strings.Contains(out, "COMMENT ON")) {
				t.Errorf("sql: no CREATE TYPE first or no COMMENT ON:\n%s", out)
			}
			_, again, _ := velox(t, dir, "migrate", "sql", "-dialect", d.String())
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
		"two packages": {"-dialect", "sqlite", "./schema", "./other"},
	} {
		code, out, errs := velox(t, dir, append([]string{"migrate", "sql"}, args...)...)
		if code != 2 || out != "" {
			t.Errorf("%s: exit %d, want 2\nstdout:\n%s\nstderr:\n%s", name, code, out, errs)
		}
	}
	code, _, errs := velox(t, dir, "migrate", "sql")
	if !strings.HasPrefix(errs, "velox-gen: migrate sql needs -dialect postgres, mysql or sqlite\n") {
		t.Errorf("no dialect: exit %d\nstderr:\n%s", code, errs)
	}
	code, out, errs := velox(t, dir, "migrate", "sql", "-dialect", "mysql")
	if code != 1 || out != "" || !regexp.MustCompile(`^velox-gen: schema/user\.go:[0-9]+: `).MatchString(errs) {
		t.Errorf("mysql Where: exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}

	// An empty output would be an empty schema to Atlas, which drops every
	// table, so a package with no schemas is refused.
	empty := tempModule(t, map[string]string{"schema/doc.go": "// Package schema has no entities.\npackage schema\n"})
	code, out, errs = velox(t, empty, "migrate", "sql", "-dialect", "postgres")
	if code != 1 || out != "" || !strings.HasPrefix(errs, "velox-gen: no schemas in ") {
		t.Errorf("no schemas: exit %d, want 1\nstdout:\n%s\nstderr:\n%s", code, out, errs)
	}
}
