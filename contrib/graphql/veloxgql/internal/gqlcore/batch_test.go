package gqlcore

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fixtureKey marks a context testReader finds a field in; its value is the
// field's fixture.
type fixtureKey struct{}

// fixture is the field testReader finds in a context, and the count of its
// waits.
type fixture struct {
	field Field
	waits atomic.Int32
}

// testReader is the one Reader the tests register: it knows only the
// contexts marked with a fixture (inField).
type testReader struct{}

func fixtureOf(ctx context.Context) *fixture {
	f, _ := ctx.Value(fixtureKey{}).(*fixture)
	return f
}

func (testReader) In(ctx context.Context) bool { return fixtureOf(ctx) != nil }

func (testReader) Field(ctx context.Context) (Field, bool) {
	if f := fixtureOf(ctx); f != nil {
		return f.field, true
	}
	return Field{}, false
}

func (testReader) Wait(ctx context.Context) func() {
	if f := fixtureOf(ctx); f != nil {
		f.waits.Add(1)
	}
	return func() {}
}

// TestMain registers testReader once: Register is global and append-only.
func TestMain(m *testing.M) {
	Register(testReader{})
	os.Exit(m.Run())
}

// inField is ctx inside the field f of testReader's engine, and its fixture.
func inField(ctx context.Context, f Field) (context.Context, *fixture) {
	fx := &fixture{field: f}
	return context.WithValue(ctx, fixtureKey{}, fx), fx
}

// TestTable: Add groups rows only inside a field with a table, Find finds a
// row's group, a row moves to the group last added, and the nil Table
// answers nothing.
func TestTable(t *testing.T) {
	tbl := NewTable()
	ctx, _ := inField(context.Background(), Field{Object: "T", Name: "f", Key: "k", Table: tbl})
	a, b := new(int), new(int)
	pa, pb := any(a), any(b)
	Add(context.Background(), []any{pa}) // in no field: nothing
	if _, _, _, ok := Find(tbl, pa); ok {
		t.Error("Add outside a field grouped a row")
	}
	// A field with no table: nothing.
	bare, _ := inField(context.Background(), Field{Object: "T", Name: "f", Key: "k"})
	Add(bare, []any{pa})
	if _, _, _, ok := Find(tbl, pa); ok {
		t.Error("Add in a field with no table grouped a row")
	}
	Add(ctx, []any{nil, (*int)(nil)})
	Add(ctx, []any{pa, nil, pb})
	if g, rows, i, ok := Find(tbl, pb); !ok || g == nil || len(rows) != 2 || i != 1 {
		t.Errorf("Find: %v %v %d %v", g, rows, i, ok)
	}
	Add(ctx, []any{pb})
	if _, rows, i, ok := Find(tbl, pb); !ok || len(rows) != 1 || i != 0 {
		t.Errorf("a row moves to its new group: %v %d %v", rows, i, ok)
	}
	for _, c := range []struct {
		t *Table
		p any
	}{{nil, pa}, {tbl, nil}, {tbl, any(new(int))}, {tbl, (*int)(nil)}} {
		if _, _, _, ok := Find(c.t, c.p); ok {
			t.Errorf("Find(%v, %v) is ok", c.t, c.p)
		}
	}
}

// TestBatch: callers of a field, key and selection share one batch, read
// once; other arguments read alone; a panicking load leaves its error for
// the other callers.
func TestBatch(t *testing.T) {
	ctx := context.Background()
	g := &Group{}
	type occurrence struct{ n int } // a field occurrence, as a source keys one
	f, other := &occurrence{1}, &occurrence{2}
	b := g.Batch(f, "k", 1)
	if g.Batch(f, "k", 1) != b || g.Batch(f, "k", 2) != nil {
		t.Error("a batch is shared by its key and arguments only")
	}
	if g.Batch(f, "k2", 1) == b || g.Batch(other, "k", 1) == b {
		t.Error("another key, or field occurrence, shares the batch")
	}
	n := 0
	load := func() (any, error) { n++; return "rows", nil }
	for range 2 {
		if v, err := b.Rows(ctx, load); v != "rows" || err != nil {
			t.Errorf("Rows: %v %v", v, err)
		}
	}
	counts := func() ([]int64, error) { n++; return []int64{1}, nil }
	for range 2 {
		if c, err := b.Counts(ctx, counts); len(c) != 1 || err != nil {
			t.Errorf("Counts: %v %v", c, err)
		}
	}
	if n != 2 {
		t.Errorf("%d loads, want one of each", n)
	}
	p := g.Batch(f, "panics", nil)
	func() {
		defer func() { _ = recover() }()
		_, _ = p.Rows(ctx, func() (any, error) { panic("load") })
	}()
	if _, err := p.Rows(ctx, load); !errors.Is(err, errPanicked) {
		t.Errorf("Rows after a panic: %v", err)
	}
	func() {
		defer func() { _ = recover() }()
		_, _ = p.Counts(ctx, func() ([]int64, error) { panic("count") })
	}()
	if _, err := p.Counts(ctx, counts); !errors.Is(err, errPanicked) {
		t.Errorf("Counts after a panic: %v", err)
	}
}

// TestSelected: a field's selection is computed once per table and type,
// and every time without a table.
func TestSelected(t *testing.T) {
	tbl := &Table{}
	f := new(int) // a field occurrence, as a source keys one
	n := 0
	compute := func() ([]string, bool) { n++; return []string{"id"}, true }
	for range 2 {
		if got, ok := tbl.Selected(f, "T", compute); !ok || len(got) != 1 {
			t.Errorf("Selected: %v %v", got, ok)
		}
	}
	tbl.Selected(f, "U", compute)
	var nilT *Table
	nilT.Selected(f, "T", compute)
	nilT.Selected(f, "T", compute)
	if n != 4 {
		t.Errorf("%d computations, want 4: T once, U once, and twice without a table", n)
	}
}

// TestBatchWaits: a caller that finds a batch's read started by another
// waits for it inside its source's Wait; the first caller, and a caller
// after the read, do not wait.
func TestBatchWaits(t *testing.T) {
	ctx, src := inField(context.Background(), Field{Object: "T", Name: "f", Key: "k"})
	b := &Batch{}
	started, release := make(chan struct{}), make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		_, _ = b.Rows(ctx, func() (any, error) {
			close(started)
			<-release
			return "rows", nil
		})
	})
	<-started
	done := make(chan any)
	go func() {
		v, _ := b.Rows(ctx, func() (any, error) { return "again", nil })
		done <- v
	}()
	for src.waits.Load() == 0 {
		// The second caller is in Wait before it blocks on the read.
		<-time.After(time.Millisecond)
	}
	close(release)
	if v := <-done; v != "rows" {
		t.Errorf("the waiter got %v", v)
	}
	wg.Wait()
	if _, _ = b.Rows(ctx, nil); src.waits.Load() != 1 {
		t.Errorf("%d waits, want the one of the caller that found the read started", src.waits.Load())
	}
}
