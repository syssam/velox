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
			With(I.Items.Where(It.Qty.GT(0)).OrderBy(It.SKU.Desc()).With(It.Invoice)).Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(invs) != 5 {
			t.Fatalf("%d invoices, want 5", len(invs))
		}
		// The filter drops item B (qty 0).
		want := map[int64][]string{1: {"A"}, 2: {"C"}, 3: nil, 4: nil, 5: nil}
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
		// Invoices 3 and 5 already have no note ("") and updated_at t0, so they
		// match without changing: 3 rows needs clientFoundRows on MySQL.
		n, err := tg.db.Update[testmodel.Invoice]().Set(I.Note.Set(""), I.UpdatedAt.Set(t0)).Where(velox.Not(I.Items.Has())).Exec(ctx)
		if err != nil || n != 3 {
			t.Errorf("update with EXISTS: %d, %v", n, err)
		}
		notes := map[int64]string{}
		all, err := tg.db.Query[testmodel.Invoice]().Limit(10).All(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, inv := range all {
			notes[inv.ID] = inv.Note
		}
		if notes[4] != "" || notes[2] != "a note" {
			t.Errorf("notes after the update: 2 %v, 4 %v", notes[2], notes[4])
		}
	})
}

func TestAfterPages(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		for name, c := range map[string]struct {
			order []velox.Order[testmodel.Invoice]
			want  []int64
		}{
			"total desc":     {[]velox.Order[testmodel.Invoice]{I.Total.Desc(), I.ID.Asc()}, []int64{5, 4, 3, 2, 1}},
			"ties: paid, id": {[]velox.Order[testmodel.Invoice]{I.Paid.Desc(), I.ID.Asc()}, []int64{1, 3, 5, 2, 4}},
			"id ascending":   {[]velox.Order[testmodel.Invoice]{I.ID.Asc()}, []int64{1, 2, 3, 4, 5}},
		} {
			t.Run(name, func(t *testing.T) {
				q := tg.db.Query[testmodel.Invoice]().OrderBy(c.order...).Limit(2)
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
				wantIDs(t, got, c.want)
			})
		}
	})
}
