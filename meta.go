package velox

import (
	"context"
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
	rels       []*relation                // in the order of the generated shape
	kit        *kit                       // set by MetaOnce.Get and Load; nil in tests that call buildInfo
	check      func(col int, v any) error // Desc.Check; nil when the entity has no validators
	immutable  bool                       // some column is Immutable
	hooks      []hookFn                   // the Hooks method's, outermost first (Desc only)

	// The entity's policy and hooks, wrapped once by the generic Schema
	// methods into functions over the entity's address and its statement.
	policy       *policy                                             // nil when none
	beforeInsert []func(ctx context.Context, p unsafe.Pointer) error // mixins' first, in declaration order
	beforeUpdate []func(ctx context.Context, st *Statement) error
	beforeDelete []func(ctx context.Context, st *Statement) error
}

// policy is an entity's Policy, with E erased.
type policy struct {
	read, write func(ctx context.Context) (Predicate, error) // nil when not declared
	insert      func(ctx context.Context, p unsafe.Pointer) error
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
	Field       string // the Go field name
	Name        string // the column name
	Type        any    // the field's Go type, a reflect.Type: read it with GoType
	Nullable    bool   // the field is a pointer
	SQLType     string // empty: the dialect's default for Type
	Default     any
	HasDefault  bool
	DefaultFunc func() any // nil when none
	// UpdateDefault is the function whose result every insert and update
	// writes to the column; nil when none.
	UpdateDefault func() any
	Immutable     bool    // an update may not assign the column (Desc only)
	Sensitive     bool    // the column holds a secret (Desc only; velox logs no values)
	off           uintptr // the field's offset in the entity
}

// GoType returns the Go type of column c's field. ColumnInfo.Type holds it
// as any, and this is a function rather than a method, so that no velox type
// reaches reflect.Type: every package that reaches it compiles about 134 KiB
// of wrappers for reflect's iterator types (design §3 rule 7).
func GoType(c ColumnInfo) reflect.Type { return rtype(c.Type) }

// rtype returns the reflect.Type that v holds; velox's types hold them as any
// for the reason given on GoType.
func rtype(v any) reflect.Type {
	t, _ := v.(reflect.Type)
	return t
}

// elemType returns, as any, the reflect.Type of what the pointer p points to.
// Generic code calls it with a nil *T in place of reflect.TypeFor[T](), so
// that its body, compiled where it is called, holds no reflect.Type (design
// §3 rule 7).
func elemType(p any) any { return reflect.TypeOf(p).Elem() }

// IndexInfo describes one index.
type IndexInfo struct {
	Columns []int
	Unique  bool
}

// Shape is what velox-gen writes for an entity: the fields it saw and the
// scan plan.
type Shape[E any] struct {
	Columns   []string
	Relations []Relation
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
		var addr func(*E, int) any
		x := new(E)
		info, err := buildEntity(x, unsafe.Pointer(x), func() ([]string, []Relation, bool) {
			sh := shape()
			addr = sh.Addr
			return sh.Columns, sh.Relations, sh.Addr != nil
		})
		if err != nil {
			addr = nil // an error Meta has no scan plan, as when Schema panics
		}
		o.m = &Meta[E]{info: info, addr: addr, err: err}
		if info != nil && addr != nil {
			info.kit = kitOf(info, addr)
		}
	})
	return o.m
}

// buildEntity builds the metadata of the entity x, a new *E at base, from the
// column and relation names shape returns, and whether it has a scan plan. It
// holds the entity's reflect.Type so that MetaOnce.Get, compiled in every
// entity's package, holds none (design §3 rule 7). A panic in shape, a
// mixin's Mixin or the entity's Schema is recovered as a *SchemaError.
func buildEntity(x any, base unsafe.Pointer, shape func() (cols []string, rels []Relation, addr bool)) (info *EntityInfo, err error) {
	t := reflect.TypeOf(x).Elem()
	defer func() {
		if p := recover(); p != nil {
			info, err = nil, &SchemaError{Entity: t.String(), Reason: fmt.Sprintf("Schema panicked: %v", p)}
		}
	}()
	cols, rels, addr := shape()
	info, err = buildInfo(t, base, x, cols, rels)
	if err == nil && !addr { // after buildInfo, so that stale code is reported as such
		err = &SchemaError{Entity: t.String(), Reason: "the generated code has no scan plan (Shape.Addr is nil); run go tool velox-gen"}
	}
	if err == nil {
		err = descMethods(t)
	}
	return info, err
}

// descMethods refuses, on an entity built from its Schema method, a Hooks or
// Policy method: only generated (Desc) entities' are read, so it would be
// ignored. The names are constants, so that the linker can still drop the
// methods nobody calls.
func descMethods(t reflect.Type) error {
	pt := reflect.PointerTo(t)
	if _, ok := pt.MethodByName("Hooks"); ok {
		return &SchemaError{Entity: t.String(), Reason: "has a Hooks method, which only generated (Desc) entities use; declare hooks with s.BeforeInsert, s.BeforeUpdate and s.BeforeDelete in Schema, or move to the Desc path"}
	}
	if _, ok := pt.MethodByName("Policy"); ok {
		return &SchemaError{Entity: t.String(), Reason: "has a Policy method, which only generated (Desc) entities use; declare the policy with s.Policy in Schema, or move to the Desc path"}
	}
	return nil
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
func flatten(t reflect.Type, base uintptr, cols, rels []field) ([]field, []field, error) {
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
			rels = append(rels, field{name: f.Name, off: base + f.Offset, typ: f.Type})
		case f.Type.Size() == 0:
			// It holds no value, and it shares its address with the next field,
			// so a pointer to it cannot name it.
			return nil, nil, fmt.Errorf("field %s has the zero-size type %v, which holds no value; tag it velox:\"-\"", f.Name, f.Type)
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

func checkShape(entity string, fs, rs []field, cols []string, rels []Relation) error {
	got := make([]string, len(fs))
	for i := range fs {
		got[i] = fs[i].name
	}
	if d := firstDiff(got, cols); d != "" {
		return fmt.Errorf("velox: %s: %w: columns %s; run go tool velox-gen", entity, ErrStale, d)
	}
	gotR, wantR := make([]string, len(rs)), make([]string, len(rels))
	for i := range rs {
		gotR[i] = rs[i].name
	}
	for i := range rels {
		wantR[i] = rels[i].name
	}
	if d := firstDiff(gotR, wantR); d != "" {
		return fmt.Errorf("velox: %s: %w: relations %s; run go tool velox-gen", entity, ErrStale, d)
	}
	return nil
}

// applyMixins calls Mixin on every Mixer embedded directly in the entity.
// While a Mixin runs, s knows the mixin's type and offset, to check and place
// its hooks.
func applyMixins(s *Schema, t reflect.Type, base unsafe.Pointer) {
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.Anonymous || f.Type.Kind() != reflect.Struct || f.Tag.Get("velox") == "-" {
			continue
		}
		if reflect.PointerTo(f.Type).Implements(mixerType) {
			s.mixin, s.mixinOff = f.Type, f.Offset
			reflect.NewAt(f.Type, unsafe.Add(base, f.Offset)).Interface().(Mixer).Mixin(s)
			s.mixin, s.mixinOff = nil, 0
		}
	}
}

// buildInfo builds an entity's metadata from its type, the column and
// relation names velox-gen saw (cols, rels), its mixins and its Schema
// method. x is a zero entity at base.
func buildInfo(t reflect.Type, base unsafe.Pointer, x any, cols []string, rels []Relation) (*EntityInfo, error) {
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
		s.cols[i] = ColumnInfo{Field: f.name, Name: snake(f.name), Type: f.typ, Nullable: rtype(f.typ).Kind() == reflect.Pointer, off: f.off}
	}
	s.rels = make([]*relation, len(rs))
	for i, r := range rs {
		s.rels[i] = &relation{name: r.name, many: strings.HasPrefix(rtype(r.typ).Name(), "Many["), off: r.off, typ: r.typ, target: rels[i].target, ref: -1}
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
	info := &EntityInfo{Name: name, Table: s.table, Schema: s.schema, Columns: s.cols, PrimaryKey: s.pk, Tenant: s.tenant, Indexes: s.idx, rels: s.rels,
		policy: s.policy, beforeInsert: append(s.mixinInsert, s.beforeInsert...), beforeUpdate: s.beforeUpdate, beforeDelete: s.beforeDelete}
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
// name, a key or index that names no field or one field twice, and a tenant
// column that cannot be compared or filled safely.
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
	if info.Tenant >= 0 {
		if c := info.Columns[info.Tenant]; rtype(c.Type).Kind() == reflect.Pointer || !rtype(c.Type).Comparable() {
			fail(c.Field, "the tenant column must be NOT NULL and comparable")
		}
		// Filled by a default, a row would land in a tenant the context
		// never named; and UpdateDefault would move rows between tenants.
		if c := info.Columns[info.Tenant]; c.HasDefault || c.DefaultFunc != nil || c.UpdateDefault != nil {
			fail(c.Field, "the tenant column cannot have Default, DefaultFunc or UpdateDefault: the context's tenant fills it")
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

// CheckEntity builds entity E's metadata and resolves its relations, and
// returns what is wrong: a mistake in its Schema or Mixin methods, a relation
// whose key is missing or mistyped, or generated code that is out of date.
// velox-gen verify calls it for every entity.
func CheckEntity[E any, PE Entity[E]]() error {
	m := PE(nil).VeloxMeta()
	if m.err != nil {
		return m.err
	}
	var errs []error
	for _, r := range m.info.rels {
		if _, err := r.resolve(m.info); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// kitOf builds entity E's kit from its scan plan.
func kitOf[E any](info *EntityInfo, addr func(*E, int) any) *kit {
	m := &Meta[E]{info: info, addr: addr}
	return &kit{
		value: func(p unsafe.Pointer, col int) any { return elem(addr((*E)(p), col)) },
		scan: func(r rows, cols []int) (any, error) {
			n := len(info.Columns)
			if cols != nil {
				n = len(cols)
			}
			dst := make([]any, n)
			var part []E
			for r.Next() {
				part = append(part, *new(E))
				if err := scanRow(r, m, cols, dst, &part[len(part)-1]); err != nil {
					return nil, err
				}
			}
			return part, nil
		},
		join: func(parts []any) (any, []unsafe.Pointer) {
			var all []E
			for _, p := range parts {
				all = append(all, p.([]E)...)
			}
			ptrs := make([]unsafe.Pointer, len(all))
			for i := range all {
				ptrs[i] = unsafe.Pointer(&all[i])
			}
			return all, ptrs
		},
		pick: func(all any, idx []int) any {
			s := all.([]E)
			out := make([]E, len(idx))
			for i, j := range idx {
				out[i] = s[j]
			}
			return out
		},
		ptr:   func(all any, i int) any { return &all.([]E)[i] },
		empty: func() any { return []E{} },
	}
}

// elem is the value p points to. It is not generic, so that the reflect.Value
// it uses is not compiled into the generic code that calls it (design §3
// rule 7).
func elem(p any) any { return reflect.ValueOf(p).Elem().Interface() }

// metaOf returns entity E's Meta, for generic code that cannot name E's
// pointer type in its constraints (a method of Rel[E, T]).
func metaOf[E any]() (*Meta[E], error) {
	p, ok := any((*E)(nil)).(interface{ VeloxMeta() *Meta[E] })
	if !ok {
		return nil, fmt.Errorf("velox: %v is not an entity: run go tool velox-gen", elemType((*E)(nil)))
	}
	m := p.VeloxMeta()
	return m, m.err
}
