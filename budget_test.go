package velox_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// TestOneRowAllocationBudget pins the one-row budget: a one-row query makes at most 8
// more allocations than hand-written code scanning the same rows.
func TestOneRowAllocationBudget(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e)
	run := func() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run() // fill the render cache
	vx := testing.AllocsPerRun(200, run)
	text, args := e.SQL, e.Args
	raw := testing.AllocsPerRun(200, func() {
		r, err := velox.RawRows(db, text, args)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]testmodel.Invoice, 0, 1)
		for r.Next() {
			out = append(out, testmodel.Invoice{})
			x := &out[len(out)-1]
			if err := r.Scan(&x.ID, &x.CreatedAt, &x.UpdatedAt, &x.OrgID, &x.Number, &x.Total, &x.Paid, &x.PaidAt, &x.Note, &x.Data); err != nil {
				t.Fatal(err)
			}
		}
		r.Close()
	})
	t.Logf("velox %.0f allocations, hand-written %.0f", vx, raw)
	if vx > raw+8 {
		t.Errorf("velox makes %.0f allocations for a one-row query and hand-written code %.0f: over the budget of +8", vx, raw)
	}
}

func BenchmarkOneRow(b *testing.B) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e)
	b.ReportAllocs()
	for b.Loop() {
		e.Log = e.Log[:0] // the fake keeps every statement's text: do not time the heap growing
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// TestTenantScopedOneRowBudget pins the tenant budget: a tenant-scoped
// one-row read makes at most one allocation more than Core's budget, the
// append of the tenant condition.
func TestTenantScopedOneRowBudget(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e)
	t1 := velox.WithTenant(context.Background(), int64(1))
	run := func() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(t1); err != nil {
			t.Fatal(err)
		}
	}
	run() // fill the render cache
	vx := testing.AllocsPerRun(200, run)
	text, args := e.SQL, e.Args
	if !strings.Contains(text, `"org_id" = $2`) {
		t.Fatalf("the read is not scoped: %s", text)
	}
	raw := testing.AllocsPerRun(200, func() {
		r, err := velox.RawRows(db, text, args)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]testmodel.Invoice, 0, 1)
		for r.Next() {
			out = append(out, testmodel.Invoice{})
			x := &out[len(out)-1]
			if err := r.Scan(&x.ID, &x.CreatedAt, &x.UpdatedAt, &x.OrgID, &x.Number, &x.Total, &x.Paid, &x.PaidAt, &x.Note, &x.Data); err != nil {
				t.Fatal(err)
			}
		}
		r.Close()
	})
	t.Logf("velox %.0f allocations, hand-written %.0f", vx, raw)
	if vx > raw+9 {
		t.Errorf("velox makes %.0f allocations for a tenant-scoped one-row query and hand-written code %.0f: over the budget of +9", vx, raw)
	}
}

// TestTenantConvertedOneRowBudget: a tenant given as an int, which the scope
// converts to the column's int64, costs at most one allocation more than the
// exact type's TestTenantScopedOneRowBudget.
func TestTenantConvertedOneRowBudget(t *testing.T) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e)
	allocs := func(id any) float64 {
		ctx := velox.WithTenant(context.Background(), id)
		run := func() {
			if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
				t.Fatal(err)
			}
		}
		run() // fill the render cache
		return testing.AllocsPerRun(200, run)
	}
	exact, converted := allocs(int64(1)), allocs(1)
	if e.Args[1] != int64(1) {
		t.Fatalf("the tenant sent is %#v, want int64(1)", e.Args[1])
	}
	t.Logf("int64 %.0f allocations, int %.0f", exact, converted)
	if converted > exact+1 {
		t.Errorf("a converted tenant makes %.0f allocations, the exact type %.0f: over the budget of +1", converted, exact)
	}
}

func BenchmarkTenantScopedOneRow(b *testing.B) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}})
	t1 := velox.WithTenant(context.Background(), int64(1))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(t1); err != nil {
			b.Fatal(err)
		}
	}
}

// TestOneRowUsesTheCache: the budget above assumes the shape is cached.
func TestOneRowUsesTheCache(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}})
	for range 3 {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if n := velox.CacheSize(db); n != 1 {
		t.Errorf("cache holds %d shapes after one shape ran three times", n)
	}
}

func hundredRows() [][]any {
	rows := make([][]any, 100)
	for i := range rows {
		rows[i] = invoiceRow(int64(i + 1))
	}
	return rows
}

// TestHundredRowAllocationBudget pins the hundred-row budget: a 100-row query makes at
// most 16 more allocations than hand-written code.
func TestHundredRowAllocationBudget(t *testing.T) {
	e := &velox.FakeExec{Rows: hundredRows()}
	db := velox.OpenFake(velox.Postgres, e)
	run := func() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Limit(100).All(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run()
	vx := testing.AllocsPerRun(50, run)
	text, args := e.SQL, e.Args
	raw := testing.AllocsPerRun(50, func() {
		r, err := velox.RawRows(db, text, args)
		if err != nil {
			t.Fatal(err)
		}
		out := make([]testmodel.Invoice, 0, 100)
		for r.Next() {
			out = append(out, testmodel.Invoice{})
			x := &out[len(out)-1]
			if err := r.Scan(&x.ID, &x.CreatedAt, &x.UpdatedAt, &x.OrgID, &x.Number, &x.Total, &x.Paid, &x.PaidAt, &x.Note, &x.Data); err != nil {
				t.Fatal(err)
			}
		}
		r.Close()
	})
	t.Logf("velox %.0f allocations, hand-written %.0f", vx, raw)
	if vx > raw+16 {
		t.Errorf("velox makes %.0f allocations for 100 rows and hand-written code %.0f: over the budget of +16", vx, raw)
	}
}

func BenchmarkHundredRows(b *testing.B) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: hundredRows()})
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(1)).Limit(100).All(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkOneRowWithDeadline(b *testing.B) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}, velox.WithDeadlines(velox.Deadlines{Statement: time.Second}))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

// TestTxAllocationBudget pins the transaction budget: with nothing
// configured, a transaction makes no TxInfo. It is measured against the same
// read outside a transaction, so that the read's allocations, which vary
// between Go versions, cancel out. What is left is the transaction's own 3:
// Tx's options (opt(&o) makes them escape), the context its rollback runs
// on, and the *DB fn is given, which holds fn's context, marked as the
// transaction's. A TxInfo would be a fourth.
func TestTxAllocationBudget(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}})
	read := func(ctx context.Context, db *velox.DB) error {
		_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx)
		return err
	}
	if err := db.Tx(ctx, read); err != nil { // fill the render cache
		t.Fatal(err)
	}
	alone := testing.AllocsPerRun(200, func() {
		if err := read(ctx, db); err != nil {
			t.Fatal(err)
		}
	})
	inTx := testing.AllocsPerRun(200, func() {
		if err := db.Tx(ctx, read); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("a one-row read makes %.0f allocations in a transaction, %.0f alone", inTx, alone)
	if inTx > alone+3 {
		t.Errorf("a transaction with nothing configured adds %.0f allocations to its read: over the budget of 3", inTx-alone)
	}
}

// TestTxAroundAllocationBudget is TestTxAllocationBudget with one
// pass-through WithTxAround hook. The hook makes the transaction build a
// TxInfo and run through the hook's closure chain: measured 8 allocations
// more than the read alone, against 3 without the hook (14 in the
// transaction and 6 alone, the same under -race).
func TestTxAroundAllocationBudget(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}},
		velox.WithTxAround(func(ctx context.Context, _ *velox.TxInfo, next func(context.Context) error) error {
			return next(ctx)
		}))
	read := func(ctx context.Context, db *velox.DB) error {
		_, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx)
		return err
	}
	if err := db.Tx(ctx, read); err != nil { // fill the render cache
		t.Fatal(err)
	}
	alone := testing.AllocsPerRun(200, func() {
		if err := read(ctx, db); err != nil {
			t.Fatal(err)
		}
	})
	inTx := testing.AllocsPerRun(200, func() {
		if err := db.Tx(ctx, read); err != nil {
			t.Fatal(err)
		}
	})
	t.Logf("a one-row read makes %.0f allocations in a transaction with a TxAround hook, %.0f alone", inTx, alone)
	if inTx > alone+8 {
		t.Errorf("a transaction with a TxAround hook adds %.0f allocations to its read: over the budget of 8", inTx-alone)
	}
}

// TestObservedOneRowBudget pins the observed budget: WithCallers and
// WithSQLComment add at most one allocation, the comment's join, to a cached
// one-row read. Its time is read from BenchmarkOneRowObserved against
// BenchmarkOneRow.
func TestObservedOneRowBudget(t *testing.T) {
	tagged := velox.WithTags(ctx, "route", "GET /invoices/{id}")
	measure := func(opts ...velox.Option) (float64, string) {
		e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
		db := velox.OpenFake(velox.Postgres, e, opts...)
		run := func() {
			if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(tagged); err != nil {
				t.Fatal(err)
			}
		}
		run() // fill the render, call-site and comment caches
		return testing.AllocsPerRun(200, run), e.SQL
	}
	plain, _ := measure()
	observed, text := measure(velox.WithCallers(), velox.WithSQLComment("billing", "route"))
	if !strings.Contains(text, "/*app='billing',caller='budget_test.go%3A") || !strings.HasSuffix(text, ",route='GET%20%2Finvoices%2F%7Bid%7D'*/") {
		t.Fatalf("the comment is not as expected: %s", text)
	}
	t.Logf("with callers and comments %.0f allocations, without %.0f", observed, plain)
	if observed > plain+1 {
		t.Errorf("callers and comments add %.0f allocations to a cached one-row read: over the budget of 1", observed-plain)
	}
}

func BenchmarkOneRowObserved(b *testing.B) {
	e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}}
	db := velox.OpenFake(velox.Postgres, e, velox.WithCallers(), velox.WithSQLComment("billing", "route"))
	tagged := velox.WithTags(ctx, "route", "GET /invoices/{id}")
	b.ReportAllocs()
	for b.Loop() {
		e.Log = e.Log[:0] // as in BenchmarkOneRow: each statement's text is a new string here
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(tagged); err != nil {
			b.Fatal(err)
		}
	}
}

// TestReplicaOneRowBudget pins the replica budget: on a DB with replicas, a
// cached one-row read makes no allocation more than on a DB without them
// (TestOneRowAllocationBudget's 6): the route stage adds one context lookup
// and a counter's increment.
func TestReplicaOneRowBudget(t *testing.T) {
	measure := func(replicas bool) (float64, *velox.FakeExec) {
		e := &velox.FakeExec{Rows: [][]any{invoiceRow(1)}} // the connection the read goes to
		p := e
		var opts []velox.Option
		if replicas {
			p = &velox.FakeExec{}
			opts = append(opts, velox.WithReplicas(velox.FakeConn(e), velox.FakeConn(&velox.FakeExec{Rows: [][]any{invoiceRow(1)}})))
		}
		db := velox.OpenFake(velox.Postgres, p, opts...)
		run := func() {
			if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).One(ctx); err != nil {
				t.Fatal(err)
			}
		}
		run() // fill the render cache
		allocs := testing.AllocsPerRun(200, run)
		if replicas && p.Calls != 0 {
			t.Fatalf("the primary read %d times", p.Calls)
		}
		return allocs, e
	}
	plain, _ := measure(false)
	routed, r0 := measure(true)
	t.Logf("a one-row read makes %.0f allocations with two replicas, %.0f without", routed, plain)
	if r0.Calls == 0 {
		t.Fatal("no read reached replica 0")
	}
	if routed != plain {
		t.Errorf("replicas change a cached one-row read's allocations from %.0f to %.0f", plain, routed)
	}
}

// TestWithLoadOnReplicasAllocations: on a DB with replicas, a cached All with
// one With load makes no allocation more than on a DB without them, its load
// pinned to the replica its query read.
func TestWithLoadOnReplicasAllocations(t *testing.T) {
	measure := func(replicas bool) float64 {
		row := [][]any{sampleRow(1, int64(1))} // its own parent: the query's row and the load's
		e, r1 := &velox.FakeExec{Rows: row}, &velox.FakeExec{Rows: row}
		p := e
		var opts []velox.Option
		if replicas {
			p = &velox.FakeExec{}
			opts = append(opts, velox.WithReplicas(velox.FakeConn(e), velox.FakeConn(r1)))
		}
		db := velox.OpenFake(velox.Postgres, p, opts...)
		run := func() {
			ss, err := db.Query[testmodel.Sample]().With(testmodel.Samples.Parent).Limit(10).All(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, loaded := ss[0].Parent.Get(); !loaded {
				t.Fatal("the parent is not loaded")
			}
		}
		run() // fill the render cache
		if replicas && (e.Calls != 2 || r1.Calls != 0) {
			// The query read replica 0, whose turn it was, and so did its
			// load: the next turn, replica 1's, is the next query's.
			t.Fatalf("replica 0 read %d times, replica 1 %d; want the query and its load on replica 0", e.Calls, r1.Calls)
		}
		allocs := testing.AllocsPerRun(200, run)
		if replicas && p.Calls != 0 {
			t.Fatalf("the primary read %d times", p.Calls)
		}
		return allocs
	}
	plain, routed := measure(false), measure(true)
	t.Logf("an All with one With load makes %.0f allocations with two replicas, %.0f without", routed, plain)
	if routed != plain {
		t.Errorf("replicas change an All with one With load's allocations from %.0f to %.0f", plain, routed)
	}
}

// oneRowAllocs is the allocations of the one-row All the First and Exists
// budgets are held to, on a fake with one row.
func oneRowAllocs(db *velox.DB) float64 {
	run := func() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(ctx); err != nil {
			panic(err)
		}
	}
	run()
	return testing.AllocsPerRun(200, run)
}

// TestFirstAllocationBudget pins First to the allocations of the one-row All.
func TestFirstAllocationBudget(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}})
	run := func() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).First(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run()
	first, all := testing.AllocsPerRun(200, run), oneRowAllocs(db)
	t.Logf("First %.0f allocations, one-row All %.0f", first, all)
	if first > all {
		t.Errorf("First makes %.0f allocations, the one-row All %.0f", first, all)
	}
}

// TestExistsAllocationBudget pins Exists to the allocations of the one-row All.
func TestExistsAllocationBudget(t *testing.T) {
	db := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{invoiceRow(1)}})
	run := func() {
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Exists(ctx); err != nil {
			t.Fatal(err)
		}
	}
	run()
	exists, all := testing.AllocsPerRun(200, run), oneRowAllocs(db)
	t.Logf("Exists %.0f allocations, one-row All %.0f", exists, all)
	if exists > all {
		t.Errorf("Exists makes %.0f allocations, the one-row All %.0f", exists, all)
	}
}

// TestLoadAllocationBudgets pins, on the path where no statement overflows
// the parameter limit, the allocations of a cached All with one With load,
// of LoadEach and of CountEach, at the numbers measured when the budgets were
// set: a load's allocations grow only by a decision to let them.
func TestLoadAllocationBudgets(t *testing.T) {
	measure := func(name string, budget float64, run func() error) {
		t.Helper()
		if err := run(); err != nil { // fill the render cache
			t.Fatalf("%s: %v", name, err)
		}
		allocs := testing.AllocsPerRun(200, func() {
			if err := run(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
		})
		t.Logf("%s makes %.0f allocations", name, allocs)
		if allocs > budget {
			t.Errorf("%s makes %.0f allocations: over the budget of %.0f", name, allocs, budget)
		}
	}
	samples := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{sampleRow(1, int64(1))}}) // its own parent
	measure("an All with one With load", 41, func() error {
		_, err := samples.Query[testmodel.Sample]().With(testmodel.Samples.Parent).Limit(10).All(ctx)
		return err
	})
	owners := []*testmodel.Invoice{{ID: 1}, {ID: 2}}
	items := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{itemRow(10, 1, "A", 1), itemRow(11, 2, "B", 1)}})
	measure("LoadEach", 33, func() error {
		_, err := velox.LoadEach(ctx, items.Query[testmodel.Item](), I.Items, owners)
		return err
	})
	counts := velox.OpenFake(velox.Postgres, &velox.FakeExec{Rows: [][]any{{int64(1), int64(3)}, {int64(2), int64(1)}}})
	measure("CountEach", 23, func() error {
		_, err := velox.CountEach(ctx, counts.Query[testmodel.Item](), I.Items, owners)
		return err
	})
}
