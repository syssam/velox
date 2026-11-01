package migrate_test

import (
	"context"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestSQLiteRebuildRenamesReferencedColumn: a column another table's foreign
// key references, renamed in a plan that rebuilds its table, is renamed in
// place first, as only ALTER TABLE rewrites the child's REFERENCES; a rebuild
// alone left a "foreign key mismatch". The rows and the key survive.
func TestSQLiteRebuildRenamesReferencedColumn(t *testing.T) {
	d := velox.SQLite
	schema := func(code string, nullable bool) *migrate.Schema {
		return &migrate.Schema{Dialect: d, Tables: []migrate.Table{
			{Name: "c", Columns: []migrate.Column{{Name: "id", Type: "integer"}, {Name: "p_code", Type: "text"}}, PrimaryKey: []string{"id"},
				ForeignKeys: []migrate.ForeignKey{{Name: "c_p", Columns: []string{"p_code"}, RefTable: migrate.TableRef{Name: "p"}, RefColumns: []string{code}}}},
			{Name: "p", Columns: []migrate.Column{{Name: "id", Type: "integer"}, {Name: code, Type: "text"}, {Name: "n", Type: "integer", Nullable: nullable}}, PrimaryKey: []string{"id"},
				Indexes: []migrate.Index{{Name: "p_code_key", Unique: true, Columns: []string{code}}}},
		}}
	}
	db, _ := freshDB(t, d)
	mustExec(t, db, "PRAGMA foreign_keys = ON")
	fsys := dirOf(t, d, step{to: schema("code", false), name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, "INSERT INTO p (id, code, n) VALUES (1, 'a', 1)")
	mustExec(t, db, "INSERT INTO c (id, p_code) VALUES (1, 'a')")
	to := schema("ref", true)
	fsys = extend(t, fsys, d, step{to: to, name: "rename", opts: migrate.PlanOptions{Diff: migrate.DiffOptions{
		RenameColumns: map[migrate.ColumnRef]string{{Table: migrate.TableRef{Name: "p"}, Column: "code"}: "ref"},
	}}})
	if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
		t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
	}
	var ref string
	if err := db.QueryRow("SELECT ref FROM p").Scan(&ref); err != nil || ref != "a" {
		t.Errorf("ref = %q, %v\n%s", ref, err, sqlFiles(fsys))
	}
	mustExec(t, db, "PRAGMA foreign_keys = ON")
	mustExec(t, db, "INSERT INTO c (id, p_code) VALUES (2, 'a')")
	if _, err := db.Exec("INSERT INTO c (id, p_code) VALUES (3, 'gone')"); err == nil {
		t.Error("the foreign key is gone")
	}
	r, err := migrate.Drift(context.Background(), db, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Changes) != 0 {
		t.Errorf("drift: %s", reportText(r))
	}
}
