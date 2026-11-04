package migrate_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// killedBase is a table a with one row, n = 0, applied as version 1.
const killedBase = "CREATE TABLE a (id integer PRIMARY KEY, n integer NOT NULL);\nINSERT INTO a VALUES (1, 0);\n"

// eventually polls cond every 10ms for 20s, and fails t when it never holds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); !cond(); {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// killDuringUpdate runs Up of fsys on dsn's database, through a proxy that is cut
// while the file's UPDATE of a waits for a row lock the test holds: the run dies, as
// if killed, with the UPDATE on the server. The lock is then let go, and once the
// server has ended the run's session, Up's error is returned.
func killDuringUpdate(t *testing.T, d velox.Dialect, db *sql.DB, dsn string, fsys fstest.MapFS) error {
	t.Helper()
	ctx := context.Background()
	blocker, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Close() }()
	begin, driver, through := "BEGIN", "pgx", pgThrough
	waiting := `SELECT pid FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE 'UPDATE a %'`
	alive := `SELECT count(*) FROM pg_stat_activity WHERE pid = $1`
	if d == velox.MySQL {
		begin, driver, through = "START TRANSACTION", "mysql", mysqlThrough
		waiting = `SELECT id FROM information_schema.processlist WHERE db = DATABASE() AND info LIKE 'UPDATE a %'`
		alive = `SELECT count(*) FROM information_schema.processlist WHERE id = ?`
	}
	for _, q := range []string{begin, "SELECT n FROM a WHERE id = 1 FOR UPDATE"} {
		if _, err := blocker.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	via, proxy := through(t, dsn)
	run := openDB(t, driver, via)
	done := make(chan error, 1)
	go func() {
		_, err := migrate.Up(ctx, run, fsys)
		done <- err
	}()
	var id int64
	eventually(t, "the run's UPDATE to wait", func() bool { return db.QueryRow(waiting).Scan(&id) == nil })
	proxy.cut()
	err = <-done
	if _, cerr := blocker.ExecContext(ctx, "COMMIT"); cerr != nil {
		t.Fatal(cerr)
	}
	eventually(t, "the server to end the run's session", func() bool {
		var n int
		return db.QueryRow(alive, id).Scan(&n) == nil && n == 0
	})
	return err
}

// nOf is a's n.
func nOf(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT n FROM a WHERE id = 1`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestUpPostgresKilledDuringStatement: a run killed while a no-transaction file's
// UPDATE waited left the UPDATE on the server, which finished it, and the history
// counting none of the file: -resume, as the refusal said, ran it twice. A unit of
// one statement now commits with its count, so the server rolls the UPDATE back with
// the session, and -resume runs it once.
func TestUpPostgresKilledDuringStatement(t *testing.T) {
	t.Parallel()
	db, dsn := freshDB(t, velox.Postgres)
	fsys := dirOf(t, velox.Postgres, files("0001_a.sql", killedBase))
	mustUp(t, db, fsys)
	fsys = extend(t, fsys, velox.Postgres, files("0002_backfill.sql", "-- velox:no-transaction\n-- velox:lock-timeout 1m\nUPDATE a SET n = n + 1;\nCREATE TABLE b (id integer);\n"))
	_ = dirtyOf(t, killDuringUpdate(t, velox.Postgres, db, dsn, fsys))
	rows := historyOf(t, db)
	if len(rows) != 2 || !rows[1].Dirty || rows[1].Statements != 0 {
		t.Fatalf("history %+v", rows)
	}
	if n := nOf(t, db); n != 0 {
		t.Errorf("after the kill n is %d, and the history counts no statement; want the UPDATE rolled back", n)
	}
	_, err := migrate.Status(context.Background(), db, fsys)
	if msg := dirtyOf(t, err).Error(); !strings.Contains(msg, "\n  fix the cause and run migrate apply -resume") {
		t.Errorf("status: %s; want -resume, as the count is exact", msg)
	}
	mustUp(t, db, fsys, migrate.WithResume())
	if n := nOf(t, db); n != 1 {
		t.Errorf("after -resume n is %d; want 1", n)
	}
}

// TestUpPostgresKilledDuringUnit: a unit of two statements is not run in a
// transaction, and a run killed during it may have run some: the refusal says to
// check first, with pg_stat_activity and the rows, and how to say what ran.
func TestUpPostgresKilledDuringUnit(t *testing.T) {
	t.Parallel()
	db, dsn := freshDB(t, velox.Postgres)
	fsys := dirOf(t, velox.Postgres, files("0001_a.sql", killedBase))
	mustUp(t, db, fsys)
	fsys = extend(t, fsys, velox.Postgres, files("0002_backfill.sql", "-- velox:no-transaction\n-- velox:lock-timeout 1m\n-- velox:unit\nUPDATE a SET n = n + 1;\nCREATE TABLE b (id integer);\n"))
	_ = dirtyOf(t, killDuringUpdate(t, velox.Postgres, db, dsn, fsys))
	if n := nOf(t, db); n != 1 {
		t.Fatalf("n is %d; want the UPDATE finished by the server", n)
	}
	_, err := migrate.Status(context.Background(), db, fsys)
	checkFirst(t, dirtyOf(t, err).Error(), 2, "pg_stat_activity")
}

// TestUpMySQLKilledDuringStatement: MySQL commits each statement, so a run killed
// during an UPDATE leaves it finished and the history counting none of it. The
// refusal said to run -resume first, which ran the UPDATE twice; it says to check
// first, with the process list and the rows, then how to tell the history.
func TestUpMySQLKilledDuringStatement(t *testing.T) {
	t.Parallel()
	db, dsn := freshDB(t, velox.MySQL)
	fsys := dirOf(t, velox.MySQL, files("0001_a.sql", killedBase))
	mustUp(t, db, fsys)
	fsys = extend(t, fsys, velox.MySQL, files("0002_backfill.sql", "UPDATE a SET n = n + 1;\nCREATE TABLE b (id integer);\n"))
	_ = dirtyOf(t, killDuringUpdate(t, velox.MySQL, db, dsn, fsys))
	if rows := historyOf(t, db); len(rows) != 2 || !rows[1].Dirty || rows[1].Statements != 0 || nOf(t, db) != 1 {
		t.Fatalf("history %+v, n %d; want the UPDATE run and counted as not", rows, nOf(t, db))
	}
	_, err := migrate.Up(context.Background(), db, fsys)
	checkFirst(t, dirtyOf(t, err).Error(), 2, "SHOW FULL PROCESSLIST")
}

// checkFirst fails t unless msg, the refusal of dirty version v, leads its way out
// with checking what ran, naming tool, before -statements and -resume.
func checkFirst(t *testing.T, msg string, v int, tool string) {
	t.Helper()
	lines := strings.Split(msg, "\n")
	if len(lines) < 3 || !strings.Contains(lines[1], "may have run on the server") || !strings.Contains(lines[1], tool) ||
		!strings.Contains(lines[1], "INSERT, UPDATE or DELETE") || strings.Contains(lines[1], "-resume") ||
		!strings.Contains(lines[2], fmt.Sprintf("migrate resolve -version %d -statements J, then migrate apply -resume", v)) {
		t.Errorf("the refusal does not lead with checking what ran:\n%s", msg)
	}
}

// TestUpPostgresOutsideABlock: a statement PostgreSQL refuses in a transaction block
// that atomicUnit takes for one fails with 25001 before it runs, and runs again alone;
// a DO block that commits fails with 2D000, rolled back, and runs again alone.
func TestUpPostgresOutsideABlock(t *testing.T) {
	// Serial: everyTxBlock is the package's, and every other Up would see it.
	defer migrate.SetEveryTxBlock()()
	db, _ := freshDB(t, velox.Postgres)
	fsys := dirOf(t, velox.Postgres, files("0001_a.sql", killedBase))
	mustUp(t, db, fsys)
	fsys = extend(t, fsys, velox.Postgres, files("0002_vacuum.sql", "-- velox:no-transaction\nVACUUM a;\nDO $$ BEGIN UPDATE a SET n = n + 1; COMMIT; END $$;\nUPDATE a SET n = n + 10;\n"))
	mustUp(t, db, fsys)
	if n := nOf(t, db); n != 11 {
		t.Errorf("n is %d; want 11, each statement once", n)
	}
	if rows := historyOf(t, db); len(rows) != 2 || rows[1].Dirty || rows[1].Statements != 3 {
		t.Errorf("history %+v", rows)
	}
}
