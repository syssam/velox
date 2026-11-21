package gqlcore

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
)

// Table is an operation's table of groups. A group is rows one statement
// read (a page, a node lookup, a batch) or veloxgql.Batch was given: the
// first edge field resolved on any of its rows reads that edge for all of
// them, in one statement, and the others take their part of it. An adapter
// puts a new Table in each response's context (a query's or a
// mutation's one response, each event of a subscription), so that requests
// never share one and a subscription's events let go of their rows. It is
// safe for concurrent use.
//
// No lock is held while a batch loads: a resolver that waits on a batch
// waits on its sync.Once alone, inside its source's Wait, and the batch's
// load may add groups. A
// waiter does not watch its own context's cancellation: it waits for the
// load, which runs under the first caller's context. Every resolver of a
// response shares the request's context, so the load ends when theirs
// would.
type Table struct {
	// scope is veloxgql.WithBatchScope's function, or nil.
	scope func(context.Context) any

	mu sync.Mutex
	// rows maps a row, a *E in an any, to its group and its place in the
	// group's rows. The key compares the pointer and its type, so rows of
	// two entities never share one.
	rows map[any]member
	// selected are Selected's results, per field occurrence and type.
	selected map[selectionKey]*selection
}

// member is a row's group and its index in the group's rows.
type member struct {
	g *Group
	i int
}

// selectionKey is a field occurrence (Field.Key) and a GraphQL type.
type selectionKey struct {
	field any
	typ   string
}

// selection is Selected's result for a field occurrence, as the objects of
// a GraphQL type.
type selection struct {
	fields []string
	ok     bool
}

// Group is a set of rows that one statement read: pointers to its rows, of
// one entity.
type Group struct {
	rows []any
	mu   sync.Mutex
	// batches are the group's batches, per edge field occurrence
	// (Field.Key): two aliases are two occurrences.
	batches map[any][]*Batch
}

// Batch is one edge field's read for a group: its rows, read once, and its
// counts, read once, each with its error, which is every row's.
type Batch struct {
	key  any // what its callers share: the edge, their row scope…; comparable
	args any // its first caller's arguments

	rows  load[any]
	count load[[]int64]
}

// load is a value read once: the first caller reads it, and the others
// wait, inside their source's Wait, for its result.
type load[T any] struct {
	started, done atomic.Bool
	once          sync.Once
	v             T
	err           error
}

// get returns l's value, which read reads on the first call; a caller that
// finds the read started by another waits for it inside its source's Wait.
func (l *load[T]) get(ctx context.Context, read func() (T, error)) (T, error) {
	if !l.started.CompareAndSwap(false, true) && !l.done.Load() {
		defer wait(ctx)()
	}
	l.once.Do(func() {
		defer l.done.Store(true)
		l.err = errPanicked
		l.v, l.err = read()
	})
	return l.v, l.err
}

// errPanicked is a batch's error when its load panicked: the resolver that
// ran it gets the panic, and the others this.
var errPanicked = errors.New("veloxgql: the batch of this edge panicked")

// tableKey is the context key of an operation's *Table.
type tableKey struct{}

// WithTable returns ctx with a new, empty Table of veloxgql.WithBatchScope's
// function scope, which may be nil.
func WithTable(ctx context.Context, scope func(context.Context) any) context.Context {
	return context.WithValue(ctx, tableKey{}, &Table{scope: scope})
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
// table, or is in no field its source knows: there nothing batches, and a
// context kept long must not keep its rows.
func Add(ctx context.Context, rows []any) {
	t := TableFromContext(ctx)
	if t == nil || len(rows) == 0 {
		return
	}
	if _, ok := FieldOf(ctx); !ok {
		return
	}
	rows = slices.DeleteFunc(slices.Clone(rows), nilRow)
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

// nilRow reports whether x is no row: nil, or a nil pointer.
func nilRow(x any) bool {
	if x == nil {
		return true
	}
	v := reflect.ValueOf(x)
	return v.Kind() == reflect.Pointer && v.IsNil()
}

// Find returns x's group, the group's rows and x's index in them; ok is
// false when t is nil or x is in no group.
func Find(t *Table, x any) (g *Group, rows []any, i int, ok bool) {
	if t == nil || x == nil {
		return nil, nil, 0, false
	}
	t.mu.Lock()
	m, ok := t.rows[x]
	t.mu.Unlock()
	if !ok {
		return nil, nil, 0, false
	}
	return m.g, m.g.rows, m.i, true
}

// Batch returns the group's batch of the edge field occurrence field (a
// Field.Key, comparable), which decides the batch's columns, for callers of
// key, a comparable value (the edge, the callers' row scope…), with the
// arguments args: a new one on the first call. Callers of different keys
// never share a batch. A caller of a batch's key whose args are not its
// first caller's (reflect.DeepEqual) gets nil: its read is not the batch's,
// and it reads alone.
func (g *Group) Batch(field, key, args any) *Batch {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, b := range g.batches[field] {
		if b.key == key {
			if !reflect.DeepEqual(b.args, args) {
				return nil
			}
			return b
		}
	}
	if g.batches == nil {
		g.batches = map[any][]*Batch{}
	}
	b := &Batch{key: key, args: args}
	g.batches[field] = append(g.batches[field], b)
	return b
}

// Rows returns the batch's rows, which load reads on the first call; the
// other calls wait for it, inside the Wait of ctx's source, and get its
// result.
func (b *Batch) Rows(ctx context.Context, load func() (any, error)) (any, error) {
	return b.rows.get(ctx, load)
}

// Counts returns the batch's counts, which count reads on the first call;
// the other calls wait for it, inside the Wait of ctx's source, and get its
// result.
func (b *Batch) Counts(ctx context.Context, count func() ([]int64, error)) ([]int64, error) {
	return b.count.get(ctx, count)
}

// Selected returns compute's result for the field occurrence field (a
// Field.Key), as the objects of typ, computing it once per t. With a nil t
// it computes it each time.
func (t *Table) Selected(field any, typ string, compute func() ([]string, bool)) ([]string, bool) {
	if t == nil {
		return compute()
	}
	k := selectionKey{field, typ}
	t.mu.Lock()
	s := t.selected[k]
	t.mu.Unlock()
	if s != nil {
		return s.fields, s.ok
	}
	// Computed outside the lock, which Add and Find take: two callers may
	// both compute, and the first to store wins.
	fields, ok := compute()
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := t.selected[k]; s != nil {
		return s.fields, s.ok
	}
	if t.selected == nil {
		t.selected = map[selectionKey]*selection{}
	}
	t.selected[k] = &selection{fields: fields, ok: ok}
	return fields, ok
}
