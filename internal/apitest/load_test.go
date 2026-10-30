package apitest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/fakeconn"
	"github.com/syssam/velox2/internal/testmodel"
)

// invoiceRow is declared in query_test.go; It is declared there too.

func itemRow(id, invoice int64, sku string, qty int64) []any {
	return []any{id, invoice, sku, qty}
}

// sampleRow is a full Sample row for the fake executor, in column order, with
// ParentID set to parentID (nil or an int64).
func sampleRow(id int64, parentID any) []any {
	return []any{id, "n", "s", nil, int64(0), nil, false, nil, t0, nil, []byte("b"), parentID, int64(0)}
}

// TestWithAttachesToOwners is half of Review Focus 2.
func TestWithAttachesToOwners(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{invoiceRow(1), invoiceRow(2), invoiceRow(3)},
		{itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1), itemRow(12, 1, "C", 1)},
	}}
	db := openFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := `SELECT "id", "invoice_id", "sku", "qty" FROM "items" WHERE "invoice_id" IN ($1, $2, $3) ORDER BY "id"`; e.Log[1] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[1], want)
	}
	skus := func(i int) []string {
		items, loaded := invs[i].Items.Get()
		if !loaded || items == nil {
			t.Fatalf("invoice %d: loaded %v, items %v", i, loaded, items)
		}
		var out []string
		for _, it := range items {
			out = append(out, it.SKU)
		}
		return out
	}
	if !reflect.DeepEqual(skus(0), []string{"A", "C"}) || !reflect.DeepEqual(skus(1), []string{"B"}) || len(skus(2)) != 0 {
		t.Errorf("%v %v %v", skus(0), skus(1), skus(2))
	}
}

func TestWithBelongsToSharesAndSkipsNil(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{itemRow(10, 1, "A", 1), itemRow(11, 1, "B", 1)},
		{invoiceRow(1)},
	}}
	db := openFake(velox.Postgres, e)
	items, err := db.Query[testmodel.Item]().With(It.Invoice).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	a, okA := items[0].Invoice.Get()
	b, okB := items[1].Invoice.Get()
	if !okA || !okB || a == nil || a != b || a.ID != 1 {
		t.Errorf("%v %v %v %v", a, b, okA, okB)
	}
	if !strings.Contains(e.Log[1], `WHERE "id" IN ($1)`) {
		t.Errorf("keys not deduplicated: %s", e.Log[1])
	}
}

func TestWithNullableKeyLoadsNothing(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{sampleRow(1, nil)},
	}}
	db := openFake(velox.Postgres, e)
	ss, err := db.Query[testmodel.Sample]().With(testmodel.Samples.Parent).Limit(10).All(ctx)
	if err != nil || e.Calls != 1 {
		t.Fatalf("%v, calls %d (a nil key must send nothing)", err, e.Calls)
	}
	if p, loaded := ss[0].Parent.Get(); !loaded || p != nil {
		t.Errorf("%v %v", p, loaded)
	}
}

// TestWithNestedLoopsBack is half of Review Focus 1.
func TestWithNestedLoopsBack(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{invoiceRow(1)},
		{itemRow(10, 1, "A", 1)},
		{invoiceRow(1)},
	}}
	db := openFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items.With(It.Invoice)).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := invs[0].Items.Get()
	back, loaded := items[0].Invoice.Get()
	if !loaded || back == nil || back.ID != 1 {
		t.Errorf("%v %v", back, loaded)
	}
}

func TestWithFilterAndOrder(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {}}}
	db := openFake(velox.MySQL, e)
	if _, err := db.Query[testmodel.Invoice]().With(I.Items.Where(It.Qty.GT(0)).OrderBy(It.SKU.Desc())).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := "SELECT `id`, `invoice_id`, `sku`, `qty` FROM `items` WHERE `invoice_id` IN (?) AND `qty` > ? ORDER BY `sku` DESC"; e.Log[1] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[1], want)
	}
}

// TestWithManyOwnersDoNotShareRows: the owners' slices share one array, so
// each is capped at its length; an append to one, or a change to one of its
// rows, leaves the next owner's rows as they were.
func TestWithManyOwnersDoNotShareRows(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{invoiceRow(1), invoiceRow(2)},
		{itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1)},
	}}
	db := openFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, _ := invs[0].Items.Get()
	second, _ := invs[1].Items.Get()
	if len(first) != 1 || cap(first) != 1 {
		t.Fatalf("len %d cap %d, want 1 and 1", len(first), cap(first))
	}
	first[0].SKU = "changed"
	_ = append(first, testmodel.Item{SKU: "appended"})
	if len(second) != 1 || second[0].SKU != "B" {
		t.Errorf("the second owner's rows are %v, want one row B", second)
	}
}

// TestWithManyRowsAcrossBlocks reads more rows than one array of the scan's
// blocks holds, and than the arrays inside it: every row arrives once, in
// order, with its own values.
func TestWithManyRowsAcrossBlocks(t *testing.T) {
	const n = 1500
	items := make([][]any, n)
	for i := range items {
		items[i] = itemRow(int64(i+1), 1, fmt.Sprint(i), int64(i))
	}
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, items}}
	db := openFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := invs[0].Items.Get()
	if len(got) != n {
		t.Fatalf("%d rows, want %d", len(got), n)
	}
	for i, it := range got {
		if it.ID != int64(i+1) || it.SKU != fmt.Sprint(i) || it.Qty != int64(i) {
			t.Fatalf("row %d is %+v", i, it)
		}
	}
}

// TestWithOneAcrossBlocks: a to-one load's targets span the scan's arrays, and
// each owner points at its own.
func TestWithOneAcrossBlocks(t *testing.T) {
	const n = 300
	items := make([][]any, n)
	invs := make([][]any, n)
	for i := range n {
		items[i] = itemRow(int64(i+1), int64(i+1), "s", 1)
		invs[i] = invoiceRow(int64(i + 1))
	}
	e := &fakeconn.Exec{Results: [][][]any{items, invs}}
	db := openFake(velox.Postgres, e)
	got, err := db.Query[testmodel.Item]().With(It.Invoice).Limit(n).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, it := range got {
		inv, ok := it.Invoice.Get()
		if !ok || inv == nil || inv.ID != it.InvoiceID || inv.Number != fmt.Sprintf("INV-%d", it.InvoiceID) {
			t.Fatalf("item %d: invoice %+v, loaded %v", i, inv, ok)
		}
	}
}

// TestWithManyOwnersAcrossBlocks: owners of three rows each, read one owner
// after another, so that some owners' rows lie in one of the scan's arrays and
// some straddle two; and owners whose rows are read out of turn. Each owner has
// its own rows, in order, capped at its length.
func TestWithManyOwnersAcrossBlocks(t *testing.T) {
	const owners = 100
	for _, interleaved := range []bool{false, true} {
		t.Run(fmt.Sprintf("interleaved=%v", interleaved), func(t *testing.T) {
			var invs, items [][]any
			for o := range owners {
				invs = append(invs, invoiceRow(int64(o+1)))
			}
			id := int64(0)
			for o := range owners {
				for k := range 3 {
					owner := int64(o + 1)
					if interleaved && k == 1 {
						owner = int64(owners - o) // the middle row is another owner's
					}
					id++
					items = append(items, itemRow(id, owner, fmt.Sprintf("%d-%d", owner, id), int64(k)))
				}
			}
			want := map[int64][]int64{} // each owner's item ids, in order
			for _, it := range items {
				want[it[1].(int64)] = append(want[it[1].(int64)], it[0].(int64))
			}
			e := &fakeconn.Exec{Results: [][][]any{invs, items}}
			db := openFake(velox.Postgres, e)
			got, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(owners).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, inv := range got {
				xs, _ := inv.Items.Get()
				var ids []int64
				for _, it := range xs {
					if it.InvoiceID != inv.ID || it.SKU != fmt.Sprintf("%d-%d", inv.ID, it.ID) {
						t.Fatalf("invoice %d has item %+v", inv.ID, it)
					}
					ids = append(ids, it.ID)
				}
				if !slices.Equal(ids, want[inv.ID]) || cap(xs) != len(xs) {
					t.Fatalf("invoice %d: items %v cap %d, want %v capped", inv.ID, ids, cap(xs), want[inv.ID])
				}
			}
		})
	}
}

// TestLoadEachOwnersOfOneKeyDoNotShareRows: two owners with the same key get
// the same rows, but not the same memory.
func TestLoadEachOwnersOfOneKeyDoNotShareRows(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{itemRow(10, 1, "A", 1), itemRow(11, 1, "B", 1)}}
	db := openFake(velox.Postgres, e)
	got, err := velox.LoadEach(ctx, db.Query[testmodel.Item](), I.Items, []*testmodel.Invoice{{ID: 1}, {ID: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || len(got[0]) != 2 || len(got[1]) != 2 {
		t.Fatalf("got %v", got)
	}
	got[0][0].SKU = "changed"
	if got[1][0].SKU != "A" {
		t.Errorf("the second owner's first row is %+v, want SKU A", got[1][0])
	}
}

func TestIterRefusesWith(t *testing.T) {
	db := openFake(velox.Postgres, &fakeconn.Exec{})
	n := 0
	for _, err := range db.Query[testmodel.Invoice]().With(I.Items).Unbounded().Iter(ctx) {
		n++
		if err == nil || !strings.Contains(err.Error(), "Iter does not load relations") {
			t.Errorf("%v", err)
		}
	}
	if n != 1 {
		t.Errorf("yielded %d times, want the error once", n)
	}
}

func TestRelationsInJSON(t *testing.T) {
	type out struct {
		Items velox.Many[testmodel.Item] `json:",omitzero"`
	}
	b, err := json.Marshal(out{})
	if err != nil || string(b) != `{}` {
		t.Errorf("not loaded: %s", b)
	}
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 2)}}}
	invs, err := openFake(velox.Postgres, e).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	b, err = json.Marshal(out{Items: invs[0].Items})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"Items":[{"id":10,"invoice_id":1,"SKU":"A","qty":2}]}` {
		t.Errorf("loaded: %s", b)
	}
}

func TestWithManyWithoutRowsIsLoadedAndEmpty(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {}}}
	invs, err := openFake(velox.Postgres, e).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items, loaded := invs[0].Items.Get(); !loaded || items == nil || len(items) != 0 {
		t.Errorf("%#v %v", items, loaded)
	}
}

func TestWithMixedNilKeys(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{
		{sampleRow(1, nil), sampleRow(2, int64(1))},
		{sampleRow(1, nil)},
	}}
	db := openFake(velox.Postgres, e)
	ss, err := db.Query[testmodel.Sample]().With(testmodel.Samples.Parent).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(e.Log[1], `WHERE "id" IN ($1)`) || !reflect.DeepEqual(e.Args, []any{int64(1)}) {
		t.Errorf("one key expected: %s %v", e.Log[1], e.Args)
	}
	if p, loaded := ss[0].Parent.Get(); !loaded || p != nil {
		t.Errorf("nil key: %v %v", p, loaded)
	}
	if p, loaded := ss[1].Parent.Get(); !loaded || p == nil || p.ID != 1 {
		t.Errorf("key 1: %v %v", p, loaded)
	}
}

// TestWithNeedsTheKeyColumn: a projection that leaves out the key a relation
// is loaded by is refused before anything runs.
func TestWithNeedsTheKeyColumn(t *testing.T) {
	e := &fakeconn.Exec{}
	db := openFake(velox.Postgres, e)
	_, err := db.Query[testmodel.Item]().Omit(It.InvoiceID).With(It.Invoice).Limit(1).All(ctx)
	if err == nil || !strings.HasPrefix(err.Error(), "velox: testmodel.Item: With(Invoice) needs the column invoice_id selected") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
	// A Many is keyed by the owner's primary key.
	e = &fakeconn.Exec{Results: [][][]any{{{int64(1)}}, {itemRow(10, 1, "A", 1)}}}
	invs, err := openFake(velox.Postgres, e).Query[testmodel.Invoice]().Columns(I.ID).With(I.Items).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items, loaded := invs[0].Items.Get(); !loaded || len(items) != 1 || invs[0].Number != "" {
		t.Errorf("%v %v %+v", items, loaded, invs[0])
	}
}

// TestWithBadOptionsFailBeforeRunning: a mistake in the With options is
// reported even when the query returns no rows, and nothing is sent.
func TestWithBadOptionsFailBeforeRunning(t *testing.T) {
	bad := velox.Rel[testmodel.Invoice, testmodel.Item]{I: 9}.Has() // a condition on Invoice that carries an error
	for name, q := range map[string]func(*velox.DB) velox.Query[testmodel.Invoice]{
		"invalid nested condition": func(db *velox.DB) velox.Query[testmodel.Invoice] {
			return db.Query[testmodel.Invoice]().With(I.Items.With(It.Invoice.Where(bad)))
		},
		"invalid condition": func(db *velox.DB) velox.Query[testmodel.Invoice] {
			return db.Query[testmodel.Invoice]().With(I.Items.Where(velox.Rel[testmodel.Item, testmodel.Invoice]{I: 9}.Has()))
		},
		"relation out of range": func(db *velox.DB) velox.Query[testmodel.Invoice] {
			return db.Query[testmodel.Invoice]().With(velox.Rel[testmodel.Invoice, testmodel.Item]{I: 9})
		},
		"nested relation out of range": func(db *velox.DB) velox.Query[testmodel.Invoice] {
			return db.Query[testmodel.Invoice]().With(I.Items.With(velox.Rel[testmodel.Item, testmodel.Invoice]{I: 9}))
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := &fakeconn.Exec{}
			_, err := q(openFake(velox.Postgres, e)).Limit(1).All(ctx)
			if err == nil || !strings.HasPrefix(err.Error(), "velox: ") || e.Calls != 0 {
				t.Errorf("%v, calls %d", err, e.Calls)
			}
		})
	}
}

// TestWithThroughHooks: relation queries run the Rewrite and Around hooks;
// a Rewrite hook that narrows the columns must keep the relation's key.
func TestWithThroughHooks(t *testing.T) {
	rewrites, arounds := map[string]int{}, 0
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 1)}}}
	db := openFake(velox.Postgres, e,
		velox.WithRewrite(func(_ context.Context, st *velox.Statement) error { rewrites[st.Entity.Name]++; return nil }),
		velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
			arounds++
			return next(ctx)
		}))
	if _, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if rewrites["testmodel.Invoice"] != 1 || rewrites["testmodel.Item"] != 1 || arounds != 2 {
		t.Errorf("%v, %d arounds", rewrites, arounds)
	}
}

func TestWithRewriteNarrowsColumns(t *testing.T) {
	narrow := func(cols ...int) velox.Option {
		return velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Entity.Name == "testmodel.Item" {
				st.Columns = cols
			}
			return nil
		})
	}
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {}}}
	_, err := openFake(velox.Postgres, e, narrow(0, 2)).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "velox: testmodel.Item: a Rewrite hook left out the relation's key column invoice_id") {
		t.Errorf("%v", err)
	}
	e = &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {{int64(1), "A"}}}}
	invs, err := openFake(velox.Postgres, e, narrow(1, 2)).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := invs[0].Items.Get()
	if len(items) != 1 || items[0].SKU != "A" || items[0].InvoiceID != 1 || items[0].ID != 0 || items[0].Qty != 0 {
		t.Errorf("%+v", items)
	}
}

func TestWithRelationQueryErrorIsWrapped(t *testing.T) {
	boom := errors.New("boom")
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}}, Errs: []error{nil, boom}}
	_, err := openFake(velox.Postgres, e).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if !errors.Is(err, boom) || !strings.HasPrefix(err.Error(), "velox: testmodel.Invoice: loading Items: ") {
		t.Errorf("%v", err)
	}
}

// TestWithRewriteCannotDropOwnerKeys: a Rewrite hook that narrows a statement's
// columns after the loads were checked must not leave out the column a loaded
// relation is keyed by; the keys would read as zero values and the relation
// would load empty without an error.
func TestWithRewriteCannotDropOwnerKeys(t *testing.T) {
	t.Run("owner query", func(t *testing.T) {
		e := &fakeconn.Exec{Results: [][][]any{{{"INV-1"}}, {}}}
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Entity.Name == "testmodel.Invoice" {
				st.Columns = []int{4} // number: leaves out id
			}
			return nil
		}))
		_, err := db.Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), "needs the column id selected") || e.Calls != 1 {
			t.Errorf("%v, calls %d", err, e.Calls)
		}
	})
	t.Run("relation query, key the load is fetched by", func(t *testing.T) {
		e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {{"A"}}, {}}}
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Entity.Name == "testmodel.Item" {
				st.Columns = []int{2} // sku: leaves out invoice_id
			}
			return nil
		}))
		_, err := db.Query[testmodel.Invoice]().With(I.Items.With(It.Invoice)).Limit(1).All(ctx)
		if err == nil || !strings.HasPrefix(err.Error(), "velox: ") {
			t.Errorf("%v", err)
		}
	})
	t.Run("relation query, key a nested load needs", func(t *testing.T) {
		// Sample.Parent twice: the second Sample statement is keyed by id and
		// the nested load by parent_id, which the hook leaves out.
		e := &fakeconn.Exec{Results: [][][]any{{sampleRow(2, int64(1))}, {{int64(1)}}, {}}}
		n := 0
		db := openFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			if st.Entity.Name == "testmodel.Sample" {
				if n++; n == 2 {
					st.Columns = []int{0} // id: leaves out parent_id
				}
			}
			return nil
		}))
		P := testmodel.Samples.Parent
		_, err := db.Query[testmodel.Sample]().With(P.With(P)).Limit(1).All(ctx)
		if err == nil || !strings.Contains(err.Error(), "needs the column parent_id selected") {
			t.Errorf("%v", err)
		}
	})
}

// TestOneLoadsNothingWhenNotSingular: One reads its rows before it loads, so
// a result it refuses costs no load statement.
func TestOneLoadsNothingWhenNotSingular(t *testing.T) {
	e := &fakeconn.Exec{Results: [][][]any{{invoiceRow(1), invoiceRow(2)}, {itemRow(10, 1, "A", 1)}}}
	db := openFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Invoice]().With(I.Items).One(ctx); !errors.Is(err, velox.ErrNotSingular) {
		t.Fatalf("two rows: %v", err)
	}
	if len(e.Log) != 1 {
		t.Errorf("%d statements sent, want 1: %v", len(e.Log), e.Log)
	}
	e = &fakeconn.Exec{Results: [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 1)}}}
	db = openFake(velox.Postgres, e)
	inv, err := db.Query[testmodel.Invoice]().With(I.Items).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items, loaded := inv.Items.Get(); !loaded || len(items) != 1 || len(e.Log) != 2 {
		t.Errorf("items %v loaded %v, %d statements", items, loaded, len(e.Log))
	}
}

// TestWithLoadPinSurvivesARewrite: a Rewrite hook that replaces the whole
// statement (*st = velox.Statement{…}) does not unpin a With load, which
// still reads the replica its query read.
func TestWithLoadPinSurvivesARewrite(t *testing.T) {
	row := [][]any{sampleRow(1, int64(1))} // its own parent
	r0, r1 := &fakeconn.Exec{Rows: row}, &fakeconn.Exec{}
	db := openFake(velox.Postgres, &fakeconn.Exec{},
		velox.WithReplicas(fakeConn(r0), fakeConn(r1)),
		velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
			freshStatement(st)
			return nil
		}))
	ss, err := db.Query[testmodel.Sample]().With(testmodel.Samples.Parent).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, loaded := ss[0].Parent.Get(); !loaded {
		t.Error("the parent is not loaded")
	}
	if r0.Calls != 2 || r1.Calls != 0 {
		t.Errorf("replica 0 read %d times, replica 1 %d; want the query and its load on replica 0", r0.Calls, r1.Calls)
	}
}
