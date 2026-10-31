package gqlcore

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"

	"github.com/99designs/gqlgen/graphql"
	"github.com/vektah/gqlparser/v2/ast"
)

// Table is an operation's table of groups. A group is rows one statement
// read (a page, a node lookup, a batch) or veloxgql.Batch was given: the
// first edge field gqlgen resolves on any of its rows reads that edge for
// all of them, in one statement, and the others take their part of it.
// veloxgql puts a new Table in each response's context (a query's or a
// mutation's one response, each event of a subscription), so that requests
// never share one and a subscription's events let go of their rows. It is
// safe for concurrent use.
//
// No lock is held while a batch loads: a resolver that waits on a batch
// waits on its sync.Once alone, and the batch's load may add groups. A
// waiter does not watch its own context's cancellation: it waits for the
// load, which runs under the first caller's context. Every resolver of a
// response shares the request's context, so the load ends when theirs
// would.
type Table struct {
	// schema is the server's schema, nil when unknown: Selected reads the
	// interfaces of a type from it.
	schema *ast.Schema
	// scope is veloxgql.WithBatchScope's function, or nil.
	scope func(context.Context) any

	mu sync.Mutex
	// rows maps a row's pointer (a *E, in an any) to its group and its
	// place in the group's rows.
	rows map[any]member
	// selected are Selected's results, per field.
	selected map[*ast.Field][]*selection
}

// member is a row's group and its index in the group's rows.
type member struct {
	g *Group
	i int
}

// selection is Selected's result for a field with the selections sels, as
// the objects of the GraphQL type typ.
type selection struct {
	sels   ast.SelectionSet
	typ    string
	fields []string
	ok     bool
}

// Group is a set of rows that one statement read: a []*E, held as an any.
type Group struct {
	rows any
	mu   sync.Mutex
	// batches are the group's batches, per edge field: one per field and
	// selection, since two aliases are two fields.
	batches map[*ast.Field][]*Batch
}

// Batch is one edge field's read for a group: its rows, read once, and its
// counts, read once, each with its error, which is every row's.
type Batch struct {
	sels ast.SelectionSet
	key  any // what its callers share: the edge, their row scope…; comparable
	args any // its first caller's arguments

	rowsOnce sync.Once
	rows     any
	rowsErr  error

	countOnce sync.Once
	counts    []int64
	countErr  error
}

// errPanicked is a batch's error when its load panicked: the resolver that
// ran it gets the panic, and the others this.
var errPanicked = errors.New("veloxgql: the batch of this edge panicked")

// tableKey is the context key of an operation's *Table.
type tableKey struct{}

// WithTable returns ctx with a new, empty Table of the server's schema,
// which may be nil, and of veloxgql.WithBatchScope's function scope, which
// may be nil too.
func WithTable(ctx context.Context, schema *ast.Schema, scope func(context.Context) any) context.Context {
	return context.WithValue(ctx, tableKey{}, &Table{schema: schema, scope: scope})
}

// Schema returns the server's schema; nil when t is nil or the schema is
// unknown.
func (t *Table) Schema() *ast.Schema {
	if t == nil {
		return nil
	}
	return t.schema
}

// Scope returns the value veloxgql.WithBatchScope's function gives ctx; nil
// when t is nil or has no function.
func (t *Table) Scope(ctx context.Context) any {
	if t == nil || t.scope == nil {
		return nil
	}
	return t.scope(ctx)
}

// TableFromContext returns ctx's Table, or nil.
func TableFromContext(ctx context.Context) *Table {
	t, _ := ctx.Value(tableKey{}).(*Table)
	return t
}

// Add makes rows, without its nil rows, one group of ctx's table. A row
// already in a group moves to this one. Add does nothing when ctx has no
// table, or is in no gqlgen field: there nothing batches, and a context kept
// long must not keep its rows.
func Add[E any](ctx context.Context, rows []*E) {
	t := TableFromContext(ctx)
	if t == nil || len(rows) == 0 || graphql.GetFieldContext(ctx) == nil {
		return
	}
	rows = slices.DeleteFunc(slices.Clone(rows), func(x *E) bool { return x == nil })
	if len(rows) == 0 {
		return
	}
	g := &Group{rows: rows}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rows == nil {
		t.rows = make(map[any]member, len(rows))
	}
	for i, x := range rows {
		t.rows[x] = member{g, i}
	}
}

// Find returns x's group, the group's rows and x's index in them; ok is
// false when t is nil or x is in no group.
func Find[E any](t *Table, x *E) (g *Group, rows []*E, i int, ok bool) {
	if t == nil || x == nil {
		return nil, nil, 0, false
	}
	t.mu.Lock()
	m, ok := t.rows[x]
	t.mu.Unlock()
	if !ok {
		return nil, nil, 0, false
	}
	return m.g, m.g.rows.([]*E), m.i, true
}

// Batch returns the group's batch of the edge field f with the selections
// sels, the merged selections of the field, which decide the batch's
// columns, for callers of key, a comparable value (the edge, the callers'
// row scope…), with the arguments args: a new one on the first call.
// Callers of different keys never share a batch. A caller of a batch's key
// whose args are not its first caller's (reflect.DeepEqual) gets nil: its
// read is not the batch's, and it reads alone.
func (g *Group) Batch(f *ast.Field, sels ast.SelectionSet, key, args any) *Batch {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, b := range g.batches[f] {
		if b.key == key && slices.Equal(b.sels, sels) {
			if !reflect.DeepEqual(b.args, args) {
				return nil
			}
			return b
		}
	}
	if g.batches == nil {
		g.batches = map[*ast.Field][]*Batch{}
	}
	b := &Batch{sels: sels, key: key, args: args}
	g.batches[f] = append(g.batches[f], b)
	return b
}

// Rows returns the batch's rows, which load reads on the first call; the
// other calls wait for it and get its result.
func (b *Batch) Rows(load func() (any, error)) (any, error) {
	b.rowsOnce.Do(func() {
		b.rowsErr = errPanicked
		b.rows, b.rowsErr = load()
	})
	return b.rows, b.rowsErr
}

// Counts returns the batch's counts, which count reads on the first call;
// the other calls wait for it and get its result.
func (b *Batch) Counts(count func() ([]int64, error)) ([]int64, error) {
	b.countOnce.Do(func() {
		b.countErr = errPanicked
		b.counts, b.countErr = count()
	})
	return b.counts, b.countErr
}

// Selected returns compute's result for the field f with the selections
// sels, as the objects of typ, computing it once per t. With a nil t it
// computes it each time.
func (t *Table) Selected(f *ast.Field, sels ast.SelectionSet, typ string, compute func() ([]string, bool)) ([]string, bool) {
	if t == nil {
		return compute()
	}
	find := func() *selection {
		for _, s := range t.selected[f] {
			if s.typ == typ && slices.Equal(s.sels, sels) {
				return s
			}
		}
		return nil
	}
	t.mu.Lock()
	s := find()
	t.mu.Unlock()
	if s != nil {
		return s.fields, s.ok
	}
	// Computed outside the lock, which Add and Find take: two callers may
	// both compute, and the first to store wins.
	fields, ok := compute()
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := find(); s != nil {
		return s.fields, s.ok
	}
	if t.selected == nil {
		t.selected = map[*ast.Field][]*selection{}
	}
	t.selected[f] = append(t.selected[f], &selection{sels: sels, typ: typ, fields: fields, ok: ok})
	return fields, ok
}
