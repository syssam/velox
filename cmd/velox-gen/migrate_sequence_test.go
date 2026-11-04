package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// allowHint is what a refused diff says to pass.
var allowHint = regexp.MustCompile(`allow with -allow (\S+)`)

// TestMigrateOrdinarySequence: the changes an ordinary schema goes through, each by
// migrate diff and migrate apply on a database with a row in it. The guard reads what
// each file writes against what the database holds, and nothing here was edited by
// hand: it must refuse none, and the row must come through.
func TestMigrateOrdinarySequence(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	t.Parallel() // each dialect's subtest takes a slot of its own (tempModule)
	const post = `package schema

import "github.com/syssam/velox/schema"

type Post struct{ schema.Schema }
`
	name := func(n int, col string) string {
		return `field.String("` + col + `").MaxLen(` + map[int]string{50: "50", 200: "200"}[n] + `)`
	}
	const (
		ageNull = `field.Int("age").Nillable()`
		age     = `field.Int("age")`
		enum    = `field.Enum("status").Values("a", "b").Default("a")`
		more    = `field.Enum("status").Values("a", "b", "c").Default("a")`
		swapped = `field.Enum("status").Values("c", "b", "a").Default("a")`
	)
	steps := []struct {
		what   string
		fields []string
		args   []string
		noPost bool
	}{
		{what: "add", fields: []string{name(50, "name"), ageNull, enum}},
		{what: "widen", fields: []string{name(200, "name"), ageNull, enum}},
		{what: "enum", fields: []string{name(200, "name"), ageNull, more}},
		{what: "not-null", fields: []string{name(200, "name"), age, more}},
		{what: "rename", fields: []string{name(200, "full_name"), age, more}, args: []string{"-rename-column", "users.name=full_name"}},
		{what: "drop", fields: []string{name(200, "full_name"), more}},
		{what: "reorder", fields: []string{name(200, "full_name"), swapped}},
		{what: "drop table", fields: []string{name(200, "full_name"), swapped}, noPost: true},
	}
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			files := map[string]string{"schema/user.go": userSchema(name(50, "name"), ageNull), "schema/post.go": post}
			dir := tempModule(t, files)
			dsn, driver := filepath.Join(dir, "app.db"), "sqlite"
			switch d {
			case velox.SQLite:
				writeFile(t, dsn, "")
			case velox.Postgres:
				dsn, driver = testDSN(t, d), "pgx"
			case velox.MySQL:
				dsn, driver = testDSN(t, d), "mysql"
			}
			apply := func(what string) {
				t.Helper()
				if code, out, errs := runCLI(t, dir, "migrate", "apply", "-db", dsn); code != 0 {
					t.Fatalf("%s: apply: exit %d\n%s%s\n%s", what, code, out, errs, migrationText(t, dir))
				}
			}
			if code, out, errs := runCLI(t, dir, "migrate", "diff", "-dialect", d.String()); code != 0 {
				t.Fatalf("first diff: exit %d\n%s%s", code, out, errs)
			}
			apply("first")
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			if _, err := db.Exec(`INSERT INTO users (name, age) VALUES ('x', 1)`); err != nil {
				t.Fatal(err)
			}
			for _, s := range steps {
				writeFile(t, filepath.Join(dir, "schema", "user.go"), userSchema(s.fields...))
				if s.noPost {
					if err := os.Remove(filepath.Join(dir, "schema", "post.go")); err != nil {
						t.Fatal(err)
					}
				}
				args := append([]string{"migrate", "diff", "-name", strings.NewReplacer(" ", "_", "-", "_").Replace(s.what)}, s.args...)
				code, out, errs := runCLI(t, dir, args...)
				for range 4 { // what the plan calls destructive is accepted, as its user would
					m := allowHint.FindAllStringSubmatch(errs, -1)
					if code == 0 || len(m) == 0 {
						break
					}
					for _, a := range m {
						args = append(args, "-allow", a[1])
					}
					code, out, errs = runCLI(t, dir, args...)
				}
				if code != 0 {
					t.Fatalf("%s: diff: exit %d\n%s%s", s.what, code, out, errs)
				}
				apply(s.what)
			}
			var n int
			if err := db.QueryRow(`SELECT count(*) FROM users WHERE full_name = 'x'`).Scan(&n); err != nil || n != 1 {
				t.Errorf("the row after the sequence: %d, %v", n, err)
			}
		})
	}
}

// migrationText is the plan files of dir, for a failure's message.
func migrationText(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	for name, text := range dirFiles(t, filepath.Join(dir, "migrations")) {
		if strings.HasSuffix(name, ".sql") {
			b.WriteString("== " + name + "\n" + text)
		}
	}
	return b.String()
}
