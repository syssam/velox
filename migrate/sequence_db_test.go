package migrate_test

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/migrate"
)

// TestPostgresWideningSerialWidensSequence: a serial's sequence follows its
// column's type, or nextval stops at the old type's top; with no name in the
// snapshot it is read from the default.
func TestPostgresWideningSerialWidensSequence(t *testing.T) {
	d := velox.Postgres
	for _, named := range []string{"t_n_seq", ""} {
		t.Run("named "+named, func(t *testing.T) {
			db, _ := freshDB(t, d)
			seq := `nextval('t_n_seq'::regclass)`
			fsys := dirOf(t, d, step{to: oneTable(d, migrate.Column{Name: "n", Type: "integer", Default: seq, Sequence: "t_n_seq"}), name: "base"})
			mustUp(t, db, fsys)
			wide := oneTable(d, migrate.Column{Name: "n", Type: "bigint", Default: seq, Sequence: "t_n_seq"})
			if named == "" {
				// A snapshot that does not name it, as an older baseline's.
				fsys = rewrite(t, fsys, "schema.json", func(s string) string { return strings.ReplaceAll(s, `"sequence": "t_n_seq"`, `"comment": ""`) })
				wide.Tables[0].Columns[1].Sequence = ""
			}
			fsys = extend(t, fsys, d, step{to: wide, name: "wide"})
			mustUp(t, db, fsys)
			var typ string
			if err := db.QueryRow(`SELECT format_type(seqtypid, NULL) FROM pg_sequence WHERE seqrelid = 't_n_seq'::regclass`).Scan(&typ); err != nil {
				t.Fatal(err)
			}
			if typ != "bigint" {
				t.Errorf("the sequence is %s\n%s", typ, sqlFiles(fsys))
			}
			mustExec(t, db, `SELECT setval('t_n_seq', 3000000000)`)
			mustExec(t, db, `INSERT INTO t (id) VALUES (1)`)
		})
	}
}

// TestPostgresWideningIdentityFollows: an identity's sequence follows its
// column on its own, so no statement is added for it.
func TestPostgresWideningIdentityFollows(t *testing.T) {
	d := velox.Postgres
	db, _ := freshDB(t, d)
	fsys := dirOf(t, d, step{to: oneTable(d, migrate.Column{Name: "n", Type: "integer", Identity: true, Sequence: "t_n_seq"}), name: "base"})
	mustUp(t, db, fsys)
	fsys = extend(t, fsys, d, step{to: oneTable(d, migrate.Column{Name: "n", Type: "bigint", Identity: true, Sequence: "t_n_seq"}), name: "wide"})
	mustUp(t, db, fsys)
	var typ string
	if err := db.QueryRow(`SELECT format_type(seqtypid, NULL) FROM pg_sequence WHERE seqrelid = 't_n_seq'::regclass`).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if typ != "bigint" {
		t.Errorf("the sequence is %s\n%s", typ, sqlFiles(fsys))
	}
}

// TestPostgresSerialToIdentityKeepsCounter: a serial turned into an identity goes
// on from where its sequence was, not from the column's largest value: the IDs of
// rows deleted from the top were handed out once, and are not handed out again. A
// row above the sequence still moves the counter past it.
func TestPostgresSerialToIdentityKeepsCounter(t *testing.T) {
	d := velox.Postgres
	serial := func(cols ...migrate.Column) *migrate.Schema {
		s := oneTable(d, cols...)
		s.Tables[0].Columns[0] = migrate.Column{Name: "id", Type: "integer", Default: "nextval('t_id_seq'::regclass)", Sequence: "t_id_seq"}
		return s
	}
	ident := func(cols ...migrate.Column) *migrate.Schema {
		s := oneTable(d, cols...)
		s.Tables[0].Columns[0] = migrate.Column{Name: "id", Type: "integer", Identity: true, Sequence: "t_id_seq"}
		return s
	}
	c := migrate.Column{Name: "c", Type: "text", Nullable: true}
	for _, named := range []string{"t_id_seq", ""} {
		t.Run("named "+named, func(t *testing.T) {
			db, _ := freshDB(t, d)
			fsys := dirOf(t, d, step{to: serial(c), name: "base"})
			mustUp(t, db, fsys)
			mustExec(t, db, `INSERT INTO t (c) SELECT 'x' FROM generate_series(1, 10)`)
			mustExec(t, db, `DELETE FROM t WHERE id > 8`)
			if named == "" {
				// A snapshot that does not name it, as an older baseline's.
				fsys = rewrite(t, fsys, "schema.json", func(s string) string { return strings.ReplaceAll(s, `"sequence": "t_id_seq"`, `"comment": ""`) })
			}
			fsys = extend(t, fsys, d, step{to: ident(c), name: "ident"})
			mustUp(t, db, fsys)
			if id := oneValue(t, db, `INSERT INTO t (c) VALUES ('y') RETURNING id`); id != "11" {
				t.Errorf("the next ID is %s; want 11\n%s", id, sqlFiles(fsys))
			}
			invariants(t, db, d, fsys, ident(c))
		})
	}
	t.Run("a row past the sequence", func(t *testing.T) {
		db, _ := freshDB(t, d)
		fsys := dirOf(t, d, step{to: serial(), name: "base"})
		mustUp(t, db, fsys)
		mustExec(t, db, `INSERT INTO t (id) VALUES (40)`)
		fsys = extend(t, fsys, d, step{to: ident(), name: "ident"})
		mustUp(t, db, fsys)
		if id := oneValue(t, db, `INSERT INTO t DEFAULT VALUES RETURNING id`); id != "41" {
			t.Errorf("the next ID is %s; want 41", id)
		}
	})
}
