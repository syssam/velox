package apitest

import (
	"context"
	"database/sql"
	"testing"

	"github.com/syssam/velox"
)

// TestMutationDBAtomicOnlyInTx: a hook's statement and the write it wraps share
// a transaction only inside db.Tx. Outside it each commits alone, as
// Mutation.DB says: here the audit insert fails, on a table that does not
// exist, and the note it follows stays.
func TestMutationDBAtomicOnlyInTx(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	if _, err := raw.Exec(`CREATE TABLE lz_notes (id INTEGER PRIMARY KEY AUTOINCREMENT, title TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, raw)
	notes := func() int {
		t.Helper()
		var n int
		if err := raw.QueryRow("SELECT count(*) FROM lz_notes").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	err = db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		return tx.Insert(&lzNote{Title: "a"}).Exec(ctx)
	})
	if err == nil || notes() != 0 {
		t.Errorf("in db.Tx: %v, %d notes; want the audit's error and none", err, notes())
	}
	err = db.Insert(&lzNote{Title: "b"}).Exec(ctx)
	if err == nil || notes() != 1 {
		t.Errorf("outside db.Tx: %v, %d notes; want the audit's error and one", err, notes())
	}
}
