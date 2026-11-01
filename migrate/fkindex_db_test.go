package migrate_test

import (
	"context"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestMySQLDropForeignKeysOnlyIndex: dropping the one index a foreign key
// uses, the key kept, works on MySQL, whose error 1553 would leave the version
// dirty; so does replacing it, and dropping one of two; a row keeps its key.
func TestMySQLDropForeignKeysOnlyIndex(t *testing.T) {
	d := velox.MySQL
	parent := migrate.Table{Name: "p", Columns: []migrate.Column{{Name: "id", Type: "bigint"}}, PrimaryKey: []string{"id"}, PrimaryKeyName: "PRIMARY", Collation: "utf8mb4_bin"}
	schema := func(ix ...migrate.Index) *migrate.Schema {
		child := migrate.Table{Name: "c", Columns: []migrate.Column{{Name: "id", Type: "bigint"}, {Name: "pid", Type: "bigint"}, {Name: "n", Type: "bigint"}}, PrimaryKey: []string{"id"}, PrimaryKeyName: "PRIMARY", Collation: "utf8mb4_bin",
			Indexes:     ix,
			ForeignKeys: []migrate.ForeignKey{{Name: "c_p", Columns: []string{"pid"}, RefTable: migrate.TableRef{Name: "p"}, RefColumns: []string{"id"}}}}
		return &migrate.Schema{Dialect: d, Tables: []migrate.Table{child, parent}}
	}
	single := migrate.Index{Name: "c_pid", Columns: []string{"pid"}}
	wide := migrate.Index{Name: "c_pid_n", Columns: []string{"pid", "n"}}
	for _, c := range []struct {
		name     string
		from, to *migrate.Schema
	}{
		{"dropped", schema(single), schema()},
		{"replaced", schema(single), schema(wide)},
		{"one of two", schema(single, wide), schema(wide)},
	} {
		t.Run(c.name, func(t *testing.T) {
			db, _ := freshDB(t, d)
			fsys := dirOf(t, d, step{to: c.from, name: "base"})
			mustUp(t, db, fsys)
			mustExec(t, db, "INSERT INTO p (id) VALUES (1)")
			mustExec(t, db, "INSERT INTO c (id, pid, n) VALUES (1, 1, 1)")
			fsys = extend(t, fsys, d, step{to: c.to, name: "drop"})
			if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
				t.Fatalf("Up: %v\n%s", err, sqlFiles(fsys))
			}
			r, err := migrate.Drift(context.Background(), db, c.to)
			if err != nil {
				t.Fatal(err)
			}
			if len(r.Changes) != 0 {
				t.Errorf("drift: %s\n%s", reportText(r), sqlFiles(fsys))
			}
			if _, err := db.Exec("INSERT INTO c (id, pid, n) VALUES (2, 99, 1)"); err == nil {
				t.Error("the foreign key is gone")
			}
		})
	}
}
