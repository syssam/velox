package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// holdingLock has another session hold a metadata lock on t until the returned func.
func holdingLock(t *testing.T, ctx context.Context, db *sql.DB) func() {
	t.Helper()
	other, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"BEGIN", "SELECT * FROM t"} {
		if _, err := other.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	return func() {
		_, _ = other.ExecContext(ctx, "ROLLBACK")
		_ = other.Close()
	}
}

// TestMySQLLockWaitLeavesNoDirtyVersion: a lock wait timeout is the commonest failure of
// a deploy, and changed nothing, yet it left a dirty version a human had to clear. A
// file's first statement takes its row away; a later file's at the head of a unit
// leaves a row the next Up goes on from, unasked.
func TestMySQLLockWaitLeavesNoDirtyVersion(t *testing.T) {
	t.Parallel()
	d := velox.MySQL
	ctx := context.Background()
	t.Run("the first statement", func(t *testing.T) {
		db, _ := freshDB(t, d)
		mustExec(t, db, "CREATE TABLE t (x int)")
		fsys := dirOf(t, d, files("0001_t.sql", "-- velox:lock-timeout 1s\nALTER TABLE t ADD COLUMN y int;\n"))
		release := holdingLock(t, ctx, db)
		_, err := migrate.Up(ctx, db, fsys)
		release()
		if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "retry" || mysqlNumber(err) != 1205 || !strings.Contains(err.Error(), "changed nothing") {
			t.Fatalf("Up: %v", err)
		}
		if rows := historyOf(t, db); len(rows) != 0 {
			t.Errorf("the history: %+v", rows)
		}
		mustUp(t, db, fsys)
	})
	t.Run("a later statement", func(t *testing.T) {
		db, _ := freshDB(t, d)
		mustExec(t, db, "CREATE TABLE t (x int)")
		fsys := dirOf(t, d, files("0001_t.sql", "-- velox:lock-timeout 1s\nCREATE TABLE u (x int);\nALTER TABLE t ADD COLUMN y int;\nCREATE TABLE v (x int);\n"))
		release := holdingLock(t, ctx, db)
		_, err := migrate.Up(ctx, db, fsys)
		release()
		de := dirtyOf(t, err)
		if de.Statement != 2 || !strings.Contains(err.Error(), "retries it") {
			t.Fatalf("Up: %v", err)
		}
		rows := historyOf(t, db)
		if len(rows) != 1 || !rows[0].Dirty || rows[0].Statements != 1 {
			t.Fatalf("the history: %+v", rows)
		}
		mustUp(t, db, fsys) // no WithResume; CREATE TABLE u would fail if run again
		if rows := historyOf(t, db); rows[0].Dirty {
			t.Errorf("the history: %+v", rows)
		}
	})
	t.Run("a refused first statement", func(t *testing.T) {
		db, _ := freshDB(t, d)
		bad := dirOf(t, d, files("0001_t.sql", "ALTER TABLE missing ADD COLUMN y int;\nCREATE TABLE u (x int);\n"))
		_, err := migrate.Up(ctx, db, bad)
		if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "retry" {
			t.Fatalf("Up: %v", err)
		}
		if rows := historyOf(t, db); len(rows) != 0 {
			t.Errorf("the history: %+v", rows)
		}
	})
	t.Run("a refused first DML stays dirty", func(t *testing.T) {
		db, _ := freshDB(t, d)
		bad := dirOf(t, d, files("0001_t.sql", "INSERT INTO missing VALUES (1);\n"))
		_, err := migrate.Up(ctx, db, bad)
		_ = dirtyOf(t, err)
	})
}

// TestMySQLStrandedResolve: a run killed with a statement on the server leaves the
// history counting fewer than ran, on a database no statement of which can be run
// twice. The refusal lists the file; -statements K says which ran, and -resume goes on
// from K+1. The count may fall inside a unit.
func TestMySQLStrandedResolve(t *testing.T) {
	t.Parallel()
	d := velox.MySQL
	ctx := context.Background()
	db, _ := freshDB(t, d)
	fsys := dirOf(t, d, files("0001_a.sql", "CREATE TABLE a (x int);\n", "0002_b.sql", "CREATE TABLE b (x int);\nCREATE TABLE c (x int);\nCREATE TABLE d (x int);\n"))
	mustUp(t, db, fsys, migrate.WithTarget(1))
	mustExec(t, db, "CREATE TABLE b (x int)")
	mustExec(t, db, "CREATE TABLE c (x int)")
	mustExec(t, db, "INSERT INTO velox_migrations (version, name, checksum, applied_at, duration_ms, dirty, statements, error) SELECT 2, 'b', '"+mustDir(t, fsys).Files[1].Sum+"', applied_at, 0, true, 0, 'killed' FROM velox_migrations WHERE version = 1")

	_, err := migrate.Up(ctx, db, fsys)
	msg := err.Error()
	for _, want := range []string{"counts 0 of 3", "1. CREATE TABLE b (x int)", "3. CREATE TABLE d (x int)", "resolve -version 2 -statements K", "SHOW FULL PROCESSLIST"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not say %q: %s", want, msg)
		}
	}
	if err := migrate.Resolve(ctx, db, fsys, 2, migrate.ResolveStatements, migrate.WithStatements(4)); err == nil || !strings.Contains(err.Error(), "the file has 3") {
		t.Errorf("4 of 3: %v", err)
	}
	if err := migrate.Resolve(ctx, db, fsys, 2, migrate.ResolveStatements); err == nil || !strings.Contains(err.Error(), "WithStatements") {
		t.Errorf("no count: %v", err)
	}
	if err := migrate.Resolve(ctx, db, fsys, 2, migrate.ResolveStatements, migrate.WithStatements(2)); err != nil {
		t.Fatal(err)
	}
	rows := historyOf(t, db)
	if !rows[1].Dirty || rows[1].Statements != 2 {
		t.Fatalf("the history: %+v", rows)
	}
	mustUp(t, db, fsys, migrate.WithResume())
	rows = historyOf(t, db)
	if rows[1].Dirty || rows[1].Statements != 3 {
		t.Errorf("the history: %+v", rows)
	}
	if got := oneValue(t, db, "SELECT count(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'd'"); got != "1" {
		t.Errorf("the last statement did not run")
	}
}
