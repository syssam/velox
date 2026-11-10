package apitest

import (
	"database/sql"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestInsertWritesZerosOverDatabaseDefaults: a column whose DEFAULT is not the
// zero value, as SchemaDefault gives one, still stores the zero a row holds, and
// a nil Nillable column stores NULL, because velox writes every column and no
// DEFAULT applies to it. A SchemaDefault is for rows written outside velox.
func TestInsertWritesZerosOverDatabaseDefaults(t *testing.T) {
	raw, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	_, err = raw.Exec(`CREATE TABLE invoices (created_at datetime NOT NULL, updated_at datetime NOT NULL, id integer PRIMARY KEY,
		org_id integer NOT NULL, number text NOT NULL DEFAULT 'dflt', total real NOT NULL DEFAULT 5, paid boolean NOT NULL DEFAULT true,
		paid_at datetime DEFAULT '2000-01-01 00:00:00', note text NOT NULL DEFAULT 'n', data blob NOT NULL DEFAULT x'01')`)
	if err != nil {
		t.Fatal(err)
	}
	db := velox.Open(velox.SQLite, raw).WithoutPolicy("velox tests")
	read := func() (total float64, paid bool, paidAt sql.NullString, number, note string) {
		t.Helper()
		err := raw.QueryRow(`SELECT total, paid, CAST(paid_at AS TEXT), number, note FROM invoices ORDER BY id DESC LIMIT 1`).Scan(&total, &paid, &paidAt, &number, &note)
		if err != nil {
			t.Fatal(err)
		}
		return
	}

	// Insert and InsertAll, whose columns are literal when every row is nil.
	for name, insert := range map[string]func() error{
		"Insert": func() error { return db.Insert(&testmodel.Invoice{OrgID: 1, Data: []byte{}}).Exec(ctx) },
		"InsertAll": func() error {
			return db.InsertAll([]testmodel.Invoice{{OrgID: 1, Data: []byte{}}, {OrgID: 2, Data: []byte{}}}).Exec(ctx)
		},
	} {
		if err := insert(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		total, paid, paidAt, number, note := read()
		if total != 0 || paid || number != "" || note != "" {
			t.Errorf("%s: total %v, paid %v, number %q, note %q: the zeros are not stored; a DEFAULT took over", name, total, paid, number, note)
		}
		if paidAt.Valid {
			t.Errorf("%s: a nil paid_at stored %q, want NULL beside the column's DEFAULT", name, paidAt.String)
		}
	}
}
