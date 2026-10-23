package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	sqlitedriver "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

// historyRow is a row of the history table, as stored.
type historyRow struct {
	Version    int64
	Name, Sum  string
	AppliedAt  string
	Duration   int64
	Dirty      bool
	Statements int
	Error      string
}

// historyOf is the rows of velox_migrations, by version.
func historyOf(t *testing.T, db *sql.DB) []historyRow {
	t.Helper()
	rows, err := db.Query(`SELECT version, name, checksum, applied_at, duration_ms, dirty, statements, error FROM velox_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []historyRow
	for rows.Next() {
		var r historyRow
		if err := rows.Scan(&r.Version, &r.Name, &r.Sum, &r.AppliedAt, &r.Duration, &r.Dirty, &r.Statements, &r.Error); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// hasTable reports whether the SQLite database has the table.
func hasTable(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func mustUp(t *testing.T, db *sql.DB, fsys fstest.MapFS, opts ...migrate.Option) *migrate.Report {
	t.Helper()
	r, err := migrate.Up(context.Background(), db, fsys, opts...)
	if err != nil {
		t.Fatalf("Up: %v", err)
	}
	return r
}

func mustStatus(t *testing.T, db *sql.DB, fsys fstest.MapFS) *migrate.State {
	t.Helper()
	s, err := migrate.Status(context.Background(), db, fsys)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	return s
}

func versions[T migrate.Applied | migrate.File](xs []T) []uint64 {
	var out []uint64
	for _, x := range xs {
		switch x := any(x).(type) {
		case migrate.Applied:
			out = append(out, x.Version)
		case migrate.File:
			out = append(out, x.Version)
		}
	}
	return out
}

// files is a step of hand files.
func files(kv ...string) step {
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return step{files: m}
}

// TestUpAppliesInOrder: two files apply as versions 1 and 2, the history
// records them, and a second Up applies nothing.
func TestUpAppliesInOrder(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	fsys := dirOf(t, velox.SQLite,
		step{to: mustTables(velox.SQLite, nil), name: "base"},
		files("0002_log.sql", "CREATE TABLE log (x integer);\nINSERT INTO log VALUES (1);\n"))
	dir, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Truncate(time.Microsecond)
	r := mustUp(t, db, fsys)
	end := time.Now().UTC()
	if len(r.Applied) != 2 || r.Ahead || len(r.Baselined) != 0 {
		t.Fatalf("report %+v", r)
	}
	rows := historyOf(t, db)
	if len(rows) != 2 {
		t.Fatalf("history %+v", rows)
	}
	for i, f := range dir.Files {
		a, row := r.Applied[i], rows[i]
		if a.Version != f.Version || a.Name != f.Name || a.Sum != f.Sum || a.Dirty || a.Statements != len(f.Statements) || a.AppliedAt.Before(start) || a.AppliedAt.After(end) {
			t.Errorf("applied %+v; file %s", a, f.Path)
		}
		at, err := time.Parse("2006-01-02 15:04:05.000000", row.AppliedAt)
		if row.Version != int64(f.Version) || row.Name != f.Name || row.Sum != f.Sum || row.Dirty || row.Statements != len(f.Statements) || row.Error != "" ||
			err != nil || at.Before(start) || at.After(end) || !at.Equal(a.AppliedAt) {
			t.Errorf("history row %+v (%v); file %s", row, err, f.Path)
		}
	}
	if r := mustUp(t, db, fsys); len(r.Applied) != 0 {
		t.Errorf("a second Up applied %+v", r.Applied)
	}
}

func TestUpWithTarget(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	fsys := dirOf(t, velox.SQLite, files(
		"0001_a.sql", "CREATE TABLE a (x integer);",
		"0002_b.sql", "CREATE TABLE b (x integer);",
		"0003_c.sql", "CREATE TABLE c (x integer);"))
	if r := mustUp(t, db, fsys, migrate.WithTarget(2)); !slices.Equal(versions(r.Applied), []uint64{1, 2}) {
		t.Errorf("applied %v; want 1, 2", versions(r.Applied))
	}
	if s := mustStatus(t, db, fsys); !slices.Equal(versions(s.Pending), []uint64{3}) {
		t.Errorf("pending %v; want 3", versions(s.Pending))
	}
}

// TestUpRollsBack: a failing statement rolls its file back, and the error
// names the file and the statement and wraps the driver's.
func TestUpRollsBack(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	fsys := dirOf(t, velox.SQLite, files(
		"0001_ok.sql", "CREATE TABLE ok (x integer);",
		"0002_bad.sql", "CREATE TABLE a (x integer);\nINSERT INTO missing VALUES (1);\n"))
	_, err := migrate.Up(context.Background(), db, fsys)
	if err == nil {
		t.Fatal("Up succeeded")
	}
	if msg := err.Error(); !strings.HasPrefix(msg, "migrate: 0002_bad.sql: statement 2 of 2: ") || !strings.HasSuffix(msg, "; the file was rolled back") {
		t.Errorf("error %q", msg)
	}
	var se *sqlitedriver.Error
	if !errors.As(err, &se) {
		t.Errorf("error %#v does not wrap the driver's", err)
	}
	if hasTable(t, db, "a") {
		t.Error("table a was kept")
	}
	if rows := historyOf(t, db); len(rows) != 1 || rows[0].Version != 1 {
		t.Errorf("history %+v; want version 1 alone", rows)
	}
}

// TestUpChecksum: an applied file edited, or gone, is a *ChecksumError.
func TestUpChecksum(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	fsys := dirOf(t, velox.SQLite, files(
		"0001_a.sql", "CREATE TABLE a (x integer);",
		"0002_b.sql", "CREATE TABLE b (x integer);"))
	mustUp(t, db, fsys)
	dir, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	old := dir.Files[0].Sum

	edited := cloneFS(fsys)
	edited["0001_a.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE a (x integer, y integer);")}
	resum(t, edited)
	nd, err := migrate.ReadDir(edited)
	if err != nil {
		t.Fatal(err)
	}
	_, err = migrate.Up(context.Background(), db, edited)
	var ce *migrate.ChecksumError
	if !errors.As(err, &ce) || *ce != (migrate.ChecksumError{Version: 1, Path: "0001_a.sql", Applied: old, Now: nd.Files[0].Sum}) {
		t.Fatalf("edited: %#v", err)
	}
	if want := "migrate: 0001_a.sql was applied with checksum " + old + " and is now " + nd.Files[0].Sum; err.Error() != want {
		t.Errorf("edited: %q\nwant %q", err, want)
	}

	gone := cloneFS(fsys)
	delete(gone, "0001_a.sql")
	resum(t, gone)
	_, err = migrate.Up(context.Background(), db, gone)
	if !errors.As(err, &ce) || ce.Version != 1 || ce.Now != "" || ce.Applied != old {
		t.Fatalf("gone: %#v", err)
	}
	if want := "migrate: version 1 is applied but the directory has no file for it"; err.Error() != want {
		t.Errorf("gone: %q\nwant %q", err, want)
	}
}

// TestUpAhead: a database a newer directory migrated is not an error; Up
// applies nothing and says so.
func TestUpAhead(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	a := "CREATE TABLE a (x integer);"
	mustUp(t, db, dirOf(t, velox.SQLite, files("0001_a.sql", a, "0002_b.sql", "CREATE TABLE b (x integer);")))
	old := dirOf(t, velox.SQLite, files("0001_a.sql", a))
	r := mustUp(t, db, old)
	if !r.Ahead || len(r.Applied) != 0 {
		t.Errorf("report %+v; want Ahead and nothing applied", r)
	}
	s := mustStatus(t, db, old)
	if !slices.Equal(versions(s.Ahead), []uint64{2}) || len(s.Pending) != 0 || !slices.Equal(versions(s.Applied), []uint64{1}) || s.Dirty != nil {
		t.Errorf("state %+v", s)
	}
}

func TestUpPendingBelow(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	a, b, c := "CREATE TABLE a (x integer);", "CREATE TABLE b (x integer);", "CREATE TABLE c (x integer);"
	mustUp(t, db, dirOf(t, velox.SQLite, files("0001_a.sql", a, "0003_c.sql", c)))
	_, err := migrate.Up(context.Background(), db, dirOf(t, velox.SQLite, files("0001_a.sql", a, "0002_b.sql", b, "0003_c.sql", c)))
	var e *migrate.Error
	if !errors.As(err, &e) || e.Code != "history" {
		t.Fatalf("%#v; want Code history", err)
	}
	if want := "migrate: 0002_b.sql is pending below version 3, the highest applied; the history is linear"; err.Error() != want {
		t.Errorf("%q\nwant %q", err, want)
	}
}

func TestUpStaleSum(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	fsys := dirOf(t, velox.SQLite, files("0001_a.sql", "CREATE TABLE a (x integer);"))
	fsys["0001_a.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE b (x integer);")}
	var se *migrate.SumError
	if _, err := migrate.Up(context.Background(), db, fsys); !errors.As(err, &se) {
		t.Fatalf("%#v; want *SumError", err)
	}
	if hasTable(t, db, "velox_migrations") {
		t.Error("the history table was made")
	}
}

func TestUpNoTransactionOnSQLite(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	fsys := dirOf(t, velox.SQLite, files("0001_a.sql", "-- velox:no-transaction\nCREATE TABLE a (x integer);\n"))
	var e *migrate.Error
	if _, err := migrate.Up(context.Background(), db, fsys); !errors.As(err, &e) || e.Code != "file" || !strings.Contains(e.Reason, "no-transaction") {
		t.Fatalf("%#v; want ParseFile's refusal", err)
	}
}

func TestUpDialectMismatch(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	fsys := dirOf(t, velox.Postgres, files("0001_a.sql", "CREATE TABLE a (x integer);"))
	var e *migrate.Error
	if _, err := migrate.Up(context.Background(), db, fsys); !errors.As(err, &e) || e.Code != "session" {
		t.Fatalf("%#v; want Code session", err)
	}
	if _, err := migrate.Status(context.Background(), db, fsys); !errors.As(err, &e) || e.Code != "session" {
		t.Fatalf("Status: %#v; want Code session", err)
	}
}

// TestUpSQLiteRebuild: a rebuilt parent keeps its children and its
// AUTOINCREMENT counter through the runner, which sets foreign keys back on
// after; a foreign-keys off file that leaves an orphan is rolled back.
func TestUpSQLiteRebuild(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	db.SetMaxOpenConns(1) // the connection Up used is the one read after
	c := caseNamed("rebuild-parent")
	from, to := models(t, c, velox.SQLite)
	fsys := dirOf(t, velox.SQLite, step{to: from, name: "base"})
	mustUp(t, db, fsys)
	for _, q := range []string{
		`INSERT INTO orgs (name) VALUES ('o')`,
		`INSERT INTO users (org_id, name, email, role, status, logins, created_at) VALUES (1, 'a', 'a@x', 'viewer', 'active', 3, '2026-01-01')`,
		`INSERT INTO users (org_id, name, email, role, status, logins, created_at) VALUES (1, 'b', 'b@x', 'viewer', 'active', 4, '2026-01-01')`,
		`DELETE FROM users WHERE id = 2`,
		`INSERT INTO posts (author_id, title, status) VALUES (1, 't', 'active')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	fsys = extend(t, fsys, velox.SQLite, step{to: to, name: "rebuild", opts: migrate.PlanOptions{Diff: c.opts, Allow: c.allow}})
	dir, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if !dir.Files[1].Directives.ForeignKeysOff {
		t.Fatalf("%s does not turn foreign keys off", dir.Files[1].Path)
	}
	mustUp(t, db, fsys)
	var posts, logins, fk int
	if err := db.QueryRow(`SELECT count(*) FROM posts`).Scan(&posts); err != nil || posts != 1 {
		t.Errorf("posts %d, %v; want 1", posts, err)
	}
	if err := db.QueryRow(`SELECT logins FROM users WHERE id = 1`).Scan(&logins); err != nil || logins != 3 {
		t.Errorf("logins %d, %v; want 3", logins, err)
	}
	if _, err := db.Exec(`INSERT INTO users (org_id, name, email, created_at) VALUES (1, 'c', 'c@x', '2026-01-01')`); err != nil {
		t.Fatal(err)
	}
	var id int
	if err := db.QueryRow(`SELECT max(id) FROM users`).Scan(&id); err != nil || id != 3 {
		t.Errorf("the next ID is %d, %v; want 3, past the deleted 2", id, err)
	}
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys %d, %v; want 1", fk, err)
	}

	fsys = extend(t, fsys, velox.SQLite, files("0003_orphan.sql", "-- velox:foreign-keys off\nINSERT INTO posts (author_id, title, status) VALUES (999, 'o', 'active');\n"))
	_, err = migrate.Up(context.Background(), db, fsys)
	if err == nil || !strings.Contains(err.Error(), "foreign_key_check") || !strings.HasSuffix(err.Error(), "; the file was rolled back") {
		t.Fatalf("orphan: %v", err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM posts`).Scan(&posts); err != nil || posts != 1 {
		t.Errorf("posts %d, %v; want 1 after the rollback", posts, err)
	}
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign_keys %d, %v; want 1 after the rollback", fk, err)
	}
	if rows := historyOf(t, db); len(rows) != 2 {
		t.Errorf("history %+v", rows)
	}
}

// concurrentFiles is a directory whose files fail if run twice or out of
// order.
var concurrentFiles = []string{
	"0001_log.sql", "CREATE TABLE log (x integer PRIMARY KEY);",
	"0002_two.sql", "INSERT INTO log VALUES (2);",
	"0003_three.sql", "INSERT INTO log VALUES (3);",
}

// upTwice runs Up on a and b at once, and fails t on either's error.
func upTwice(t *testing.T, fsys fstest.MapFS, a, b *sql.DB) {
	t.Helper()
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, db := range []*sql.DB{a, b} {
		wg.Go(func() { _, errs[i] = migrate.Up(context.Background(), db, fsys) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
}

func TestUpSQLiteConcurrent(t *testing.T) {
	db, dsn := freshDB(t, velox.SQLite)
	fsys := dirOf(t, velox.SQLite, files(concurrentFiles...))
	upTwice(t, fsys, db, openDB(t, "sqlite", dsn))
	if rows := historyOf(t, db); len(rows) != 3 {
		t.Errorf("history %+v", rows)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM log`).Scan(&n); err != nil || n != 2 {
		t.Errorf("log rows %d, %v; want 2", n, err)
	}
}

// holdSQLite is a connection to dsn's database holding BEGIN IMMEDIATE,
// rolled back when t ends.
func holdSQLite(t *testing.T, dsn string) *sql.Conn {
	t.Helper()
	ctx := context.Background()
	holder, err := openDB(t, "sqlite", dsn).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Close() })
	if _, err := holder.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = holder.ExecContext(ctx, "ROLLBACK") })
	return holder
}

// TestUpSQLiteNoWait: a lock wait of 0 tries once.
func TestUpSQLiteNoWait(t *testing.T) {
	db, dsn := freshDB(t, velox.SQLite)
	mustUp(t, db, dirOf(t, velox.SQLite), migrate.WithLockWait(0))
	holdSQLite(t, dsn)
	start := time.Now()
	if _, err := migrate.Up(context.Background(), db, dirOf(t, velox.SQLite), migrate.WithLockWait(0)); !errors.Is(err, migrate.ErrLockWait) {
		t.Fatalf("%#v; want ErrLockWait", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Up waited %s; want no wait", d)
	}
}

// TestUpSQLiteHugeWait: a wait past what the database takes is clamped.
func TestUpSQLiteHugeWait(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	mustUp(t, db, dirOf(t, velox.SQLite), migrate.WithLockWait(math.MaxInt64))
}

func TestLockWaitUnits(t *testing.T) {
	for _, c := range []struct {
		wait        time.Duration
		ms, seconds int64
	}{
		{0, 0, 0},
		{-time.Second, 0, 0},
		{time.Nanosecond, 1, 1},
		{1500 * time.Millisecond, 1500, 2},
		{5 * time.Minute, 300000, 300},
		{30 * 24 * time.Hour, math.MaxInt32, 2592000},
		{math.MaxInt64, math.MaxInt32, math.MaxInt32},
	} {
		if ms, sec := migrate.WaitMillis(c.wait), migrate.WaitSeconds(c.wait); ms != c.ms || sec != c.seconds {
			t.Errorf("%v: %d ms, %d s; want %d, %d", c.wait, ms, sec, c.ms, c.seconds)
		}
	}
}

// TestUpSQLiteLockWaitCancel: a cancelled context ends the wait at once,
// with the context's error.
func TestUpSQLiteLockWaitCancel(t *testing.T) {
	db, dsn := freshDB(t, velox.SQLite)
	holdSQLite(t, dsn)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(200*time.Millisecond, cancel)
	start := time.Now()
	_, err := migrate.Up(ctx, db, dirOf(t, velox.SQLite), migrate.WithLockWait(time.Minute))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%#v; want context.Canceled", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("Up took %s after the cancel", d)
	}
}

// TestRunnerErrorsSayWhat: a driver's error is wrapped with what failed.
func TestRunnerErrorsSayWhat(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	// An index takes the history table's name.
	if _, err := db.Exec(`CREATE TABLE x (a integer); CREATE INDEX velox_migrations ON x (a)`); err != nil {
		t.Fatal(err)
	}
	_, err := migrate.Up(context.Background(), db, dirOf(t, velox.SQLite))
	var se *sqlitedriver.Error
	if err == nil || !strings.HasPrefix(err.Error(), "migrate: creating the history table velox_migrations: ") || !errors.As(err, &se) {
		t.Errorf("Up: %#v", err)
	}

	db, _ = freshDB(t, velox.SQLite)
	if _, err := db.Exec(`CREATE TABLE velox_migrations (version integer)`); err != nil {
		t.Fatal(err)
	}
	_, err = migrate.Status(context.Background(), db, dirOf(t, velox.SQLite))
	if err == nil || !strings.HasPrefix(err.Error(), "migrate: reading the history table velox_migrations: ") || !errors.As(err, &se) {
		t.Errorf("Status: %#v", err)
	}
}

// TestSessionDiscard: the runner's connection is closed after its run, on
// success too, never returned to the pool.
func TestSessionDiscard(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	if err := migrate.SessionClose(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if n := db.Stats().Idle; n != 0 {
		t.Errorf("SessionClose: %d idle connections; want 0", n)
	}
	fsys := dirOf(t, velox.SQLite, files("0001_a.sql", "CREATE TABLE a (x integer);"))
	mustUp(t, db, fsys)
	if _, err := migrate.Status(context.Background(), db, fsys); err != nil {
		t.Fatal(err)
	}
	if n := db.Stats().Idle; n != 0 {
		t.Errorf("Up and Status: %d idle connections; want 0", n)
	}
}

// TestUpSQLiteLockWait: a writer holding the database makes Up wait, then
// give ErrLockWait, before it creates the history table.
func TestUpSQLiteLockWait(t *testing.T) {
	ctx := context.Background()
	db, dsn := freshDB(t, velox.SQLite)
	holder, err := openDB(t, "sqlite", dsn).Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Close() }()
	if _, err := holder.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = migrate.Up(ctx, db, dirOf(t, velox.SQLite), migrate.WithLockWait(200*time.Millisecond))
	if !errors.Is(err, migrate.ErrLockWait) {
		t.Fatalf("%#v; want ErrLockWait", err)
	}
	if d := time.Since(start); d < 200*time.Millisecond {
		t.Errorf("Up waited %s; want 200ms", d)
	}
	if _, err := holder.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if hasTable(t, db, "velox_migrations") {
		t.Error("the history table was made")
	}
}

// TestStatusDirty: a dirty version is the state and a *DirtyError together,
// whatever its file's checksum, and also above the directory's last file.
func TestStatusDirty(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	a, b := "CREATE TABLE a (x integer);", "CREATE TABLE b (x integer);"
	fsys := dirOf(t, velox.SQLite, files("0001_a.sql", a, "0002_b.sql", b))
	mustUp(t, db, fsys)
	if _, err := db.Exec(`UPDATE velox_migrations SET dirty = true, error = 'boom' WHERE version = 2`); err != nil {
		t.Fatal(err)
	}
	edited := cloneFS(fsys)
	edited["0002_b.sql"] = &fstest.MapFile{Data: []byte("CREATE TABLE b (y integer);")}
	resum(t, edited)
	cleanEdited := cloneFS(fsys) // a clean version's file edited
	cleanEdited["0001_a.sql"].Data = []byte("CREATE TABLE a (y integer);")
	resum(t, cleanEdited)
	for name, dir := range map[string]fstest.MapFS{
		"same":         fsys,
		"edited":       edited,
		"clean edited": cleanEdited,
		"ahead":        dirOf(t, velox.SQLite, files("0001_a.sql", a)),
	} {
		s, err := migrate.Status(context.Background(), db, dir)
		var de *migrate.DirtyError
		if !errors.As(err, &de) || de.Version != 2 || de.Statement != 0 || de.Err == nil || de.Err.Error() != "boom" {
			t.Errorf("%s: %#v; want *DirtyError of version 2", name, err)
			continue
		}
		if s == nil || s.Dirty == nil || s.Dirty.Version != 2 || !s.Dirty.Dirty || s.Dirty.Error != "boom" || len(s.Ahead) != 0 ||
			!slices.Equal(versions(s.Applied), []uint64{1}) {
			t.Errorf("%s: state %+v", name, s)
		}
		if _, err := migrate.Up(context.Background(), db, dir); !errors.As(err, &de) {
			t.Errorf("%s: Up: %#v; want *DirtyError", name, err)
		}
	}
}

// TestStatus: Status neither locks nor creates the history table.
func TestStatus(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	fsys := dirOf(t, velox.SQLite, files("0001_a.sql", "CREATE TABLE a (x integer);", "0002_b.sql", "CREATE TABLE b (x integer);"))
	s := mustStatus(t, db, fsys)
	if !slices.Equal(versions(s.Pending), []uint64{1, 2}) || len(s.Applied) != 0 || len(s.Ahead) != 0 || s.Dirty != nil {
		t.Errorf("before: %+v", s)
	}
	if hasTable(t, db, "velox_migrations") {
		t.Error("Status made the history table")
	}
	mustUp(t, db, fsys)
	s = mustStatus(t, db, fsys)
	if !slices.Equal(versions(s.Applied), []uint64{1, 2}) || len(s.Pending) != 0 {
		t.Errorf("after: %+v", s)
	}
}

func TestHistoryTableSQLite(t *testing.T) {
	db, _ := freshDB(t, velox.SQLite)
	mustUp(t, db, dirOf(t, velox.SQLite))
	rows, err := db.Query(`SELECT name, type, "notnull", pk FROM pragma_table_info('velox_migrations') ORDER BY cid`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var name, typ string
		var notNull, pk int
		if err := rows.Scan(&name, &typ, &notNull, &pk); err != nil {
			t.Fatal(err)
		}
		typ = strings.ToLower(typ) // SQLite reports its own type names upper-cased
		if notNull != 1 {
			typ += " NULL"
		}
		if pk == 1 {
			typ += " PRIMARY KEY"
		}
		got = append(got, name+" "+typ)
	}
	want := []string{
		"version integer PRIMARY KEY",
		"name text",
		"checksum text",
		"applied_at text",
		"duration_ms bigint",
		"dirty boolean",
		"statements integer",
		"error text",
	}
	if !slices.Equal(got, want) {
		t.Errorf("columns\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
