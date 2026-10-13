// Package orm is the runtime of the code cmd/velox-gen generates: one
// implementation of queries, mutations, eager loading and paging, for every
// entity, that the thin generated types delegate to.
//
// An application rarely names this package. What it writes most is in
// package velox: the database handle and the transaction (velox.DB, velox.Tx,
// velox.Executor), the errors (velox.ErrNotFound, velox.IsNotFound, ...), the
// types of a paged list (velox.Cursor, velox.PageInfo) and velox.Must. It
// comes here for what the typed API of the generated packages cannot say:
//
//   - a condition, an order or a statement written against the SQL builder:
//     Raw, RawOrder, Compile, ExecSQL, QuerySQL;
//   - conditions built at run time or over any entity: FieldPredicate,
//     FieldOrder, And, Or, Not, IsZero, and a column as the plain column
//     type of its level (*OrderedColumn[T, E], through the column's
//     AsOrdered);
//   - a grouped read: Count, Sum, Min, Max, Mean, As;
//   - an input field that may be absent or null: Opt, Some, Null;
//   - a page without its total: WithoutTotal;
//   - a hook or an interceptor that works on the query or the mutation
//     itself: Query, Mutation, TraverseFunc, InterceptFunc.
//
// What an application calls without naming it is here too: the methods of
// the builders (QueryOf, CreateOf, CreateBulkOf, UpdateOf, DeleteOf), of the
// columns (StringColumn and the others of columns.go) and of the edges
// (ToOne, ToMany, LoadOf). A generated tree gives them the entity's names:
// entity.UserQuery, user.Name, user.Posts.
//
// The rest is for generated code: Meta, FieldMeta and EdgeMeta describe an
// entity; QueryCore and MutCore are what a query and a mutation hold; P, S
// and O are a condition, an assignment and an order term as data; ClientOf
// is the variable of an entity in the generated root package; FieldCode,
// StitchFields and the Default and Validators functions are what the
// generated schemainit package gives an entity the code of its schema with.
//
// Nothing here knows GraphQL. What the generated GraphQL layer needs at run
// time (its where and order inputs, the id of a node) is in
// contrib/graphql/gqlinput, which is built on this package.
//
// The exported types here are deliberately small, and reach no type of the
// SQL builder, of reflect or of database/sql by a field or by the signature
// of a method. A package compiles something for every type the types it
// names can reach, so a wide type here would be paid for in every package
// of the application (reach_test.go measures it).
package orm

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
)

// Rel is the kind of an edge.
type Rel uint8

// Edge kinds.
const (
	O2O Rel = iota + 1
	O2M
	M2O
	M2M
)

// FieldMeta describes one column of an entity.
type FieldMeta struct {
	Name      string // schema name
	Column    string
	Type      field.Type
	Nillable  bool // Go field is a pointer; NULL is nil
	Optional  bool // may be omitted on create
	NotNull   bool // the column does not accept NULL
	Immutable bool
	Sensitive bool // never printed
	// Default and UpdateDefault produce the value used when the field is not
	// set on create / on update. Validate checks a value before it is
	// written. All three are stitched from the schema by the generated
	// schemainit package and are nil when the schema declares none.
	Default       func() any
	UpdateDefault func() any
	Validate      func(any) error
	// Enums lists the allowed values of an enum field without a Go type.
	Enums []string
}

// Meta is the runtime description of one entity. The generated entity package
// declares one per entity; everything in this package runs on it. A Meta is
// used through its address and must not be copied.
type Meta struct {
	Name  string
	Table string
	// Fields are the schema fields, the id first.
	Fields []*FieldMeta
	// Columns are all selectable columns: Fields plus foreign keys that have
	// no schema field.
	Columns []string
	// New returns a new *Entity.
	New func() any
	// Ptr returns the address of the Go field of e that holds Columns[i]:
	// a *T, or a **T for a nillable field and for a foreign key without a
	// schema field. It is the only per-field code the generator emits; scan
	// destinations, reads and writes are derived from it here.
	Ptr func(e any, i int) any
	// Edge marks edge i of e loaded and returns the address of its field in
	// e.Edges: a *[]*Target or a **Target.
	Edge func(e any, i int) any
	// Link stores entities of this type into dst, an address returned by the
	// Edge function of an entity that has an edge to this one.
	Link func(dst any, entities []any)
	// Edges are the edges of the entity, in the order of Edge's index.
	Edges []*EdgeMeta
	// Hooks and Policy are the schema's (and its mixins'), stitched in by
	// the generated schemainit package. Without that package imported an
	// entity runs with neither. They are for that package to set: what
	// reads them, in this package, goes through hooks and policy, which
	// stitch first.
	Hooks  []velox.Hook
	Policy velox.Policy
	// Runtime says the schema has parts that only schemainit can provide
	// (defaults, validators, hooks, a policy). An entity with Runtime that
	// cannot be stitched refuses to run: without its policy it would answer
	// every caller. See Slot.
	Runtime bool
	// Package is the import path of the generated tree, for that error.
	Package string

	stitched atomic.Bool // schemainit has provided what Runtime promises
	stitchMu sync.Mutex  // held while it does
	stitchFn func(*Meta) // what schemainit gave the entity's slot
	once     sync.Once
	byColumn map[string]*FieldMeta
	byName   map[string]*FieldMeta
	byGoName map[string]*FieldMeta // by the name of the entity's struct field
	index    map[string]int
	cols     []column
	// typ is the reflect.Type of *Entity. It is held as any, and read
	// through typeOf: with reflect.Type among the types this one reaches,
	// every package that names a predicate (all of an application's) compiled
	// some 200 KB of what the reflect package's types need, 0.02 CPU-s each.
	typ any
}

// ready reports the error of running an entity whose schema runtime was not
// linked in.
func (m *Meta) ready() error {
	m.stitch()
	if m.Runtime && !m.stitched.Load() {
		return fmt.Errorf("velox: %s has defaults, validators, hooks or a policy that are not linked into this program: import _ %q", m.Name, m.Package+"/schemainit")
	}
	return nil
}

// Slot is where an entity meets the parts of its schema that are code:
// default functions, validators, hooks and a policy. The generated entity
// package binds the entity's Meta to its slot, the generated schemainit
// package gives the slot a function that fills a Meta in, and the function
// runs when the entity is first used (see Meta.stitch), or in StitchAll.
//
// The slot is what lets schemainit not import the entity package. Both are
// recompiled by an edit to a schema field, and the root package of the
// generated tree waits for both; importing one from the other made it wait
// for one after the other (1.8 s of the 20 s a rebuild takes on a 450-entity
// schema).
type Slot struct {
	m  *Meta
	fn func(*Meta)
}

// Bind gives the slot its entity.
func (s *Slot) Bind(m *Meta) {
	s.m = m
	slotted.Lock()
	slotted.metas = append(slotted.metas, m)
	slotted.Unlock()
	s.join()
}

// Stitch gives the slot the function that fills the entity's Meta in.
func (s *Slot) Stitch(fn func(*Meta)) {
	s.fn = fn
	s.join()
}

func (s *Slot) join() {
	if s.m == nil || s.fn == nil {
		return
	}
	s.m.stitchMu.Lock()
	s.m.stitchFn = s.fn
	s.m.stitchMu.Unlock()
}

// slotted are the entities that have a slot.
var slotted struct {
	sync.Mutex
	metas []*Meta
}

// stitch fills m in with the code of its schema, the first time something
// of it is asked for: the function schemainit gave the entity's slot calls
// the schema's Fields, Mixin, Hooks and Policy, and what those build is
// built once per entity that is used, not once per entity when the program
// starts. On a schema of 455 entities, stitching them all took a program
// 160 ms, 550,000 allocations and 52 MB before main, every time it started:
// a test binary, a command that touches three tables.
//
// Everything that reads what the schema provides calls it first: ready, for
// the entity a query or a mutation is of, and hooks and policy, for an
// entity reached another way (the target of an edge, the entity of a join
// table). A function that reads a field's Default, UpdateDefault or
// Validate calls it itself.
func (m *Meta) stitch() {
	if m.stitched.Load() {
		return
	}
	m.stitchMu.Lock()
	defer m.stitchMu.Unlock()
	if m.stitched.Load() || m.stitchFn == nil {
		return
	}
	m.stitchFn(m)
	m.stitched.Store(true)
}

// hooks returns the hooks of the schema.
func (m *Meta) hooks() []velox.Hook {
	m.stitch()
	return m.Hooks
}

// policy returns the policy of the schema, or nil.
func (m *Meta) policy() velox.Policy {
	m.stitch()
	return m.Policy
}

// StitchAll fills every entity in with the code of its schema now, where
// each is otherwise filled in when it is first used. A program calls it
// when it starts to learn then, and not at a first request, that the
// generated code is the schema's (a default or a validator of another type
// than the generated code expects panics here), or to keep the first use of
// each entity from paying for it.
func StitchAll() {
	slotted.Lock()
	metas := slices.Clone(slotted.metas)
	slotted.Unlock()
	for _, m := range metas {
		m.stitch()
	}
}

// Describe renders an entity for logs: every column but the sensitive ones.
// Generated String methods of entities with sensitive fields call it.
func Describe(m *Meta, e any) string {
	m.prepare()
	var b strings.Builder
	b.WriteString(m.Name)
	b.WriteByte('(')
	for i, name := range m.Columns {
		f := m.byColumn[name]
		if f == nil {
			continue
		}
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(f.Name)
		b.WriteByte('=')
		if f.Sensitive {
			b.WriteString("<sensitive>")
			continue
		}
		fmt.Fprint(&b, m.read(e, i))
	}
	b.WriteByte(')')
	return b.String()
}

// column is what the runtime knows about Columns[i].
type column struct {
	dest destKind
	ptr  bool // the Go field is a pointer to the value
	zero any  // the zero value of the Go field's base type
}

// typeOf returns the type of the pointer to the entity's struct. It is a
// function and not a method of Meta for what the field is an any for: a
// method's signature is part of what the type reaches.
func typeOf(m *Meta) reflect.Type {
	t, _ := m.typ.(reflect.Type)
	return t
}

// id returns the primary-key field.
func (m *Meta) id() *FieldMeta {
	if len(m.Fields) == 0 {
		return nil
	}
	return m.Fields[0]
}

// prepare derives the per-column tables from Fields, Columns and Ptr.
func (m *Meta) prepare() {
	m.once.Do(func() {
		m.byColumn = make(map[string]*FieldMeta, len(m.Fields))
		m.byName = make(map[string]*FieldMeta, len(m.Fields))
		for _, f := range m.Fields {
			m.byColumn[f.Column] = f
			m.byName[f.Name] = f
		}
		m.index = make(map[string]int, len(m.Columns))
		m.cols = make([]column, len(m.Columns))
		sample := m.New()
		m.typ = reflect.TypeOf(sample)
		at := make(map[uintptr]*FieldMeta, len(m.Fields)) // address of a struct field -> its column
		for i, name := range m.Columns {
			m.index[name] = i
			f := m.byColumn[name]
			ptr := f == nil || f.Nillable
			m.cols[i] = column{dest: destKindOf(f, f != nil && f == m.id(), ptr, m.Ptr(sample, i)), ptr: ptr}
			if f != nil {
				at[reflect.ValueOf(m.Ptr(sample, i)).Pointer()] = f
			}
			if !ptr {
				m.cols[i].zero = m.read(sample, i)
				// A nil byte slice is stored as NULL; the zero value of a
				// bytes column is the empty one, in the field's own type.
				if rv := reflect.ValueOf(m.cols[i].zero); rv.Kind() == reflect.Slice && rv.IsNil() && rv.Type().Elem().Kind() == reflect.Uint8 {
					m.cols[i].zero = reflect.MakeSlice(rv.Type(), 0, 0).Interface()
				}
			}
		}
		// The struct field of each column, for what is matched by name.
		m.byGoName = make(map[string]*FieldMeta, len(m.Fields))
		if ev := reflect.ValueOf(sample); ev.Kind() == reflect.Pointer && ev.Elem().Kind() == reflect.Struct {
			ev = ev.Elem()
			for j := range ev.NumField() {
				if sf := ev.Type().Field(j); sf.IsExported() && sf.Type.Size() > 0 {
					if f := at[ev.Field(j).Addr().Pointer()]; f != nil {
						m.byGoName[sf.Name] = f
					}
				}
			}
		}
	})
}

// Field returns the schema field stored in column, or nil.
func (m *Meta) Field(column string) *FieldMeta {
	m.prepare()
	return m.byColumn[column]
}

// col returns the index of column in Columns, or -1.
func (m *Meta) col(column string) int {
	m.prepare()
	if i, ok := m.index[column]; ok {
		return i
	}
	return -1
}

// EdgeMeta describes one edge. Table and Columns follow sqlgraph: for O2M and
// a non-inverse O2O the table is the target's and Columns[0] its foreign key;
// for M2O and an inverse O2O the table is the owner's; for M2M the table is
// the join table and Columns are (owner key, target key) from the owner's side.
type EdgeMeta struct {
	Name    string
	Owner   *Meta
	Target  *Meta
	Rel     Rel
	Inverse bool
	Bidi    bool
	Table   string
	Columns []string
	// Index is the position of the edge among its owner's edges, as the
	// owner's Edge function counts them.
	Index int
	// Immutable edges cannot be changed by an update.
	Immutable bool
	// Required edges cannot be cleared.
	Required bool
	// Through is the entity of the join table of an M2M edge declared with
	// an edge schema; its defaults are written with every join row.
	Through *Meta
}

// link stores the loaded neighbors of parent and marks the edge loaded.
func (e *EdgeMeta) link(parent any, neighbors []any) {
	e.Target.Link(e.Owner.Edge(parent, e.Index), neighbors)
}

// fkOnOwner reports whether the foreign key of the edge is a column of the
// owner's table.
func (e *EdgeMeta) fkOnOwner() bool {
	return e.Rel == M2O || (e.Rel == O2O && e.Inverse)
}
