package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// keyedSchemas is a schema package of users, their posts (author_id, a
// NULL-able edge field, so SET NULL) and groups of users (a join table).
const keyedSchemas = `package veloxschema

import (
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type User struct{ schema.Schema }

func (User) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (User) Edges() []schema.Edge {
	return []schema.Edge{edge.To("posts", Post.Type), edge.To("groups", Group.Type)}
}

type Post struct{ schema.Schema }

func (Post) Fields() []schema.Field {
	return []schema.Field{field.String("title"), field.Int64("author_id").Nillable().Optional()}
}

func (Post) Edges() []schema.Edge {
	return []schema.Edge{edge.From("author", User.Type).Ref("posts").Unique().Field("author_id")}
}

type Group struct{ schema.Schema }

func (Group) Fields() []schema.Field { return []schema.Field{field.String("name")} }

func (Group) Edges() []schema.Edge { return []schema.Edge{edge.From("users", User.Type).Ref("groups")} }
`

// foreignKeySQL matches a foreign key in DDL, inline or as a constraint.
var foreignKeySQL = regexp.MustCompile(`(?i)FOREIGN KEY|REFERENCES`)

// readDir is the migration directory at path.
func readDir(t *testing.T, path string) *migrate.Dir {
	t.Helper()
	d, err := migrate.ReadDir(os.DirFS(path))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestMigrateDiffNoForeignKeys: diff -foreign-keys=false starts a directory
// whose files and schema.json hold no foreign key on any dialect, and whose
// velox.json records it; the indexes are the default directory's. The
// directory then diffs and checks clean, refuses a -foreign-keys that
// disagrees with it, and a database built from it has no drift.
func TestMigrateDiffNoForeignKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"veloxschema/schema.go": keyedSchemas})
	for _, d := range []velox.Dialect{velox.Postgres, velox.MySQL, velox.SQLite} {
		off, on := "nofk_"+d.String(), "fk_"+d.String()
		code, out, errs := runCLI(t, dir, "migrate", "diff", "-dialect", d.String(), "-foreign-keys=false", "-dir", off)
		if code != 0 || errs != "" {
			t.Fatalf("%s: diff -foreign-keys=false: %d %s %s", d, code, out, errs)
		}
		if code, out, errs := runCLI(t, dir, "migrate", "diff", "-dialect", d.String(), "-dir", on); code != 0 || errs != "" {
			t.Fatalf("%s: diff: %d %s %s", d, code, out, errs)
		}
		golden := filepath.Join("testdata", "nofk", d.String())
		sqlFile := readFile(t, filepath.Join(dir, off, "0001_create_groups.sql"))
		compareGolden(t, filepath.Join(golden, "0001_create_groups.sql"), generated(sqlFile))
		compareGolden(t, filepath.Join(golden, "schema.json"), readFile(t, filepath.Join(dir, off, "schema.json")))
		compareGolden(t, filepath.Join(golden, "velox.json"), readFile(t, filepath.Join(dir, off, "velox.json")))
		if foreignKeySQL.MatchString(sqlFile) {
			t.Errorf("%s: the file has a foreign key:\n%s", d, sqlFile)
		}
		if !foreignKeySQL.MatchString(readFile(t, filepath.Join(dir, on, "0001_create_groups.sql"))) {
			t.Errorf("%s: the default directory has no foreign key", d)
		}
		if s := readFile(t, filepath.Join(dir, off, "schema.json")); strings.Contains(s, `"foreign_keys"`) {
			t.Errorf("%s: schema.json has foreign keys", d)
		}
		if !strings.Contains(readFile(t, filepath.Join(dir, off, "velox.json")), `"foreign_keys": false`) {
			t.Errorf("%s: velox.json does not record foreign_keys", d)
		}
		if strings.Contains(readFile(t, filepath.Join(dir, on, "velox.json")), "foreign_keys") {
			t.Errorf("%s: the default velox.json names foreign_keys", d)
		}
		// The same tables, columns and indexes as the default directory's.
		offDir, onDir := readDir(t, filepath.Join(dir, off)), readDir(t, filepath.Join(dir, on))
		if !offDir.Config.NoForeignKeys || onDir.Config.NoForeignKeys {
			t.Errorf("%s: NoForeignKeys %v and %v", d, offDir.Config.NoForeignKeys, onDir.Config.NoForeignKeys)
		}
		if !onDir.Snapshot.WithoutForeignKeys().Equal(offDir.Snapshot) {
			t.Errorf("%s: the snapshot is not the default one without its foreign keys", d)
		}

		code, out, errs = runCLI(t, dir, "migrate", "diff", "-dir", off)
		want(t, d.String()+" again", code, out, errs, 0, "no changes\n", "")
		code, out, errs = runCLI(t, dir, "migrate", "diff", "-check", "-replay=false", "-dir", off)
		want(t, d.String()+" -check -replay=false", code, out, errs, 0, "", "")
	}

	// -check replays the files on a temporary SQLite file.
	code, out, errs := runCLI(t, dir, "migrate", "diff", "-check", "-dir", "nofk_sqlite")
	want(t, "-check", code, out, errs, 0, "", "")

	// -foreign-keys that agrees is accepted, one that disagrees refused.
	code, out, errs = runCLI(t, dir, "migrate", "diff", "-foreign-keys=false", "-dir", "nofk_sqlite")
	want(t, "agrees, false", code, out, errs, 0, "no changes\n", "")
	code, out, errs = runCLI(t, dir, "migrate", "diff", "-foreign-keys", "-dir", "fk_sqlite")
	want(t, "agrees, true", code, out, errs, 0, "no changes\n", "")
	code, out, errs = runCLI(t, dir, "migrate", "diff", "-foreign-keys=false", "-dir", "fk_sqlite")
	want(t, "disagrees, false", code, out, errs, 2, "", foreignKeysDisagree(false, "fk_sqlite/velox.json")+"\n")
	code, out, errs = runCLI(t, dir, "migrate", "diff", "-foreign-keys=true", "-check", "-dir", "nofk_sqlite")
	want(t, "disagrees, true", code, out, errs, 2, "", foreignKeysDisagree(true, "nofk_sqlite/velox.json")+"\n")
	if !strings.Contains(errs, `edit "foreign_keys" in velox.json, run velox-gen migrate hash`) {
		t.Errorf("the refusal does not say how to change it: %s", errs)
	}

	// A database built from the directory has no drift.
	dsn := filepath.Join(dir, "app.db")
	if code, _, errs := runCLI(t, dir, "migrate", "apply", "-dir", "nofk_sqlite", "-db", dsn); code != 0 {
		t.Fatalf("apply: %s", errs)
	}
	code, out, errs = runCLI(t, dir, "migrate", "drift", "-dir", "nofk_sqlite", "-db", dsn)
	want(t, "drift", code, out, errs, 0, "", "")

	// migrate sql -foreign-keys=false writes none either.
	for _, d := range []string{"postgres", "mysql", "sqlite"} {
		code, out, errs := runCLI(t, dir, "migrate", "sql", "-dialect", d, "-foreign-keys=false")
		if code != 0 || errs != "" || foreignKeySQL.MatchString(out) || !strings.Contains(out, "CREATE TABLE") {
			t.Errorf("sql %s -foreign-keys=false: %d %s\n%s", d, code, errs, out)
		}
	}
}

// TestMigrateForeignKeysChanged: a directory's "foreign_keys" changed by
// hand, then hashed, makes the next diff drop the constraints, and changed
// back, add them again; each result replays to its schema.json.
func TestMigrateForeignKeysChanged(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	dir := tempModule(t, map[string]string{"veloxschema/schema.go": keyedSchemas})
	mig := filepath.Join(dir, "migrations")
	if code, _, errs := runCLI(t, dir, "migrate", "diff", "-dialect", "sqlite"); code != 0 {
		t.Fatalf("diff: %s", errs)
	}
	dsn := filepath.Join(dir, "app.db")
	setKeys := func(off bool) {
		t.Helper()
		cfg := "{\n  \"version\": 1,\n  \"dialect\": \"sqlite\",\n  \"table\": \"velox_migrations\",\n  \"lock_timeout\": \"5s\"\n}\n"
		if off {
			cfg = strings.Replace(cfg, "\"5s\"\n", "\"5s\",\n  \"foreign_keys\": false\n", 1)
		}
		writeFile(t, filepath.Join(mig, "velox.json"), cfg)
		if code, _, errs := runCLI(t, dir, "migrate", "hash"); code != 0 {
			t.Fatalf("hash: %s", errs)
		}
		code, out, errs := runCLI(t, dir, "migrate", "diff", "-check", "-replay=false")
		if code != 3 || !strings.Contains(errs, "foreign-key") {
			t.Errorf("-check after the edit: %d %q %q", code, out, errs)
		}
	}

	setKeys(true)
	code, out, errs := runCLI(t, dir, "migrate", "diff", "-name", "drop_keys")
	if code != 0 || errs != "" || !strings.Contains(out, "0002_drop_keys.sql") {
		t.Fatalf("diff after foreign_keys false: %d %s %s", code, out, errs)
	}
	if s := readFile(t, filepath.Join(mig, "0002_drop_keys.sql")); strings.Contains(s, "REFERENCES") {
		t.Errorf("0002_drop_keys.sql keeps a foreign key:\n%s", s)
	}
	if s := readFile(t, filepath.Join(mig, "schema.json")); strings.Contains(s, `"foreign_keys"`) {
		t.Errorf("schema.json keeps foreign keys:\n%s", s)
	}
	code, out, errs = runCLI(t, dir, "migrate", "diff", "-check")
	want(t, "-check, off", code, out, errs, 0, "", "")
	if code, _, errs := runCLI(t, dir, "migrate", "apply", "-db", dsn); code != 0 {
		t.Fatalf("apply: %s", errs)
	}
	code, out, errs = runCLI(t, dir, "migrate", "drift", "-db", dsn)
	want(t, "drift, off", code, out, errs, 0, "", "")

	setKeys(false)
	code, out, errs = runCLI(t, dir, "migrate", "diff", "-name", "add_keys")
	if code != 0 || errs != "" || !strings.Contains(out, "0003_add_keys.sql") {
		t.Fatalf("diff after foreign_keys true: %d %s %s", code, out, errs)
	}
	if s := readFile(t, filepath.Join(mig, "0003_add_keys.sql")); !strings.Contains(s, "REFERENCES") {
		t.Errorf("0003_add_keys.sql adds no foreign key:\n%s", s)
	}
	code, out, errs = runCLI(t, dir, "migrate", "diff", "-check")
	want(t, "-check, on", code, out, errs, 0, "", "")
	if code, _, errs := runCLI(t, dir, "migrate", "apply", "-db", dsn); code != 0 {
		t.Fatalf("apply: %s", errs)
	}
	code, out, errs = runCLI(t, dir, "migrate", "drift", "-db", dsn)
	want(t, "drift, on", code, out, errs, 0, "", "")
}

// TestMigrateBaselineNoForeignKeys: baseline -foreign-keys=false adopts
// ent's SQLite tables, which have foreign keys, into a directory that has
// none, naming each it leaves out; the original database, baselined, shows
// them as drift.
func TestMigrateBaselineNoForeignKeys(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	files := map[string]string{}
	for _, f := range []string{"user.go", "post.go", "audit_entry.go"} {
		files["veloxschema/"+f] = readFile(t, filepath.Join("testdata", "ent", "schema", f))
	}
	dir := tempModule(t, files)
	original := filepath.Join(dir, "original.db")
	execFile(t, velox.SQLite, original, filepath.Join("testdata", "ent", "sqlite.sql"))
	mig := filepath.Join(dir, "migrations")

	code, out, errs := runCLI(t, dir, "migrate", "baseline", "-dialect", "sqlite", "-db", original, "-foreign-keys=false")
	if code != 0 || out != "migrations/0001_baseline.sql\nmigrations/schema.json\nmigrations/velox.json\nmigrations/velox.sum\n" {
		t.Fatalf("baseline: %d\n%s\n%s", code, out, errs)
	}
	for _, fk := range []string{"posts.posts_users_posts", "posts.posts_users_drafts_written_but__f5ecc73789cbf6208f1deb12fd406c67"} {
		if !strings.Contains(errs, "velox-gen: migrate baseline: warning: "+fk+": the database has this foreign key and -foreign-keys=false leaves it out") {
			t.Errorf("no warning of %s:\n%s", fk, errs)
		}
	}
	if s := readFile(t, filepath.Join(mig, "0001_baseline.sql")); foreignKeySQL.MatchString(s) {
		t.Errorf("0001_baseline.sql has a foreign key:\n%s", s)
	}
	if d := readDir(t, mig); !d.Config.NoForeignKeys {
		t.Error("velox.json does not record foreign_keys false")
	} else if d.Snapshot.WithoutForeignKeys().Equal(d.Snapshot) == false {
		t.Error("schema.json has foreign keys")
	}
	code, out, errs = runCLI(t, dir, "migrate", "diff", "-dry-run", "-allow", "alter-type=users.role")
	if code != 0 || foreignKeySQL.MatchString(out) {
		t.Errorf("the next diff: %d %s\n%s", code, errs, out)
	}

	if code, _, errs := runCLI(t, dir, "migrate", "apply", "-baseline", "1", "-db", original); code != 0 {
		t.Fatalf("apply -baseline: %s", errs)
	}
	code, out, _ = runCLI(t, dir, "migrate", "drift", "-db", original)
	if code != 3 || !strings.Contains(out, "change: drop-foreign-key posts.posts_users_posts\n") {
		t.Errorf("drift of the database with foreign keys: %d\n%s", code, out)
	}
}
