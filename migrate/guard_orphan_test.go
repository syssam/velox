package migrate_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// orphaned inserts a row of c whose key names no row of p, as foreign keys left off
// allow: SQLite's own default.
func orphaned(t *testing.T, db *sql.DB, id int) {
	t.Helper()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "INSERT INTO c (id, p) VALUES (?, 999)", id); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		t.Fatal(err)
	}
}

// TestSQLiteOrphansAlreadyThere: an orphan row from before a file is no refusal of the
// rebuild of its parent or child, and Up says it is there. A row the file orphans is
// still a refusal, rolled back.
func TestSQLiteOrphansAlreadyThere(t *testing.T) {
	t.Parallel()
	d := velox.SQLite
	base := dirOf(t, d, step{to: keyed(""), name: "base"})
	setup := func(t *testing.T) *sql.DB {
		db, _ := freshDB(t, d)
		mustUp(t, db, base)
		mustExec(t, db, "INSERT INTO p (id) VALUES (1)")
		mustExec(t, db, "INSERT INTO c (id, p) VALUES (1, 1)")
		orphaned(t, db, 2)
		return db
	}
	child := keyed("")
	child.Tables[1].Columns[1].Type = "bigint"
	parent := keyed("")
	parent.Tables[0].Columns = append(parent.Tables[0].Columns, migrate.Column{Name: "x", Type: "integer", Nullable: true})
	parent.Tables[0].Checks = []migrate.Check{{Name: "p_x", Expr: `"x" > 0`}}
	for name, to := range map[string]*migrate.Schema{"a rebuild of the child": child, "a rebuild of the parent": parent} {
		t.Run(name, func(t *testing.T) {
			db := setup(t)
			fsys := extend(t, base, d, step{to: to, name: "next", opts: allowAll("alter-type")})
			var notes []string
			mustUp(t, db, fsys, migrate.WithNotes(func(s string) { notes = append(notes, s) }))
			if len(notes) != 1 || !strings.Contains(notes[0], "row 2 of c") || !strings.Contains(notes[0], "names no row of p") {
				t.Errorf("the notes: %q", notes)
			}
		})
	}
	t.Run("a row the file orphans", func(t *testing.T) {
		db := setup(t)
		fsys := extend(t, base, d, files("0002_cut.sql", `-- velox:foreign-keys off
DELETE FROM p WHERE id = 1;
`))
		_, err := migrate.Up(context.Background(), db, fsys)
		if err == nil || !strings.Contains(err.Error(), "row 1 of c") {
			t.Fatalf("Up: %v", err)
		}
		if got := oneValue(t, db, "SELECT count(*) FROM p"); got != "1" {
			t.Errorf("the row after the refusal: %s rows", got)
		}
	})
	t.Run("a row the file repairs", func(t *testing.T) {
		db := setup(t)
		fsys := extend(t, base, d, files("0002_fix.sql", `-- velox:foreign-keys off
DELETE FROM c WHERE id = 2;
`))
		var notes []string
		mustUp(t, db, fsys, migrate.WithNotes(func(s string) { notes = append(notes, s) }))
		if len(notes) != 0 {
			t.Errorf("the notes: %q", notes)
		}
	})
}

// TestRefusalSaysWhereAnEditIsSafe: a refusal's advice to edit a statement changes the
// file's checksum, which an environment that applied it then refuses. It says so, and
// names what has no effect on the checksum.
func TestRefusalSaysWhereAnEditIsSafe(t *testing.T) {
	t.Parallel()
	d := velox.MySQL
	small, wide := migrate.Column{Name: "c", Type: "varchar(10)", Nullable: true}, migrate.Column{Name: "c", Type: "varchar(20)", Nullable: true}
	db, fsys := planned(t, d, small, wide, migrate.PlanOptions{}, "ALTER TABLE t MODIFY c varchar(10) NULL DEFAULT 'x'")
	refused(t, db, fsys, "not yet applied anywhere", "ChecksumError", "a directive line, which does not change the checksum", "a new migration")
}
