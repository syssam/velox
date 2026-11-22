package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
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
	t.Parallel()
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
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name IN ('alphas', 'beta')`).Scan(&n); err != nil || n == 0 {
		t.Errorf("the plan's tables: %d, %v", n, err)
	}
}

// TestUpPostgresConcurrentIndexBuild: a second Up, as a second instance of an
// app that migrates at start runs, waits for the migration lock while the
// first builds an index CONCURRENTLY. The build waits at its end for every
// transaction older than it, and the second Up waited in one, in
// pg_advisory_lock, for the first's lock: a deadlock, which PostgreSQL broke
// by cancelling the build, leaving the version dirty and the index invalid.
// The second Up now waits between tries, in no transaction.
func TestUpPostgresConcurrentIndexBuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, dsn := freshDB(t, velox.Postgres)
	fsys := dirOf(t, velox.Postgres,
		files("0001_t.sql", "CREATE TABLE t (x int);\nINSERT INTO t SELECT g FROM generate_series(1, 1000) g;\n"),
		files("0002_t_online.sql", "-- velox:no-transaction\n\n-- velox:unit\nDROP INDEX CONCURRENTLY IF EXISTS t_x;\nCREATE INDEX CONCURRENTLY t_x ON t (x);\n"))
	mustUp(t, db, fsys, migrate.WithTarget(1))
	// A writer holds the build at its start, until the second Up waits for the lock.
	writer, err := openDB(t, "pgx", dsn).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Close() }()
	if _, err := writer.ExecContext(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(ctx, `UPDATE t SET x = x WHERE x = 1`); err != nil {
		t.Fatal(err)
	}
	first := make(chan error, 1)
	go func() {
		_, err := migrate.Up(ctx, openDB(t, "pgx", dsn), fsys)
		first <- err
	}()
	waitFor(t, db, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND query LIKE 'CREATE INDEX CONCURRENTLY%' AND wait_event_type = 'Lock'`)
	second := make(chan error, 1)
	go func() {
		_, err := migrate.Up(ctx, openDB(t, "pgx", dsn), fsys, migrate.WithLockWait(time.Minute))
		second <- err
	}()
	// The second Up has tried the lock, and waits for it, before the writer
	// lets the build go on. Its session's last statement is its try; the
	// first's is the build. Polled, so that a loaded runner cannot leave the
	// second Up not yet started; a pg_advisory_lock would match too.
	waitFor(t, db, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND query LIKE 'SELECT pg\_%advisory\_lock(%'`)
	if _, err := writer.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}
	if err := <-first; err != nil {
		t.Errorf("the Up that builds the index: %v", err)
	}
	if err := <-second; err != nil {
		t.Errorf("the Up that waits: %v", err)
	}
	if !pgIndexValid(t, db, "t_x") {
		t.Error("t_x is invalid")
	}
}

// waitFor waits, up to 30s, until the count q reads is above 0.
func waitFor(t *testing.T, db *sql.DB, q string) {
	t.Helper()
	for range 600 {
		var n int
		if err := db.QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n > 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("never true: %s", q)
}

func TestUpPostgresConcurrent(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	ctx := context.Background()
	db, _ := freshDB(t, velox.Postgres)
	other, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err := other.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrate.LockKey("public", "velox_migrations")); err != nil {
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
	if _, err := other.ExecContext(ctx, `SELECT pg_advisory_unlock($1)`, migrate.LockKey("public", "velox_migrations")); err != nil {
		t.Fatal(err)
	}
	mustUp(t, db, fsys, migrate.WithLockWait(300*time.Millisecond))
	mustUp(t, db, fsys, migrate.WithLockWait(0))
	mustUp(t, db, fsys, migrate.WithLockWait(math.MaxInt64)) // past lock_timeout's range, clamped
}

// TestUpPostgresLockPerSchema: two schemas of one database, each with its own
// history table, migrate at once. The lock's key held the table's name alone, so the
// second run waited for the first, and with no lock wait was refused.
func TestUpPostgresLockPerSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin, dsn := freshDB(t, velox.Postgres)
	mustExec(t, admin, "CREATE SCHEMA a")
	mustExec(t, admin, "CREATE SCHEMA b")
	in := func(schema string) *sql.DB {
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		return openDB(t, "pgx", dsn+sep+"search_path="+schema)
	}
	blocker, err := admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Close() }()
	if _, err := blocker.ExecContext(ctx, "SELECT pg_advisory_lock(4242)"); err != nil {
		t.Fatal(err)
	}
	// a's run holds the migration lock while its file waits for the test's lock.
	done := make(chan error, 1)
	go func() {
		_, err := migrate.Up(ctx, in("a"), dirOf(t, velox.Postgres, files("0001_wait.sql", "SELECT pg_advisory_xact_lock(4242);\nCREATE TABLE x (id integer);\n")))
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var n int
		if err := admin.QueryRow(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND query LIKE 'SELECT pg_advisory_xact_lock(4242)%' AND wait_event_type = 'Lock'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("a's run never reached its file")
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, err = migrate.Up(ctx, in("b"), dirOf(t, velox.Postgres, files("0001_y.sql", "CREATE TABLE y (id integer);\n")), migrate.WithLockWait(0))
	if _, uerr := blocker.ExecContext(ctx, "SELECT pg_advisory_unlock(4242)"); uerr != nil {
		t.Fatal(uerr)
	}
	if err != nil {
		t.Errorf("b, while a's run holds its lock: %v; want b's own lock", err)
	}
	if err := <-done; err != nil {
		t.Errorf("a: %v", err)
	}
}

func TestUpPostgresStandardConformingStrings(t *testing.T) {
	t.Parallel()
	db, dsn := freshDB(t, velox.Postgres)
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `ALTER DATABASE "`+name+`" SET standard_conforming_strings = off`)
	off := openDB(t, "pgx", dsn) // a new session takes the database's setting
	_, err := migrate.Up(context.Background(), off, dirOf(t, velox.Postgres))
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "session" || !strings.Contains(e.Reason, "standard_conforming_strings") {
		t.Fatalf("%#v; want Code session", err)
	}
}

// TestUpPostgresSearchPath: a file that empties search_path still records
// itself, and the next file runs with the session as Up was given it. The
// history table is named by the schema open read.
func TestUpPostgresSearchPath(t *testing.T) {
	t.Parallel()
	db, _ := freshDB(t, velox.Postgres)
	fsys := dirOf(t, velox.Postgres, files(
		"0001_dump.sql", "SELECT pg_catalog.set_config('search_path', '', false);\nCREATE TABLE public.dumped (id integer);\n",
		"0002_plain.sql", "CREATE TABLE plain (id integer);\n"))
	mustUp(t, db, fsys)
	if h := historyOf(t, db); len(h) != 2 || h[0].Version != 1 || h[1].Version != 2 {
		t.Errorf("history %+v; want versions 1 and 2", h)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pg_tables WHERE schemaname = 'public' AND tablename IN ('dumped', 'plain')`).Scan(&n); err != nil || n != 2 {
		t.Errorf("tables %d, %v; want 2", n, err)
	}
}

// withSearchPath is dsn, a URL, with its session's search_path set to value.
func withSearchPath(t *testing.T, dsn, value string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", value)
	u.RawQuery = q.Encode()
	return u.String()
}

// TestUpPostgresQuotedSchema: a schema whose name needs quoting, first in
// the session's search_path, holds the history table: Up applies, in a
// transaction and statement by statement, Status reads the row, and a
// second Up applies nothing, for hasHistory finds the quoted table. The
// name cannot be Config.Table's, which NewDir refuses dotted; it comes from
// the session.
func TestUpPostgresQuotedSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for _, c := range []struct{ name, schema string }{
		{"dotted", "my.schema"},
		{"quote", `we"ird`},
	} {
		t.Run(c.name, func(t *testing.T) {
			admin, dsn := freshDB(t, velox.Postgres)
			quoted := `"` + strings.ReplaceAll(c.schema, `"`, `""`) + `"`
			mustExec(t, admin, "CREATE SCHEMA "+quoted)
			db := openDB(t, "pgx", withSearchPath(t, dsn, quoted))
			fsys := dirOf(t, velox.Postgres, files(
				"0001_a.sql", "CREATE TABLE a (id integer);\n",
				"0002_b.sql", "-- velox:no-transaction\nCREATE TABLE b (id integer);\nCREATE TABLE c (id integer);\n"))
			if r := mustUp(t, db, fsys); !slices.Equal(versions(r.Applied), []uint64{1, 2}) {
				t.Fatalf("applied %v", versions(r.Applied))
			}
			if s := mustStatus(t, db, fsys); !slices.Equal(versions(s.Applied), []uint64{1, 2}) || len(s.Pending) != 0 || s.Dirty != nil {
				t.Errorf("state %+v", s)
			}
			if r := mustUp(t, db, fsys); len(r.Applied) != 0 {
				t.Errorf("second Up applied %v", versions(r.Applied))
			}
			var tables []string
			rows, err := admin.Query(`SELECT tablename FROM pg_tables WHERE schemaname = $1 ORDER BY tablename`, c.schema)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var n string
				if err := rows.Scan(&n); err != nil {
					t.Fatal(err)
				}
				tables = append(tables, n)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			_ = rows.Close()
			if want := []string{"a", "b", "c", "velox_migrations"}; !slices.Equal(tables, want) {
				t.Errorf("tables of %s: %v; want %v", c.schema, tables, want)
			}
			var n int
			if err := admin.QueryRow(`SELECT count(*) FROM pg_tables WHERE schemaname = 'public'`).Scan(&n); err != nil || n != 0 {
				t.Errorf("tables in public: %d, %v; want 0", n, err)
			}
			// A failing no-transaction file is recorded in the quoted schema too.
			bad := extend(t, fsys, velox.Postgres, files("0003_bad.sql", "-- velox:no-transaction\nSELECT 1/0;\n"))
			_, err = migrate.Up(ctx, db, bad)
			if de := dirtyOf(t, err); de.Version != 3 {
				t.Errorf("%+v; want version 3", de)
			}
			if h := historyOf(t, db); len(h) != 3 || !h[2].Dirty || !strings.Contains(h[2].Error, "division by zero") {
				t.Errorf("history %+v", h)
			}
		})
	}
}

// TestUpMySQLDottedDatabase: a database named my.db, the one the DSN
// names, holds the history table: Up applies, Status reads the row, and a
// second Up applies nothing.
func TestUpMySQLDottedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	const name = "my.db"
	base := os.Getenv("VELOX_TEST_MYSQL_ADMIN")
	if base == "" {
		t.Skip("needs VELOX_TEST_MYSQL_ADMIN")
	}
	mysqlDatabase(t, name)
	cfg, err := mysql.ParseDSN(base)
	if err != nil {
		t.Fatal(err)
	}
	cfg.DBName = name
	db := openDB(t, "mysql", cfg.FormatDSN())
	fsys := dirOf(t, velox.MySQL, files(
		"0001_a.sql", "CREATE TABLE a (id int);\n",
		"0002_b.sql", "CREATE TABLE b (id int);\nCREATE TABLE c (id int);\n"))
	if r := mustUp(t, db, fsys); !slices.Equal(versions(r.Applied), []uint64{1, 2}) {
		t.Fatalf("applied %v", versions(r.Applied))
	}
	if s := mustStatus(t, db, fsys); !slices.Equal(versions(s.Applied), []uint64{1, 2}) || len(s.Pending) != 0 || s.Dirty != nil {
		t.Errorf("state %+v", s)
	}
	if r := mustUp(t, db, fsys); len(r.Applied) != 0 {
		t.Errorf("second Up applied %v", versions(r.Applied))
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema = ? AND table_name IN ('a', 'b', 'c', 'velox_migrations')`, name).Scan(&n); err != nil || n != 4 {
		t.Errorf("tables of %s: %d, %v; want 4", name, n, err)
	}
	bad := extend(t, fsys, velox.MySQL, files("0003_bad.sql", "CREATE TABLE d (id int);\nINSERT INTO nope VALUES (1);\n"))
	_, err = migrate.Up(ctx, db, bad)
	if de := dirtyOf(t, err); de.Version != 3 || mysqlNumber(err) != 1146 {
		t.Errorf("%+v; want version 3, error 1146", de)
	}
	if h := historyOf(t, db); len(h) != 3 || !h[2].Dirty || h[2].Statements != 1 || !strings.Contains(h[2].Error, "1146") {
		t.Errorf("history %+v", h)
	}
}

// TestUpPostgresNoCurrentSchema: a session whose search_path names no
// schema is refused, not left to fail on the history table.
func TestUpPostgresNoCurrentSchema(t *testing.T) {
	t.Parallel()
	db, dsn := freshDB(t, velox.Postgres)
	var name string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	mustExec(t, db, `ALTER DATABASE "`+name+`" SET search_path = ''`)
	_, err := migrate.Up(context.Background(), openDB(t, "pgx", dsn), dirOf(t, velox.Postgres))
	if e := asError(t, err, "session"); e.Error() != "migrate: the connection has no current schema; set search_path" {
		t.Errorf("%v", err)
	}
}

// TestUpMySQLUse: a file that USEs another database records itself in the
// database open read, and the next file runs there again.
func TestUpMySQLUse(t *testing.T) {
	t.Parallel()
	db, _ := freshDB(t, velox.MySQL)
	mysqlDatabase(t, "velox_use_other")
	fsys := dirOf(t, velox.MySQL, files(
		"0001_a.sql", "CREATE TABLE a (id int);\nUSE velox_use_other;\nCREATE TABLE b (id int);\n",
		"0002_c.sql", "CREATE TABLE c (id int);\n"))
	mustUp(t, db, fsys)
	if h := historyOf(t, db); len(h) != 2 || h[0].Version != 1 || h[1].Version != 2 {
		t.Errorf("history %+v; want versions 1 and 2", h)
	}
	where := func(table string) string {
		var schema string
		if err := db.QueryRow(`SELECT table_schema FROM information_schema.TABLES WHERE table_name = ? AND table_schema IN (DATABASE(), 'velox_use_other')`, table).Scan(&schema); err != nil {
			t.Fatalf("%s: %v", table, err)
		}
		return schema
	}
	var fresh string
	if err := db.QueryRow(`SELECT DATABASE()`).Scan(&fresh); err != nil {
		t.Fatal(err)
	}
	if got := where("c"); got != fresh {
		t.Errorf("c is in %s; want %s", got, fresh)
	}
	if got := where("b"); got != "velox_use_other" {
		t.Errorf("b is in %s; want velox_use_other", got)
	}
}

// TestMySQLLock: another session holding the lock makes Up wait, then give
// ErrLockWait; released, Up creates the history table with MySQL's types.
func TestMySQLLock(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

// TestUpBaselineEmptyDatabase on PostgreSQL and MySQL: a database with no
// application table is refused; on PostgreSQL a temporary table and a
// table an extension owns do not count, and a table in any other user
// schema does, even one named as the history.
func TestUpBaselineEmptyDatabasePostgres(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fsys := dirOf(t, velox.Postgres, files(baselineFiles...))
	run := func(db *sql.DB) (*migrate.Report, error) {
		return migrate.Up(ctx, db, fsys, migrate.WithBaseline(1), migrate.WithTarget(1))
	}
	up := func(db *sql.DB) error {
		_, err := run(db)
		return err
	}
	t.Run("fresh", func(t *testing.T) {
		db, _ := freshDB(t, velox.Postgres)
		baselineErr(t, "fresh", up(db), "the database has no tables besides the history: there is nothing to adopt; apply without a baseline")
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM velox_migrations`).Scan(&n); err != nil || n != 0 {
			t.Errorf("history rows %d, %v", n, err)
		}
	})
	t.Run("temporary", func(t *testing.T) {
		db, _ := freshDB(t, velox.Postgres)
		conn, err := db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.ExecContext(ctx, `CREATE TEMPORARY TABLE scratch (x integer)`); err != nil {
			t.Fatal(err)
		}
		baselineErr(t, "temporary", up(db), "the database has no tables besides the history: there is nothing to adopt; apply without a baseline")
	})
	t.Run("extension", func(t *testing.T) {
		db, _ := freshDB(t, velox.Postgres)
		// A table an extension owns, as PostGIS owns spatial_ref_sys: one
		// added to an extension is its member as one it made would be.
		if _, err := db.Exec(`CREATE EXTENSION citext`); err != nil {
			t.Skipf("CREATE EXTENSION citext: %v", err)
		}
		mustExec(t, db, `CREATE TABLE spatial_ref_sys (srid integer PRIMARY KEY)`)
		mustExec(t, db, `ALTER EXTENSION citext ADD TABLE spatial_ref_sys`)
		baselineErr(t, "extension", up(db), "the database has no tables besides the history: there is nothing to adopt; apply without a baseline")
	})
	t.Run("schema", func(t *testing.T) {
		db, _ := freshDB(t, velox.Postgres)
		mustExec(t, db, `CREATE SCHEMA billing`)
		mustExec(t, db, `CREATE TABLE billing.invoices (id bigint PRIMARY KEY)`)
		baselinedOne(t, db)(run(db))
	})
	t.Run("history elsewhere", func(t *testing.T) {
		db, _ := freshDB(t, velox.Postgres)
		mustExec(t, db, `CREATE SCHEMA other`)
		mustExec(t, db, `CREATE TABLE other.velox_migrations (version bigint PRIMARY KEY)`)
		baselinedOne(t, db)(run(db))
	})
}

// baselinedOne checks the result of a baseline of version 1 on db: no
// error, Baselined [1], and the history its one row.
func baselinedOne(t *testing.T, db *sql.DB) func(*migrate.Report, error) {
	return func(r *migrate.Report, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(r.Baselined, []uint64{1}) || len(r.Applied) != 0 {
			t.Errorf("report %+v", r)
		}
		if rows := historyOf(t, db); len(rows) != 1 || rows[0].Version != 1 || rows[0].Name != "a" {
			t.Errorf("history %+v", rows)
		}
	}
}

func TestUpBaselineEmptyDatabaseMySQL(t *testing.T) {
	t.Parallel()
	fsys := dirOf(t, velox.MySQL, files(baselineFiles...))
	run := func(db *sql.DB) (*migrate.Report, error) {
		return migrate.Up(context.Background(), db, fsys, migrate.WithBaseline(1), migrate.WithTarget(1))
	}
	up := func(db *sql.DB) error {
		_, err := run(db)
		return err
	}
	db, _ := freshDB(t, velox.MySQL)
	baselineErr(t, "fresh", up(db), "the database has no tables besides the history: there is nothing to adopt; apply without a baseline")
	var n int
	if err := db.QueryRow("SELECT count(*) FROM velox_migrations").Scan(&n); err != nil || n != 0 {
		t.Errorf("history rows %d, %v", n, err)
	}
	mustExec(t, db, "CREATE TABLE t (x integer)")
	baselinedOne(t, db)(run(db))
}
