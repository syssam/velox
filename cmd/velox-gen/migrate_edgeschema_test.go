package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// joinTableSchema is a schema package of users in groups: a plain
// many-to-many relation, or with through, one through the edge schema
// Membership, whose role has a default and whose joined_at has a function
// default, which no column DEFAULT fills; with table, Membership's table is
// table.
func joinTableSchema(through bool, table ...string) string {
	edges, ms := `edge.To("groups", Group.Type)`, ""
	inverse := `edge.From("users", User.Type).Ref("groups")`
	if through {
		edges += `.Through("memberships", Membership.Type)`
		inverse += `.Through("memberships", Membership.Type)`
		ms = `
type Membership struct{ schema.Schema }

func (Membership) Fields() []schema.Field {
	return []schema.Field{field.String("role").Default("member"), field.Time("joined_at").Default(time.Now), field.Int64("user_id"), field.Int64("group_id")}
}

func (Membership) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("user", User.Type).Unique().Required().Field("user_id").OnDelete(edge.Cascade),
		edge.To("group", Group.Type).Unique().Required().Field("group_id").OnDelete(edge.Cascade),
	}
}
`
		if len(table) > 0 {
			ms += "\nfunc (Membership) Annotations() []schema.Annotation { return []schema.Annotation{sqlschema.Table(\"" + table[0] + "\")} }\n"
		}
	}
	imports := ""
	if through {
		imports = "\t\"time\"\n\n"
		if len(table) > 0 {
			imports += "\t\"github.com/syssam/velox/dialect/sqlschema\"\n"
		}
	}
	return `package veloxschema

import (
` + imports + `	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (User) Edges() []schema.Edge { return []schema.Edge{` + edges + `} }

type Group struct{ schema.Schema }

func (Group) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (Group) Edges() []schema.Edge { return []schema.Edge{` + inverse + `} }
` + ms
}

// TestConvertJoinTable runs migrations-operations.md's recipe that turns a
// plain many-to-many join table into an edge schema's table, on each
// dialect: diff refuses to keep the join table's name, as its primary key
// changes, and refuses to drop it, diff with -allow plans the
// new table and the drop, the doc's INSERT ... SELECT copies the links in
// between, and the result holds every link, with the role's default, and
// no drift.
func TestConvertJoinTable(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			dir := tempModule(t, map[string]string{"veloxschema/schema.go": joinTableSchema(false)})
			driver, dsn := "sqlite", filepath.Join(dir, "app.db")
			switch d {
			case velox.Postgres:
				driver, dsn = "pgx", testDSN(t, d)
			case velox.MySQL:
				driver, dsn = "mysql", testDSN(t, d)
			}
			cli := func(wantCode int, args ...string) string {
				t.Helper()
				code, out, errs := runCLI(t, dir, args...)
				if code != wantCode {
					t.Fatalf("%v: exit %d, want %d\n%s%s", args, code, wantCode, out, errs)
				}
				return out + errs
			}
			cli(0, "migrate", "diff", "-dialect", d.String(), "-name", "init")
			cli(0, "migrate", "apply", "-db", dsn)
			db, err := sql.Open(driver, dsn)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = db.Close() }()
			for _, q := range []string{
				"INSERT INTO users (name) VALUES ('ann'), ('bob')",
				"INSERT INTO `groups` (name) VALUES ('go'), ('sql')",
				"INSERT INTO user_groups (user_id, group_id) SELECT u.id, g.id FROM users u, `groups` g WHERE u.name = 'ann' OR g.name = 'sql'",
			} {
				if d != velox.MySQL {
					q = strings.ReplaceAll(q, "`", `"`)
				}
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}

			// Keeping the join table's name changes its primary key, which
			// diff leaves to a hand-written file.
			writeFile(t, filepath.Join(dir, "veloxschema", "schema.go"), joinTableSchema(true, "user_groups"))
			if out := cli(1, "migrate", "diff"); !strings.Contains(out, "the primary key changes from (user_id, group_id) to (id); write this migration by hand") {
				t.Errorf("diff of the same table:\n%s", out)
			}
			writeFile(t, filepath.Join(dir, "veloxschema", "schema.go"), joinTableSchema(true))
			if out := cli(1, "migrate", "diff"); !strings.Contains(out, "drop-table") {
				t.Errorf("diff without -allow:\n%s", out)
			}
			cli(0, "migrate", "diff", "-allow", "drop-table=user_groups", "-name", "memberships")
			path := filepath.Join(dir, "migrations", "0002_memberships.sql")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			file := string(b)
			at := strings.Index(file, "-- review: drop-table")
			if at < 0 || !strings.Contains(file[:at], `CREATE TABLE`) {
				t.Fatalf("the plan does not create the table before the drop:\n%s", file)
			}
			writeFile(t, path, file[:at]+copyStatement(t)+"\n"+file[at:])
			cli(0, "migrate", "hash")
			cli(0, "migrate", "apply", "-db", dsn)
			var n, members int
			if err := db.QueryRow("SELECT count(*), sum(CASE WHEN role = 'member' THEN 1 ELSE 0 END) FROM memberships").Scan(&n, &members); err != nil {
				t.Fatal(err)
			}
			if n != 3 || members != 3 {
				t.Errorf("%d links, %d with the default role; want 3 and 3", n, members)
			}
			cli(0, "migrate", "drift", "-db", dsn)
			cli(0, "migrate", "diff", "-check")
		})
	}
}

// copyStatement is the statement migrations-operations.md says to add to
// the planned file: the one SQL block of its edge schema section.
func copyStatement(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "docs", "migrations-operations.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(b), "\n## A join table becomes an edge schema\n")
	if !ok {
		t.Fatal("no section on an edge schema")
	}
	sec, _, _ = strings.Cut(sec, "\n## ")
	_, block, ok := strings.Cut(sec, "```sql\n")
	block, _, ok2 := strings.Cut(block, "```")
	if !ok || !ok2 {
		t.Fatal("the section has no sql block")
	}
	return block
}
