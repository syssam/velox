package integration

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/apitest/fixture"
	"github.com/syssam/velox/internal/testmodel"
)

// pageAll reads q page by page, n rows a page, through After, and returns
// the ids in the order read.
func pageAll[E any](t *testing.T, q velox.Query[E], n, most int, id func(*E) int64) []int64 {
	t.Helper()
	var got []int64
	page, err := q.Limit(n).All(ctx)
	for ; err == nil && len(page) > 0; page, err = q.After(page[len(page)-1]).Limit(n).All(ctx) {
		for i := range page {
			got = append(got, id(page[i]))
		}
		if len(got) > most {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// TestRelAggregateAfterOnRealDatabases: invoices ordered by their items'
// count, with many ties, and by the sum of their quantities, NULL for an
// invoice with no item, each way, with the key ascending and descending:
// After pages every order exactly, a row once, on every dialect.
func TestRelAggregateAfterOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		invoices := make([]testmodel.Invoice, 7)
		for i := range invoices {
			invoices[i] = newInvoice(7, fmt.Sprintf("R-%02d", i))
			invoices[i].ID = int64(400 + i)
		}
		if err := tg.db.InsertAll(invoices).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		// Invoice i has i%3 items: 0, 1 or 2, so the counts tie and three
		// invoices sum to NULL.
		count, sum := map[int64]int64{}, map[int64]*int64{}
		var items []testmodel.Item
		for i, inv := range invoices {
			for k := range i % 3 {
				qty := int64(i + k)
				items = append(items, testmodel.Item{ID: int64(500 + len(items)), InvoiceID: inv.ID, SKU: fmt.Sprintf("R-%d-%d", i, k), Qty: qty})
				count[inv.ID]++
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
		for _, o := range []struct {
			bySum, desc, idDesc bool
		}{{false, false, false}, {false, true, false}, {false, false, true}, {true, false, false}, {true, true, false}, {true, true, true}} {
			name := fmt.Sprintf("sum %v, desc %v, id desc %v", o.bySum, o.desc, o.idDesc)
			want := slices.Clone(invoices)
			slices.SortFunc(want, func(a, b testmodel.Invoice) int {
				c := cmp.Compare(count[a.ID], count[b.ID])
				if o.bySum {
					c = nullsLastInt(sum[a.ID], sum[b.ID])
				}
				if o.desc {
					c = -c
				}
				if c == 0 {
					c = cmp.Compare(a.ID, b.ID)
					if o.idDesc {
						c = -c
					}
				}
				return c
			})
			term := I.Items.Count()
			if o.bySum {
				term = I.Items.Sum(It.Qty)
			}
			by, key := term.Asc(), I.ID.Asc()
			if o.desc {
				by = term.Desc()
			}
			if o.idDesc {
				key = I.ID.Desc()
			}
			q := tg.db.Query[testmodel.Invoice]().Where(I.ID.GTE(400)).OrderBy(by, key)
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

// nullsLastInt compares a and b with NULL after every value.
func nullsLastInt(a, b *int64) int {
	switch {
	case a == nil && b == nil:
		return 0
	case a == nil:
		return 1
	case b == nil:
		return -1
	}
	return cmp.Compare(*a, *b)
}

// TestRelAggregateHidesRowsOnRealDatabases: on each database, a relation's
// count leaves out the rows the target's interceptor and Read policy hide,
// directly and through the join entity, and a soft-deleted row; the books'
// tags of another org are not counted.
func TestRelAggregateHidesRowsOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		interceptorTables(t, tg)
		bg := context.Background()
		O := fixture.IOwners
		os, err := tg.pdb.Query[fixture.IOwner]().OrderBy(O.Tagged.Count().Desc(), O.Notes.Count().Desc(), O.ID.Desc()).AllowUnbounded().All(bg)
		if err != nil {
			t.Fatal(err)
		}
		// Visible notes: 1 of owner 1, 4 of owner 2; tagged: owner 1 has 1.
		wantIDs(t, ownerIDs(os), []int64{1, 2})
		os, err = tg.pdb.Query[fixture.IOwner]().Where(velox.Expr[fixture.IOwner]("? = ?", O.Notes.Count(), 1)).OrderBy(O.ID.Asc()).AllowUnbounded().All(bg)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, ownerIDs(os), []int64{1, 2})

		softTables(t, tg)
		in1 := testmodel.Org.With(bg, int64(1))
		if _, err := tg.pdb.Delete[fixture.SDoc]().Where(fixture.SDocs.ID.EQ(2)).Exec(in1); err != nil {
			t.Fatal(err)
		}
		so, err := tg.pdb.Query[fixture.SOwner]().Where(velox.Expr[fixture.SOwner]("? > ?", fixture.SOwners.Docs.Count(), 0)).OrderBy(fixture.SOwners.ID.Asc()).AllowUnbounded().All(in1)
		if err != nil {
			t.Fatal(err)
		}
		// Owner 1 keeps doc 1; doc 3 of owner 2 is org 2's.
		if len(so) != 1 || so[0].ID != 1 {
			t.Errorf("owners with docs, org 1: %v", so)
		}

		throughSetup(t, tg)
		bs, err := tg.on(in1).Query[book]().OrderBy(Books.Tags.Count().Desc(), Books.ID.Asc()).AllowUnbounded().All(in1)
		if err != nil {
			t.Fatal(err)
		}
		// Org 1 sees tags 1 and 2: book 1 has two, book 2 one; book 4's tag
		// is org 2's.
		got := []int64{}
		for _, b := range bs {
			got = append(got, b.ID)
		}
		wantIDs(t, got, []int64{1, 2, 3, 4})
		n, err := tg.on(in1).Query[book]().Where(velox.Expr[book]("? = ?", Books.Tags.Count(), 0)).Count(in1)
		if err != nil || n != 2 {
			t.Errorf("books with no tag of org 1: %d, %v; want 2", n, err)
		}
	})
}

// TestRelAggregateEdgeSchemaOnRealDatabases: through an edge schema, each
// player's teams count, page by page.
func TestRelAggregateEdgeSchemaOnRealDatabases(t *testing.T) {
	each(t, func(t *testing.T, tg target) {
		rosterSetup(t, tg)
		var want []int64
		for i, teams := range [][]any{{1, 2}, {}, {3}, {1, 2, 3}} {
			p := testmodel.Player{Name: fmt.Sprintf("p%d", i)}
			q := tg.db.Insert(&p)
			if len(teams) > 0 {
				q = q.Set(testmodel.Players.Teams.Add(teams...))
			}
			if err := q.Exec(ctx); err != nil {
				t.Fatal(err)
			}
			want = append(want, p.ID)
		}
		// Most teams first: p3 (3), p0 (2), p2 (1), p1 (0).
		want = []int64{want[3], want[0], want[2], want[1]}
		q := tg.db.Query[testmodel.Player]().OrderBy(testmodel.Players.Teams.Count().Desc(), testmodel.Players.ID.Asc())
		got := pageAll(t, q, 1, 4, func(p *testmodel.Player) int64 { return p.ID })
		wantIDs(t, got, want)
	})
}

func ownerIDs(os []*fixture.IOwner) []int64 {
	out := []int64{}
	for _, o := range os {
		out = append(out, o.ID)
	}
	return out
}
