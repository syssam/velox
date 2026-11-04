package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

func TestDirtyErrorText(t *testing.T) {
	t.Parallel()
	cause := errors.New("Error 1205: Lock wait timeout exceeded")
	e := &migrate.DirtyError{Version: 4, Path: "0004_add_users_org_id.sql", Statement: 2, Statements: 3, Err: cause}
	want := "migrate: 0004_add_users_org_id.sql: statement 2 of 3: Error 1205: Lock wait timeout exceeded\n" +
		"  version 4 is dirty; fix the cause and run migrate apply -resume, or undo statement 1 and run migrate resolve -version 4 -undone"
	if got := e.Error(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if errors.Unwrap(e) != cause {
		t.Errorf("Unwrap is %v", errors.Unwrap(e))
	}
	for _, c := range []struct {
		statement int
		want      string
	}{
		{1, "migrate: 0004_add_users_org_id.sql: statement 1 of 3: Error 1205: Lock wait timeout exceeded\n" +
			"  version 4 is dirty; fix the cause and run migrate apply -resume, or run migrate resolve -version 4 -undone"},
		{3, "migrate: 0004_add_users_org_id.sql: statement 3 of 3: Error 1205: Lock wait timeout exceeded\n" +
			"  version 4 is dirty; fix the cause and run migrate apply -resume, or undo statements 1 to 2 and run migrate resolve -version 4 -undone"},
		{0, "migrate: 0004_add_users_org_id.sql: version 4 is dirty: Error 1205: Lock wait timeout exceeded\n" +
			"  fix the cause and run migrate apply -resume, or undo its applied statements and run migrate resolve -version 4 -undone"},
	} {
		e := *e
		e.Statement = c.statement
		if got := e.Error(); got != c.want {
			t.Errorf("statement %d: got\n%s\nwant\n%s", c.statement, got, c.want)
		}
	}
	found := &migrate.DirtyError{Version: 4, Path: "0004_add_users_org_id.sql", Statements: 3}
	if got, want := found.Error(), "migrate: 0004_add_users_org_id.sql: version 4 is dirty\n"+
		"  fix the cause and run migrate apply -resume, or undo its applied statements and run migrate resolve -version 4 -undone"; got != want {
		t.Errorf("no error: got\n%s\nwant\n%s", got, want)
	}
}

// TestDirtyErrorUnsureText: a version left with a statement under way leads its way
// out with checking whether the statement took effect, then -statements, then
// -resume; on MySQL it lists the statements.
func TestDirtyErrorUnsureText(t *testing.T) {
	t.Parallel()
	f, err := migrate.ParseFile(velox.MySQL, "0002_backfill.sql", []byte("UPDATE a SET n = n + 1;\nCREATE TABLE b (id int);\n"))
	if err != nil {
		t.Fatal(err)
	}
	e := &migrate.DirtyError{Version: 2, Path: f.Path, Statements: 2}
	migrate.MarkUnsure(e, velox.MySQL, f, 0)
	want := "migrate: 0002_backfill.sql: version 2 is dirty\n" +
		"  version 2 is dirty, and the run stopped with statement 1 under way: it may have run on the server after the history's count of 0, or be running still. " +
		"Wait until SHOW FULL PROCESSLIST no longer shows it, then check whether it took effect: SHOW CREATE TABLE for a schema change, its rows for an INSERT, UPDATE or DELETE\n" +
		"  if the first J statements ran, J above 0, run migrate resolve -version 2 -statements J, then migrate apply -resume; " +
		"if none past 0 did, fix the cause and run migrate apply -resume; or undo the statements that ran and run migrate resolve -version 2 -undone\n" +
		"  the file's statements:\n" +
		"    1. UPDATE a SET n = n + 1\n" +
		"    2. CREATE TABLE b (id int)"
	if got := e.Error(); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	e = &migrate.DirtyError{Version: 2, Path: f.Path, Statement: 2, Statements: 2, Err: errors.New("boom")}
	migrate.MarkUnsure(e, velox.Postgres, f, 1)
	if got := e.Error(); !strings.HasPrefix(got, "migrate: 0002_backfill.sql: statement 2 of 2: boom\n  version 2 is dirty, and the run stopped with statement 2 under way") ||
		!strings.Contains(got, "pg_stat_activity") || !strings.Contains(got, `\d of the table`) || strings.Contains(got, "the file's statements") {
		t.Errorf("PostgreSQL:\n%s", got)
	}
	// A row that counts every statement has none under way.
	e = &migrate.DirtyError{Version: 2, Path: f.Path, Statements: 2}
	migrate.MarkUnsure(e, velox.MySQL, f, 2)
	if got := e.Error(); strings.Contains(got, "under way") {
		t.Errorf("every statement counted:\n%s", got)
	}
}

// TestAtomicUnit: on PostgreSQL a no-transaction file's unit of one statement
// commits with its count, unless PostgreSQL cannot run it in a transaction block,
// it may end one, or the file begins or ends one.
func TestAtomicUnit(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		d    velox.Dialect
		src  string
		want bool
	}{
		{velox.Postgres, "-- velox:no-transaction\nUPDATE a SET n = 1;\n", true},
		{velox.Postgres, "-- velox:no-transaction\nALTER TABLE a VALIDATE CONSTRAINT c;\n", true},
		{velox.Postgres, "-- velox:no-transaction\nALTER TYPE e ADD VALUE 'x';\n", true},
		{velox.Postgres, "-- velox:no-transaction\nCREATE INDEX CONCURRENTLY i ON a (n);\n", false},
		{velox.Postgres, "-- velox:no-transaction\nALTER TABLE p DETACH PARTITION c CONCURRENTLY;\n", false},
		{velox.Postgres, "-- velox:no-transaction\nVACUUM a;\n", false},
		{velox.Postgres, "-- velox:no-transaction\nCALL p();\n", false},
		{velox.Postgres, "-- velox:no-transaction\nDO $$ BEGIN COMMIT; END $$;\n", false},
		{velox.Postgres, "-- velox:no-transaction\nCREATE DATABASE x;\n", false},
		{velox.Postgres, "-- velox:no-transaction\nALTER SYSTEM SET work_mem = '8MB';\n", false},
		{velox.Postgres, "-- velox:no-transaction\nSET search_path = x;\n", false},
		{velox.Postgres, "-- velox:no-transaction\nBEGIN;\nUPDATE a SET n = 1;\nCOMMIT;\n", false},
		{velox.MySQL, "UPDATE a SET n = 1;\n", false},
	} {
		f, err := migrate.ParseFile(c.d, "0001_a.sql", []byte(c.src))
		if err != nil {
			t.Fatal(err)
		}
		at := len(f.Statements) / 2
		if got := migrate.AtomicUnit(c.d, f, at, at+1); got != c.want {
			t.Errorf("%v %q: %v; want %v", c.d, c.src, got, c.want)
		}
	}
	f, err := migrate.ParseFile(velox.Postgres, "0001_a.sql", []byte("-- velox:no-transaction\n-- velox:unit\nUPDATE a SET n = 1;\nUPDATE a SET m = 1;\n"))
	if err != nil {
		t.Fatal(err)
	}
	if migrate.AtomicUnit(velox.Postgres, f, 0, 2) {
		t.Error("a unit of two statements commits with its count")
	}
}

// TestResolveSQLite: Resolve acts on a dirty version only; ResolveApplied
// needs the file unchanged, ResolveUndone does not.
func TestResolveSQLite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := freshDB(t, velox.SQLite)
	fsys := dirOf(t, velox.SQLite, files("0001_a.sql", "CREATE TABLE a (x integer);", "0002_b.sql", "CREATE TABLE b (x integer);\nCREATE TABLE c (x integer);"))
	mustUp(t, db, fsys)
	if err := migrate.Resolve(ctx, db, fsys, 2, migrate.ResolveApplied); err == nil || asError(t, err, "history").Reason != "version 2 is not dirty" {
		t.Errorf("clean: %v", err)
	}
	if err := migrate.Resolve(ctx, db, fsys, 3, migrate.ResolveUndone); err == nil {
		t.Error("no version 3: no error")
	} else {
		_ = asError(t, err, "history")
	}
	if err := migrate.Resolve(ctx, db, fsys, 2, 0); err == nil {
		t.Error("no resolution: no error")
	} else {
		_ = asError(t, err, "resolution")
	}
	dirty := func() {
		t.Helper()
		if _, err := db.Exec(`UPDATE velox_migrations SET dirty = true, statements = 1, error = 'boom' WHERE version = 2`); err != nil {
			t.Fatal(err)
		}
	}
	dirty()
	if err := migrate.Resolve(ctx, db, fsys, 2, migrate.ResolveApplied); err != nil {
		t.Fatal(err)
	}
	if rows := historyOf(t, db); len(rows) != 2 || rows[1].Dirty || rows[1].Statements != 2 || rows[1].Error != "" {
		t.Errorf("applied: %+v", rows)
	}
	dirty()
	edited := cloneFS(fsys)
	edited["0002_b.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE b (y integer);")}
	resum(t, edited)
	var ce *migrate.ChecksumError
	if err := migrate.Resolve(ctx, db, edited, 2, migrate.ResolveApplied); !errors.As(err, &ce) || ce.Version != 2 {
		t.Errorf("edited: %#v; want *ChecksumError", err)
	}
	// An edit not yet hashed: ResolveApplied reads the directory and is
	// refused; ResolveUndone reads velox.json alone.
	stale := cloneFS(fsys)
	stale["0002_b.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE b (y integer);")}
	var se *migrate.SumError
	if err := migrate.Resolve(ctx, db, stale, 2, migrate.ResolveApplied); !errors.As(err, &se) {
		t.Errorf("stale, applied: %#v; want *SumError", err)
	}
	if err := migrate.Resolve(ctx, db, stale, 2, migrate.ResolveUndone); err != nil {
		t.Fatal(err)
	}
	if rows := historyOf(t, db); len(rows) != 1 {
		t.Errorf("undone: %+v", rows)
	}
	if err := migrate.Resolve(ctx, db, fstest.MapFS{}, 2, migrate.ResolveUndone); err == nil {
		t.Error("no velox.json: no error")
	} else {
		_ = asError(t, err, "config")
	}
}

// TestUpResumeRefused: WithResume needs the dirty version's file, unchanged,
// and one run statement by statement.
func TestUpResumeRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := freshDB(t, velox.SQLite)
	a, b := "CREATE TABLE a (x integer);", "CREATE TABLE b (x integer);"
	fsys := dirOf(t, velox.SQLite, files("0001_a.sql", a, "0002_b.sql", b))
	mustUp(t, db, fsys)
	if _, err := db.Exec(`UPDATE velox_migrations SET dirty = true WHERE version = 2`); err != nil {
		t.Fatal(err)
	}
	_, err := migrate.Up(ctx, db, fsys, migrate.WithResume())
	if e := asError(t, err, "history"); !strings.Contains(e.Reason, "runs in a transaction") {
		t.Errorf("in a transaction: %v", err)
	}
	edited := cloneFS(fsys)
	edited["0002_b.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE b (y integer);")}
	resum(t, edited)
	var ce *migrate.ChecksumError
	if _, err := migrate.Up(ctx, db, edited, migrate.WithResume()); !errors.As(err, &ce) || ce.Version != 2 || ce.Now == "" {
		t.Errorf("edited: %#v; want *ChecksumError", err)
	}
	old := dirOf(t, velox.SQLite, files("0001_a.sql", a))
	_, err = migrate.Up(ctx, db, old, migrate.WithResume())
	if !errors.As(err, &ce) || ce.Version != 2 || ce.Now != "" {
		t.Errorf("above the last file: %#v; want *ChecksumError", err)
	}
	if want := "migrate: version 2 is dirty, and the directory has no file for it; resume it with the directory that has its file, or undo its applied statements and run migrate resolve -version 2 -undone"; err == nil || err.Error() != want {
		t.Errorf("above the last file, resumed:\n%v\nwant\n%s", err, want)
	}
	_, err = migrate.Up(ctx, db, old)
	if de := dirtyOf(t, err); de.Version != 2 {
		t.Errorf("above the last file: %+v", de)
	}
	if want := "migrate: version 2 is dirty\n  the directory has no file for it; resume it with the directory that has its file, or undo its applied statements and run migrate resolve -version 2 -undone"; err.Error() != want {
		t.Errorf("above the last file:\n%v\nwant\n%s", err, want)
	}
}

// TestUpResumeAhead: a dirty version with clean versions above the
// directory's last file is refused with WithResume too.
func TestUpResumeAhead(t *testing.T) {
	t.Parallel()
	db, _ := freshDB(t, velox.SQLite)
	a := "CREATE TABLE a (x integer);"
	newer := dirOf(t, velox.SQLite, files("0001_a.sql", a, "0002_b.sql", "CREATE TABLE b (x integer);", "0003_c.sql", "CREATE TABLE c (x integer);"))
	mustUp(t, db, newer)
	if _, err := db.Exec(`UPDATE velox_migrations SET dirty = true WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	r, err := migrate.Up(context.Background(), db, dirOf(t, velox.SQLite, files("0001_a.sql", a, "0002_b.sql", "CREATE TABLE b (x integer);")), migrate.WithResume())
	if e := asError(t, err, "history"); r != nil || e.Error() != "migrate: version 1 is dirty, and the database has versions above the directory's last file, up to 3; "+
		"restore the missing files to the directory and resume with it, or undo its applied statements and run migrate resolve -version 1 -undone" {
		t.Errorf("%v, %+v", err, r)
	}

	// Dirty above the directory's last file too: clean 2, dirty 3, the
	// directory at 1.
	db, _ = freshDB(t, velox.SQLite)
	mustUp(t, db, newer)
	if _, err := db.Exec(`UPDATE velox_migrations SET dirty = true WHERE version = 3`); err != nil {
		t.Fatal(err)
	}
	_, err = migrate.Up(context.Background(), db, dirOf(t, velox.SQLite, files("0001_a.sql", a)), migrate.WithResume())
	if e := asError(t, err, "history"); !strings.Contains(e.Reason, "version 3 is dirty, and the database has versions above the directory's last file, up to 3") {
		t.Errorf("%v", err)
	}
}

func TestLostMessage(t *testing.T) {
	t.Parallel()
	if got, want := migrate.LostMessage(1, 3), "the connection was lost during statements 2 to 3; check whether they ran before resuming"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if got, want := migrate.LostMessage(1, 2), "the connection was lost during statement 2; check whether it ran before resuming"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// TestResolveAppliedPendingRefusedWhileDirty: marking a pending file applied while
// another version is dirty is refused. An operator who meant the dirty version and
// typed the next one would skip the whole file and see every command succeed.
func TestResolveAppliedPendingRefusedWhileDirty(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := freshDB(t, velox.SQLite)
	one := dirOf(t, velox.SQLite, files("0001_a.sql", "CREATE TABLE a (x integer);"))
	mustUp(t, db, one)
	if _, err := db.Exec(`UPDATE velox_migrations SET dirty = true, statements = 0, error = 'boom' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	two := dirOf(t, velox.SQLite, files("0001_a.sql", "CREATE TABLE a (x integer);", "0002_b.sql", "CREATE TABLE b (x integer);"))
	err := migrate.Resolve(ctx, db, two, 2, migrate.ResolveApplied)
	if err == nil || !strings.Contains(asError(t, err, "history").Reason, "version 1 is dirty") {
		t.Errorf("pending 2 while 1 is dirty: %v", err)
	}
	if rows := historyOf(t, db); len(rows) != 1 {
		t.Errorf("recorded: %+v", rows)
	}
}
