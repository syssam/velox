// Package orm is the runtime of generated code: one entity-agnostic
// implementation of queries, mutations, eager loading and transactions that
// the thin generated wrappers of every entity delegate to.
//
// The exported types of this package are deliberately small and mention no
// SQL-builder type. Generated code and application code name them in
// signatures, and the Go compiler writes every type a signature reaches into
// each package's export data; a wide type here would be paid for in every
// package of the application.
package orm

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

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
	// entity runs with neither.
	Hooks  []velox.Hook
	Policy velox.Policy
	// Runtime says the schema has parts that only schemainit can provide
	// (defaults, validators, hooks, a policy). An entity with Runtime that
	// was not stitched refuses to run: without its policy it would answer
	// every caller. See Slot.
	Runtime bool
	// Package is the import path of the generated tree, for that error.
	Package string

	stitched bool // schemainit has provided what Runtime promises
	once     sync.Once
	byColumn map[string]*FieldMeta
	byName   map[string]*FieldMeta
	byGoName map[string]*FieldMeta // by the name of the entity's struct field
	index    map[string]int
	cols     []column
	typ      reflect.Type // *Entity
}

// ready reports the error of running an entity whose schema runtime was not
// linked in.
func (m *Meta) ready() error {
	if m.Runtime && !m.stitched {
		return fmt.Errorf("velox: %s has defaults, validators, hooks or a policy that are not linked into this program: import _ %q", m.Name, m.Package+"/schemainit")
	}
	return nil
}

// Slot is where an entity meets the parts of its schema that are code:
// default functions, validators, hooks and a policy. The generated entity
// package binds the entity's Meta to its slot, the generated schemainit
// package gives the slot a function that fills a Meta in, and the function
// runs as soon as the slot has both: during package initialization, before
// any query can.
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
	s.join()
}

// Stitch gives the slot the function that fills the entity's Meta in.
func (s *Slot) Stitch(fn func(*Meta)) {
	s.fn = fn
	s.join()
}

func (s *Slot) join() {
	if s.m == nil || s.fn == nil || s.m.stitched {
		return
	}
	s.fn(s.m)
	s.m.stitched = true
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
