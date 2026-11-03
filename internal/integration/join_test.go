package integration

import (
	"fmt"
	"testing"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// TestJoinReadsTheTargetThroughItsPolicy: a joined relation is read as With reads it,
// so a join shows a caller nothing a query on the target would not. LeftJoin keeps
// the ticket with no parent the caller may see (ann's ticket 5 has bob's ticket 3);
// Join leaves it out. The parents are TestScopeRelations's.
func TestJoinReadsTheTargetThroughItsPolicy(t *testing.T) {
	parents := map[string]map[int64]int64{
		"ann":    {1: 0, 2: 1, 5: 0},
		"admin":  {1: 0, 2: 1, 3: 0, 4: 3, 5: 3},
		"anyone": {1: 0, 2: 1, 3: 0, 4: 3, 5: 3, 6: 0, 7: 6, 8: 6},
	}
	hasParent := map[string][]int64{"ann": {2}, "admin": {2, 4, 5}, "anyone": {2, 4, 5, 7, 8}}
	eachCaller(t, func(t *testing.T, tg target, c who) {
		xs, err := tg.db.Query[testmodel.Ticket]().LeftJoin(Tk.Parent).OrderBy(Tk.ID.Asc()).Unbounded().All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		got := map[int64]int64{}
		for _, x := range xs {
			p, loaded := x.Parent.Get()
			if !loaded {
				t.Fatalf("ticket %d: parent not loaded", x.ID)
			}
			got[x.ID] = 0
			if p != nil {
				got[x.ID] = p.ID
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(parents[c.name]) {
			t.Errorf("LeftJoin(Parent): %v, want %v", got, parents[c.name])
		}

		inner, err := tg.db.Query[testmodel.Ticket]().Join(Tk.Parent).OrderBy(Tk.ID.Asc()).Unbounded().All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		wantIDs(t, ticketIDs(inner), hasParent[c.name])
		for _, x := range inner {
			if p, loaded := x.Parent.Get(); !loaded || p == nil {
				t.Errorf("Join(Parent): ticket %d has parent %v, loaded %v", x.ID, p, loaded)
			}
		}
		n, err := tg.db.Query[testmodel.Ticket]().Join(Tk.Parent).Count(c.ctx)
		if err != nil || n != int64(len(hasParent[c.name])) {
			t.Errorf("Count of Join(Parent): %d, %v", n, err)
		}
	})
}

// TestJoinFiltersAndOrdersByTheJoinedColumns: the feed query. Condition and order on
// the parent's columns, with the ticket's own, in one statement; every row has its
// parent filled from that statement.
func TestJoinFiltersAndOrdersByTheJoinedColumns(t *testing.T) {
	eachCaller(t, func(t *testing.T, tg target, c who) {
		if c.name != "anyone" {
			t.Skip("the other callers' rows are TestJoinReadsTheTargetThroughItsPolicy's")
		}
		// Tickets with a done parent: 2 (parent 1: not done), 4 (parent 3: done),
		// 5 (parent 3), 7 (parent 6: not done), 8 (parent 6). Order by the parent's
		// title, then the ticket's id descending.
		xs, err := tg.db.Query[testmodel.Ticket]().
			Join(Tk.Parent).
			Where(Tk.Parent.Is(Tk.Done.EQ(true))).
			OrderBy(Tk.Parent.By(Tk.Title.Asc()), Tk.ID.Desc()).
			Limit(10).All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		wantOrder(t, ticketIDs(xs), []int64{5, 4})
		for _, x := range xs {
			if p, ok := x.Parent.Get(); !ok || p == nil || p.ID != 3 || p.Title != "B1" {
				t.Errorf("ticket %d: parent %+v, loaded %v", x.ID, p, ok)
			}
		}

		// Both rows' columns are in one scan: the tickets whose parent has the same
		// owner (2 and 8 are ann's, 4 is bob's).
		var sameOwner []int64
		all, err := tg.db.Query[testmodel.Ticket]().LeftJoin(Tk.Parent).OrderBy(Tk.ID.Asc()).Unbounded().All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, x := range all {
			if p, _ := x.Parent.Get(); p != nil && p.Owner == x.Owner {
				sameOwner = append(sameOwner, x.ID)
			}
		}
		if fmt.Sprint(sameOwner) != "[2 4 8]" {
			t.Errorf("tickets with a parent of the same owner: %v, want [2 4 8]", sameOwner)
		}
	})
}

// TestJoinAgreesWithWith: for every caller, LeftJoin of the parent with the parent's
// invoice loaded gives what With gives for the same relations: the same parent, and the
// same invoice, each hidden or shown by its own policy.
func TestJoinAgreesWithWith(t *testing.T) {
	eachCaller(t, func(t *testing.T, tg target, c who) {
		view := func(xs []testmodel.Ticket) string {
			var out []string
			for _, x := range xs {
				p, ploaded := x.Parent.Get()
				s := fmt.Sprintf("%d:", x.ID)
				if !ploaded {
					out = append(out, s+"parent not loaded")
					continue
				}
				if p == nil {
					out = append(out, s+"no parent")
					continue
				}
				inv, iloaded := p.Invoice.Get()
				if !iloaded {
					out = append(out, s+fmt.Sprintf("parent %d, invoice not loaded", p.ID))
					continue
				}
				if inv == nil {
					out = append(out, s+fmt.Sprintf("parent %d, no invoice", p.ID))
					continue
				}
				out = append(out, s+fmt.Sprintf("parent %d, invoice %d", p.ID, inv.ID))
			}
			return fmt.Sprint(out)
		}
		with, err := tg.db.Query[testmodel.Ticket]().With(Tk.Parent.With(Tk.Invoice)).OrderBy(Tk.ID.Asc()).Unbounded().All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		joined, err := tg.db.Query[testmodel.Ticket]().LeftJoin(Tk.Parent.With(Tk.Invoice)).OrderBy(Tk.ID.Asc()).Unbounded().All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := view(joined), view(with); got != want {
			t.Errorf("LeftJoin:\n %s\nWith:\n %s", got, want)
		}
	})
}

// TestJoinAfterPagesByAJoinedColumn: pages of two, each after the last row of the one
// before, are the whole result in order. The tickets with a parent, by the parent's
// title and then their own id: 2, 7 and 8 (parents titled A1), then 4 and 5 (B1).
func TestJoinAfterPagesByAJoinedColumn(t *testing.T) {
	eachCaller(t, func(t *testing.T, tg target, c who) {
		if c.name != "anyone" {
			t.Skip("one caller sees every ticket")
		}
		q := tg.db.Query[testmodel.Ticket]().Join(Tk.Parent).OrderBy(Tk.Parent.By(Tk.Title.Asc()), Tk.ID.Asc()).Limit(2)
		var got []int64
		var last *testmodel.Ticket
		for range 5 {
			page := q
			if last != nil {
				page = page.After(last)
			}
			xs, err := page.All(c.ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(xs) == 0 {
				break
			}
			got = append(got, ticketIDs(xs)...)
			last = &xs[len(xs)-1]
		}
		wantOrder(t, got, []int64{2, 7, 8, 4, 5})
		// Count with After counts the rows past it.
		n, err := q.After(&testmodel.Ticket{}).Count(c.ctx)
		if err == nil {
			t.Errorf("After a row without the join counted %d", n)
		}
	})
}

func wantOrder(t *testing.T, got, want []int64) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestJoinInAPerOwnerRead: LoadEach and CountEach with a Join, on every database. The
// children of tickets 3 and 6 are read with their invoice joined: a child whose
// invoice the caller may not read is left out, before the window ranks the rows, so
// LimitEach(1) is the first child that has one. The result is what a read with With
// gives, minus the children With leaves without an invoice.
func TestJoinInAPerOwnerRead(t *testing.T) {
	eachCaller(t, func(t *testing.T, tg target, c who) {
		xs, err := tg.db.Query[testmodel.Ticket]().Where(Tk.ID.In(3, 6)).OrderBy(Tk.ID.Asc()).Unbounded().All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		owners := make([]*testmodel.Ticket, len(xs))
		for i := range xs {
			owners[i] = &xs[i]
		}
		plain, err := velox.LoadEach(c.ctx, tg.db.Query[testmodel.Ticket]().With(Tk.Invoice).OrderBy(Tk.ID.Asc()), Tk.Children, owners)
		if err != nil {
			t.Fatal(err)
		}
		// What the join must give: the children that have an invoice, as With reads it.
		want := make([][]int64, len(plain))
		wantInvoice := map[int64]int64{}
		for i, kids := range plain {
			want[i] = []int64{}
			for _, k := range kids {
				if inv, _ := k.Invoice.Get(); inv != nil {
					want[i] = append(want[i], k.ID)
					wantInvoice[k.ID] = inv.ID
				}
			}
		}
		check := func(name string, got [][]testmodel.Ticket, per int) {
			t.Helper()
			if len(got) != len(want) {
				t.Fatalf("%s: %d lists, want %d", name, len(got), len(want))
			}
			for i, kids := range got {
				w := want[i]
				if per > 0 && len(w) > per {
					w = w[:per]
				}
				gotIDs := []int64{}
				for _, k := range kids {
					gotIDs = append(gotIDs, k.ID)
					if inv, loaded := k.Invoice.Get(); !loaded || inv == nil || inv.ID != wantInvoice[k.ID] {
						t.Errorf("%s: ticket %d: invoice %+v, loaded %v, want %d", name, k.ID, inv, loaded, wantInvoice[k.ID])
					}
				}
				wantOrder(t, gotIDs, w)
			}
		}
		q := tg.db.Query[testmodel.Ticket]().Join(Tk.Invoice).OrderBy(Tk.ID.Asc())
		got, err := velox.LoadEach(c.ctx, q, Tk.Children, owners)
		if err != nil {
			t.Fatal(err)
		}
		check("no limit", got, 0)
		if got, err = velox.LoadEach(c.ctx, q, Tk.Children.LimitEach(1), owners); err != nil {
			t.Fatal(err)
		}
		check("LimitEach(1)", got, 1)
		// Ordered by the joined invoice's number, descending: invoice 5 before invoice 1.
		byInvoice := tg.db.Query[testmodel.Ticket]().Join(Tk.Invoice).OrderBy(Tk.Invoice.By(testmodel.Invoices.Number.Desc()), Tk.ID.Asc())
		if got, err = velox.LoadEach(c.ctx, byInvoice, Tk.Children.LimitEach(5), owners); err != nil {
			t.Fatal(err)
		}
		// The database's own order of the numbers: its collation is not Go's.
		sorted, err := tg.db.Query[testmodel.Invoice]().OrderBy(testmodel.Invoices.Number.Desc()).Unbounded().All(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		rank := map[int64]int{}
		for i, inv := range sorted {
			rank[inv.ID] = i
		}
		for i, kids := range got {
			if len(kids) != len(want[i]) {
				t.Errorf("by invoice: owner %d has %d children, want %d", owners[i].ID, len(kids), len(want[i]))
			}
			for k := 1; k < len(kids); k++ {
				a, _ := kids[k-1].Invoice.Get()
				b, _ := kids[k].Invoice.Get()
				if rank[a.ID] > rank[b.ID] {
					t.Errorf("by invoice: owner %d: %s before %s", owners[i].ID, a.Number, b.Number)
				}
			}
		}
		counts, err := velox.CountEach(c.ctx, q, Tk.Children, owners)
		if err != nil {
			t.Fatal(err)
		}
		for i, w := range want {
			if counts[i] != int64(len(w)) {
				t.Errorf("CountEach: owner %d has %d, want %d", owners[i].ID, counts[i], len(w))
			}
		}
	})
}
