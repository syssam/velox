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
