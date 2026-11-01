package main

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox"
)

// opsDB is a migration directory and a SQLite database, for the recoveries of
// docs/migrations-operations.md.
type opsDB struct {
	t        *testing.T
	dir, dsn string
	db       *sql.DB
}

func newOps(t *testing.T, files map[string]string) *opsDB {
	t.Helper()
	dir := t.TempDir()
	handDir(t, dir, velox.SQLite, files)
	dsn := filepath.Join(t.TempDir(), "app.db")
	o := &opsDB{t: t, dir: dir, dsn: dsn}
	o.ok("apply")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	o.db = db
	return o
}

func (o *opsDB) run(args ...string) (int, string, string) {
	o.t.Helper()
	return runCLI(o.t, o.dir, append([]string{"migrate", args[0], "-dir", ".", "-db", o.dsn}, args[1:]...)...)
}

// ok runs a command that must succeed, and is its standard error.
func (o *opsDB) ok(args ...string) string {
	o.t.Helper()
	code, out, errs := o.run(args...)
	if code != 0 {
		o.t.Fatalf("%v: %d\n%s\n%s", args, code, out, errs)
	}
	return errs
}

// refused runs a command that must fail, saying each of says.
func (o *opsDB) refused(args []string, says ...string) {
	o.t.Helper()
	code, out, errs := o.run(args...)
	if code != 1 {
		o.t.Fatalf("%v: %d\n%s\n%s", args, code, out, errs)
	}
	for _, s := range says {
		if !strings.Contains(errs, s) {
			o.t.Errorf("%v does not say %q:\n%s", args, s, errs)
		}
	}
}

// files leaves the directory with the migration files given, hashed.
func (o *opsDB) files(remove []string, add map[string]string) {
	o.t.Helper()
	for _, p := range remove {
		if err := os.Remove(filepath.Join(o.dir, p)); err != nil {
			o.t.Fatal(err)
		}
	}
	for p, src := range add {
		writeFile(o.t, filepath.Join(o.dir, p), src)
	}
	if code, out, errs := runCLI(o.t, o.dir, "migrate", "hash", "-dir", "."); code != 0 {
		o.t.Fatalf("hash: %d\n%s\n%s", code, out, errs)
	}
}

func (o *opsDB) exec(q string) {
	o.t.Helper()
	if _, err := o.db.Exec(q); err != nil {
		o.t.Fatal(err)
	}
}

func (o *opsDB) hasTable(name string) bool {
	o.t.Helper()
	var n int
	if err := o.db.QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n); err != nil {
		o.t.Fatal(err)
	}
	return n == 1
}

// TestOperationsDoc runs each recovery of docs/migrations-operations.md as it is written.
func TestOperationsDoc(t *testing.T) {
	const a = "CREATE TABLE a (id integer);\n"
	const mine = "CREATE TABLE mine (id integer);\n"
	const main = "CREATE TABLE main_t (id integer);\n"

	t.Run("two branches took one version", func(t *testing.T) {
		o := newOps(t, map[string]string{"0001_a.sql": a, "0002_mine.sql": mine})
		o.files([]string{"0002_mine.sql"}, map[string]string{"0002_main.sql": main, "0003_mine.sql": mine})
		o.refused([]string{"apply"}, "version 2 was applied as 0002_mine.sql", "the directory's file for it is 0002_main.sql", "-adopt", "-forget")
		o.exec("DROP TABLE mine")
		if errs := o.ok("resolve", "-version", "2", "-forget"); !strings.Contains(errs, "nothing was undone") || !strings.Contains(errs, "0002_main.sql is pending") {
			t.Errorf("the note: %s", errs)
		}
		o.ok("apply")
		if !o.hasTable("main_t") || !o.hasTable("mine") {
			t.Error("the two files did not run")
		}
	})

	t.Run("an applied file was edited", func(t *testing.T) {
		o := newOps(t, map[string]string{"0001_a.sql": a})
		const edited = "CREATE TABLE a (id integer, x integer);\n"
		o.files(nil, map[string]string{"0001_a.sql": edited})
		o.refused([]string{"apply"}, "0001_a.sql was applied with checksum", "resolve -version 1 -adopt")
		o.exec("ALTER TABLE a ADD COLUMN x integer")
		if errs := o.ok("resolve", "-version", "1", "-adopt"); !strings.Contains(errs, "none of the file ran") {
			t.Errorf("the note: %s", errs)
		}
		o.ok("apply")
	})

	t.Run("an applied file is gone", func(t *testing.T) {
		o := newOps(t, map[string]string{"0001_a.sql": a, "0002_mine.sql": mine})
		o.files([]string{"0002_mine.sql"}, map[string]string{"0003_c.sql": "CREATE TABLE c (id integer);\n"})
		o.refused([]string{"apply"}, "version 2 is applied but the directory has no file for it", "resolve -version 2 -forget")
		o.ok("resolve", "-version", "2", "-forget")
		o.ok("apply")
		if !o.hasTable("mine") || !o.hasTable("c") {
			t.Error("the database does not keep what it ran, or the next file did not run")
		}
	})

	t.Run("forget refuses what apply would run again", func(t *testing.T) {
		o := newOps(t, map[string]string{"0001_a.sql": a})
		o.refused([]string{"resolve", "-version", "1", "-forget"}, "would run it again")
		o.refused([]string{"resolve", "-version", "9", "-forget"}, "not in the history")
		o.refused([]string{"resolve", "-version", "9", "-adopt"}, "not in the history")
	})

	t.Run("a pending file the database already holds", func(t *testing.T) {
		o := newOps(t, map[string]string{"0001_a.sql": a})
		o.files(nil, map[string]string{"0002_mine.sql": mine, "0003_c.sql": "CREATE TABLE c (id integer);\n"})
		o.refused([]string{"resolve", "-version", "3", "-applied"}, "0002_mine.sql is pending below version 3")
		o.refused([]string{"resolve", "-version", "9", "-applied"}, "no file")
		o.exec(strings.TrimSpace(mine))
		o.ok("resolve", "-version", "2", "-applied")
		o.refused([]string{"resolve", "-version", "2", "-applied"}, "version 2 is not dirty")
		o.ok("apply")
		if !o.hasTable("c") {
			t.Error("the next file did not run")
		}
		// A file that ran is still one -forget refuses.
		o.refused([]string{"resolve", "-version", "3", "-forget"}, "would run it again")
	})

	t.Run("forgetting a marked file", func(t *testing.T) {
		o := newOps(t, map[string]string{"0001_a.sql": a})
		o.files(nil, map[string]string{"0002_mine.sql": mine})
		o.ok("resolve", "-version", "2", "-applied")
		o.ok("resolve", "-version", "2", "-forget")
		o.ok("apply")
		if !o.hasTable("mine") {
			t.Error("the file did not run")
		}
	})

	t.Run("squashing", func(t *testing.T) {
		o := newOps(t, map[string]string{"0001_a.sql": a, "0002_mine.sql": mine})
		o.files([]string{"0001_a.sql", "0002_mine.sql"}, map[string]string{"0003_baseline.sql": a + mine})
		o.refused([]string{"apply"}, "versions below this baseline", "resolve -version 3 -applied")
		if code, out, _ := o.run("status"); code != 3 || !strings.Contains(out, "pending 3 0003_baseline.sql") {
			t.Errorf("status: %d\n%s", code, out)
		}
		o.ok("resolve", "-version", "3", "-applied")
		o.ok("apply")
		o.files(nil, map[string]string{"0004_c.sql": "CREATE TABLE c (id integer);\n"})
		o.ok("apply")
		if !o.hasTable("c") {
			t.Error("the next file did not run")
		}

		// A new database runs the baseline.
		fresh := &opsDB{t: t, dir: o.dir, dsn: filepath.Join(t.TempDir(), "new.db")}
		fresh.ok("apply")
	})

	t.Run("a version is not squashed unless the first file is a baseline", func(t *testing.T) {
		o := newOps(t, map[string]string{"0001_a.sql": a, "0002_mine.sql": mine})
		o.files([]string{"0001_a.sql"}, nil)
		o.refused([]string{"apply"}, "version 1 is applied but the directory has no file for it")
	})

	t.Run("diff -db sees a version it would reuse", func(t *testing.T) {
		dir := t.TempDir()
		mig := filepath.Join(dir, "migrations")
		writeMigrations(t, mig, velox.SQLite, usersSchema)
		writeFile(t, filepath.Join(dir, "go.mod"), "module x\n")
		dsn := filepath.Join(dir, "app.db")
		env := noVeloxDB()
		m := func(args ...string) (int, string, string) {
			return veloxEnv(t, dir, env, append([]string{"migrate"}, args...)...)
		}
		if code, out, errs := m("apply", "-db", dsn); code != 0 {
			t.Fatalf("apply: %d\n%s\n%s", code, out, errs)
		}
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if _, err := db.Exec("INSERT INTO velox_migrations SELECT 2, 'other', checksum, applied_at, duration_ms, dirty, statements, error FROM velox_migrations WHERE version = 1"); err != nil {
			t.Fatal(err)
		}
		code, out, errs := m("diff", "-db", dsn, "./nothing")
		if code != 1 || out != "" || !strings.Contains(errs, "version 2 (other) is in the database's history") || !strings.Contains(errs, "resolve -version 2 -forget") {
			t.Errorf("diff: %d\n%s\n%s", code, out, errs)
		}
		if code, out, errs := m("resolve", "-db", dsn, "-version", "2", "-forget"); code != 0 {
			t.Fatalf("forget: %d\n%s\n%s", code, out, errs)
		}
		if _, _, errs := m("diff", "-db", dsn, "./nothing"); strings.Contains(errs, "in the database's history") {
			t.Errorf("diff after -forget: %s", errs)
		}
	})
}

// TestOperationsDocMySQL: -statements, as the doc writes it, on a real server.
func TestOperationsDocMySQL(t *testing.T) {
	dsn := testDSN(t, velox.MySQL)
	dir := t.TempDir()
	handDir(t, dir, velox.MySQL, map[string]string{"0001_a.sql": "CREATE TABLE a (id int);\nCREATE TABLE a (id int);\nCREATE TABLE b (id int);\n"})
	m := func(args ...string) (int, string, string) {
		return runCLI(t, dir, append([]string{"migrate", args[0], "-dir", ".", "-db", dsn}, args[1:]...)...)
	}
	if code, out, errs := m("apply"); code != 1 || !strings.Contains(errs, "resolve -version 1 -statements K") {
		t.Fatalf("apply: %d\n%s\n%s", code, out, errs)
	}
	code, out, errs := m("resolve", "-version", "1", "-statements", "2")
	want(t, "resolve", code, out, errs, 0, "resolved version 1 as 2 statements run, to resume\n", "")
	if code, out, errs := m("apply", "-resume"); code != 0 {
		t.Fatalf("apply -resume: %d\n%s\n%s", code, out, errs)
	}
	if code, out, errs := m("status"); code != 0 || !strings.HasPrefix(out, "applied 1 0001_a.sql") {
		t.Errorf("status: %d\n%s\n%s", code, out, errs)
	}
}
