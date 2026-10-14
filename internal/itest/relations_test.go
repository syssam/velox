package itest

import (
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

var It = testmodel.Items

func TestWith(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		invs, err := tg.db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).
			With(I.Items.Where(It.Qty.GTE(0)).OrderBy(It.SKU.Desc()).With(It.Invoice)).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		want := map[int64][]string{1: {"B", "A"}, 2: {"C"}, 3: nil, 4: nil, 5: nil}
		for _, inv := range invs {
			items, loaded := inv.Items.Get()
			if !loaded || items == nil {
				t.Fatalf("invoice %d: loaded %v %v", inv.ID, loaded, items)
			}
			var skus []string
			for _, it := range items {
				skus = append(skus, it.SKU)
				back, ok := it.Invoice.Get()
				if !ok || back == nil || back.ID != inv.ID {
					t.Errorf("item %d: its invoice %v %v", it.ID, back, ok)
				}
			}
			wantStrings(t, skus, want[inv.ID])
		}
	})
}

func TestHas(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		ids := func(ps ...velox.Pred[testmodel.Invoice]) []int64 {
			got, err := tg.db.Query[testmodel.Invoice]().Where(ps...).OrderBy(I.ID.Asc()).Limit(10).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var out []int64
			for _, inv := range got {
				out = append(out, inv.ID)
			}
			return out
		}
		wantIDs(t, ids(I.Items.Has()), []int64{1, 2})
		wantIDs(t, ids(I.Items.HasWhere(It.Qty.GT(3))), []int64{2})
		wantIDs(t, ids(velox.Not(I.Items.Has())), []int64{3, 4, 5})
		wantIDs(t, ids(I.Items.HasWhere(It.Invoice.HasWhere(I.Number.EQ("INV-2")))), []int64{2})
		n, err := tg.db.Update[testmodel.Invoice]().Set(I.Note.SetNull()).Where(velox.Not(I.Items.Has())).Exec(ctx)
		if err != nil || n != 3 {
			t.Errorf("update with EXISTS: %d, %v", n, err)
		}
	})
}

func TestAfterPages(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		q := tg.db.Query[testmodel.Invoice]().OrderBy(I.Total.Desc(), I.ID.Asc()).Limit(2)
		var got []int64
		var last *testmodel.Invoice
		for range 4 {
			page := q
			if last != nil {
				page = q.After(last)
			}
			rows, err := page.All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) == 0 {
				break
			}
			for _, r := range rows {
				got = append(got, r.ID)
			}
			last = &rows[len(rows)-1]
		}
		wantIDs(t, got, []int64{5, 4, 3, 2, 1})
	})
}
