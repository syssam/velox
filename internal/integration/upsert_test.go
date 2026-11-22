package integration

import (
	"strings"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestUpsertUpdateSet: the update branch's Add adds to the stored value, Set
// writes a value, and an expression reads the existing row and the inserted
// one (Excluded); Exec reads the stored values back into the entity. An
// InsertAll adds to each conflicting row.
func TestUpsertUpdateSet(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		total := func(id int64) (v float64) {
			t.Helper()
			if err := tg.direct.QueryRowContext(ctx, rebind(tg.d, "SELECT total FROM invoices WHERE id = ?"), id).Scan(&v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		over := newInvoice(1, "INV-1")
		over.Total = 5
		if err := tg.db.Insert(&over).OnConflict(I.OrgID, I.Number).UpdateSet(I.Total.Add(5), I.Note.Set("bumped")).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if v := total(1); v != 15 || over.ID != 1 || over.Total != 15 || over.Note != "bumped" {
			t.Errorf("Add: stored %v, entity ID %d total %v note %q; want 15, 1, 15, bumped", v, over.ID, over.Total, over.Note)
		}

		again := newInvoice(1, "INV-1")
		again.Total = 1
		set := velox.SetExpr(I.Total, "? + ? * 2", I.Total, velox.Excluded(I.Total))
		if err := tg.db.Insert(&again).OnConflict(I.OrgID, I.Number).UpdateSet(set).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if v := total(1); v != 17 || again.Total != 17 {
			t.Errorf("Excluded: stored %v, entity %v; want 17", v, again.Total)
		}

		fresh := newInvoice(7, "NEW")
		if err := tg.db.Insert(&fresh).OnConflict(I.OrgID, I.Number).UpdateSet(I.Total.Add(5)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if fresh.ID == 0 || fresh.Total != 1 || total(fresh.ID) != 1 {
			t.Errorf("no conflict: ID %d, total %v; want the inserted row", fresh.ID, fresh.Total)
		}

		two := []testmodel.Invoice{newInvoice(1, "INV-1"), newInvoice(1, "INV-2")}
		if err := tg.db.InsertAll(two).OnConflict(I.OrgID, I.Number).UpdateSet(I.Total.Add(1)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if a, b := total(1), total(2); a != 18 || b != 21 {
			t.Errorf("InsertAll: totals %v, %v; want 18, 21", a, b)
		}
		if tg.d != velox.MySQL && (two[0].ID != 1 || two[1].ID != 2 || two[0].Total != 18 || two[1].Total != 21) {
			t.Errorf("InsertAll read back: %+v %+v", two[0], two[1])
		}
	})
}

// TestUpsertUpdateWhere: a conflicting row the update branch's condition
// rejects is left as it was, and the entity's key stays zero; a row it
// accepts is updated. MySQL refuses the condition before sending anything.
func TestUpsertUpdateWhere(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		x := newInvoice(1, "INV-1")
		x.Total = 99
		q := tg.db.Insert(&x).OnConflict(I.OrgID, I.Number).Update(I.Total).
			UpdateWhere(velox.Expr[testmodel.Invoice]("? > ?", velox.Excluded(I.Total), I.Total))
		if tg.d == velox.MySQL {
			if err := q.Exec(ctx); err == nil || !strings.Contains(err.Error(), "UpdateWhere") {
				t.Errorf("MySQL: %v", err)
			}
			return
		}
		if err := q.Exec(ctx); err != nil || x.ID != 1 {
			t.Fatalf("accepted: %v, ID %d", err, x.ID)
		}
		low := newInvoice(1, "INV-1")
		low.Total = 3
		if err := tg.db.Insert(&low).OnConflict(I.OrgID, I.Number).Update(I.Total).
			UpdateWhere(velox.Expr[testmodel.Invoice]("? > ?", velox.Excluded(I.Total), I.Total)).Exec(ctx); err != nil || low.ID != 0 {
			t.Fatalf("rejected: %v, ID %d", err, low.ID)
		}
		got, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
		if err != nil || got.Total != 99 {
			t.Errorf("row 1: %v, %v; want total 99", got.Total, err)
		}
	})
}

// TestUpsertConflictTarget: OnConflictWhere picks a partial unique index on
// PostgreSQL and SQLite, and OnConstraint a named constraint on PostgreSQL.
func TestUpsertConflictTarget(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		if tg.d == velox.MySQL {
			return // refused before sending: internal/apitest
		}
		tg.exec(t, "CREATE UNIQUE INDEX invoices_open_number ON invoices (number) WHERE paid_at IS NULL")
		x := newInvoice(9, "INV-2") // row 2 is open, with number INV-2
		if err := tg.db.Insert(&x).OnConflict(I.Number).OnConflictWhere(I.PaidAt.IsNull()).UpdateSet(I.Total.Add(5)).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if x.ID != 2 || x.Total != 25 {
			t.Errorf("partial index: ID %d, total %v; want 2, 25", x.ID, x.Total)
		}
		if tg.d != velox.Postgres {
			return
		}
		y := newInvoice(1, "INV-1")
		y.Total = 7
		if err := tg.db.Insert(&y).OnConflict(I.OrgID, I.Number).OnConstraint("invoices_org_number_uq").Update(I.Total).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if y.ID != 1 {
			t.Errorf("constraint: ID %d, want 1", y.ID)
		}
	})
}
