package apitest

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/fakeconn"
	"github.com/syssam/velox/internal/testmodel"
	ts "github.com/syssam/velox/internal/testmodel/schema"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/veloxtest"
)

// countedDB is a SQLite database with Sample and Line, three samples and a
// line for each, opened by veloxtest, which counts statements.
func countedDB(t *testing.T) *velox.DB {
	t.Helper()
	db := veloxtest.Open(t, "sqlite", "", []schema.Interface{ts.Sample{}, ts.Line{}})
	for i := range 3 {
		s := testmodel.Sample{Name: string(rune('a' + i)), Status: "new", When: time.Now(), Blob: []byte("b")}
		if err := db.Insert(&s).Exec(ctx); err != nil {
			t.Fatal(err)
		}
		if err := db.Insert(&testmodel.Line{SampleID: s.ID}).Exec(ctx); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestCountStatementsNPlusOne: a read of each sample's lines in a loop runs
// one shape once per sample, which Check reports as an N+1, naming the
// statement; the same lines loaded with With are two statements, within
// the budget.
func TestCountStatementsNPlusOne(t *testing.T) {
	db := countedDB(t)
	S, L := testmodel.Samples, testmodel.Lines

	c1, n := velox.CountStatements(ctx)
	samples, err := db.Query[testmodel.Sample]().Limit(10).All(c1)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range samples {
		if _, err := db.Query[testmodel.Line]().Where(L.SampleID.EQ(s.ID)).Limit(10).All(c1); err != nil {
			t.Fatal(err)
		}
	}
	if n.Total() != 4 {
		t.Errorf("Total %d, want 4", n.Total())
	}
	shapes := n.Shapes()
	if len(shapes) != 2 || shapes[0].Count != 1 || shapes[1].Count != 3 || shapes[1].Entity != "testmodel.Line" || shapes[1].Op != velox.OpSelect {
		t.Errorf("Shapes %+v", shapes)
	}
	err = n.Check(velox.StatementBudget{Repeats: 1})
	be, ok := errors.AsType[*velox.BudgetError](err)
	if !ok || len(be.Repeats) != 1 || !strings.Contains(err.Error(), `ran 3 times, of 1 allowed: SELECT`) || !strings.Contains(err.Error(), "N+1") {
		t.Errorf("Check: %v", err)
	}
	if err := n.Check(velox.StatementBudget{Repeats: 3}); err != nil {
		t.Errorf("Check within Repeats: %v", err)
	}

	c2, n2 := velox.CountStatements(ctx)
	if _, err := db.Query[testmodel.Sample]().With(S.Lines).Limit(10).All(c2); err != nil {
		t.Fatal(err)
	}
	if err := n2.Check(velox.StatementBudget{Statements: 2, Repeats: 1}); err != nil || n2.Total() != 2 {
		t.Errorf("With: %d statements, %v", n2.Total(), err)
	}
}

// TestCountStatementsBudget: the statement budget counts writes and raw
// SQL too; a repeated write is no N+1; a context without a counter, and a
// DB without WithStatementCounting, count nothing.
func TestCountStatementsBudget(t *testing.T) {
	db := countedDB(t)
	c, n := velox.CountStatements(ctx)
	for range 3 {
		if err := db.Insert(&testmodel.Sample{Name: "w", Status: "new", When: time.Now(), Blob: []byte("b")}).Exec(c); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(c, "a test", "UPDATE samples SET name = name WHERE id = 0"); err != nil {
		t.Fatal(err)
	}
	if n.Total() != 4 {
		t.Errorf("Total %d, want 4", n.Total())
	}
	if err := n.Check(velox.StatementBudget{Repeats: 1}); err != nil {
		t.Errorf("repeated inserts: %v", err)
	}
	err := n.Check(velox.StatementBudget{Statements: 3})
	if be, ok := errors.AsType[*velox.BudgetError](err); !ok || be.Total != 4 || !strings.Contains(err.Error(), "4 statements ran, of 3 allowed") {
		t.Errorf("Check over Statements: %v", err)
	}

	if _, err := db.Query[testmodel.Sample]().Limit(1).All(ctx); err != nil {
		t.Fatal(err)
	}
	if n.Total() != 4 {
		t.Errorf("a statement on another context was counted: %d", n.Total())
	}

	plain, err := velox.Connect("sqlite", t.TempDir()+"/plain.db")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = plain.Close() }()
	c3, n3 := velox.CountStatements(ctx)
	if _, err := plain.Exec(c3, "a test", "SELECT 1"); err != nil {
		t.Fatal(err)
	}
	if n3.Total() != 0 {
		t.Errorf("a DB without WithStatementCounting counted %d", n3.Total())
	}
}

// TestCountStatementsConcurrent: statements of one context on several
// goroutines are all counted.
func TestCountStatementsConcurrent(t *testing.T) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openFake(velox.Postgres, e, velox.WithStatementCounting())
	c, n := velox.CountStatements(ctx)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 10 {
				_, _ = db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(c)
			}
		})
	}
	wg.Wait()
	if n.Total() != 80 || len(n.Shapes()) != 1 || n.Shapes()[0].Count != 80 {
		t.Errorf("Total %d, shapes %+v", n.Total(), n.Shapes())
	}
}

// TestCountStatementsCost pins the cost: a DB without
// WithStatementCounting runs nothing of it. With it, a cached one-row read
// makes no allocation more than with an Around hook that does nothing,
// whether its context has a counter or not, once its shape is counted.
func TestCountStatementsCost(t *testing.T) {
	measure := func(c context.Context, opts ...velox.Option) float64 {
		e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
		db := openFake(velox.Postgres, e, opts...)
		run := func() {
			e.Log = e.Log[:0]
			if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(c); err != nil {
				t.Fatal(err)
			}
		}
		run()
		return testing.AllocsPerRun(200, run)
	}
	counted, _ := velox.CountStatements(ctx)
	nothing := velox.WithAround(func(ctx context.Context, _ *velox.Statement, next func(context.Context) error) error {
		return next(ctx)
	})
	plain := measure(ctx)
	hook := measure(ctx, nothing)
	off := measure(ctx, velox.WithStatementCounting())
	on := measure(counted, velox.WithStatementCounting())
	t.Logf("no hook %.0f allocations, a hook that does nothing %.0f, counting on a context without a counter %.0f, with one %.0f", plain, hook, off, on)
	if off > hook || on > hook {
		t.Errorf("counting adds allocations to an Around hook's %.0f: %.0f without a counter, %.0f with one", hook, off, on)
	}
}

func BenchmarkOneRowCounted(b *testing.B) {
	e := &fakeconn.Exec{Rows: [][]any{invoiceRow(1)}}
	db := openFake(velox.Postgres, e, velox.WithStatementCounting())
	c, _ := velox.CountStatements(ctx)
	b.ReportAllocs()
	for b.Loop() {
		e.Log = e.Log[:0]
		if _, err := db.Query[testmodel.Invoice]().Where(I.ID.EQ(1)).Limit(1).All(c); err != nil {
			b.Fatal(err)
		}
	}
}
