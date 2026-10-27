package velox_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2"
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
	e := &velox.FakeExec{Results: [][][]any{
		{invoiceRow(1), invoiceRow(2), invoiceRow(3)},
		{itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1), itemRow(12, 1, "C", 1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
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
	e := &velox.FakeExec{Results: [][][]any{
		{itemRow(10, 1, "A", 1), itemRow(11, 1, "B", 1)},
		{invoiceRow(1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
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
	e := &velox.FakeExec{Results: [][][]any{
		{sampleRow(1, nil)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
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
	e := &velox.FakeExec{Results: [][][]any{
		{invoiceRow(1)},
		{itemRow(10, 1, "A", 1)},
		{invoiceRow(1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
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
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {}}}
	db := velox.OpenFake(velox.MySQL, e)
	if _, err := db.Query[testmodel.Invoice]().With(I.Items.Where(It.Qty.GT(0)).OrderBy(It.SKU.Desc())).Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if want := "SELECT `id`, `invoice_id`, `sku`, `qty` FROM `items` WHERE `invoice_id` IN (?) AND `qty` > ? ORDER BY `sku` DESC"; e.Log[1] != want {
		t.Errorf("\n got %s\nwant %s", e.Log[1], want)
	}
}

// TestWithChunksUnderTheParameterLimit is Review Focus 4.
func TestWithChunksUnderTheParameterLimit(t *testing.T) {
	// 64 parameters of headroom for hooks' conditions, one for the filter, two keys per chunk.
	defer velox.SetMaxParams(velox.Postgres, 64+3)()
	e := &velox.FakeExec{Results: [][][]any{
		{invoiceRow(1), invoiceRow(2), invoiceRow(3)},
		{itemRow(11, 1, "B", 1), itemRow(10, 1, "A", 1), itemRow(12, 2, "C", 1)},
		{itemRow(13, 3, "D", 1)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
	invs, err := db.Query[testmodel.Invoice]().With(I.Items.Where(It.Qty.GT(0))).Limit(10).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Log) != 3 || !strings.Contains(e.Log[1], "IN ($1, $2)") || !strings.Contains(e.Log[2], "IN ($1)") {
		t.Fatalf("%q", e.Log)
	}
	var got []string
	for _, inv := range invs {
		items, _ := inv.Items.Get()
		for _, it := range items {
			got = append(got, it.SKU)
		}
	}
	if !reflect.DeepEqual(got, []string{"B", "A", "C", "D"}) {
		t.Errorf("%v: every row once, in the order the database returned within each owner", got)
	}
}

func TestIterRefusesWith(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{})
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
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 2)}}}
	invs, err := velox.OpenFake(velox.Postgres, e).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
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
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {}}}
	invs, err := velox.OpenFake(velox.Postgres, e).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items, loaded := invs[0].Items.Get(); !loaded || items == nil || len(items) != 0 {
		t.Errorf("%#v %v", items, loaded)
	}
}

func TestWithMixedNilKeys(t *testing.T) {
	e := &velox.FakeExec{Results: [][][]any{
		{sampleRow(1, nil), sampleRow(2, int64(1))},
		{sampleRow(1, nil)},
	}}
	db := velox.OpenFake(velox.Postgres, e)
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
	e := &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, e)
	_, err := db.Query[testmodel.Item]().Omit(It.InvoiceID).With(It.Invoice).Limit(1).All(ctx)
	if err == nil || !strings.HasPrefix(err.Error(), "velox: testmodel.Item: With(Invoice) needs the column invoice_id selected") || e.Calls != 0 {
		t.Errorf("%v, calls %d", err, e.Calls)
	}
	// A Many is keyed by the owner's primary key.
	e = &velox.FakeExec{Results: [][][]any{{{int64(1)}}, {itemRow(10, 1, "A", 1)}}}
	invs, err := velox.OpenFake(velox.Postgres, e).Query[testmodel.Invoice]().Columns(I.ID).With(I.Items).Limit(1).All(ctx)
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
			e := &velox.FakeExec{}
			_, err := q(velox.OpenFake(velox.Postgres, e)).Limit(1).All(ctx)
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
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 1)}}}
	db := velox.OpenFake(velox.Postgres, e,
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
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {}}}
	_, err := velox.OpenFake(velox.Postgres, e, narrow(0, 2)).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
	if err == nil || !strings.Contains(err.Error(), "velox: testmodel.Item: a Rewrite hook left out the relation's key column invoice_id") {
		t.Errorf("%v", err)
	}
	e = &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {{int64(1), "A"}}}}
	invs, err := velox.OpenFake(velox.Postgres, e, narrow(1, 2)).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
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
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}}, Errs: []error{nil, boom}}
	_, err := velox.OpenFake(velox.Postgres, e).Query[testmodel.Invoice]().With(I.Items).Limit(1).All(ctx)
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
		e := &velox.FakeExec{Results: [][][]any{{{"INV-1"}}, {}}}
		db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
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
		e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {{"A"}}, {}}}
		db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
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
		e := &velox.FakeExec{Results: [][][]any{{sampleRow(2, int64(1))}, {{int64(1)}}, {}}}
		n := 0
		db := velox.OpenFake(velox.Postgres, e, velox.WithRewrite(func(_ context.Context, st *velox.Statement) error {
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
	e := &velox.FakeExec{Results: [][][]any{{invoiceRow(1), invoiceRow(2)}, {itemRow(10, 1, "A", 1)}}}
	db := velox.OpenFake(velox.Postgres, e)
	if _, err := db.Query[testmodel.Invoice]().With(I.Items).One(ctx); !errors.Is(err, velox.ErrNotSingular) {
		t.Fatalf("two rows: %v", err)
	}
	if len(e.Log) != 1 {
		t.Errorf("%d statements sent, want 1: %v", len(e.Log), e.Log)
	}
	e = &velox.FakeExec{Results: [][][]any{{invoiceRow(1)}, {itemRow(10, 1, "A", 1)}}}
	db = velox.OpenFake(velox.Postgres, e)
	inv, err := db.Query[testmodel.Invoice]().With(I.Items).One(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if items, loaded := inv.Items.Get(); !loaded || len(items) != 1 || len(e.Log) != 2 {
		t.Errorf("items %v loaded %v, %d statements", items, loaded, len(e.Log))
	}
}

// ovOwner has notes, and picks of notes through ovPick; ovNote's Read policy
// is an In of the values ovReading puts in the context: the fixtures of a
// load whose statement overflows the parameter limit once the policy's
// conditions are added.
type ovOwner struct {
	ID    int64
	Notes velox.Many[ovNote]
	Picks velox.Many[ovNote]
}

var ovOwners = struct {
	ID    velox.NumberCol[ovOwner, int64]
	Notes velox.Rel[ovOwner, ovNote]
	Picks velox.Rel[ovOwner, ovNote]
}{velox.NumberCol[ovOwner, int64]{I: 0}, velox.Rel[ovOwner, ovNote]{I: 0}, velox.Rel[ovOwner, ovNote]{I: 1}}

var ovOwnerMeta velox.MetaOnce[ovOwner]

func (*ovOwner) VeloxMeta() *velox.Meta[ovOwner] {
	return ovOwnerMeta.Load(func() velox.Desc[ovOwner] {
		return velox.Desc[ovOwner]{
			Table:      "ov_owners",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Edges:      []velox.EdgeDesc{velox.EdgeTo[ovNote]("Notes", 0, 1), velox.EdgeThrough[ovPick, ovNote]("Picks", 0, 0, 1, 0)},
			Addr:       func(x *ovOwner, i int) any { return &x.ID },
			EdgeAddr: func(x *ovOwner, i int) any {
				if i == 0 {
					return &x.Notes
				}
				return &x.Picks
			},
		}
	})
}

type ovNote struct {
	ID      int64
	OwnerID int64
	Rank    int64
}

var ovNotes = struct {
	ID      velox.NumberCol[ovNote, int64]
	OwnerID velox.NumberCol[ovNote, int64]
	Rank    velox.NumberCol[ovNote, int64]
}{velox.NumberCol[ovNote, int64]{I: 0}, velox.NumberCol[ovNote, int64]{I: 1}, velox.NumberCol[ovNote, int64]{I: 2}}

// ovKey's value, a []int64, is what ovNote's Read policy's In holds.
type ovKey struct{}

// ovReading is a context under which ovNote's Read policy is an In of n
// values, n parameters.
func ovReading(n int) context.Context {
	vs := make([]int64, n)
	for i := range vs {
		vs[i] = int64(i)
	}
	return context.WithValue(context.Background(), ovKey{}, vs)
}

func (*ovNote) Policy() velox.Policy[ovNote] {
	return velox.Policy[ovNote]{Read: func(ctx context.Context) (velox.Pred[ovNote], error) {
		if vs, ok := ctx.Value(ovKey{}).([]int64); ok {
			return ovNotes.Rank.In(vs...), nil
		}
		return velox.Allow[ovNote](), nil
	}}
}

var ovNoteMeta velox.MetaOnce[ovNote]

func (*ovNote) VeloxMeta() *velox.Meta[ovNote] {
	return ovNoteMeta.Load(func() velox.Desc[ovNote] {
		return velox.Desc[ovNote]{
			Policy:     func() velox.BoundPolicy { return velox.BindPolicy((*ovNote)(nil).Policy()) },
			Table:      "ov_notes",
			Columns:    []velox.ColumnDesc{{Field: "ID", Name: "id"}, {Field: "OwnerID", Name: "owner_id"}, {Field: "Rank", Name: "rank"}},
			PrimaryKey: []int{0},
			Tenant:     -1,
			Addr:       func(x *ovNote, i int) any { return [...]any{&x.ID, &x.OwnerID, &x.Rank}[i] },
		}
	})
}

// ovPick is the join entity of ovOwner.Picks.
type ovPick struct{ OwnerID, NoteID int64 }

var ovPickMeta velox.MetaOnce[ovPick]

func (*ovPick) VeloxMeta() *velox.Meta[ovPick] {
	return ovPickMeta.Load(func() velox.Desc[ovPick] {
		return velox.Desc[ovPick]{
			Table:      "ov_picks",
			Columns:    []velox.ColumnDesc{{Field: "OwnerID", Name: "owner_id"}, {Field: "NoteID", Name: "note_id"}},
			PrimaryKey: []int{0, 1},
			Tenant:     -1,
			Addr:       func(x *ovPick, i int) any { return [...]any{&x.OwnerID, &x.NoteID}[i] },
		}
	})
}

// ovSent records the parameters of every statement the Around hooks see.
type ovSent struct{ args []int }

func (s *ovSent) around(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
	s.args = append(s.args, len(st.Args))
	return next(ctx)
}

// ovRun opens a fake PostgreSQL database answering with results, whose
// Around hook records into the ovSent returned.
func ovRun(results [][][]any) (*velox.DB, *velox.FakeExec, *ovSent) {
	e := &velox.FakeExec{Results: results}
	s := &ovSent{}
	return velox.OpenFake(velox.Postgres, e, velox.WithAround(s.around)), e, s
}

// ovOwnerRows are owners 1–n.
func ovOwnerRows(n int) [][]any {
	var rows [][]any
	for id := int64(1); id <= int64(n); id++ {
		rows = append(rows, []any{id})
	}
	return rows
}

// checkSent fails t unless every statement the fake received was one the
// Around hooks saw, of at most 100 parameters, and the statements had want
// parameters each.
func checkSent(t *testing.T, e *velox.FakeExec, s *ovSent, want []int) {
	t.Helper()
	if e.Calls != len(s.args) {
		t.Errorf("%d statements reached the database, the Around hooks saw %d", e.Calls, len(s.args))
	}
	for i, n := range s.args {
		if n > 100 {
			t.Errorf("statement %d has %d parameters", i, n)
		}
	}
	if !reflect.DeepEqual(s.args, want) {
		t.Errorf("the statements' parameters %v, want %v", s.args, want)
	}
}

func TestLoadReChunksOnParameterOverflow(t *testing.T) {
	// keysPer leaves 36 keys: with the policy's 80 values, 116 parameters,
	// 16 too many, so the chunks hold 20 keys.
	defer velox.SetMaxParams(velox.Postgres, 100)()
	pctx := ovReading(80)
	owners := func(t *testing.T, db *velox.DB, r velox.Rel[ovOwner, ovNote]) []ovOwner {
		t.Helper()
		os, err := db.Query[ovOwner]().OrderBy(ovOwners.ID.Asc()).With(r).Limit(50).All(pctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(os) != 50 {
			t.Fatalf("%d owners", len(os))
		}
		return os
	}
	ids := func(t *testing.T, o ovOwner, m velox.Many[ovNote]) []int64 {
		t.Helper()
		ns, ok := m.Get()
		if !ok {
			t.Fatalf("owner %d: not loaded", o.ID)
		}
		out := []int64{}
		for _, n := range ns {
			out = append(out, n.ID)
		}
		return out
	}
	t.Run("one-to-many", func(t *testing.T) {
		// Owner o's note is 1000+o.
		notes := func(lo, hi int64) [][]any {
			var rows [][]any
			for o := lo; o <= hi; o++ {
				rows = append(rows, []any{1000 + o, o, int64(0)})
			}
			return rows
		}
		db, e, s := ovRun([][][]any{ovOwnerRows(50), notes(1, 20), notes(21, 40), notes(41, 50)})
		for _, o := range owners(t, db, ovOwners.Notes) {
			if got := ids(t, o, o.Notes); !reflect.DeepEqual(got, []int64{1000 + o.ID}) {
				t.Errorf("owner %d: notes %v", o.ID, got)
			}
		}
		checkSent(t, e, s, []int{1, 100, 100, 90}) // the owners' LIMIT, then the notes'
		// Without hooks, exec's check splits the chunk the same way.
		e = &velox.FakeExec{Results: [][][]any{ovOwnerRows(50), notes(1, 20), notes(21, 40), notes(41, 50)}}
		for _, o := range owners(t, velox.OpenFake(velox.Postgres, e), ovOwners.Notes) {
			if got := ids(t, o, o.Notes); !reflect.DeepEqual(got, []int64{1000 + o.ID}) {
				t.Errorf("without hooks: owner %d: notes %v", o.ID, got)
			}
		}
		if e.Calls != 4 {
			t.Errorf("without hooks: %d statements sent, want 4", e.Calls)
		}
	})
	t.Run("many-to-many", func(t *testing.T) {
		// Owner o picks notes 10o+1 and 10o+2, ranked 1 and 2: two keys per
		// owner, so 10 owners per chunk. The fake returns each owner's in
		// the order OrderBy(Rank.Desc()) gives.
		links := func(lo, hi int64) [][]any {
			var rows [][]any
			for o := lo; o <= hi; o++ {
				rows = append(rows, []any{o, 10*o + 1}, []any{o, 10*o + 2})
			}
			return rows
		}
		targets := func(lo int64) [][]any {
			var rows [][]any
			for o := lo; o < lo+10; o++ {
				rows = append(rows, []any{10*o + 2, int64(0), int64(2)}, []any{10*o + 1, int64(0), int64(1)})
			}
			return rows
		}
		db, e, s := ovRun([][][]any{ovOwnerRows(50), links(1, 36), links(37, 50), targets(1), targets(11), targets(21), targets(31), targets(41)})
		for _, o := range owners(t, db, ovOwners.Picks.OrderBy(ovNotes.Rank.Desc())) {
			if got, want := ids(t, o, o.Picks), []int64{10*o.ID + 2, 10*o.ID + 1}; !reflect.DeepEqual(got, want) {
				t.Errorf("owner %d: picks %v, want %v", o.ID, got, want)
			}
		}
		// The join rows have no policy: 36 keys, then 14.
		checkSent(t, e, s, []int{1, 36, 14, 100, 100, 100, 100, 100})
	})
	t.Run("many-to-many with LimitEach", func(t *testing.T) {
		// The limit is a parameter too: 117, 17 too many, 19 keys a chunk.
		picks := func(lo, hi int64) [][]any {
			var rows [][]any
			for o := lo; o <= hi; o++ {
				rows = append(rows, []any{10*o + 1, int64(0), int64(1), o})
			}
			return rows
		}
		db, e, s := ovRun([][][]any{ovOwnerRows(50), picks(1, 19), picks(20, 38), picks(39, 50)})
		for _, o := range owners(t, db, ovOwners.Picks.LimitEach(1)) {
			if got := ids(t, o, o.Picks); !reflect.DeepEqual(got, []int64{10*o.ID + 1}) {
				t.Errorf("owner %d: picks %v", o.ID, got)
			}
		}
		checkSent(t, e, s, []int{1, 100, 100, 93})
	})
	// picksOf are owner o's n picks, notes 100o+1 to 100o+n: its join rows
	// (links) and its targets (notes).
	links := func(o, n int64) [][]any {
		var rows [][]any
		for i := int64(1); i <= n; i++ {
			rows = append(rows, []any{o, 100*o + i})
		}
		return rows
	}
	notes := func(o, n int64) [][]any {
		var rows [][]any
		for i := int64(1); i <= n; i++ {
			rows = append(rows, []any{100*o + i, int64(0), int64(0)})
		}
		return rows
	}
	t.Run("many-to-many, a later chunk overflows", func(t *testing.T) {
		// The policy's 70 values leave 30 keys. Owners 1, 2 and 3 have 20,
		// 20 and 16 picks: owner 1's chunk is read; owners 2 and 3's, 36
		// keys, overflows and is not sent. The chunks are planned again from
		// owner 2 only, 30 keys a chunk: owner 1's is not read again.
		join := slices.Concat(links(1, 20), links(2, 20), links(3, 16))
		db, e, s := ovRun([][][]any{ovOwnerRows(3), join, notes(1, 20), notes(2, 20), notes(3, 16)})
		os, err := db.Query[ovOwner]().OrderBy(ovOwners.ID.Asc()).With(ovOwners.Picks).Limit(3).All(ovReading(70))
		if err != nil {
			t.Fatal(err)
		}
		for i, o := range os {
			if got := ids(t, o, o.Picks); len(got) != []int{20, 20, 16}[i] || got[0] != 100*o.ID+1 {
				t.Errorf("owner %d: picks %v", o.ID, got)
			}
		}
		checkSent(t, e, s, []int{1, 3, 90, 90, 86})
		for i := range e.ArgLog {
			for j := range i {
				if reflect.DeepEqual(e.ArgLog[i], e.ArgLog[j]) {
					t.Errorf("statements %d and %d were sent with the same arguments %v", j, i, e.ArgLog[i])
				}
			}
		}
	})
	t.Run("many-to-many, an owner too many for the smaller chunks", func(t *testing.T) {
		// Owner 1's 32 picks overflow by 2 with the policy's 70 values:
		// 30 keys a chunk cannot hold them, and the error says why.
		db, e, s := ovRun([][][]any{ovOwnerRows(1), links(1, 32)})
		_, err := db.Query[ovOwner]().With(ovOwners.Picks).Limit(1).All(ovReading(70))
		const want = "velox: velox_test.ovOwner: relation Picks: an owner has 32 related rows, more than one statement can load in order: velox: velox_test.ovNote: the statement has 102 parameters; postgres allows 100"
		if err == nil || err.Error() != want {
			t.Errorf("err = %v,\nwant %s", err, want)
		}
		checkSent(t, e, s, []int{1, 1})
	})
	t.Run("many-to-many, one key cannot fit", func(t *testing.T) {
		// One pick an owner: the chunks shrink to one key, which still
		// overflows, and the load fails with the parameter error.
		join := slices.Concat(links(1, 1), links(2, 1))
		db, e, s := ovRun([][][]any{ovOwnerRows(2), join})
		_, err := db.Query[ovOwner]().OrderBy(ovOwners.ID.Asc()).With(ovOwners.Picks).Limit(2).All(ovReading(100))
		if err == nil || !strings.HasPrefix(err.Error(), "velox: velox_test.ovOwner: loading Picks: ") || !strings.Contains(err.Error(), "the statement has 101 parameters; postgres allows 100") {
			t.Errorf("%v", err)
		}
		checkSent(t, e, s, []int{1, 2})
	})
	t.Run("one key cannot fit", func(t *testing.T) {
		db, e, s := ovRun([][][]any{ovOwnerRows(50)})
		_, err := db.Query[ovOwner]().OrderBy(ovOwners.ID.Asc()).With(ovOwners.Notes).Limit(50).All(ovReading(100))
		if err == nil || !strings.Contains(err.Error(), "the statement has 101 parameters; postgres allows 100") {
			t.Errorf("%v", err)
		}
		if e.Calls != 1 || len(s.args) != 1 {
			t.Errorf("%d statements sent, %d seen by the Around hooks; want the owners' only", e.Calls, len(s.args))
		}
	})
}

// TestWithLoadPinSurvivesARewrite: a Rewrite hook that replaces the whole
// statement (*st = velox.Statement{…}) does not unpin a With load, which
// still reads the replica its query read.
func TestWithLoadPinSurvivesARewrite(t *testing.T) {
	row := [][]any{sampleRow(1, int64(1))} // its own parent
	r0, r1 := &velox.FakeExec{Rows: row}, &velox.FakeExec{}
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{},
		velox.WithReplicas(velox.FakeConn(r0), velox.FakeConn(r1)),
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
