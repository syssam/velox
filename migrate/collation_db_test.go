package migrate_test

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// mapFS is the files as a directory.
func mapFS(files map[string][]byte) fstest.MapFS {
	out := fstest.MapFS{}
	for p, data := range files {
		out[p] = &fstest.MapFile{Data: data}
	}
	return out
}

// mustDir reads fsys.
func mustDir(t *testing.T, fsys fstest.MapFS) *migrate.Dir {
	t.Helper()
	d, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestMySQLUnstatedCollationStays: a baselined table at the server's default collation
// had every diff refused until CONVERT TO utf8mb4_bin, which makes lookups
// case-sensitive. A table whose model states no collation keeps what the database
// holds; a new table keeps the planner's default; a stated one is still a plan.
func TestMySQLUnstatedCollationStays(t *testing.T) {
	d := velox.MySQL
	ctx := context.Background()
	db, _ := freshDB(t, d)
	mustExec(t, db, "CREATE TABLE t (id integer NOT NULL PRIMARY KEY, name varchar(20) NULL) DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci")
	mustExec(t, db, "INSERT INTO t (id, name) VALUES (1, 'Ann')")
	unstated := func(cols ...migrate.Column) *migrate.Schema {
		s := oneTable(d, cols...)
		s.Tables[0].Collation = ""
		return s
	}
	name := migrate.Column{Name: "name", Type: "varchar(20)", Nullable: true}
	model := unstated(name)
	snap, _, err := migrate.Baseline(ctx, db, model)
	if err != nil {
		t.Fatal(err)
	}
	if got := snap.Tables[0].Collation; got != "utf8mb4_0900_ai_ci" {
		t.Errorf("the baseline holds %q", got)
	}
	changes, err := migrate.Diff(snap, model, migrate.DiffOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) > 0 {
		t.Errorf("a diff after the baseline: %v", changes)
	}

	// A later plan adds a column and a table, and writes no CONVERT.
	dir, err := migrate.NewDir(migrate.Config{Dialect: d})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := migrate.NewPlan(&migrate.Schema{Dialect: d}, snap, migrate.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	written, err := dir.Add("baseline", plan, snap)
	if err != nil {
		t.Fatal(err)
	}
	fsys := mapFS(written)
	mustUp(t, db, fsys, migrate.WithBaseline(1))
	next := unstated(name, migrate.Column{Name: "more", Type: "integer", Nullable: true})
	next.Tables = append(next.Tables, migrate.Table{Name: "u", Columns: []migrate.Column{{Name: "id", Type: "integer"}, {Name: "s", Type: "varchar(5)", Nullable: true}}, PrimaryKey: []string{"id"}, PrimaryKeyName: "PRIMARY"})
	fsys = extend(t, fsys, d, step{to: next, name: "more"})
	text := string(fsys["0002_more.sql"].Data)
	if strings.Contains(text, "CONVERT") {
		t.Errorf("the file:\n%s", text)
	}
	mustUp(t, db, fsys)
	r, err := migrate.Drift(ctx, db, next)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) > 0 {
		t.Errorf("drift against the model:\n%s", reportText(r))
	}
	r, err = migrate.Drift(ctx, db, mustDir(t, fsys).Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) > 0 {
		t.Errorf("drift against the snapshot:\n%s", reportText(r))
	}
	if got := oneValue(t, db, "SELECT id FROM t WHERE name = 'ANN'"); got != "1" {
		t.Errorf("the lookup after the plans: %q", got)
	}
	if got := oneValue(t, db, "SELECT TABLE_COLLATION FROM information_schema.TABLES WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = 'u'"); got != "utf8mb4_bin" {
		t.Errorf("a new table's collation: %q", got)
	}
	if got := mustDir(t, fsys).Snapshot.Tables[1].Collation; got != "utf8mb4_bin" {
		t.Errorf("the snapshot's new table: %q", got)
	}
	if got := mustDir(t, fsys).Snapshot.Tables[0].Collation; got != "utf8mb4_0900_ai_ci" {
		t.Errorf("the snapshot's table: %q", got)
	}

	// A stated one is a plan, as before.
	stated := unstated(name, migrate.Column{Name: "more", Type: "integer", Nullable: true})
	stated.Tables = append(stated.Tables, next.Tables[1])
	stated.Tables[0].Collation = "utf8mb4_bin"
	if _, err := migrate.NewPlan(mustDir(t, fsys).Snapshot, stated, migrate.PlanOptions{}); err == nil || !strings.Contains(err.Error(), "convert-collation") {
		t.Errorf("a stated collation: %v", err)
	}
}
