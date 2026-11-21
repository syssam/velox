package integration

import (
	"errors"
	"testing"

	"github.com/syssam/velox"
)

// ehRow is a row of a table with every kind of constraint: a unique code,
// a foreign key to invoices, a CHECK on qty and a NOT NULL note, which a
// nil Note leaves unset.
type ehRow struct {
	ID        int64
	InvoiceID int64
	Code      string
	Qty       int64
	Note      *string
}

var ehRowMeta velox.MetaOnce[ehRow]

func (*ehRow) VeloxMeta() *velox.Meta[ehRow] {
	return ehRowMeta.Load(func() velox.Descriptor[ehRow] {
		return velox.Descriptor[ehRow]{
			Table: "eh_rows",
			Columns: []velox.ColumnDescriptor{
				{Field: "ID", Name: "id"}, {Field: "InvoiceID", Name: "invoice_id"}, {Field: "Code", Name: "code"},
				{Field: "Qty", Name: "qty"}, {Field: "Note", Name: "note"},
			},
			PrimaryKey: []int{0},
			FieldPtr: func(x *ehRow, i int) any {
				return [...]any{&x.ID, &x.InvoiceID, &x.Code, &x.Qty, &x.Note}[i]
			},
		}
	})
}

func ehRowDDL(d velox.Dialect) string {
	switch d {
	case velox.Postgres:
		return `CREATE TABLE eh_rows (id bigint PRIMARY KEY, invoice_id bigint NOT NULL REFERENCES invoices (id),
			code text NOT NULL UNIQUE, qty bigint NOT NULL CHECK (qty >= 0), note text NOT NULL)`
	case velox.MySQL:
		return "CREATE TABLE eh_rows (id BIGINT PRIMARY KEY, invoice_id BIGINT NOT NULL, code VARCHAR(16) NOT NULL UNIQUE, " +
			"qty BIGINT NOT NULL, note VARCHAR(16) NOT NULL, CONSTRAINT eh_rows_qty_ck CHECK (qty >= 0), " +
			"CONSTRAINT eh_rows_invoice_fk FOREIGN KEY (invoice_id) REFERENCES invoices (id))"
	}
	return `CREATE TABLE eh_rows (id INTEGER PRIMARY KEY, invoice_id INTEGER NOT NULL REFERENCES invoices (id),
		code TEXT NOT NULL UNIQUE, qty INTEGER NOT NULL CHECK (qty >= 0), note TEXT NOT NULL)`
}

// TestIsConstraintErrorOnEveryDialect: velox.IsConstraintError is true for
// a unique, a foreign key, a CHECK and a NOT NULL violation that the
// database reports, on each database, and IsValidationError is not.
func TestIsConstraintErrorOnEveryDialect(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.d == velox.SQLite {
			tg.exec(t, "PRAGMA foreign_keys = ON")
		}
		tg.exec(t, "DROP TABLE IF EXISTS eh_rows")
		tg.exec(t, ehRowDDL(tg.d))
		t.Cleanup(func() { tg.exec(t, "DROP TABLE IF EXISTS eh_rows") })
		note := "n"
		first := ehRow{ID: 1, InvoiceID: 1, Code: "A", Qty: 1, Note: &note}
		if err := tg.db.Insert(&first).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for _, c := range []struct {
			name string
			row  ehRow
			kind velox.ConstraintKind
		}{
			{"unique", ehRow{ID: 2, InvoiceID: 1, Code: "A", Qty: 1, Note: &note}, velox.ConstraintUnique},
			{"foreign key", ehRow{ID: 3, InvoiceID: 999, Code: "B", Qty: 1, Note: &note}, velox.ConstraintForeignKey},
			{"check", ehRow{ID: 4, InvoiceID: 1, Code: "C", Qty: -1, Note: &note}, velox.ConstraintCheck},
			{"not null", ehRow{ID: 5, InvoiceID: 1, Code: "D", Qty: 1}, velox.ConstraintNotNull},
		} {
			err := tg.db.Insert(&c.row).Exec(ctx)
			if !velox.IsConstraintError(err) || velox.IsValidationError(err) {
				t.Errorf("%s: IsConstraintError false for %v", c.name, err)
			}
			if ce, ok := errors.AsType[*velox.ConstraintError](err); !ok || ce.Kind != c.kind {
				t.Errorf("%s: %v, want kind %v", c.name, err, c.kind)
			}
		}
	})
}
