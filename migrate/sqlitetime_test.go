package migrate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// timed is SQLite's table t with a time column of type typ.
func timed(typ string) *migrate.Schema {
	return &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{{
		Name:       "t",
		Columns:    []migrate.Column{{Name: "id", Type: "integer"}, {Name: "at", Type: typ}},
		PrimaryKey: []string{"id"},
	}}}
}

// TestSQLiteDatetimeBecomesDatetime9: a time column velox declared datetime,
// as it did before datetime(9), is a type change that the plan makes by
// rebuilding the table. It keeps every value, the column's NUMERIC affinity
// being the same, so the only finding is the rebuild's, and the file says
// the old type it drops. The row's text is copied as it is, and inspecting
// the database after gives datetime(9).
func TestSQLiteDatetimeBecomesDatetime9(t *testing.T) {
	t.Parallel()
	d := velox.SQLite
	changes, err := migrate.Diff(timed("datetime"), timed("datetime(9)"), migrate.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 {
		t.Fatalf("changes: %v", changes)
	}
	for _, f := range migrate.Lint(timed("datetime"), changes) {
		if f.Code != "sqlite-rebuild" {
			t.Errorf("a finding other than the rebuild's: %v", f)
		}
	}

	base := dirOf(t, d, step{to: timed("datetime"), name: "base"})
	fsys := extend(t, base, d, step{to: timed("datetime(9)"), name: "datetime9"})
	if s := string(fsys["0002_datetime9.sql"].Data); !strings.Contains(s, "-- velox:drops t.at:type=datetime\n") || !strings.Contains(s, `"at" datetime(9) NOT NULL`) {
		t.Fatalf("the file:\n%s", s)
	}
	db, _ := freshDB(t, d)
	mustUp(t, db, base)
	const at = "2026-11-01 12:00:00.000000005+00:00"
	mustExec(t, db, "INSERT INTO t (id, at) VALUES (1, '"+at+"')")
	mustUp(t, db, fsys)
	var got, typ string
	if err := db.QueryRow("SELECT at, typeof(at) FROM t").Scan(&got, &typ); err != nil || got != at || typ != "text" {
		t.Errorf("the row after the rebuild: %q %s %v; want %q text", got, typ, err, at)
	}
	s, err := migrate.Inspect(context.Background(), db, d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c := s.Tables[0].Columns[1]; c.Type != "datetime(9)" {
		t.Errorf("inspected type %q, want datetime(9)", c.Type)
	}
}
