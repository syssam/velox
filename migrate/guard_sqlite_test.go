package migrate_test

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// keyed is SQLite's p and c, c.c_fk with the action, as the plan writes them.
func keyed(onDelete string) *migrate.Schema {
	return &migrate.Schema{Dialect: velox.SQLite, Tables: []migrate.Table{
		{Name: "p", Columns: []migrate.Column{{Name: "id", Type: "integer"}}, PrimaryKey: []string{"id"}},
		{Name: "c", Columns: []migrate.Column{{Name: "id", Type: "integer"}, {Name: "p", Type: "integer", Nullable: true}}, PrimaryKey: []string{"id"},
			ForeignKeys: []migrate.ForeignKey{{Name: "c_fk", Columns: []string{"p"}, RefTable: migrate.TableRef{Name: "p"}, RefColumns: []string{"id"}, OnDelete: onDelete}}},
	}}
}

// TestRebuildDropsNameTheAttribute: a plan that changes a key's ON DELETE says only
// that, so a hand ON UPDATE and DEFERRABLE on the key are still seen. A plain plan
// runs, and a key the plan really drops is dropped whole.
func TestRebuildDropsNameTheAttribute(t *testing.T) {
	t.Parallel()
	d := velox.SQLite
	base := dirOf(t, d, step{to: keyed("CASCADE"), name: "base"})
	fsys := extend(t, base, d, step{to: keyed("SET NULL"), name: "action"})
	if s := string(fsys["0002_action.sql"].Data); !strings.Contains(s, "-- velox:drops c.c_fk:on-delete\n") {
		t.Fatalf("the file:\n%s", s)
	}
	db, _ := freshDB(t, d)
	mustUp(t, db, base)
	mustUp(t, db, fsys)

	db, _ = freshDB(t, d)
	mustUp(t, db, base)
	mustExec(t, db, "DROP TABLE c")
	mustExec(t, db, "CREATE TABLE c (id integer NOT NULL PRIMARY KEY, p integer, CONSTRAINT c_fk FOREIGN KEY (p) REFERENCES p (id) ON DELETE CASCADE ON UPDATE CASCADE DEFERRABLE)")
	_, err := migrate.Up(context.Background(), db, fsys)
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "unmodeled" || !strings.Contains(err.Error(), "ON UPDATE CASCADE") || !strings.Contains(err.Error(), "DEFERRABLE") {
		t.Fatalf("Up: %v", err)
	}

	gone := keyed("")
	gone.Tables[1].ForeignKeys = nil
	dropped := extend(t, base, d, step{to: gone, name: "gone"})
	if s := string(dropped["0002_gone.sql"].Data); !strings.Contains(s, "-- velox:drops c.c_fk\n") {
		t.Fatalf("the file:\n%s", s)
	}
	db, _ = freshDB(t, d)
	mustUp(t, db, base)
	mustUp(t, db, dropped)

	// The key points elsewhere: the file names that, and a hand edit that changes
	// another attribute, or none is said, is refused.
	db, _ = freshDB(t, d)
	mustUp(t, db, base)
	handed := rewrite(t, fsys, "0002_action.sql", func(s string) string {
		return strings.ReplaceAll(s, "-- velox:drops c.c_fk:on-delete\n", "")
	})
	_, err = migrate.Up(context.Background(), db, handed)
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "unmodeled" || !strings.Contains(err.Error(), "c.c_fk") {
		t.Fatalf("Up with no drops line: %v", err)
	}
}

// TestRebuildRefusesPrimaryKeyDescriptor: INTEGER PRIMARY KEY DESC is no rowid alias, so
// the plan's copy renumbers the rowids, and PRIMARY KEY (id DESC) loses DESC. Inspect
// notes both, so the guard refuses both, and either form can be accepted.
func TestRebuildRefusesPrimaryKeyDescriptor(t *testing.T) {
	t.Parallel()
	for _, create := range []string{
		"CREATE TABLE t (id INTEGER PRIMARY KEY DESC, n integer NOT NULL, s text, o integer)",
		"CREATE TABLE t (id integer NOT NULL, n integer NOT NULL, s text, o integer, PRIMARY KEY (id DESC))",
	} {
		db, fsys := guarded(t, migrate.PlanOptions{}, "DROP TABLE t", create)
		_, err := migrate.Up(context.Background(), db, fsys)
		if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "unmodeled" || !strings.Contains(err.Error(), "DESC") {
			t.Errorf("%s: Up: %v", create, err)
		}
		if got := createText(t, db); !strings.Contains(got, "DESC") {
			t.Errorf("%s: after the refusal: %s", create, got)
		}
		db, fsys = guarded(t, migrate.PlanOptions{Allow: []migrate.Allow{{Code: "unmodeled-loss", Target: "t"}}}, "DROP TABLE t", create)
		if _, err := migrate.Up(context.Background(), db, fsys); err != nil {
			t.Errorf("%s: accepted: %v", create, err)
		}
	}
}

// droppedUse is a database with p and a table o, a row in each, after hand, and the
// file that drops p under its allow line.
func droppedUse(t *testing.T, head string, hand ...string) (*sql.DB, fstest.MapFS) {
	t.Helper()
	d := velox.SQLite
	db, _ := freshDB(t, d)
	base := dirOf(t, d, files("0001_base.sql", "CREATE TABLE p (id integer NOT NULL PRIMARY KEY);\nCREATE TABLE o (id integer NOT NULL PRIMARY KEY);\n"))
	mustUp(t, db, base)
	for _, q := range hand {
		mustExec(t, db, q)
	}
	return db, extend(t, base, d, files("0002_drop.sql", head+"DROP TABLE p;\n"))
}

// TestDropTableRefusesDependents: after DROP TABLE p, a trigger of another table that
// names p fails every INSERT there, and a view that does fails the next rebuild of
// any table, with Up nil. The drop is refused, naming each; a directive accepts it, a
// file that drops the dependent first needs none, and one that makes p again leaves
// no dependent broken.
func TestDropTableRefusesDependents(t *testing.T) {
	t.Parallel()
	trigger := "CREATE TRIGGER o_ai AFTER INSERT ON o BEGIN INSERT INTO p (id) VALUES (new.id); END"
	view := "CREATE VIEW p_v AS SELECT id FROM p"
	ctx := context.Background()
	const allow = "-- velox:allow drop-table\n"

	for name, hand := range map[string]string{"o_ai": trigger, "p_v": view} {
		db, fsys := droppedUse(t, allow, hand)
		_, err := migrate.Up(ctx, db, fsys)
		if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "unmodeled" || !strings.Contains(err.Error(), name) || !strings.Contains(err.Error(), "velox:allow unmodeled-loss "+name) {
			t.Fatalf("%s: Up: %v", name, err)
		}
		if !objectOf(t, db, "p") {
			t.Errorf("%s: p is gone after the refusal", name)
		}
		db, fsys = droppedUse(t, allow+"-- velox:allow unmodeled-loss "+name+"\n", hand)
		if _, err := migrate.Up(ctx, db, fsys); err != nil {
			t.Errorf("%s: accepted: %v", name, err)
		}
	}

	db, fsys := droppedUse(t, allow+"DROP VIEW p_v;\n", view)
	if _, err := migrate.Up(ctx, db, fsys); err != nil {
		t.Errorf("the file drops the view: %v", err)
	}

	d := velox.SQLite
	db, _ = freshDB(t, d)
	base := dirOf(t, d, files("0001_base.sql", "CREATE TABLE p (id integer NOT NULL PRIMARY KEY);\n"))
	mustUp(t, db, base)
	mustExec(t, db, view)
	again := extend(t, base, d, files("0002_again.sql", "DROP TABLE p;\nCREATE TABLE p (id integer NOT NULL PRIMARY KEY, n integer);\n"))
	if _, err := migrate.Up(ctx, db, again); err != nil {
		t.Errorf("the file makes it again: %v", err)
	}

	// A column named as the dropped table is no use of it.
	db, fsys = droppedUse(t, allow, "CREATE TABLE q (id integer, p integer)", "CREATE VIEW q_v AS SELECT p FROM q")
	if _, err := migrate.Up(ctx, db, fsys); err != nil {
		t.Errorf("a column of that name: %v", err)
	}
	names := queryColumn[string](t, db, "SELECT name FROM sqlite_schema WHERE type = 'view'")
	if !slices.Equal(names, []string{"q_v"}) {
		t.Errorf("views: %v", names)
	}
}
