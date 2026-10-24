package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// columnsOf is the history table's columns, "<name> <type>", by position.
func columnsOf(t *testing.T, db *sql.DB, q string) []string {
	t.Helper()
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var name, typ, nullable string
		if err := rows.Scan(&name, &typ, &nullable); err != nil {
			t.Fatal(err)
		}
		if nullable != "NO" {
			typ += " NULL"
		}
		out = append(out, name+" "+typ)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// TestUpPostgres: a plan and a hand file apply, each with the Config's lock
// timeout, and the history table has its PostgreSQL types.
func TestUpPostgres(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	_, to := models(t, caseNamed("create-tables"), velox.Postgres)
	fsys := dirOf(t, velox.Postgres, step{to: to}, files("0002_check.sql",
		`DO $$ BEGIN IF current_setting('lock_timeout') <> '5s' THEN RAISE EXCEPTION 'lock_timeout is %', current_setting('lock_timeout'); END IF; END $$;`))
	start := time.Now().UTC().Truncate(time.Microsecond)
	r := mustUp(t, db, fsys)
	if !slices.Equal(versions(r.Applied), []uint64{1, 2}) {
		t.Fatalf("applied %v", versions(r.Applied))
	}
	s := mustStatus(t, db, fsys)
	if !slices.Equal(versions(s.Applied), []uint64{1, 2}) || len(s.Pending) != 0 {
		t.Errorf("state %+v", s)
	}
	for i, a := range s.Applied {
		if a.AppliedAt.Before(start) || a.AppliedAt.After(time.Now()) || !a.AppliedAt.Equal(r.Applied[i].AppliedAt) || a.Statements != r.Applied[i].Statements {
			t.Errorf("read back %+v; applied %+v", a, r.Applied[i])
		}
	}
	got := columnsOf(t, db, `SELECT column_name, data_type, is_nullable FROM information_schema.columns WHERE table_name = 'velox_migrations' ORDER BY ordinal_position`)
	want := []string{
		"version bigint",
		"name text",
		"checksum text",
		"applied_at timestamp with time zone",
		"duration_ms bigint",
		"dirty boolean",
		"statements integer",
		"error text",
	}
	if !slices.Equal(got, want) {
		t.Errorf("columns\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('alphas', 'beta', 'betas', 'alpha')`).Scan(&n); err != nil || n == 0 {
		t.Errorf("the plan's tables: %d, %v", n, err)
	}
}

func TestUpPostgresConcurrent(t *testing.T) {
	db, dsn := freshDB(t, velox.Postgres)
	fsys := dirOf(t, velox.Postgres, files(concurrentFiles...))
	upTwice(t, fsys, db, openDB(t, "pgx", dsn))
	var rows, n int
	if err := db.QueryRow(`SELECT count(*) FROM velox_migrations`).Scan(&rows); err != nil || rows != 3 {
		t.Errorf("history rows %d, %v; want 3", rows, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM log`).Scan(&n); err != nil || n != 2 {
		t.Errorf("log rows %d, %v; want 2", n, err)
	}
}

func TestUpPostgresLockWait(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.Postgres)
	other, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err := other.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrate.LockKey("velox_migrations")); err != nil {
		t.Fatal(err)
	}
	fsys := dirOf(t, velox.Postgres)
	start := time.Now()
	if _, err := migrate.Up(ctx, db, fsys, migrate.WithLockWait(300*time.Millisecond)); !errors.Is(err, migrate.ErrLockWait) {
		t.Fatalf("%#v; want ErrLockWait", err)
	}
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Errorf("Up waited %s; want 300ms", d)
	}
	if _, err := migrate.Up(ctx, db, fsys, migrate.WithLockWait(0)); !errors.Is(err, migrate.ErrLockWait) {
		t.Fatalf("no wait: %#v; want ErrLockWait", err)
	}
	if _, err := other.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, migrate.LockKey("velox_migrations")); err != nil {
		t.Fatal(err)
	}
	mustUp(t, db, fsys, migrate.WithLockWait(300*time.Millisecond))
	mustUp(t, db, fsys, migrate.WithLockWait(0))
	mustUp(t, db, fsys, migrate.WithLockWait(math.MaxInt64)) // past lock_timeout's range, clamped
}

func TestUpPostgresStandardConformingStrings(t *testing.T) {
	db, dsn := freshDB(t, velox.Postgres)
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	pgExec(t, db, `ALTER DATABASE "`+name+`" SET standard_conforming_strings = off`)
	off := openDB(t, "pgx", dsn) // a new session takes the database's setting
	var e *migrate.Error
	if _, err := migrate.Up(context.Background(), off, dirOf(t, velox.Postgres)); !errors.As(err, &e) || e.Code != "session" || !strings.Contains(e.Reason, "standard_conforming_strings") {
		t.Fatalf("%#v; want Code session", err)
	}
}

// TestMySQLLock: another session holding the lock makes Up wait, then give
// ErrLockWait; released, Up creates the history table with MySQL's types.
func TestMySQLLock(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.MySQL)
	other, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	var name string
	if err := other.QueryRowContext(ctx, `SELECT DATABASE()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	lock := migrate.LockName(name, "velox_migrations")
	var got int
	if err := other.QueryRowContext(ctx, `SELECT GET_LOCK(?, 0)`, lock).Scan(&got); err != nil || got != 1 {
		t.Fatalf("GET_LOCK: %d, %v", got, err)
	}
	fsys := dirOf(t, velox.MySQL)
	start := time.Now()
	if _, err := migrate.Up(ctx, db, fsys, migrate.WithLockWait(time.Second)); !errors.Is(err, migrate.ErrLockWait) {
		t.Fatalf("%#v; want ErrLockWait", err)
	}
	if d := time.Since(start); d < time.Second {
		t.Errorf("Up waited %s; want 1s", d)
	}
	if _, err := migrate.Up(ctx, db, fsys, migrate.WithLockWait(0)); !errors.Is(err, migrate.ErrLockWait) {
		t.Fatalf("no wait: %#v; want ErrLockWait", err)
	}
	if err := other.QueryRowContext(ctx, `SELECT RELEASE_LOCK(?)`, lock).Scan(&got); err != nil || got != 1 {
		t.Fatalf("RELEASE_LOCK: %d, %v", got, err)
	}
	mustUp(t, db, fsys, migrate.WithLockWait(time.Second))
	mustUp(t, db, fsys, migrate.WithLockWait(math.MaxInt64))
	cols := columnsOf(t, db, `SELECT column_name, column_type, is_nullable FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = 'velox_migrations' ORDER BY ordinal_position`)
	want := []string{
		"version bigint",
		"name varchar(255)",
		"checksum varchar(64)",
		"applied_at datetime(6)",
		"duration_ms bigint",
		"dirty tinyint(1)",
		"statements int",
		"error text",
	}
	if !slices.Equal(cols, want) {
		t.Errorf("columns\n%s\nwant\n%s", strings.Join(cols, "\n"), strings.Join(want, "\n"))
	}
	var engine, collation string
	if err := db.QueryRow(`SELECT engine, table_collation FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'velox_migrations'`).Scan(&engine, &collation); err != nil || engine != "InnoDB" || collation != "utf8mb4_bin" {
		t.Errorf("engine %s, collation %s, %v", engine, collation, err)
	}
	if s := mustStatus(t, db, fsys); len(s.Applied)+len(s.Pending) != 0 {
		t.Errorf("state %+v", s)
	}
	mustUp(t, db, dirOf(t, velox.MySQL, files("0001_a.sql", "CREATE TABLE a (x int);")))
}

// TestUpPostgresTransactionControl: a transaction file's COMMIT or ROLLBACK
// is refused before any file runs; one the check cannot see is reported
// after the file, and its version is not recorded.
func TestUpPostgresTransactionControl(t *testing.T) {
	for _, lead := range []string{"COMMIT", "ROLLBACK", "ABORT"} {
		db, _ := freshDB(t, velox.Postgres)
		fsys := dirOf(t, velox.Postgres, files(
			"0001_ok.sql", "CREATE TABLE ok (id integer);",
			"0002_x.sql", "CREATE TABLE a (id integer);\n"+lead+";\nCREATE TABLE b (id integer);\n"))
		_, err := migrate.Up(context.Background(), db, fsys)
		if e := asError(t, err, "file"); e.Pos != "0002_x.sql:2" {
			t.Errorf("%s: %v", lead, err)
		}
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM pg_tables WHERE tablename IN ('ok', 'a', 'b')`).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: %d tables, %v", lead, n, err)
		}
	}

	db, _ := freshDB(t, velox.Postgres)
	fsys := dirOf(t, velox.Postgres, files("0001_x.sql", "CREATE TABLE a (id integer);\nCOMMIT;\nCREATE TABLE b (id integer);\n"))
	_, err := migrate.Up(context.Background(), db, fsys, migrate.WithoutTxControlCheck())
	if err == nil || !strings.Contains(err.Error(), "0001_x.sql: the file ended the runner's transaction") {
		t.Fatalf("%v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM velox_migrations`).Scan(&n); err != nil || n != 0 {
		t.Errorf("history rows %d, %v", n, err)
	}
}

// TestUpPostgresHugeLockTimeout: a Config lock timeout past what
// lock_timeout takes is cut to it, as a file's directive is.
func TestUpPostgresHugeLockTimeout(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	fsys := dirOf(t, velox.Postgres, files("0001_x.sql", lockTimeoutIs("2147483647ms")))
	dir, err := migrate.NewDir(migrate.Config{Dialect: velox.Postgres, LockTimeout: 720 * time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	fsys["velox.json"] = &fstest.MapFile{Data: migrate.ConfigJSON(dir.Config)}
	resum(t, fsys)
	mustUp(t, db, fsys)
}
