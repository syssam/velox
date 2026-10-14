package velox

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unsafe"
)

// EntityInfo is what velox knows about an entity, built once on its first
// use. It is the non-generic view that hooks see in a Statement.
//
// One EntityInfo is shared by every query on the entity, in every goroutine,
// so it must be treated as read-only, slices included. A hook that needs
// different metadata for a statement copies it and points Statement.Entity
// at the copy; it never changes the shared one.
type EntityInfo struct {
	Name       string // the Go type, e.g. "billing.Invoice"
	Table      string
	Columns    []ColumnInfo
	PrimaryKey []int // indexes into Columns
	Tenant     int   // index into Columns; -1 when the entity has none
	Indexes    []IndexInfo
}

// Column returns the index of the column with the given name.
func (e *EntityInfo) Column(name string) (int, bool) {
	for i := range e.Columns {
		if e.Columns[i].Name == name {
			return i, true
		}
	}
	return -1, false
}

// ColumnInfo describes one column.
type ColumnInfo struct {
	Field      string       // the Go field name
	Name       string       // the column name
	Type       reflect.Type // the field's Go type
	Nullable   bool         // the field is a pointer
	SQLType    string       // empty: the dialect's default for Type
	Default    any
	HasDefault bool
	OnUpdate   func() any // nil when none
}

// IndexInfo describes one index.
type IndexInfo struct {
	Columns []int
	Unique  bool
}

// Shape is what velox-gen writes for an entity: the fields it saw and the
// scan plan.
type Shape[E any] struct {
	Columns   []string
	Relations []string
	Addr      func(x *E, i int) any
}

// Meta is an entity's metadata and scan plan.
type Meta[E any] struct {
	info *EntityInfo
	addr func(*E, int) any
	err  error
}

// MetaOnce builds an entity's Meta on first use. Its zero value is ready, so
// a generated file declares one without running anything at program start.
type MetaOnce[E any] struct {
	once sync.Once
	m    *Meta[E]
}

// Get returns the entity's Meta, building it from shape the first time.
func (o *MetaOnce[E]) Get[_ Lazy[E]](shape func() Shape[E]) *Meta[E] {
	o.once.Do(func() {
		sh := shape()
		x := new(E)
		info, err := buildInfo(reflect.TypeFor[E](), unsafe.Pointer(x), any(x), sh.Columns, sh.Relations)
		o.m = &Meta[E]{info: info, addr: sh.Addr, err: err}
	})
	return o.m
}

// Entity is the constraint velox's generic API puts on an entity type E: its
// pointer has the method velox-gen writes.
type Entity[E any] interface {
	*E
	VeloxMeta() *Meta[E]
}

var (
	veloxPkg  = reflect.TypeFor[Schema]().PkgPath()
	mixerType = reflect.TypeFor[Mixer]()
)

// isRelation reports a One or Many field. It goes by the type's name: a
// marker method would give every instantiation a method table, compiled into
// every package that uses it (design §3 rule 3).
func isRelation(t reflect.Type) bool {
	if t.PkgPath() != veloxPkg {
		return false
	}
	n := t.Name()
	return strings.HasPrefix(n, "One[") || strings.HasPrefix(n, "Many[")
}

// flatten lists an entity's columns and relations in declaration order,
// descending into embedded structs.
func flatten(t reflect.Type, base uintptr, cols []field, rels []string) ([]field, []string, error) {
	for i := range t.NumField() {
		f := t.Field(i)
		switch {
		case f.Tag.Get("velox") == "-":
		case f.Anonymous && f.Type.Kind() == reflect.Pointer:
			return nil, nil, fmt.Errorf("embedded pointer %s is not supported; embed the struct itself", f.Name)
		case f.Anonymous && f.Type.Kind() == reflect.Struct:
			var err error
			if cols, rels, err = flatten(f.Type, base+f.Offset, cols, rels); err != nil {
				return nil, nil, err
			}
		case !f.IsExported():
		case isRelation(f.Type):
			rels = append(rels, f.Name)
		default:
			cols = append(cols, field{name: f.Name, off: base + f.Offset, typ: f.Type})
		}
	}
	return cols, rels, nil
}

func firstDiff(got, want []string) string {
	for i := range max(len(got), len(want)) {
		g, w := "(none)", "(none)"
		if i < len(got) {
			g = got[i]
		}
		if i < len(want) {
			w = want[i]
		}
		if g != w {
			return fmt.Sprintf("#%d is %s in the struct and %s in the generated code", i, g, w)
		}
	}
	return ""
}

func checkShape(entity string, fs []field, rs, cols, rels []string) error {
	got := make([]string, len(fs))
	for i := range fs {
		got[i] = fs[i].name
	}
	if d := firstDiff(got, cols); d != "" {
		return fmt.Errorf("%w: %s: columns %s; run go tool velox-gen", ErrStale, entity, d)
	}
	if d := firstDiff(rs, rels); d != "" {
		return fmt.Errorf("%w: %s: relations %s; run go tool velox-gen", ErrStale, entity, d)
	}
	return nil
}

// applyMixins calls Mixin on every Mixer embedded directly in the entity.
func applyMixins(s *Schema, t reflect.Type, base unsafe.Pointer) {
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.Anonymous || f.Type.Kind() != reflect.Struct || f.Tag.Get("velox") == "-" {
			continue
		}
		if reflect.PointerTo(f.Type).Implements(mixerType) {
			reflect.NewAt(f.Type, unsafe.Add(base, f.Offset)).Interface().(Mixer).Mixin(s)
		}
	}
}

// buildInfo builds an entity's metadata from its type, the column and
// relation names velox-gen saw (cols, rels), its mixins and its Schema
// method. x is a zero entity at base.
func buildInfo(t reflect.Type, base unsafe.Pointer, x any, cols, rels []string) (*EntityInfo, error) {
	name := t.String()
	fs, rs, err := flatten(t, 0, nil, nil)
	if err != nil {
		return nil, &SchemaError{Entity: name, Reason: err.Error()}
	}
	if err := checkShape(name, fs, rs, cols, rels); err != nil {
		return nil, err
	}
	s := &Schema{entity: name, base: base, size: t.Size(), fields: fs, tenant: -1}
	s.cols = make([]ColumnInfo, len(fs))
	for i, f := range fs {
		s.cols[i] = ColumnInfo{Field: f.name, Name: snake(f.name), Type: f.typ, Nullable: f.typ.Kind() == reflect.Pointer}
	}
	applyMixins(s, t, base)
	sc, ok := x.(Schemer)
	if !ok {
		return nil, &SchemaError{Entity: name, Reason: "has no Schema(*velox.Schema) method"}
	}
	sc.Schema(s)
	if len(s.errs) > 0 {
		return nil, errors.Join(s.errs...)
	}
	info := &EntityInfo{Name: name, Table: s.table, Columns: s.cols, PrimaryKey: s.pk, Tenant: s.tenant, Indexes: s.idx}
	if info.Table == "" {
		info.Table = snake(t.Name())
	}
	if len(info.PrimaryKey) == 0 {
		for i, f := range fs {
			if f.name == "ID" {
				info.PrimaryKey = []int{i}
			}
		}
		if len(info.PrimaryKey) == 0 {
			return nil, &SchemaError{Entity: name, Reason: "no primary key: name a field ID or call s.PrimaryKey"}
		}
	}
	return info, nil
}
