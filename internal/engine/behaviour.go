package engine

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unsafe"
)

// Policy is an entity's row-level policy, declared in the schema package on
// the entity's schema or on a mixin. It names columns by name (FieldEQ), so
// that one Policy serves every entity that lists a mixin, and the schema
// package never imports the generated one.
//
// Any field may be nil. Read and Write return Allow() for no restriction;
// their zero Predicate fails the statement, naming the entity, so that a code
// path that forgot to return a condition never lets a caller through. Any
// error they return denies the statement.
//
// A policy function decides from ctx and, for Insert, the row; it does not
// query. A rule that must see other rows is a hook: it queries through
// Mutation.DB and returns the error that refuses the write.
type Policy struct {
	// Read is ANDed into every SELECT, Count, With load and Has/HasWith
	// subquery on the entity, into every UPDATE, DELETE and Save, and into
	// the update branch of an upsert, on the existing row.
	Read func(ctx context.Context) (Predicate, error)
	// Write is ANDed into every UPDATE, DELETE and Save, and into an upsert's
	// update branch, with Read. It decides which existing rows may change; it
	// does not check the new values, so a writer may set a column the policy
	// reads, such as an owner. Only an Immutable column cannot be changed.
	//
	// A row Write hides is left alone, and that is not an error: an Update or
	// Delete that matches only such rows affects 0 rows, as with SQL
	// row-level security.
	Write func(ctx context.Context) (Predicate, error)
	// Insert checks every inserted or upserted row, after the entity's hooks,
	// the defaults and the validators. It reads the row with Row.Value; a
	// Row.Set in it is a misuse, which fails the insert: a policy decides,
	// and does not change the row.
	Insert func(ctx context.Context, r Row) error

	// Query is veloxe's query rules (package privacy). Their conditions are
	// ANDed into every read of the entity, after Read's: a SELECT, Count,
	// With load, join and Has/HasWith subquery of a statement that reads.
	// Unlike Read, they do not apply to an UPDATE, a DELETE, a Save, an
	// upsert's update branch, or anything inside one, as veloxe's query
	// policy did not. Any error they return, but a Decision of Allow or
	// Skip, denies the read.
	Query QueryPolicy
	// Mutation is veloxe's mutation rules (package privacy). They judge
	// every inserted or upserted row, after Insert, and their conditions
	// are ANDed into every UPDATE, DELETE and Save, and an upsert's update
	// branch, after Write's.
	Mutation MutationPolicy
}

// Interceptor filters an entity's rows for the application. Filter's
// condition is ANDed into every statement that reads or changes the rows:
// SELECT, Count, the aggregates, Each and Iter, a With load, an EXISTS on
// the entity (Has, HasWith), a join, UPDATE, DELETE, Save and an upsert's
// update branch. Raw SQL is not filtered.
//
// The interceptors' conditions come before the Read policy's in the WHERE.
// WithoutPolicy does not lift them: an interceptor offers its own way to be
// skipped, such as a context value its Filter reads. A Filter that reads a
// context value records it with WithScopeValue, so that the GraphQL batcher
// keeps callers apart.
//
// Filter returns Allow() for no restriction. Its zero Predicate fails the
// statement, as a policy's does. An error fails the statement and is not a
// denial. A nil Filter is refused when the metadata is built, by its index.
//
// A join entity of a many-to-many edge cannot have interceptors, as it
// cannot have a Read policy: it is read through its two sides. On MySQL, an
// upsert that updates an entity with interceptors is refused with
// ErrPolicyUpsert.
type Interceptor struct {
	Filter func(ctx context.Context) (Predicate, error)
}

// PolicyFor is p as entity in keeps it.
func PolicyFor(in *EntityInfo, p Policy) *EntityPolicy {
	pol := &EntityPolicy{Read: p.Read}
	if w := p.Write; w != nil {
		pol.Write = func(ctx context.Context, _ Op) (Predicate, error) { return w(ctx) }
	}
	ins := p.Insert
	if len(p.Query) > 0 {
		pol.Query = queryCond(in, p.Query)
	}
	if len(p.Mutation) > 0 {
		pol.Write = andWriteConds([]func(context.Context, Op) (Predicate, error){pol.Write, mutationCond(in, p.Mutation)})
		ins = andInserts(ins, mutationInsert(in, p.Mutation))
	}
	if ins != nil {
		pol.Insert = func(ctx context.Context, x unsafe.Pointer) error {
			var misuse error
			err := ins(ctx, NewRow(in, x, &misuse, true))
			if misuse != nil {
				return &rowMisuseError{misuse}
			}
			return err
		}
	}
	return pol
}

// andInserts is the Insert policy that runs a, then b; either may be nil.
func andInserts(a, b func(ctx context.Context, r Row) error) func(ctx context.Context, r Row) error {
	if a == nil {
		return b
	}
	return func(ctx context.Context, r Row) error {
		if err := a(ctx, r); err != nil {
			return err
		}
		return b(ctx, r)
	}
}

// rowMisuseError is the misuse of a Row in an Insert policy: a wrong name, a
// value of another kind, a Set. It fails the insert, and is not a denial.
type rowMisuseError struct{ err error }

func (e *rowMisuseError) Error() string { return e.err.Error() }
func (e *rowMisuseError) Unwrap() error { return e.err }

// FiltersFor is the filters of is, the interceptors one function of the
// Descriptor returned, and the reason a nil Filter is refused when one is:
// base is the index of is's first in the entity's list.
func FiltersFor(is []Interceptor, base int) ([]FilterFn, string) {
	fs := make([]FilterFn, len(is))
	for i, x := range is {
		f := x.Filter
		if f == nil {
			return nil, fmt.Sprintf("Interceptors()[%d] has a nil Filter", base+i)
		}
		fs[i] = f
	}
	return fs, ""
}

// WithEntityHooks adds hs to the hooks of entity in, for every DB the
// option configures and every Tx and shard it opens: they run after the
// entity's own, innermost. An in of nil adds nothing: an entity whose
// metadata failed fails its statements.
func WithEntityHooks(in *EntityInfo, hs []HookFn) Option {
	return Option{func(c *config) {
		if in == nil || len(hs) == 0 {
			return
		}
		if c.Hooks == nil {
			c.Hooks = map[*EntityInfo][]HookFn{}
		}
		c.Hooks[in] = append(c.Hooks[in], hs...)
	}}
}

// WithAllHooks adds hs to the hooks of every entity, for every DB the
// option configures and every Tx and shard it opens: they run before the
// entity's own and WithEntityHooks's, outermost, in the order given.
func WithAllHooks(hs []HookFn) Option {
	return Option{func(c *config) { c.AllHooks = append(c.AllHooks, hs...) }}
}

// EntityFilterFn is a filter WithFilter adds, as the engine runs it: given
// the statement's context, the DB the statement runs on (never nil), an
// entity's name (EntityName) and the op it is read for (RowCond's fop), it
// returns the entity's row condition.
type EntityFilterFn func(ctx context.Context, db *DB, entity string, op Op) (Predicate, error)

// WithFilter adds f to the row filters of every entity, for every DB the
// option configures and every Tx and shard it opens. The scope calls it
// where it calls an entity's interceptors, before them, and checks what it
// returns as theirs. A nil f panics.
func WithFilter(f EntityFilterFn) Option {
	if f == nil {
		panic("velox: WithFilter(nil)")
	}
	return Option{func(c *config) { c.Filters = append(c.Filters, f) }}
}

// EntityName is the name of entity in that WithFilter's filters and
// Mutation.Entity give: its Go type's, without the package, which for a
// generated entity is its schema's name.
func EntityName(in *EntityInfo) string {
	head := in.Name
	if i := strings.IndexByte(head, '['); i >= 0 {
		head = head[:i]
	}
	return in.Name[strings.LastIndexByte(head, '.')+1:]
}

// HasHooks reports whether a write of entity in on db runs hooks: the
// entity's own, those WithEntityHooks added, or WithAllHooks's.
func (db *DB) HasHooks(in *EntityInfo) bool {
	return len(in.hooks) > 0 || len(db.Cfg.Hooks[in]) > 0 || len(db.Cfg.AllHooks) > 0
}

// hooksOf is the hooks a write of in on db runs: WithAllHooks's, the
// entity's own, then WithEntityHooks's.
func (db *DB) hooksOf(in *EntityInfo) []HookFn {
	all, extra := db.Cfg.AllHooks, db.Cfg.Hooks[in]
	if len(all) == 0 && len(extra) == 0 {
		return in.hooks
	}
	return slices.Concat(all, in.hooks, extra)
}

// tables holds each entity's Table that MutationTable built, by its
// *EntityInfo: a map, not a field, so that EntityInfo's export data does
// not grow (TestGenericTypesCompileSmall).
var tables sync.Map

// MutationTable returns the Table of mu's entity, for a hook that makes a
// write itself: its columns, rows and writes. It names no target type, so a
// relation to the entity does not resolve through it: TableOf's does.
func MutationTable(mu *MutationState) Table {
	in := mu.entity()
	if in == nil || in.kit == nil {
		return Table{}
	}
	if t, ok := tables.Load(in); ok {
		return t.(Table)
	}
	typ := reflect.TypeOf(in.kit.Ptr(nil)).Elem()
	addr := func(p unsafe.Pointer, col int) any {
		c := &in.Columns[col]
		return reflect.NewAt(GoType(*c), field(c, p)).Interface()
	}
	get := func() (*EntityInfo, error) { return in, nil }
	t, _ := tables.LoadOrStore(in, NewTable(in, nil, addr, NilColumns(in), typ, RelTarget{Get: get}))
	return t.(Table)
}

// NilColumns are the columns of a slice or a map that database/sql cannot
// read a NULL into, nil when there are none: it knows of []byte alone, and
// its error fails each page holding the row. A pointer it reads, an encoded
// column and a type's own Scan are told of NULL themselves.
func NilColumns(info *EntityInfo) []bool {
	var cols []bool
	for i := range info.Columns {
		c := &info.Columns[i]
		t := rtype(c.Type)
		if t == nil || c.Codec || t.Kind() != reflect.Slice && t.Kind() != reflect.Map || t == reflect.TypeFor[[]byte]() ||
			reflect.PointerTo(t).Implements(reflect.TypeFor[interface{ Scan(src any) error }]()) {
			continue
		}
		if cols == nil {
			cols = make([]bool, len(info.Columns))
		}
		cols[i] = true
	}
	return cols
}
