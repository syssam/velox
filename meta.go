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
	Schema     string // the schema holding Table; empty for the default
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
	Field       string       // the Go field name
	Name        string       // the column name
	Type        reflect.Type // the field's Go type
	Nullable    bool         // the field is a pointer
	SQLType     string       // empty: the dialect's default for Type
	Default     any
	HasDefault  bool
	DefaultFunc func() any // nil when none
	OnUpdate    func() any // nil when none
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
//
// The build runs once, so a panic in it (in the entity's Schema method, a
// mixin's Mixin or shape) is recovered and kept as the Meta's error, a
// *SchemaError: every use of the entity reports it, not just the first.
func (o *MetaOnce[E]) Get[_ Lazy[E]](shape func() Shape[E]) *Meta[E] {
	o.once.Do(func() {
		t := reflect.TypeFor[E]()
		defer func() {
			if p := recover(); p != nil {
				o.m = &Meta[E]{err: &SchemaError{Entity: t.String(), Reason: fmt.Sprintf("Schema panicked: %v", p)}}
			}
		}()
		sh := shape()
		x := new(E)
		info, err := buildInfo(t, unsafe.Pointer(x), any(x), sh.Columns, sh.Relations)
		if err == nil && sh.Addr == nil { // after buildInfo, so that stale code is reported as such
			err = &SchemaError{Entity: t.String(), Reason: "the generated code has no scan plan (Shape.Addr is nil); run go tool velox-gen"}
		}
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
	s := &Schema{entity: name, base: base, size: t.Size(), fields: fs, typ: t, tenant: -1}
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
	info := &EntityInfo{Name: name, Table: s.table, Schema: s.schema, Columns: s.cols, PrimaryKey: s.pk, Tenant: s.tenant, Indexes: s.idx}
	if info.Table == "" {
		info.Table = snake(t.Name())
	}
	if !s.pkSet {
		for i, f := range fs {
			if f.name == "ID" {
				info.PrimaryKey = []int{i}
			}
		}
		if len(info.PrimaryKey) == 0 {
			return nil, &SchemaError{Entity: name, Reason: "no primary key: name a field ID or call s.PrimaryKey"}
		}
	}
	if err := validate(info); err != nil {
		return nil, err
	}
	return info, nil
}

// validate refuses metadata that would render wrong SQL: two columns of one
// name, and a key or index that names no field or one field twice.
func validate(info *EntityInfo) error {
	var errs []error
	fail := func(field, format string, args ...any) {
		errs = append(errs, &SchemaError{Entity: info.Name, Field: field, Reason: fmt.Sprintf(format, args...)})
	}
	seen := make(map[string]int, len(info.Columns))
	for i, c := range info.Columns {
		if j, ok := seen[c.Name]; ok {
			fail(c.Field, "column name %q is also the name of %s", c.Name, info.Columns[j].Field)
			continue
		}
		seen[c.Name] = i
	}
	set := func(what string, cols []int) {
		if len(cols) == 0 {
			fail("", "%s names no fields", what)
			return
		}
		named := make(map[int]bool, len(cols))
		for _, c := range cols {
			if named[c] {
				fail(info.Columns[c].Field, "%s names the field twice", what)
			}
			named[c] = true
		}
	}
	set("the primary key", info.PrimaryKey)
	for _, ix := range info.Indexes {
		if ix.Unique {
			set("a unique index", ix.Columns)
		} else {
			set("an index", ix.Columns)
		}
	}
	return errors.Join(errs...)
}

// CheckEntity builds entity E's metadata and returns the error it holds, if
// any: a mistake in its Schema or Mixin methods, or generated code that is
// out of date. velox-gen verify calls it for every entity.
func CheckEntity[E any, PE Entity[E]]() error { return PE(nil).VeloxMeta().err }
