package migrate_test

import (
	"context"
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestPostgresNarrowingFailsOnLongRow: narrowing a varchar fails on a row
// over the new length, where an explicit cast would cut it. One over it by
// trailing spaces fails too, which the assignment cast trims unseen.
func TestPostgresNarrowingFailsOnLongRow(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	for _, c := range []struct{ from, to string }{
		{"character varying(10)", "character varying(5)"},
		{"text", "character varying(5)"},
		{"character varying(10)", "character(5)"},
		{"text[]", "character varying(5)[]"},
		{"bit(10)", "bit(5)"},
		{"text", "bpchar(5)"},
		{"text", "pg_catalog.varchar(5)"},
		{"text", "pg_catalog.bit(5)"},
		{"text", "bit(5)"},
		{"text", "varbit(5)"},
	} {
		values := []string{"'1234567890'"}
		switch {
		case c.from == "text[]":
			values = []string{"ARRAY['1234567890']", "ARRAY['abc       ']", "ARRAY['ab', NULL, 'abc       ']"}
		case c.from == "bit(10)":
			values = []string{"B'1010101010'"}
		case !strings.Contains(c.to, "bit"):
			values = append(values, "'abc       '")
		}
		for _, value := range values {
			t.Run(c.from+" to "+c.to+" "+value, func(t *testing.T) {
				db, _ := freshDB(t, d)
				fsys := dirOf(t, d, step{to: oneTable(d, migrate.Column{Name: "c", Type: c.from, Nullable: true}), name: "base"})
				mustUp(t, db, fsys)
				mustExec(t, db, `INSERT INTO t (id, c) VALUES (1, `+value+`)`)
				fsys = extend(t, fsys, d, step{to: oneTable(d, migrate.Column{Name: "c", Type: c.to, Nullable: true}), name: "narrow", opts: allowAll("alter-type")})
				if _, err := migrate.Up(context.Background(), db, fsys); err == nil {
					t.Errorf("Up narrowed an over-length row\n%s", sqlFiles(fsys))
				}
				var n int
				if err := db.QueryRow(`SELECT count(*) FROM t WHERE c::text = (` + value + `)::text`).Scan(&n); err != nil || n != 1 {
					t.Errorf("the row changed: %d, %v", n, err)
				}
			})
		}
	}
}

// TestPostgresRetypeKeepsCastWithoutAssignment: a change no assignment cast
// makes, text to integer, keeps its USING and runs; a narrowing that fits
// runs without it; a number to a string needs none.
func TestPostgresRetypeKeepsCastWithoutAssignment(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	db, _ := freshDB(t, d)
	col := func(name, typ string) migrate.Column { return migrate.Column{Name: name, Type: typ, Nullable: true} }
	fsys := dirOf(t, d, step{to: oneTable(d, col("c", "text"), col("b", "character varying(10)"), col("n", "integer")), name: "base"})
	mustUp(t, db, fsys)
	mustExec(t, db, `INSERT INTO t (id, c, b, n) VALUES (1, '42', 'abc', 7)`)
	to := oneTable(d, col("c", "integer"), col("b", "character varying(5)"), col("n", "text"))
	fsys = extend(t, fsys, d, step{to: to, name: "retype", opts: allowAll("alter-type")})
	mustUp(t, db, fsys)
	var c int
	var b, n string
	if err := db.QueryRow(`SELECT c, b, n FROM t`).Scan(&c, &b, &n); err != nil || c != 42 || b != "abc" || n != "7" {
		t.Errorf("c, b, n = %d, %q, %q, %v", c, b, n, err)
	}
}

// TestPostgresBitFromString: a string reaches a bit type through varbit, so a
// value of another length fails where a cast to bit(3) would cut it to 101, and
// one that fits runs. A varbit target takes a shorter one.
func TestPostgresBitFromString(t *testing.T) {
	t.Parallel()
	d := velox.Postgres
	col := func(typ string) migrate.Column { return migrate.Column{Name: "c", Type: typ, Nullable: true} }
	for _, to := range []string{"bit(3)", "pg_catalog.bit(3)", "bit varying(3)"} {
		db, _ := freshDB(t, d)
		fsys := dirOf(t, d, step{to: oneTable(d, col("text")), name: "base"})
		mustUp(t, db, fsys)
		mustExec(t, db, `INSERT INTO t (id, c) VALUES (1, '10101')`)
		long := extend(t, fsys, d, step{to: oneTable(d, col(to)), name: "narrow", opts: allowAll("alter-type")})
		if _, err := migrate.Up(context.Background(), db, long); err == nil {
			t.Errorf("%s: Up cut the row\n%s", to, sqlFiles(long))
		}
		mustExec(t, db, `UPDATE t SET c = '101'`)
		mustUp(t, db, long)
		var c string
		if err := db.QueryRow(`SELECT c::text FROM t`).Scan(&c); err != nil || c != "101" {
			t.Errorf("%s: c = %q, %v", to, c, err)
		}
	}
}
