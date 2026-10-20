package itest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

func newInvoice(org int64, number string) testmodel.Invoice {
	return testmodel.Invoice{OrgID: org, Number: number, Total: 1, Data: []byte("d")}
}

func TestInsertAndReadBack(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		inv := newInvoice(3, "NEW")
		if err := tg.db.Insert(&inv).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if inv.ID <= 5 || inv.CreatedAt.IsZero() {
			t.Fatalf("ID %d, CreatedAt %v", inv.ID, inv.CreatedAt)
		}
		got, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(inv.ID)).One(ctx)
		if err != nil || got.Number != "NEW" || got.CreatedAt.Sub(inv.CreatedAt).Abs() > time.Millisecond {
			t.Errorf("%+v, %v", got, err)
		}
		given := newInvoice(3, "GIVEN")
		given.ID = 50
		if err := tg.db.Insert(&given).Exec(ctx); err != nil || given.ID != 50 {
			t.Errorf("%v, ID %d", err, given.ID)
		}
		if row, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(50)).One(ctx); err != nil || row.Number != "GIVEN" {
			t.Errorf("row 50: %+v, %v", row, err)
		}
	})
}

func TestUniqueViolation(t *testing.T) {
	want := map[velox.Dialect]string{
		velox.Postgres: "invoices_org_number_uq",
		velox.MySQL:    "invoices_org_number_uq",
		velox.SQLite:   "invoices.org_id, invoices.number",
	}
	each(t, func(t *testing.T, tg target) {
		dup := newInvoice(1, "INV-1")
		err := tg.db.Insert(&dup).Exec(ctx)
		var ce *velox.ConstraintError
		if !errors.As(err, &ce) || ce.Kind != velox.ConstraintUnique || ce.Constraint != want[tg.d] || ce.Entity != "testmodel.Invoice" {
			t.Errorf("%v (%#v)", err, ce)
		}
	})
}

func TestUpsert(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		keep := newInvoice(1, "INV-1")
		keep.Total = 77
		if err := tg.db.Insert(&keep).OnConflict(I.OrgID, I.Number).DoNothing().Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if keep.ID != 0 {
			t.Errorf("DO NOTHING read back ID %d, want 0: no row was inserted", keep.ID)
		}
		kept, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx)
		if err != nil || kept.Total != 10 {
			t.Fatalf("DO NOTHING must keep the row: %+v, %v", kept, err)
		}
		over := newInvoice(1, "INV-1")
		over.Total = 99
		if err := tg.db.Insert(&over).OnConflict(I.OrgID, I.Number).Update(I.Total).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		got, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1), I.Number.EQ("INV-1")).One(ctx)
		if err != nil || got.ID != 1 || got.Total != 99 {
			t.Errorf("%+v, %v", got, err)
		}
		if tg.d != velox.MySQL && over.ID != 1 {
			t.Errorf("RETURNING after DO UPDATE: ID %d, want 1", over.ID)
		}
		if n, err := tg.db.Query[testmodel.Invoice]().Count(ctx); err != nil || n != 5 {
			t.Errorf("rows %d, %v, want 5: an upsert inserted", n, err)
		}
	})
}

func TestInsertAll(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		rows := []testmodel.Invoice{newInvoice(9, "A"), newInvoice(9, "B"), newInvoice(9, "C")}
		if err := tg.db.InsertAll(rows).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if n, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(9)).Count(ctx); err != nil || n != 3 {
			t.Errorf("%d, %v", n, err)
		}
	})
}

func TestUpdateDeleteSave(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		n, err := tg.db.Update[testmodel.Invoice]().Set(I.Total.Add(1)).Where(I.OrgID.EQ(1)).Exec(ctx)
		if err != nil || n != 4 {
			t.Fatalf("update: %d, %v", n, err)
		}
		one, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(2)).One(ctx)
		if err != nil || one.Total != 21 || !one.UpdatedAt.After(t0) {
			t.Fatalf("%+v, %v", one, err)
		}
		one.Number = "CHANGED"
		if err := tg.db.Save(ctx, one, I.Number); err != nil {
			t.Fatal(err)
		}
		again, err := tg.db.Query[testmodel.Invoice]().Where(I.ID.EQ(2)).One(ctx)
		if err != nil || again.Number != "CHANGED" {
			t.Errorf("%+v, %v", again, err)
		}
		// Naming UpdatedAt keeps its UpdateDefault from firing, so every value sent equals
		// the stored one (one.UpdatedAt is what the first Save sent) and the row
		// is matched but not changed: only clientFoundRows makes MySQL count it.
		if err := tg.db.Save(ctx, one, I.Number, I.UpdatedAt); err != nil {
			t.Errorf("saving unchanged values must still find the row (clientFoundRows on MySQL): %v", err)
		}
		missing := newInvoice(1, "X")
		missing.ID = 999
		if err := tg.db.Save(ctx, &missing, I.Number); !errors.Is(err, velox.ErrNotFound) {
			t.Errorf("missing row: %v", err)
		}
		n, err = tg.db.Delete[testmodel.Invoice]().Where(I.ID.EQ(5)).Exec(ctx)
		if err != nil || n != 1 {
			t.Errorf("delete: %d, %v", n, err)
		}
	})
}

func TestTransactions(t *testing.T) {
	boom := errors.New("boom")
	each(t, func(t *testing.T, tg target) {
		err := tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			a := newInvoice(7, "A")
			if err := tx.Insert(&a).Exec(ctx); err != nil {
				return err
			}
			if err := tx.Tx(ctx, func(ctx context.Context, inner *velox.DB) error {
				b := newInvoice(7, "B")
				if err := inner.Insert(&b).Exec(ctx); err != nil {
					return err
				}
				return boom
			}); !errors.Is(err, boom) {
				t.Errorf("savepoint: %v", err)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		wantStrings(t, numbers(t, tg, 7), []string{"A"})
		err = tg.db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
			c := newInvoice(8, "C")
			if err := tx.Insert(&c).Exec(ctx); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatal(err)
		}
		if n, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(8)).Count(ctx); err != nil || n != 0 {
			t.Errorf("a rolled-back insert is visible: %d rows, %v", n, err)
		}
	})
}

func numbers(t *testing.T, tg target, org int64) []string {
	t.Helper()
	got, err := tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(org)).OrderBy(I.Number.Asc()).Unbounded().All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(got))
	for i := range got {
		out[i] = got[i].Number
	}
	return out
}
