package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// dirtyOf is err as a *DirtyError, or fails t.
func dirtyOf(t *testing.T, err error) *migrate.DirtyError {
	t.Helper()
	var de *migrate.DirtyError
	if !errors.As(err, &de) {
		t.Fatalf("%#v; want *DirtyError", err)
	}
	return de
}

// mysqlNumber is the MySQL error number in err, or 0.
func mysqlNumber(err error) uint16 {
	if me, ok := errors.AsType[*mysql.MySQLError](err); ok {
		return me.Number
	}
	return 0
}

// TestUpMySQLDirty: a failing statement leaves the version dirty with the
// statements before it; a later run is refused until it resumes, and the
// resume starts after them.
func TestUpMySQLDirty(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.MySQL)
	fsys := dirOf(t, velox.MySQL, files("0001_abc.sql", "CREATE TABLE a (x int);\nINSERT INTO b VALUES (1);\nCREATE TABLE c (x int);\n"))
	_, err := migrate.Up(ctx, db, fsys)
	de := dirtyOf(t, err)
	if de.Version != 1 || de.Path != "0001_abc.sql" || de.Statement != 2 || de.Statements != 3 || mysqlNumber(err) != 1146 {
		t.Fatalf("%+v; want statement 2 of 3, error 1146", de)
	}
	rows := historyOf(t, db)
	if len(rows) != 1 || !rows[0].Dirty || rows[0].Statements != 1 || rows[0].Error != de.Err.Error() {
		t.Fatalf("history %+v", rows)
	}
	_, err = migrate.Up(ctx, db, fsys)
	if de := dirtyOf(t, err); de.Statement != 0 || de.Err == nil || de.Err.Error() != rows[0].Error {
		t.Errorf("again: %+v", de)
	}
	s, err := migrate.Status(ctx, db, fsys)
	if _ = dirtyOf(t, err); s == nil || s.Dirty == nil || s.Dirty.Version != 1 || s.Dirty.Statements != 1 || s.Dirty.Error != rows[0].Error {
		t.Errorf("state %+v", s)
	}
	mustExec(t, db, "CREATE TABLE b (x int)")
	r := mustUp(t, db, fsys, migrate.WithResume())
	if len(r.Applied) != 1 || r.Applied[0].Version != 1 || r.Applied[0].Statements != 3 {
		t.Errorf("report %+v", r)
	}
	rows = historyOf(t, db)
	if len(rows) != 1 || rows[0].Dirty || rows[0].Statements != 3 || rows[0].Error != "" {
		t.Errorf("history %+v", rows)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM b`).Scan(&n); err != nil || n != 1 {
		t.Errorf("b holds %d, %v", n, err)
	}
	if _, err := db.Exec(`SELECT * FROM c`); err != nil {
		t.Errorf("c: %v", err)
	}
	mustUp(t, db, fsys, migrate.WithResume()) // nothing dirty: an ordinary run
}

func TestResolveMySQL(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.MySQL)
	fsys := dirOf(t, velox.MySQL, files("0001_abc.sql", "CREATE TABLE a (x int);\nINSERT INTO b VALUES (1);\n"))
	_ = dirtyOf(t, func() error { _, err := migrate.Up(ctx, db, fsys); return err }())
	// Undone: a human reverts statement 1, fixes the file, and applies it.
	mustExec(t, db, "DROP TABLE a")
	if err := migrate.Resolve(ctx, db, fsys, 1, migrate.ResolveUndone); err != nil {
		t.Fatal(err)
	}
	if rows := historyOf(t, db); len(rows) != 0 {
		t.Fatalf("history %+v", rows)
	}
	fsys["0001_abc.sql"].Data = []byte("CREATE TABLE a (x int);\nCREATE TABLE b (x int);\nINSERT INTO b VALUES (1);\n")
	resum(t, fsys)
	mustUp(t, db, fsys)
	// Applied: a human finishes the file.
	fsys = extend(t, fsys, velox.MySQL, files("0002_d.sql", "CREATE TABLE d (x int);\nINSERT INTO nope VALUES (1);\n"))
	_ = dirtyOf(t, func() error { _, err := migrate.Up(ctx, db, fsys); return err }())
	if err := migrate.Resolve(ctx, db, fsys, 2, migrate.ResolveApplied); err != nil {
		t.Fatal(err)
	}
	if rows := historyOf(t, db); len(rows) != 2 || rows[1].Dirty || rows[1].Statements != 2 || rows[1].Error != "" {
		t.Fatalf("history %+v", rows)
	}
	if err := migrate.Resolve(ctx, db, fsys, 2, migrate.ResolveApplied); err == nil || asError(t, err, "history").Reason != "version 2 is not dirty" {
		t.Errorf("clean: %v", err)
	}
	// Edited: ResolveApplied is refused, ResolveUndone is not.
	fsys = extend(t, fsys, velox.MySQL, files("0003_e.sql", "CREATE TABLE e (x int);\nINSERT INTO nope VALUES (1);\n"))
	_ = dirtyOf(t, func() error { _, err := migrate.Up(ctx, db, fsys); return err }())
	fsys["0003_e.sql"].Data = []byte("CREATE TABLE e (x int);\n")
	resum(t, fsys)
	var ce *migrate.ChecksumError
	if err := migrate.Resolve(ctx, db, fsys, 3, migrate.ResolveApplied); !errors.As(err, &ce) || ce.Version != 3 {
		t.Errorf("edited: %#v; want *ChecksumError", err)
	}
	if err := migrate.Resolve(ctx, db, fsys, 3, migrate.ResolveUndone); err != nil {
		t.Errorf("edited, undone: %v", err)
	}
}

// TestUpMySQLLockTimeout: a file's lock timeout bounds its wait for a
// metadata lock another session holds.
func TestUpMySQLLockTimeout(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.MySQL)
	mustExec(t, db, "CREATE TABLE t (x int)")
	other, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	for _, q := range []string{"BEGIN", "SELECT * FROM t"} {
		if _, err := other.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	fsys := dirOf(t, velox.MySQL, files("0001_t.sql", "-- velox:lock-timeout 1s\nALTER TABLE t ADD COLUMN y int;\n"))
	start := time.Now()
	_, err = migrate.Up(ctx, db, fsys)
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "retry" || mysqlNumber(err) != 1205 {
		t.Errorf("%v; want error 1205, to retry", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Up took %s", d)
	}
	if _, err := other.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if rows := historyOf(t, db); len(rows) != 0 {
		t.Errorf("history %+v", rows)
	}
}

// TestUpMySQLSQLMode: a session whose sql_mode would read a pending file's
// literals otherwise is refused before any file runs.
func TestUpMySQLSQLMode(t *testing.T) {
	ctx := context.Background()
	_, dsn := freshDB(t, velox.MySQL)
	withMode := func(mode string) *sql.DB {
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Params = map[string]string{"sql_mode": "'" + mode + "'"}
		return openDB(t, "mysql", cfg.FormatDSN())
	}
	for _, c := range []struct {
		mode, file, reason string
	}{
		{"NO_BACKSLASH_ESCAPES", `INSERT INTO a VALUES ('a\\b');`,
			"0002_b.sql holds a backslash in a literal, which NO_BACKSLASH_ESCAPES would store as two; turn the mode off for migrating"},
		{"ANSI_QUOTES", `INSERT INTO a VALUES ("ab");`,
			`0002_b.sql holds a "…" literal, which ANSI_QUOTES would read as a name; turn the mode off for migrating`},
	} {
		db := withMode(c.mode)
		fsys := dirOf(t, velox.MySQL, files("0001_a.sql", "CREATE TABLE a (x varchar(10));", "0002_b.sql", c.file))
		_, err := migrate.Up(ctx, db, fsys)
		if e := asError(t, err, "session"); e.Reason != c.reason {
			t.Errorf("%s: %q", c.mode, e.Reason)
		}
		if rows := historyOf(t, db); len(rows) != 0 {
			t.Errorf("%s: history %+v", c.mode, rows)
		}
		// A run that stops below the file is not refused.
		mustUp(t, db, fsys, migrate.WithTarget(1))
		mustExec(t, db, "DROP TABLE a")
		mustExec(t, db, "DELETE FROM velox_migrations")
	}
}

func TestUpMySQLConcurrent(t *testing.T) {
	db, dsn := freshDB(t, velox.MySQL)
	fsys := dirOf(t, velox.MySQL, files(concurrentFiles...))
	upTwice(t, fsys, db, openDB(t, "mysql", dsn))
	if rows := historyOf(t, db); len(rows) != 3 {
		t.Errorf("history %+v", rows)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM log`).Scan(&n); err != nil || n != 2 {
		t.Errorf("log rows %d, %v; want 2", n, err)
	}
}

// pgIndexValid is pg_index.indisvalid of the index.
func pgIndexValid(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var ok bool
	if err := db.QueryRow(`SELECT indisvalid FROM pg_index WHERE indexrelid = $1::regclass`, name).Scan(&ok); err != nil {
		t.Fatal(err)
	}
	return ok
}

// usersRows makes an org and n users, with bio NULL.
func usersRows(t *testing.T, db *sql.DB, n int) {
	t.Helper()
	mustExec(t, db, `INSERT INTO orgs (name) VALUES ('o')`)
	mustExec(t, db, fmt.Sprintf(`INSERT INTO users (org_id, name, email, created_at) SELECT 1, 'u' || g, 'u' || g || '@x', now() FROM generate_series(1, %d) g`, n))
}

// TestUpPostgresOnlineResume: an index build cancelled while it waits for
// an older snapshot leaves the version dirty with its error and an invalid
// index; the resume drops it and builds it again.
func TestUpPostgresOnlineResume(t *testing.T) {
	ctx := context.Background()
	db, dsn := freshDB(t, velox.Postgres)
	fsys := caseDir(t, caseNamed("add-index"), velox.Postgres)
	mustUp(t, db, fsys, migrate.WithTarget(1))
	usersRows(t, db, 1000)
	other, err := openDB(t, "pgx", dsn).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	var n int
	if _, err := other.ExecContext(ctx, `BEGIN ISOLATION LEVEL REPEATABLE READ`); err != nil {
		t.Fatal(err)
	}
	if err := other.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil || n != 1000 {
		t.Fatalf("users %d, %v", n, err)
	}
	short, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, err = migrate.Up(short, db, fsys)
	cancel()
	if de := dirtyOf(t, err); de.Version != 2 || de.Statement != 2 {
		t.Errorf("%+v; want version 2 at statement 2", de)
	}
	rows := historyOf(t, db)
	if len(rows) != 2 || !rows[1].Dirty || rows[1].Statements != 0 || rows[1].Error == "" {
		t.Fatalf("history %+v", rows)
	}
	if pgIndexValid(t, db, "user_created_at") {
		t.Error("the cancelled index is valid")
	}
	if _, err := other.ExecContext(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	mustUp(t, db, fsys, migrate.WithResume())
	if !pgIndexValid(t, db, "user_created_at") {
		t.Error("the resumed index is invalid")
	}
	if rows := historyOf(t, db); rows[1].Dirty || rows[1].Statements != 2 || rows[1].Error != "" {
		t.Errorf("history %+v", rows)
	}
}

// TestUpPostgresSetNotNullResume: SET NOT NULL fails at VALIDATE on a NULL;
// fixed, the resume runs the unit from its start.
func TestUpPostgresSetNotNullResume(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.Postgres)
	fsys := caseDir(t, caseNamed("set-not-null"), velox.Postgres)
	mustUp(t, db, fsys, migrate.WithTarget(1))
	usersRows(t, db, 3)
	mustExec(t, db, `UPDATE users SET bio = 'b' WHERE id > 1`)
	_, err := migrate.Up(ctx, db, fsys)
	de := dirtyOf(t, err)
	if code, _ := velox.DriverCode(err); code != "23514" || de.Statement != 3 || !strings.Contains(string(fsys[de.Path].Data), "VALIDATE CONSTRAINT") {
		t.Errorf("%v; want a check violation at VALIDATE, statement 3", err)
	}
	rows := historyOf(t, db)
	if last := rows[len(rows)-1]; !last.Dirty || last.Statements != 0 || last.Error == "" {
		t.Fatalf("history %+v", rows)
	}
	mustExec(t, db, `UPDATE users SET bio = '' WHERE bio IS NULL`)
	mustUp(t, db, fsys, migrate.WithResume())
	var nullable string
	if err := db.QueryRow(`SELECT is_nullable FROM information_schema.columns WHERE table_name = 'users' AND column_name = 'bio'`).Scan(&nullable); err != nil || nullable != "NO" {
		t.Errorf("bio is_nullable %s, %v", nullable, err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pg_constraint WHERE conname = 'users_bio_not_null'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("users_bio_not_null: %d, %v", n, err)
	}
}

// lockTimeoutIs is a statement that raises unless lock_timeout is want.
func lockTimeoutIs(want string) string {
	return fmt.Sprintf(`DO $$ BEGIN IF current_setting('lock_timeout') <> '%s' THEN RAISE EXCEPTION 'lock_timeout is %%', current_setting('lock_timeout'); END IF; END $$;`, want)
}

// TestUpPostgresNoTransactionLockTimeout: a no-transaction file runs with
// no lock timeout unless its directive gives one, as the enum file Add
// wrote does.
func TestUpPostgresNoTransactionLockTimeout(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	mustUp(t, db, dirOf(t, velox.Postgres, files(
		"0001_off.sql", "-- velox:no-transaction\n"+lockTimeoutIs("0"),
		"0002_three.sql", "-- velox:no-transaction\n-- velox:lock-timeout 3s\n"+lockTimeoutIs("3s"))))

	db, _ = freshDB(t, velox.Postgres)
	c := caseNamed("enum-insert")
	from, to := models(t, c, velox.Postgres)
	fsys := dirOf(t, velox.Postgres, step{to: from})
	dir, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := migrate.NewPlan(dir.Snapshot, to, migrate.PlanOptions{Allow: c.allow})
	if err != nil {
		t.Fatal(err)
	}
	added, err := dir.Add("", plan, to)
	if err != nil {
		t.Fatal(err)
	}
	enum := ""
	for p, b := range added {
		fsys[p] = &fstest.MapFile{Data: b}
		if strings.HasSuffix(p, "_enum.sql") {
			enum = string(b)
		}
	}
	if !strings.Contains(enum, "\n-- velox:no-transaction\n-- velox:lock-timeout 5s\n") {
		t.Fatalf("enum file:\n%s", enum)
	}
	resum(t, fsys)
	mustUp(t, db, fsys)
	var values string
	if err := db.QueryRow(`SELECT string_agg(enumlabel, ',' ORDER BY enumsortorder) FROM pg_enum WHERE enumtypid = 'users_role'::regtype`).Scan(&values); err != nil || values != "admin,editor,viewer" {
		t.Errorf("users_role: %s, %v", values, err)
	}
}

// TestUpMySQLLockWaitTimeout: a file runs with its directive's
// lock_wait_timeout in whole seconds, rounded up, else the Config's; 0 is
// the session's own, which is set back after each file.
func TestUpMySQLLockWaitTimeout(t *testing.T) {
	_, dsn := freshDB(t, velox.MySQL)
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Params = map[string]string{"lock_wait_timeout": "77"}
	db := openDB(t, "mysql", cfg.FormatDSN())
	db.SetMaxOpenConns(1)
	const log = "INSERT INTO log SELECT @@SESSION.lock_wait_timeout;"
	mustUp(t, db, dirOf(t, velox.MySQL, files(
		"0001_config.sql", "CREATE TABLE log (n bigint);\n"+log,
		"0002_off.sql", "-- velox:lock-timeout 0s\n"+log,
		"0003_directive.sql", "-- velox:lock-timeout 1500ms\n"+log)))
	rows, err := db.Query(`SELECT n FROM log`)
	if err != nil {
		t.Fatal(err)
	}
	var got []int64
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		got = append(got, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if fmt.Sprint(got) != "[5 77 2]" {
		t.Errorf("lock_wait_timeout in the files: %v; want [5 77 2]", got)
	}
	var after int64
	if err := db.QueryRow(`SELECT @@SESSION.lock_wait_timeout`).Scan(&after); err != nil || after != 77 {
		t.Errorf("after: %d, %v; want 77", after, err)
	}
}

// TestUpMySQLCancelAfterUnit: a cancel that lands after a unit ran keeps
// its count, and the run stops before the next unit with a *DirtyError.
func TestUpMySQLCancelAfterUnit(t *testing.T) {
	db, _ := freshDB(t, velox.MySQL)
	fsys := dirOf(t, velox.MySQL, files("0001_abc.sql", "CREATE TABLE a (x int);\nCREATE TABLE b (x int);\nCREATE TABLE c (x int);\n"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hook := migrate.WithUnitDone(func(_ uint64, end int) {
		if end == 1 {
			cancel()
		}
	})
	_, err := migrate.Up(ctx, db, fsys, hook)
	if de := dirtyOf(t, err); de.Statement != 2 || !errors.Is(err, context.Canceled) {
		t.Errorf("%v; want a cancel at statement 2", err)
	}
	if rows := historyOf(t, db); len(rows) != 1 || !rows[0].Dirty || rows[0].Statements != 1 || !strings.Contains(rows[0].Error, "canceled") {
		t.Fatalf("history %+v", rows)
	}
	mustUp(t, db, fsys, migrate.WithResume()) // CREATE TABLE a again would fail
	if rows := historyOf(t, db); rows[0].Dirty || rows[0].Statements != 3 {
		t.Errorf("history %+v", rows)
	}
}

// TestUpMySQLOwnTransaction: a file's own transaction is rolled back when
// a statement in it fails, or when the file ends with it open; the error is
// committed, the row is not locked, the pooled connection holds no
// transaction, and the resume starts at START TRANSACTION.
func TestUpMySQLOwnTransaction(t *testing.T) {
	ctx := context.Background()
	db, dsn := freshDB(t, velox.MySQL)
	db.SetMaxOpenConns(1)
	other := openDB(t, "mysql", dsn)
	mustExec(t, db, "CREATE TABLE t (x int PRIMARY KEY)")
	fsys := dirOf(t, velox.MySQL, files("0001_tx.sql", "START TRANSACTION;\nINSERT INTO t VALUES (1);\nINSERT INTO nope VALUES (1);\nCOMMIT;\n"))
	_, err := migrate.Up(ctx, db, fsys)
	if de := dirtyOf(t, err); de.Statement != 3 || mysqlNumber(err) != 1146 ||
		!strings.Contains(err.Error(), "\n  version 1 is dirty; fix the cause and run migrate apply -resume, or run migrate resolve -version 1 -undone\n") {
		t.Errorf("%v; want 1146 at statement 3, with nothing to undo", err)
	}
	if rows := historyOf(t, other); len(rows) != 1 || !rows[0].Dirty || rows[0].Statements != 0 || !strings.Contains(rows[0].Error, "1146") {
		t.Fatalf("history, from another session: %+v", rows)
	}
	conn, err := other.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	for _, q := range []string{"SET SESSION innodb_lock_wait_timeout = 1", "UPDATE velox_migrations SET duration_ms = duration_ms + 1 WHERE version = 1"} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	inTx := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow(`SELECT count(*) FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = CONNECTION_ID()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if inTx() != 0 {
		t.Error("the pooled connection holds a transaction")
	}
	mustExec(t, db, "CREATE TABLE nope (x int)")
	mustUp(t, db, fsys, migrate.WithResume())
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Errorf("t holds %d, %v; want 1", n, err)
	}
	// A file that leaves its transaction open.
	fsys = extend(t, fsys, velox.MySQL, files("0002_open.sql", "START TRANSACTION;\nINSERT INTO t VALUES (2);\n"))
	_, err = migrate.Up(ctx, db, fsys)
	if de := dirtyOf(t, err); de.Version != 2 || de.Statement != 1 || !strings.Contains(err.Error(), "the file leaves a transaction open") {
		t.Errorf("open: %v", err)
	}
	if rows := historyOf(t, other); len(rows) != 2 || !rows[1].Dirty || rows[1].Statements != 0 || rows[1].Error != "the file leaves a transaction open" {
		t.Errorf("open: history %+v", rows)
	}
	if err := other.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil || n != 1 {
		t.Errorf("open: t holds %d, %v; want 1", n, err)
	}
	if inTx() != 0 {
		t.Error("open: the pooled connection holds a transaction")
	}
}

// TestUpPostgresOwnTransaction: a no-transaction file left in a
// transaction block is rolled back and dirty.
func TestUpPostgresOwnTransaction(t *testing.T) {
	db, _ := freshDB(t, velox.Postgres)
	db.SetMaxOpenConns(1)
	fsys := dirOf(t, velox.Postgres, files("0001_open.sql", "-- velox:no-transaction\nBEGIN;\nCREATE TABLE p (x int);\n"))
	_, err := migrate.Up(context.Background(), db, fsys)
	if de := dirtyOf(t, err); de.Statement != 1 || !strings.Contains(err.Error(), "the file leaves a transaction open") {
		t.Errorf("%v", err)
	}
	if rows := historyOf(t, db); len(rows) != 1 || !rows[0].Dirty || rows[0].Error != "the file leaves a transaction open" {
		t.Errorf("history %+v", rows)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_name = 'p'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("p: %d, %v; want rolled back", n, err)
	}
}

// TestUpMySQLProgressLost: a unit's count that cannot be written is a
// wrapped error, not a *DirtyError.
func TestUpMySQLProgressLost(t *testing.T) {
	db, dsn := freshDB(t, velox.MySQL)
	other := openDB(t, "mysql", dsn)
	fsys := dirOf(t, velox.MySQL, files("0001_ab.sql", "CREATE TABLE a (x int);\nCREATE TABLE b (x int);\n"))
	hook := migrate.WithUnitDone(func(_ uint64, end int) {
		if end == 1 {
			mustExec(t, other, "DROP TABLE velox_migrations")
		}
	})
	_, err := migrate.Up(context.Background(), db, fsys, hook)
	var de *migrate.DirtyError
	if err == nil || errors.As(err, &de) || !strings.HasPrefix(err.Error(), "migrate: 0001_ab.sql: statements 1 to 1 ran, but recording them in velox_migrations: ") || mysqlNumber(err) != 1146 {
		t.Errorf("%v; want the wrapped error", err)
	}
}

// TestUpMySQLConnectionKilled: when the session's connection is lost after
// a unit, the count stays at what the session last wrote, and the error,
// written through the pool, says the unit's outcome is unknown.
func TestUpMySQLConnectionKilled(t *testing.T) {
	db, dsn := freshDB(t, velox.MySQL)
	killer := openDB(t, "mysql", dsn)
	fsys := dirOf(t, velox.MySQL, files("0001_abc.sql", "CREATE TABLE a (x int);\nCREATE TABLE b (x int);\nCREATE TABLE c (x int);\n"))
	hook := migrate.WithUnitDone(func(_ uint64, end int) {
		if end != 2 {
			return
		}
		var id int64
		if err := killer.QueryRow(`SELECT id FROM information_schema.processlist WHERE db = DATABASE() AND id <> CONNECTION_ID()`).Scan(&id); err != nil {
			t.Errorf("the session's id: %v", err)
			return
		}
		mustExec(t, killer, fmt.Sprintf("KILL CONNECTION %d", id))
	})
	_, err := migrate.Up(context.Background(), db, fsys, hook)
	const msg = "the connection was lost during statement 2; check whether it ran before resuming"
	if de := dirtyOf(t, err); de.Statement != 2 || de.Err == nil || de.Err.Error() != msg {
		t.Errorf("%v; want statement 2, the connection lost", err)
	}
	if rows := historyOf(t, db); len(rows) != 1 || !rows[0].Dirty || rows[0].Statements != 1 || rows[0].Error != msg {
		t.Errorf("history %+v; want the count of unit 1 and the message", rows)
	}
}

// stepwiseFailing is a directory of a table and a no-transaction file that
// empties the session's search_path and then runs the statement fail.
func stepwiseFailing(t *testing.T, fail string) fstest.MapFS {
	t.Helper()
	return dirOf(t, velox.Postgres, files(
		"0001_a.sql", "CREATE TABLE a (id integer);\n",
		"0002_b.sql", "-- velox:no-transaction\nSELECT pg_catalog.set_config('search_path', '', false);\n"+fail+"\n"))
}

// TestUpPostgresStepwiseSearchPath: a no-transaction file that empties
// search_path and then fails still leaves its row dirty with the error, for
// the statements that record it name the history table by its schema. Status
// and Resolve find the row too.
func TestUpPostgresStepwiseSearchPath(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.Postgres)
	fsys := stepwiseFailing(t, "SELECT 1/0;")
	_, err := migrate.Up(ctx, db, fsys)
	de := dirtyOf(t, err)
	if code, _ := velox.DriverCode(err); de.Version != 2 || de.Statement != 2 || de.Statements != 2 || code != "22012" {
		t.Fatalf("%+v; want version 2, statement 2 of 2, error 22012", de)
	}
	rows := historyOf(t, db)
	if len(rows) != 2 || rows[0].Dirty || !rows[1].Dirty || rows[1].Statements != 1 ||
		rows[1].Error != de.Err.Error() || !strings.Contains(rows[1].Error, "division by zero") {
		t.Fatalf("history %+v; want version 2 dirty after statement 1, with the division by zero", rows)
	}
	s, err := migrate.Status(ctx, db, fsys)
	if _ = dirtyOf(t, err); s == nil || s.Dirty == nil || s.Dirty.Version != 2 || s.Dirty.Error != rows[1].Error || !slices.Equal(versions(s.Applied), []uint64{1}) {
		t.Errorf("state %+v", s)
	}
	if err := migrate.Resolve(ctx, db, fsys, 2, migrate.ResolveApplied); err != nil {
		t.Fatal(err)
	}
	s = mustStatus(t, db, fsys)
	if s.Dirty != nil || !slices.Equal(versions(s.Applied), []uint64{1, 2}) || len(s.Pending) != 0 {
		t.Errorf("state after Resolve %+v", s)
	}
	if rows := historyOf(t, db); len(rows) != 2 || rows[1].Dirty || rows[1].Statements != 2 || rows[1].Error != "" {
		t.Errorf("history after Resolve %+v", rows)
	}
}

// TestUpPostgresStepwiseLostConnection: a statement that ends its own
// session leaves the row dirty with an error, written through the pool
// when the session no longer answers.
func TestUpPostgresStepwiseLostConnection(t *testing.T) {
	ctx := context.Background()
	db, _ := freshDB(t, velox.Postgres)
	fsys := stepwiseFailing(t, "SELECT pg_terminate_backend(pg_backend_pid());")
	_, err := migrate.Up(ctx, db, fsys)
	de := dirtyOf(t, err)
	if code, _ := velox.DriverCode(err); de.Version != 2 || de.Statement != 2 || de.Statements != 2 || code != "57P01" {
		t.Fatalf("%+v; want version 2, statement 2 of 2, error 57P01", de)
	}
	rows := historyOf(t, db)
	if len(rows) != 2 || !rows[1].Dirty || rows[1].Error == "" || rows[1].Error != de.Err.Error() {
		t.Fatalf("history %+v; want version 2 dirty with the error", rows)
	}
	if s, err := migrate.Status(ctx, db, fsys); s == nil || s.Dirty == nil || s.Dirty.Error != rows[1].Error {
		_ = dirtyOf(t, err)
		t.Errorf("state %+v", s)
	}
}

// TestUpMySQLAutocommitOff: a file that turns autocommit off leaves a
// transaction open, at its end or when it fails; either way it is rolled
// back, the row's error is committed, and the row is not locked.
func TestUpMySQLAutocommitOff(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name, file, error string
		statement         int
	}{
		{"left open", "SET autocommit = 0;\nINSERT INTO t VALUES (1);\n", "the file leaves a transaction open", 1},
		{"failing", "SET autocommit = 0;\nINSERT INTO t VALUES (1);\nINSERT INTO nope VALUES (1);\n", "1146", 3},
	} {
		db, dsn := freshDB(t, velox.MySQL)
		db.SetMaxOpenConns(1)
		other := openDB(t, "mysql", dsn)
		mustExec(t, db, "CREATE TABLE t (x int)")
		_, err := migrate.Up(ctx, db, dirOf(t, velox.MySQL, files("0001_off.sql", c.file)))
		if de := dirtyOf(t, err); de.Statement != c.statement || !strings.Contains(err.Error(), c.error) {
			t.Errorf("%s: %v", c.name, err)
		}
		if rows := historyOf(t, other); len(rows) != 1 || !rows[0].Dirty || rows[0].Statements != 0 || !strings.Contains(rows[0].Error, c.error) {
			t.Errorf("%s: history, from another session: %+v", c.name, rows)
		}
		var n int
		if err := other.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: t holds %d, %v; want 0", c.name, n, err)
		}
		conn, err := other.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range []string{"SET SESSION innodb_lock_wait_timeout = 1", "UPDATE velox_migrations SET duration_ms = duration_ms + 1 WHERE version = 1"} {
			if _, err := conn.ExecContext(ctx, q); err != nil {
				t.Errorf("%s: %s: %v", c.name, q, err)
			}
		}
		_ = conn.Close()
		var autocommit int
		if err := db.QueryRow(`SELECT count(*) FROM information_schema.innodb_trx WHERE trx_mysql_thread_id = CONNECTION_ID()`).Scan(&n); err != nil || n != 0 {
			t.Errorf("%s: the pool's connection holds %d transactions, %v", c.name, n, err)
		}
		if err := db.QueryRow(`SELECT @@autocommit`).Scan(&autocommit); err != nil || autocommit != 1 {
			t.Errorf("%s: the pool's connection has autocommit %d, %v", c.name, autocommit, err)
		}
	}
}

// TestUpMySQLSessionNotPooled: what a file sets on the session never
// reaches the pool, though the file applies.
func TestUpMySQLSessionNotPooled(t *testing.T) {
	db, _ := freshDB(t, velox.MySQL)
	db.SetMaxOpenConns(1)
	mustUp(t, db, dirOf(t, velox.MySQL, files("0001_mode.sql", "SET SESSION sql_mode = 'ANSI_QUOTES';\nSET SESSION foreign_key_checks = 0;\n")))
	var mode string
	var fk int
	if err := db.QueryRow(`SELECT @@SESSION.sql_mode, @@SESSION.foreign_key_checks`).Scan(&mode, &fk); err != nil || strings.Contains(mode, "ANSI_QUOTES") || fk != 1 {
		t.Errorf("the pool's connection: sql_mode %q, foreign_key_checks %d, %v", mode, fk, err)
	}
}
