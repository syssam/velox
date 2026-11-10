package integration

import (
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/syssam/velox/internal/testmodel"
)

// TestJoinAfterNullsExact: an order by a nullable column of a joined row, with
// many NULLs and ties, sorts NULL after every value ascending and before them
// descending on every dialect, and After pages it exactly, from the joined row
// of the last row of each page: every row once, in the order.
func TestJoinAfterNullsExact(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		t1, t2 := t0.Add(time.Hour), t0.Add(2*time.Hour)
		at := []*time.Time{nil, &t1, nil, &t2, &t1}
		invoices := make([]testmodel.Invoice, len(at))
		for i, p := range at {
			invoices[i] = newInvoice(7, fmt.Sprintf("J-%02d", i))
			invoices[i].ID, invoices[i].PaidAt = int64(200+i), p
		}
		if err := tg.db.InsertAll(invoices).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		// Two or three items to an invoice, so that the ties are many.
		var items []testmodel.Item
		paidAt := map[int64]*time.Time{}
		for i := range 13 {
			inv := invoices[i%len(invoices)]
			id := int64(300 + i)
			items = append(items, testmodel.Item{ID: id, InvoiceID: inv.ID, SKU: fmt.Sprintf("S-%02d", i), Qty: 1})
			paidAt[id] = inv.PaidAt
		}
		if err := tg.db.InsertAll(items).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for _, o := range []struct {
			paidDesc, idDesc bool
		}{{false, false}, {false, true}, {true, false}, {true, true}} {
			name := fmt.Sprintf("paid desc %v, id desc %v", o.paidDesc, o.idDesc)
			want := slices.Clone(items)
			slices.SortFunc(want, func(a, b testmodel.Item) int {
				c := nullsLast(paidAt[a.ID], paidAt[b.ID])
				if o.paidDesc {
					c = -c
				}
				if c == 0 {
					c = int(a.ID - b.ID)
					if o.idDesc {
						c = -c
					}
				}
				return c
			})
			by, key := I.PaidAt.Asc(), It.ID.Asc()
			if o.paidDesc {
				by = I.PaidAt.Desc()
			}
			if o.idDesc {
				key = It.ID.Desc()
			}
			q := tg.db.Query[testmodel.Item]().Join(It.Invoice).Where(It.ID.GTE(300)).
				OrderBy(It.Invoice.By(by), key)
			all, err := q.AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			wantIDs(t, itemIDs(all), itemIDs(want))
			for n := 1; n <= 4; n++ {
				var got []testmodel.Item
				page, err := q.Limit(n).All(ctx)
				for ; err == nil && len(page) > 0; page, err = q.After(&page[len(page)-1]).Limit(n).All(ctx) {
					got = append(got, page...)
					if len(got) > len(items) {
						break
					}
				}
				if err != nil {
					t.Fatalf("%s, %d a page: %v", name, n, err)
				}
				if !slices.Equal(itemIDs(got), itemIDs(want)) {
					t.Errorf("%s, %d a page:\n got %v\nwant %v", name, n, itemIDs(got), itemIDs(want))
				}
			}
		}
	})
}

func itemIDs(xs []testmodel.Item) []int64 {
	out := make([]int64, len(xs))
	for i, x := range xs {
		out[i] = x.ID
	}
	return out
}
