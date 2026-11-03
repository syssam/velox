package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// TestOperationsDocKilledMySQL: a run killed during a backfill UPDATE, which the
// server finishes: status leads with checking, and the recovery the doc writes
// applies the UPDATE once.
func TestOperationsDocKilledMySQL(t *testing.T) {
	dsn := testDSN(t, velox.MySQL)
	dir := t.TempDir()
	handDir(t, dir, velox.MySQL, map[string]string{"0001_a.sql": "CREATE TABLE a (id int PRIMARY KEY, n int NOT NULL);\nINSERT INTO a VALUES (1, 0);\n"})
	o := &opsDB{t: t, dir: dir, dsn: dsn}
	o.ok("apply")
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	o.db = db
	o.files(nil, map[string]string{"0002_backfill.sql": "UPDATE a SET n = n + 1;\nCREATE TABLE b (id int);\n"})
	if code, errs := killApply(t, velox.MySQL, db, dsn, dir); code != 1 {
		t.Fatalf("apply: %d\n%s", code, errs)
	}
	code, out, errs := o.run("status")
	if lines := strings.Split(errs, "\n"); code != 1 || !strings.Contains(out, "dirty 2 0002_backfill.sql: 0 statements done") || len(lines) < 3 ||
		!strings.Contains(lines[1], "may have run on the server") || !strings.Contains(lines[1], "SHOW FULL PROCESSLIST") || strings.Contains(lines[1], "-resume") ||
		!strings.Contains(lines[2], "resolve -version 2 -statements J, then migrate apply -resume") {
		t.Fatalf("status: %d\n%s\n%s", code, out, errs)
	}
	// The UPDATE took effect: n moved.
	if n := nOfA(t, db); n != 1 {
		t.Fatalf("n is %d; want the UPDATE finished by the server", n)
	}
	o.ok("resolve", "-version", "2", "-statements", "1")
	o.ok("apply", "-resume")
	if n := nOfA(t, db); n != 1 {
		t.Errorf("after the recovery n is %d; want 1", n)
	}
	if code, out, errs := o.run("status"); code != 0 || !strings.Contains(out, "applied 2 0002_backfill.sql") {
		t.Errorf("status: %d\n%s\n%s", code, out, errs)
	}
}

// killApply runs migrate apply of dir on dsn's database, through a proxy cut while
// the file's UPDATE of a waits for a row lock the test holds on db: the run dies with
// the UPDATE on the server, as if killed. The lock is let go, and once the server has
// ended the run's session, apply's exit code and standard error are returned.
func killApply(t *testing.T, d velox.Dialect, db *sql.DB, dsn, dir string) (int, string) {
	t.Helper()
	ctx := context.Background()
	blocker, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Close() }()
	begin, through := "BEGIN", pgThrough
	waiting := `SELECT pid FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE 'UPDATE a %'`
	alive := `SELECT count(*) FROM pg_stat_activity WHERE pid = $1`
	if d == velox.MySQL {
		begin, through = "START TRANSACTION", mysqlThrough
		waiting = `SELECT id FROM information_schema.processlist WHERE db = DATABASE() AND info LIKE 'UPDATE a %'`
		alive = `SELECT count(*) FROM information_schema.processlist WHERE id = ?`
	}
	for _, q := range []string{begin, "SELECT n FROM a WHERE id = 1 FOR UPDATE"} {
		if _, err := blocker.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	via, proxy := through(t, dsn)
	type result struct {
		code int
		errs string
	}
	done := make(chan result, 1)
	go func() {
		code, _, errs := runCLI(t, dir, "migrate", "apply", "-dir", ".", "-db", via)
		done <- result{code, errs}
	}()
	var id int64
	poll := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); !cond(); {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting: %s", what)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	poll("the run's UPDATE to wait", func() bool { return db.QueryRow(waiting).Scan(&id) == nil })
	proxy.cut()
	r := <-done
	if _, err := blocker.ExecContext(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	poll("the server to end the run's session", func() bool {
		var n int
		return db.QueryRow(alive, id).Scan(&n) == nil && n == 0
	})
	return r.code, r.errs
}

// nOfA is a's n.
func nOfA(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT n FROM a WHERE id = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestOperationsDocPostgres: the recoveries of a dirty PostgreSQL online file and
// of a hand hotfix, as the doc writes them, on a real server.
func TestOperationsDocPostgres(t *testing.T) {
	const a = "CREATE TABLE a (id integer, x integer);\nINSERT INTO a VALUES (1, 1), (2, 1);\n"
	const online = "-- velox:no-transaction\n\n-- velox:unit\nDROP INDEX CONCURRENTLY IF EXISTS a_x_key;\nCREATE UNIQUE INDEX CONCURRENTLY a_x_key ON a (x);\n"
	newPG := func(t *testing.T) *opsDB {
		t.Helper()
		dsn := testDSN(t, velox.Postgres)
		dir := t.TempDir()
		handDir(t, dir, velox.Postgres, map[string]string{"0001_a.sql": a, "0002_a_online.sql": online})
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return &opsDB{t: t, dir: dir, dsn: dsn, db: db}
	}
	valid := func(o *opsDB) string {
		o.t.Helper()
		var v string
		if err := o.db.QueryRow(`SELECT coalesce((SELECT indisvalid::text FROM pg_index WHERE indexrelid = to_regclass('a_x_key')), 'none')`).Scan(&v); err != nil {
			o.t.Fatal(err)
		}
		return v
	}

	t.Run("fix the cause and resume", func(t *testing.T) {
		o := newPG(t)
		o.refused([]string{"apply"}, "could not create unique index", "version 2 is dirty", "apply -resume")
		if v := valid(o); v != "false" {
			t.Errorf("the failed build left the index %s; want it invalid", v)
		}
		o.refused([]string{"apply"}, "version 2 is dirty")
		o.exec("UPDATE a SET x = 2 WHERE id = 2")
		o.ok("apply", "-resume")
		if v := valid(o); v != "true" {
			t.Errorf("after -resume the index is %s", v)
		}
	})

	t.Run("give up the file", func(t *testing.T) {
		o := newPG(t)
		o.refused([]string{"apply"}, "version 2 is dirty")
		o.exec("DROP INDEX CONCURRENTLY a_x_key")
		o.ok("resolve", "-version", "2", "-undone")
		if code, out, errs := o.run("status"); code != 3 || !strings.Contains(out, "pending 2") {
			t.Errorf("status: %d\n%s%s", code, out, errs)
		}
	})

	t.Run("a run killed during a one-statement unit", func(t *testing.T) {
		dsn := testDSN(t, velox.Postgres)
		dir := t.TempDir()
		handDir(t, dir, velox.Postgres, map[string]string{"0001_a.sql": "CREATE TABLE a (id integer PRIMARY KEY, n integer NOT NULL);\nINSERT INTO a VALUES (1, 0);\n"})
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Close() })
		o := &opsDB{t: t, dir: dir, dsn: dsn, db: db}
		o.ok("apply")
		o.files(nil, map[string]string{"0002_backfill.sql": "-- velox:no-transaction\nUPDATE a SET n = n + 1;\nCREATE TABLE b (id integer);\n"})
		if code, errs := killApply(t, velox.Postgres, db, dsn, dir); code != 1 {
			t.Fatalf("apply: %d\n%s", code, errs)
		}
		if n := nOfA(t, db); n != 0 {
			t.Fatalf("n is %d; want the UPDATE rolled back with its count", n)
		}
		o.refused([]string{"status"}, "version 2 is dirty\n  fix the cause and run migrate apply -resume")
		o.ok("apply", "-resume")
		if n := nOfA(t, db); n != 1 {
			t.Errorf("after -resume n is %d; want 1", n)
		}
	})

	t.Run("a hotfix the next file repeats", func(t *testing.T) {
		o := newPG(t)
		o.ok("apply", "-to", "1")
		o.exec("UPDATE a SET x = 2 WHERE id = 2")
		o.ok("apply")
		o.exec("ALTER TABLE a ADD COLUMN note text")
		o.files(nil, map[string]string{"0003_note.sql": "ALTER TABLE a ADD COLUMN note text;\n", "0004_b.sql": "CREATE TABLE b (id integer);\n"})
		o.refused([]string{"apply"}, `column "note" of relation "a" already exists`, "rolled back")
		o.ok("resolve", "-version", "3", "-applied")
		o.ok("apply")
	})
}
