package integration

import (
	"cmp"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestNullsOrderExact: NullsFirst and NullsLast put NULL where they say,
// in each direction, giving the same rows in the same order on every
// dialect, and After pages that order exactly at each page size. An
// expression with bound values, which MySQL writes twice, orders the same.
func TestNullsOrderExact(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		t1, t2 := t0.Add(time.Hour), t0.Add(2*time.Hour)
		at := []*time.Time{nil, &t1, nil, &t2, &t1, nil, nil, &t2, &t1, nil, &t1, nil, &t2}
		rows := make([]testmodel.Invoice, len(at))
		for i, p := range at {
			rows[i] = newInvoice(7, fmt.Sprintf("NO-%02d", i))
			rows[i].ID, rows[i].PaidAt = int64(400+i), p
		}
		if err := tg.db.InsertAll(rows).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		// NULLIF(id % 3, 0): NULL for every third row.
		calc := velox.Calc[testmodel.Invoice]("NULLIF(? % ?, ?)", I.ID, int64(3), int64(0))
		mod := func(x testmodel.Invoice) *int64 {
			if x.ID%3 == 0 {
				return nil
			}
			m := x.ID % 3
			return &m
		}
		for _, o := range []struct {
			desc, first, idDesc bool
		}{
			{false, false, false}, {false, true, false}, {true, false, false}, {true, true, false},
			{false, true, true}, {true, false, true},
		} {
			name := fmt.Sprintf("desc %v, nulls first %v, id desc %v", o.desc, o.first, o.idDesc)
			by, byCalc := I.PaidAt.Asc(), calc.Asc()
			if o.desc {
				by, byCalc = I.PaidAt.Desc(), calc.Desc()
			}
			if o.first {
				by, byCalc = by.NullsFirst(), byCalc.NullsFirst()
			} else {
				by, byCalc = by.NullsLast(), byCalc.NullsLast()
			}
			key := I.ID.Asc()
			if o.idDesc {
				key = I.ID.Desc()
			}
			sorted := func(less func(a, b testmodel.Invoice) int) []testmodel.Invoice {
				want := slices.Clone(rows)
				slices.SortFunc(want, func(a, b testmodel.Invoice) int {
					c := less(a, b)
					if c == 0 {
						c = cmp.Compare(a.ID, b.ID)
						if o.idDesc {
							c = -c
						}
					}
					return c
				})
				return want
			}
			want := sorted(func(a, b testmodel.Invoice) int {
				return placed(a.PaidAt == nil, b.PaidAt == nil, o.first, func() int { return a.PaidAt.Compare(*b.PaidAt) }, o.desc)
			})
			q := tg.db.Query[testmodel.Invoice]().Where(I.ID.GTE(400)).OrderBy(by, key)
			all, err := q.AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			wantIDs(t, ids(all), ids(want))
			for n := 1; n <= 4; n++ {
				var got []*testmodel.Invoice
				page, err := q.Limit(n).All(ctx)
				for ; err == nil && len(page) > 0; page, err = q.After(page[len(page)-1]).Limit(n).All(ctx) {
					got = append(got, page...)
					if len(got) > len(rows) {
						break
					}
				}
				if err != nil {
					t.Fatalf("%s, %d a page: %v", name, n, err)
				}
				if !slices.Equal(ids(got), ids(want)) {
					t.Errorf("%s, %d a page:\n got %v\nwant %v", name, n, ids(got), ids(want))
				}
			}
			wantCalc := sorted(func(a, b testmodel.Invoice) int {
				ma, mb := mod(a), mod(b)
				return placed(ma == nil, mb == nil, o.first, func() int { return cmp.Compare(*ma, *mb) }, o.desc)
			})
			all, err = tg.db.Query[testmodel.Invoice]().Where(I.ID.GTE(400)).OrderBy(byCalc, key).AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatalf("%s, by an expression: %v", name, err)
			}
			if !slices.Equal(ids(all), ids(wantCalc)) {
				t.Errorf("%s, by an expression:\n got %v\nwant %v", name, ids(all), ids(wantCalc))
			}
		}
	})
}

// placed compares two values, NULL when aNull or bNull says so, with NULL
// first when first says so, else last, and the values by byValue, reversed
// when desc says so; NULL's place does not turn with desc.
func placed(aNull, bNull, first bool, byValue func() int, desc bool) int {
	switch {
	case aNull && bNull:
		return 0
	case aNull == first && (aNull || bNull):
		return -1
	case aNull || bNull:
		return 1
	}
	if desc {
		return -byValue()
	}
	return byValue()
}

// TestNullsOrderRelAggregateAfter: an ordering by a relation's sum, NULL
// for an invoice with no item, with NullsFirst or NullsLast, sorts as it
// says on every dialect, and After pages it exactly.
func TestNullsOrderRelAggregateAfter(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		invoices := make([]testmodel.Invoice, 7)
		for i := range invoices {
			invoices[i] = newInvoice(7, fmt.Sprintf("NR-%02d", i))
			invoices[i].ID = int64(400 + i)
		}
		if err := tg.db.InsertAll(invoices).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		// Invoice i has i%3 items, so three invoices sum to NULL.
		sum := map[int64]*int64{}
		var items []testmodel.Item
		for i, inv := range invoices {
			for k := range i % 3 {
				qty := int64(i%2 + k)
				items = append(items, testmodel.Item{ID: int64(500 + len(items)), InvoiceID: inv.ID, SKU: fmt.Sprintf("NR-%d-%d", i, k), Qty: qty})
				s := qty
				if p := sum[inv.ID]; p != nil {
					s += *p
				}
				sum[inv.ID] = &s
			}
		}
		if err := tg.db.InsertAll(items).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		for _, o := range []struct{ desc, first bool }{{false, true}, {false, false}, {true, true}, {true, false}} {
			name := fmt.Sprintf("desc %v, nulls first %v", o.desc, o.first)
			want := slices.Clone(invoices)
			slices.SortFunc(want, func(a, b testmodel.Invoice) int {
				sa, sb := sum[a.ID], sum[b.ID]
				c := placed(sa == nil, sb == nil, o.first, func() int { return cmp.Compare(*sa, *sb) }, o.desc)
				if c == 0 {
					c = cmp.Compare(a.ID, b.ID)
				}
				return c
			})
			term := I.Items.Sum(It.Qty)
			by := term.Asc()
			if o.desc {
				by = term.Desc()
			}
			if o.first {
				by = by.NullsFirst()
			} else {
				by = by.NullsLast()
			}
			q := tg.db.Query[testmodel.Invoice]().Where(I.ID.GTE(400)).OrderBy(by, I.ID.Asc())
			all, err := q.AllowUnbounded().All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			wantIDs(t, ids(all), ids(want))
			for n := 1; n <= 3; n++ {
				got := pageAll(t, q, n, len(invoices), func(x *testmodel.Invoice) int64 { return x.ID })
				if !slices.Equal(got, ids(want)) {
					t.Errorf("%s, %d a page:\n got %v\nwant %v", name, n, got, ids(want))
				}
			}
		}
	})
}
