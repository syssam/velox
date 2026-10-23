package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/migrate"
)

func TestDirtyErrorText(t *testing.T) {
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

// TestResolveSQLite: Resolve acts on a dirty version only; ResolveApplied
// needs the file unchanged, ResolveUndone does not.
func TestResolveSQLite(t *testing.T) {
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
	if err := migrate.Resolve(ctx, db, edited, 2, migrate.ResolveUndone); err != nil {
		t.Fatal(err)
	}
	if rows := historyOf(t, db); len(rows) != 1 {
		t.Errorf("undone: %+v", rows)
	}
}

// TestUpResumeRefused: WithResume needs the dirty version's file, unchanged,
// and one run statement by statement.
func TestUpResumeRefused(t *testing.T) {
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
	if _, err := migrate.Up(ctx, db, dirOf(t, velox.SQLite, files("0001_a.sql", a)), migrate.WithResume()); !errors.As(err, &ce) || ce.Version != 2 || ce.Now != "" {
		t.Errorf("above the last file: %#v; want *ChecksumError", err)
	}
}
