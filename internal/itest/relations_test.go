package itest

import (
	"context"
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

func TestOfOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		itemIDs := func(p velox.Pred[testmodel.Item]) []int64 {
			t.Helper()
			got, err := tg.db.Query[testmodel.Item]().Where(p).OrderBy(It.ID.Asc()).Limit(10).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var out []int64
			for _, it := range got {
				out = append(out, it.ID)
			}
			return out
		}
		inv1, inv2 := testmodel.Invoice{ID: 1}, testmodel.Invoice{ID: 2}
		wantIDs(t, itemIDs(I.Items.Of(&inv1, &inv2)), []int64{1, 2, 3})
		wantIDs(t, itemIDs(I.Items.Of(&inv1, &inv1)), []int64{1, 2})
		wantIDs(t, itemIDs(I.Items.Of()), nil)
		wantIDs(t, itemIDs(I.Items.Where(It.Qty.GT(0)).Of(&inv1)), []int64{1})

		// The inverse; the target's tenant applies to it.
		item3 := testmodel.Item{ID: 3, InvoiceID: 2}
		for _, c := range []struct {
			ctx  context.Context
			want []int64
		}{{ctx, []int64{2}}, {velox.WithTenant(ctx, int64(1)), []int64{2}}, {velox.WithTenant(ctx, int64(2)), nil}} {
			got, err := tg.db.Query[testmodel.Invoice]().Where(It.Invoice.Of(&item3)).Limit(10).All(c.ctx)
			if err != nil {
				t.Fatal(err)
			}
			wantIDs(t, ids(got), c.want)
		}

		// A NULL key relates to nothing; the target's Read policy applies:
		// ticket 5's parent, ticket 3, is bob's.
		parents := func(qctx context.Context, x testmodel.Ticket) []int64 {
			t.Helper()
			got, err := tg.db.Query[testmodel.Ticket]().Where(Tk.Parent.Of(&x)).Limit(10).All(qctx)
			if err != nil {
				t.Fatal(err)
			}
			return ticketIDs(got)
		}
		three := int64(3)
		wantIDs(t, parents(admin.ctx, testmodel.Ticket{ID: 1}), nil)
		wantIDs(t, parents(admin.ctx, testmodel.Ticket{ID: 5, ParentID: &three}), []int64{3})
		wantIDs(t, parents(ann.ctx, testmodel.Ticket{ID: 5, ParentID: &three}), nil)

		// A write whose only condition is Of with no owners changes nothing.
		n, err := tg.db.Update[testmodel.Item]().Set(It.Qty.Set(9)).Where(I.Items.Of()).Exec(ctx)
		if err != nil || n != 0 {
			t.Errorf("update of no owners: %d, %v", n, err)
		}
	})
}
