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

// withU is oneTable with a second table u: an integer key, a column y of
// type typ and the index u_y on y.
func withU(d velox.Dialect, typ string) *migrate.Schema {
	s := oneTable(d)
	u := s.Tables[0]
	u.Name, u.PrimaryKeyName = "u", strings.Replace(u.PrimaryKeyName, "t_", "u_", 1)
	u.Columns = []migrate.Column{{Name: "id", Type: "integer"}, {Name: "y", Type: typ}}
	u.Indexes = []migrate.Index{{Name: "u_y", Columns: []string{"y"}}}
	s.Tables = append(s.Tables, u)
	return s
}

// checkDirs is the directory the check tests run: 0001 makes t, 0002 makes
// u. With third, 0003 makes a table w by hand.
func checkDirs(t *testing.T, d velox.Dialect, third bool) fstest.MapFS {
	t.Helper()
	steps := []step{{to: oneTable(d), name: "a"}, {to: withU(d, "integer"), name: "b"}}
	if third {
		steps = append(steps, files("0003_c.sql", "CREATE TABLE w (id integer NOT NULL PRIMARY KEY);\n"))
	}
	fsys := dirOf(t, d, steps...)
	dir, err := migrate.ReadDir(fsys)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{1, 2}
	if third {
		want = append(want, 3)
	}
	if got := versions(dir.Files); !slices.Equal(got, want) {
		t.Fatalf("the directory's versions are %v; want %v", got, want)
	}
	return fsys
}

// markDirty leaves version v of db's history dirty, as a failed run does.
func markDirty(t *testing.T, db *sql.DB, v int) {
	t.Helper()
	if _, err := db.Exec(`UPDATE velox_migrations SET dirty = true, statements = 0, error = 'boom' WHERE version = ?`, v); err != nil {
		// PostgreSQL's placeholder is $1.
		if _, err2 := db.Exec(`UPDATE velox_migrations SET dirty = true, statements = 0, error = 'boom' WHERE version = $1`, v); err2 != nil {
			t.Fatalf("%v; %v", err, err2)
		}
	}
}

// dropIndexSQL is DROP INDEX of index on table, as d writes it.
func dropIndexSQL(d velox.Dialect, table, index string) string {
	if d == velox.MySQL {
		return "DROP INDEX " + index + " ON " + table
	}
	return "DROP INDEX " + index
}

// resolveNotes is Resolve with its notes.
func resolveNotes(db *sql.DB, fsys fstest.MapFS, version uint64, r migrate.Resolution, opts ...migrate.Option) ([]string, error) {
	var notes []string
	opts = append(opts, migrate.WithNotes(func(n string) { notes = append(notes, n) }))
	err := migrate.Resolve(context.Background(), db, fsys, version, r, opts...)
	return notes, err
}

// refusedWith is err as an *Error of code that says each of says.
func refusedWith(t *testing.T, err error, code string, says ...string) {
	t.Helper()
	var me *migrate.Error
	if !errors.As(err, &me) || me.Code != code {
		t.Fatalf("%v; want an *Error of Code %q", err, code)
	}
	for _, s := range says {
		if !strings.Contains(me.Reason, s) {
			t.Errorf("the refusal does not say %q:\n%s", s, me.Reason)
		}
	}
}

// TestResolveChecksTheDatabase: ResolveApplied and ResolveAdopted compare
// the database with schema.json when the version is the directory's last,
// on every dialect. A hand-finished version that matches is accepted; a
// missing column, a wrong type and an extra index are refused and listed;
// WithForce records the override in the row's Error.
func TestResolveChecksTheDatabase(t *testing.T) {
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			fsys := checkDirs(t, d, false)
			setup := func(t *testing.T, hand ...string) *sql.DB {
				t.Helper()
				db, _ := freshDB(t, d)
				mustUp(t, db, fsys)
				markDirty(t, db, 2)
				for _, q := range hand {
					mustExec(t, db, q)
				}
				return db
			}

			t.Run("finished by hand", func(t *testing.T) {
				db := setup(t)
				if _, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied); err != nil {
					t.Fatal(err)
				}
				if rows := historyOf(t, db); rows[1].Dirty || rows[1].Error != "" {
					t.Errorf("history: %+v", rows)
				}
			})

			t.Run("a missing column", func(t *testing.T) {
				db := setup(t, dropIndexSQL(d, "u", "u_y"), "ALTER TABLE u DROP COLUMN y")
				_, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied)
				refusedWith(t, err, "drift", "version 2 is not marked applied: the database differs from schema.json",
					"\n  change: add-column u.y", "\n  change: add-index u.u_y", "-statements k", "-force")
				if rows := historyOf(t, db); !rows[1].Dirty {
					t.Errorf("recorded: %+v", rows)
				}
			})

			t.Run("a wrong type", func(t *testing.T) {
				db := setup(t, "DROP TABLE u", "CREATE TABLE u (id integer NOT NULL PRIMARY KEY, y varchar(10) NOT NULL)", "CREATE INDEX u_y ON u (y)")
				_, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied)
				refusedWith(t, err, "drift", "change: alter-column u.y")
			})

			t.Run("an extra index", func(t *testing.T) {
				db := setup(t, "CREATE INDEX u_extra ON u (id, y)")
				_, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied)
				refusedWith(t, err, "drift", "change: drop-index u.u_extra")
			})

			t.Run("forced", func(t *testing.T) {
				db := setup(t, "CREATE INDEX u_extra ON u (id, y)")
				notes, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied, migrate.WithForce("checked by hand"))
				if err != nil {
					t.Fatal(err)
				}
				rows := historyOf(t, db)
				if want := "forced: checked by hand; the database differed from schema.json: drop-index u.u_extra"; rows[1].Dirty || rows[1].Error != want {
					t.Errorf("history: %+v\nwant Error %q", rows[1], want)
				}
				if len(notes) != 1 || !strings.HasPrefix(notes[0], "version 2 is marked applied by force (checked by hand), though the database differs from schema.json") ||
					!strings.Contains(notes[0], "change: drop-index u.u_extra") {
					t.Errorf("notes: %q", notes)
				}
				_, err = resolveNotes(db, fsys, 1, migrate.ResolveApplied, migrate.WithForce(" "))
				refusedWith(t, err, "resolution", "WithForce needs a reason")
			})

			t.Run("a pending file the database holds", func(t *testing.T) {
				db, _ := freshDB(t, d)
				mustUp(t, db, fsys, migrate.WithTarget(1))
				_, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied)
				refusedWith(t, err, "drift", "change: create-table u", "make the database hold what 0002_b.sql makes")
				dir, err := migrate.ReadDir(fsys)
				if err != nil {
					t.Fatal(err)
				}
				for _, q := range dir.Files[1].Statements {
					mustExec(t, db, q)
				}
				if _, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied); err != nil {
					t.Fatal(err)
				}
				if rows := historyOf(t, db); len(rows) != 2 || rows[1].Statements != 0 || rows[1].Error != "" {
					t.Errorf("history: %+v", rows)
				}
			})
		})
	}
}

// TestResolveAdoptChecks: ResolveAdopted of an edited file is accepted when
// the database holds what the file now makes, and refused when not.
func TestResolveAdoptChecks(t *testing.T) {
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			fsys := checkDirs(t, d, false)
			// The same 0001, and a 0002_b.sql that makes y bigint.
			edited := dirOf(t, d, step{to: oneTable(d), name: "a"}, step{to: withU(d, "bigint"), name: "b"})
			dir, err := migrate.ReadDir(edited)
			if err != nil {
				t.Fatal(err)
			}
			if string(edited["0001_a.sql"].Data) != string(fsys["0001_a.sql"].Data) || string(edited["0002_b.sql"].Data) == string(fsys["0002_b.sql"].Data) {
				t.Fatal("the edit is not to 0002_b.sql alone")
			}

			db, _ := freshDB(t, d)
			mustUp(t, db, fsys)
			_, err = resolveNotes(db, edited, 2, migrate.ResolveAdopted)
			refusedWith(t, err, "drift", "version 2 is not adopted: the database differs from schema.json", "change: alter-column u.y",
				"make the database hold what 0002_b.sql now makes and resolve again; restore the file")
			if rows := historyOf(t, db); rows[1].Sum == dir.Files[1].Sum {
				t.Errorf("adopted: %+v", rows)
			}

			mustExec(t, db, "DROP TABLE u")
			for _, q := range dir.Files[1].Statements {
				mustExec(t, db, q)
			}
			notes, err := resolveNotes(db, edited, 2, migrate.ResolveAdopted)
			if err != nil {
				t.Fatal(err)
			}
			if rows := historyOf(t, db); rows[1].Sum != dir.Files[1].Sum || rows[1].Error != "" {
				t.Errorf("history: %+v", rows)
			}
			if len(notes) != 1 || !strings.Contains(notes[0], "none of the file ran") {
				t.Errorf("notes: %q", notes)
			}
		})
	}
}

// TestResolveChecksByReplay: with a file pending above the version, the
// database is compared with the files up to it applied to a scratch
// database. SQLite makes one in a temporary file; PostgreSQL and MySQL need
// WithScratch, or WithForce, and a scratch database that is not empty is
// refused.
func TestResolveChecksByReplay(t *testing.T) {
	for _, d := range []velox.Dialect{velox.SQLite, velox.Postgres, velox.MySQL} {
		t.Run(d.String(), func(t *testing.T) {
			fsys := checkDirs(t, d, true)
			db, _ := freshDB(t, d)
			mustUp(t, db, fsys, migrate.WithTarget(2))
			markDirty(t, db, 2)
			mustExec(t, db, "CREATE INDEX u_extra ON u (id, y)")
			var opts []migrate.Option
			if d != velox.SQLite {
				_, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied)
				refusedWith(t, err, "unverified", "files above version 2 are pending", "-scratch-db", "-force")
				scratch, _ := freshDB(t, d)
				opts = append(opts, migrate.WithScratch(scratch))
			}
			_, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied, opts...)
			refusedWith(t, err, "drift", "the database differs from what the files up to 0002_b.sql make on an empty database", "change: drop-index u.u_extra")

			mustExec(t, db, dropIndexSQL(d, "u", "u_extra"))
			if d != velox.SQLite {
				scratch, _ := freshDB(t, d)
				opts = []migrate.Option{migrate.WithScratch(scratch)}
			}
			if _, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied, opts...); err != nil {
				t.Fatal(err)
			}
			if rows := historyOf(t, db); len(rows) != 2 || rows[1].Dirty {
				t.Errorf("history: %+v", rows)
			}
			// The scratch database the first replay used holds the files now.
			if d != velox.SQLite {
				markDirty(t, db, 2)
				scratch, _ := freshDB(t, d)
				mustUp(t, scratch, fsys, migrate.WithTarget(1))
				_, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied, migrate.WithScratch(scratch))
				refusedWith(t, err, "drift", "the scratch database must be empty")
			}
			// Unchecked, forced.
			if d != velox.SQLite {
				markDirty(t, db, 2)
				notes, err := resolveNotes(db, fsys, 2, migrate.ResolveApplied, migrate.WithForce("no scratch here"))
				if err != nil {
					t.Fatal(err)
				}
				if rows := historyOf(t, db); !strings.HasPrefix(rows[1].Error, "forced: no scratch here; unchecked: files above version 2 are pending") {
					t.Errorf("history: %+v", rows[1])
				}
				if len(notes) != 1 || !strings.Contains(notes[0], "by force (no scratch here), unchecked") {
					t.Errorf("notes: %q", notes)
				}
			}
		})
	}
}
