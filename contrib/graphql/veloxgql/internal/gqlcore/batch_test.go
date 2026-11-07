package gqlcore

import (
	"context"
	"errors"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

// TestTable: Add groups rows only inside a field of a context with a table,
// Find finds a row's group, a row moves to the group last added, and the
// nil Table answers nothing.
func TestTable(t *testing.T) {
	schema := &ast.Schema{}
	scoped := func(context.Context) any { return "scope" }
	ctx := WithTable(context.Background(), schema, scoped)
	tbl := TableFromContext(ctx)
	if tbl.Schema() != schema || tbl.Scope(ctx) != "scope" {
		t.Error("the table's schema and scope")
	}
	var nilT *Table
	if nilT.Schema() != nil || nilT.Scope(ctx) != nil || TableFromContext(context.Background()) != nil {
		t.Error("the nil Table")
	}
	if (&Table{}).Scope(ctx) != nil {
		t.Error("a table without a scope")
	}
	a, b := new(int), new(int)
	pa, pb := any(a), any(b)
	Add(ctx, []any{pa}) // in no field: nothing
	if _, _, _, ok := Find(tbl, pa); ok {
		t.Error("Add outside a field grouped a row")
	}
	fctx := graphql.WithFieldContext(ctx, &graphql.FieldContext{})
	Add(fctx, []any{nil, (*int)(nil)})
	Add(fctx, []any{pa, nil, pb})
	if g, rows, i, ok := Find(tbl, pb); !ok || g == nil || len(rows) != 2 || i != 1 {
		t.Errorf("Find: %v %v %d %v", g, rows, i, ok)
	}
	Add(fctx, []any{pb})
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
	g := &Group{}
	f, other := &ast.Field{}, &ast.Field{}
	sels := ast.SelectionSet{&ast.Field{Name: "id"}}
	b := g.Batch(f, sels, "k", 1)
	if g.Batch(f, sels, "k", 1) != b || g.Batch(f, sels, "k", 2) != nil {
		t.Error("a batch is shared by its key and arguments only")
	}
	if g.Batch(f, sels, "k2", 1) == b || g.Batch(other, sels, "k", 1) == b || g.Batch(f, nil, "k", 1) == b {
		t.Error("another key, field or selection shares the batch")
	}
	n := 0
	load := func() (any, error) { n++; return "rows", nil }
	for range 2 {
		if v, err := b.Rows(load); v != "rows" || err != nil {
			t.Errorf("Rows: %v %v", v, err)
		}
	}
	counts := func() ([]int64, error) { n++; return []int64{1}, nil }
	for range 2 {
		if c, err := b.Counts(counts); len(c) != 1 || err != nil {
			t.Errorf("Counts: %v %v", c, err)
		}
	}
	if n != 2 {
		t.Errorf("%d loads, want one of each", n)
	}
	p := g.Batch(f, sels, "panics", nil)
	func() {
		defer func() { _ = recover() }()
		_, _ = p.Rows(func() (any, error) { panic("load") })
	}()
	if _, err := p.Rows(load); !errors.Is(err, errPanicked) {
		t.Errorf("Rows after a panic: %v", err)
	}
	func() {
		defer func() { _ = recover() }()
		_, _ = p.Counts(func() ([]int64, error) { panic("count") })
	}()
	if _, err := p.Counts(counts); !errors.Is(err, errPanicked) {
		t.Errorf("Counts after a panic: %v", err)
	}
}

// TestSelected: a field's selection is computed once per table and type,
// and every time without a table.
func TestSelected(t *testing.T) {
	tbl := &Table{}
	f := &ast.Field{}
	sels := ast.SelectionSet{&ast.Field{Name: "id"}}
	n := 0
	compute := func() ([]string, bool) { n++; return []string{"id"}, true }
	for range 2 {
		if got, ok := tbl.Selected(f, sels, "T", compute); !ok || len(got) != 1 {
			t.Errorf("Selected: %v %v", got, ok)
		}
	}
	tbl.Selected(f, sels, "U", compute)
	var nilT *Table
	nilT.Selected(f, sels, "T", compute)
	nilT.Selected(f, sels, "T", compute)
	if n != 4 {
		t.Errorf("%d computations, want 4: T once, U once, and twice without a table", n)
	}
}
