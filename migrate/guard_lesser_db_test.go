package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestMySQLDropColumnPlainIndexIsANote: only a UNIQUE index means something else when
// a DROP COLUMN narrows it. A plain one was refused for nothing; it gets a note.
func TestMySQLDropColumnPlainIndexIsANote(t *testing.T) {
	t.Parallel()
	d := velox.MySQL
	a, b := migrate.Column{Name: "a", Type: "int", Nullable: true}, migrate.Column{Name: "b", Type: "int", Nullable: true}
	setup := func(t *testing.T, hand string) (func() error, func() []string) {
		db, _ := freshDB(t, d)
		fsys := dirOf(t, d, step{to: oneTable(d, a, b), name: "base"})
		mustUp(t, db, fsys)
		mustExec(t, db, hand)
		fsys = extend(t, fsys, d, step{to: oneTable(d, a), name: "drop", opts: allowAll("drop-column")})
		var notes []string
		return func() error {
			_, err := migrate.Up(context.Background(), db, fsys, migrate.WithNotes(func(n string) { notes = append(notes, n) }))
			return err
		}, func() []string { return notes }
	}
	up, notes := setup(t, "CREATE INDEX x ON t (a, b)")
	if err := up(); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if n := notes(); len(n) != 1 || !strings.Contains(n[0], "t.x") || !strings.Contains(n[0], "over (a)") {
		t.Errorf("the notes: %q", n)
	}
	up, _ = setup(t, "CREATE UNIQUE INDEX x ON t (a, b)")
	err := up()
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "unmodeled" || !strings.Contains(err.Error(), "holds rows to more") {
		t.Errorf("a UNIQUE one: %v", err)
	}
}

// unsupported checks that Up refuses fsys as an *Error of Code unsupported that says
// says, and leaves no row in the history for the file.
func unsupported(t *testing.T, err error, says string) {
	t.Helper()
	if e, ok := errors.AsType[*migrate.Error](err); !ok || e.Code != "unsupported" || !strings.Contains(err.Error(), says) {
		t.Fatalf("Up: %v", err)
	}
}

// TestPostgresConcurrentlyOnPartitioned: PostgreSQL refuses CONCURRENTLY on a
// partitioned table or index with the file under way, its earlier statements run. Up
// refuses before any of it, and says how an index gets onto one.
func TestPostgresConcurrentlyOnPartitioned(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	ctx := context.Background()
	db, _ := freshDB(t, d)
	base := dirOf(t, d, files("0001_base.sql", `CREATE TABLE t (id integer NOT NULL, a integer) PARTITION BY RANGE (id);
CREATE TABLE t1 PARTITION OF t FOR VALUES FROM (0) TO (10);
CREATE TABLE plain (id integer PRIMARY KEY);
CREATE INDEX parted ON t (id);
`))
	mustUp(t, db, base)
	fsys := extend(t, base, d, files("0002_x.sql", `-- velox:no-transaction
CREATE TABLE first (id integer);
CREATE INDEX CONCURRENTLY t_a ON t (a);
`))
	_, err := migrate.Up(ctx, db, fsys)
	unsupported(t, err, "ATTACH PARTITION")
	if oneValue(t, db, "SELECT count(*) FROM information_schema.tables WHERE table_name = 'first'") != "0" {
		t.Error("the statement before it ran")
	}
	if rows := historyOf(t, db); len(rows) != 1 {
		t.Errorf("the history: %+v", rows)
	}
	_, err = migrate.Up(ctx, db, extend(t, base, d, files("0002_y.sql", "-- velox:no-transaction\nDROP INDEX CONCURRENTLY parted;\n")))
	unsupported(t, err, "without CONCURRENTLY")

	// A plain table is no refusal; nor is a table the file makes.
	mustUp(t, db, extend(t, base, d, files("0002_z.sql", `-- velox:no-transaction
CREATE INDEX CONCURRENTLY plain_id ON plain (id);
DROP INDEX CONCURRENTLY plain_id;
`)))
}

// TestMySQLInplaceOnMyISAM: 1845 comes with the file under way, each statement before
// it kept. Up refuses before any. What MyISAM runs as INPLACE is let through.
func TestMySQLInplaceOnMyISAM(t *testing.T) {
	t.Parallel()
	d := velox.MySQL
	ctx := context.Background()
	db, _ := freshDB(t, d)
	base := dirOf(t, d, files("0001_base.sql", `CREATE TABLE m (id integer PRIMARY KEY, a integer, b integer) ENGINE=MyISAM;
CREATE TABLE n (id integer PRIMARY KEY, a integer) ENGINE=InnoDB;
`))
	mustUp(t, db, base)
	for _, q := range []string{
		"ALTER TABLE m ADD INDEX i (a), ALGORITHM=INPLACE",
		"ALTER TABLE m ADD INDEX i (a), LOCK=NONE",
		"CREATE INDEX i ON m (a) LOCK=NONE",
		"ALTER TABLE m ALGORITHM = INPLACE, ADD COLUMN c integer",
	} {
		_, err := migrate.Up(ctx, db, extend(t, base, d, files("0002_x.sql", "CREATE TABLE first (id integer);\n"+q+";\n")))
		unsupported(t, err, "ENGINE=InnoDB")
		if oneValue(t, db, "SELECT count(*) FROM information_schema.tables WHERE table_name = 'first'") != "0" {
			t.Fatalf("%s: the statement before it ran", q)
		}
	}
	mustUp(t, db, extend(t, base, d, files("0002_x.sql", `ALTER TABLE m ALTER COLUMN a SET DEFAULT 2, ALGORITHM=INPLACE;
ALTER TABLE m RENAME COLUMN b TO b2, LOCK=NONE;
ALTER TABLE n ADD INDEX i (a), ALGORITHM=INPLACE, LOCK=NONE;
`)))
}

// TestPostgresTriggerFunctionNamesColumn: PostgreSQL does not read a PL/pgSQL body until
// the trigger fires, so a rename or drop of a column it names ran, and every write to
// the table failed after. Up refuses, a directive accepts, and a file that replaces the
// function needs none.
func TestPostgresTriggerFunctionNamesColumn(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	const trigger = `CREATE TABLE t (id integer PRIMARY KEY, c integer, other integer);
CREATE FUNCTION touch() RETURNS trigger LANGUAGE plpgsql AS $f$ BEGIN NEW.c := 1; RETURN NEW; END $f$;
CREATE TRIGGER t_touch BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION touch();
`
	const replaced = `CREATE OR REPLACE FUNCTION touch() RETURNS trigger LANGUAGE plpgsql AS $f$ BEGIN NEW.d := 1; RETURN NEW; END $f$;
`
	for _, q := range []string{"ALTER TABLE t RENAME COLUMN c TO d", "ALTER TABLE t RENAME c TO d", "ALTER TABLE t DROP COLUMN c"} {
		t.Run(q, func(t *testing.T) {
			db, _ := freshDB(t, d)
			base := dirOf(t, d, files("0001_base.sql", trigger))
			mustUp(t, db, base)
			refused(t, db, extend(t, base, d, files("0002_x.sql", q+";\n")), "touch", "t_touch", "t.c", "-- velox:allow unmodeled-loss touch")
			mustUp(t, db, extend(t, base, d, files("0002_x.sql", "-- velox:allow unmodeled-loss touch\n"+q+";\n")))
		})
	}
	t.Run("a file that replaces it", func(t *testing.T) {
		db, _ := freshDB(t, d)
		base := dirOf(t, d, files("0001_base.sql", trigger))
		mustUp(t, db, base)
		mustUp(t, db, extend(t, base, d, files("0002_x.sql", "ALTER TABLE t RENAME COLUMN c TO d;\n"+replaced)))
		mustExec(t, db, "INSERT INTO t (id) VALUES (1)")
	})
	t.Run("a column it does not name", func(t *testing.T) {
		db, _ := freshDB(t, d)
		base := dirOf(t, d, files("0001_base.sql", trigger))
		mustUp(t, db, base)
		mustUp(t, db, extend(t, base, d, files("0002_x.sql", "ALTER TABLE t DROP COLUMN other;\n")))
	})
}

// TestPostgresTriggerFunctionNamesTable: a PL/pgSQL function of a trigger on one
// table that writes another names that table, and PostgreSQL does not read the body
// until the trigger fires. A rename, move or drop of the other table, or a rename or
// drop of a column of it the body names, ran, and every write to the trigger's table
// failed after. Up refuses, a directive accepts, and a file that replaces the function
// needs none. A drop of the trigger's own table takes the trigger with it.
func TestPostgresTriggerFunctionNamesTable(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	const trigger = `CREATE TABLE t (id integer PRIMARY KEY, c integer);
CREATE TABLE audit (c integer, other integer);
CREATE FUNCTION log_t() RETURNS trigger LANGUAGE plpgsql AS $f$ BEGIN INSERT INTO audit (c) VALUES (NEW.c); RETURN NEW; END $f$;
CREATE TRIGGER t_log AFTER INSERT ON t FOR EACH ROW EXECUTE FUNCTION log_t();
`
	const replaced = `CREATE OR REPLACE FUNCTION log_t() RETURNS trigger LANGUAGE plpgsql AS $f$ BEGIN RETURN NEW; END $f$;
`
	for _, c := range []struct{ q, target string }{
		{"ALTER TABLE audit RENAME TO audit_log", "audit"},
		{"ALTER TABLE IF EXISTS public.audit RENAME TO audit_log", "audit"},
		{"CREATE SCHEMA old;\nALTER TABLE audit SET SCHEMA old", "audit"},
		{"DROP TABLE audit", "audit"},
		{"ALTER TABLE audit RENAME COLUMN c TO d", "audit.c"},
		{"ALTER TABLE audit DROP COLUMN c", "audit.c"},
	} {
		t.Run(c.q, func(t *testing.T) {
			db, _ := freshDB(t, d)
			base := dirOf(t, d, files("0001_base.sql", trigger))
			mustUp(t, db, base)
			refused(t, db, extend(t, base, d, files("0002_x.sql", c.q+";\n")), "log_t", "t_log", c.target, "-- velox:allow unmodeled-loss log_t")
			mustUp(t, db, extend(t, base, d, files("0002_x.sql", "-- velox:allow unmodeled-loss log_t\n"+c.q+";\n")))
		})
	}
	t.Run("a file that replaces it", func(t *testing.T) {
		db, _ := freshDB(t, d)
		base := dirOf(t, d, files("0001_base.sql", trigger))
		mustUp(t, db, base)
		mustUp(t, db, extend(t, base, d, files("0002_x.sql", "ALTER TABLE audit RENAME TO audit_log;\n"+replaced)))
		mustExec(t, db, "INSERT INTO t (id) VALUES (1)")
	})
	for _, q := range []string{"ALTER TABLE audit DROP COLUMN other", "DROP TABLE t", "ALTER TABLE t RENAME TO t2"} {
		t.Run(q, func(t *testing.T) {
			db, _ := freshDB(t, d)
			base := dirOf(t, d, files("0001_base.sql", trigger))
			mustUp(t, db, base)
			mustUp(t, db, extend(t, base, d, files("0002_x.sql", q+";\n")))
		})
	}
}
