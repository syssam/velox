package engine

import (
	"fmt"
	"reflect"
	"strings"
	"unsafe"
)

// DescriptorFields are the fields of a Descriptor that do not depend on E, so that
// BuildDescriptor, which uses reflect, need not be generic.
type DescriptorFields struct {
	Table      string
	Schema     string
	Columns    []ColumnDescriptor
	PrimaryKey []int
	Tenant     int
	Bidi       bool
	Indexes    []IndexInfo
	Relations  []EdgeDescriptor
	TenantRefs []TenantRef
	Check      func(col int, v any) error
	Policy     bool // Descriptor.Policy is set
	Hooks      bool // Descriptor.Hooks is set
}

// DescriptorView is a Descriptor with E erased: its fields, and its Addr and EdgeAddr
// bound to one new entity. addr and relAddr are nil when Addr and EdgeAddr
// are.
type DescriptorView struct {
	D       *DescriptorFields
	Addr    func(i int) any // d.Addr(x, i)
	RelAddr func(i int) any // d.EdgeAddr(x, i)
}

// BuildDescriptor builds the metadata of the entity x, a new *E at base, from the
// Descriptor that load returns with E erased. A panic in load, or in the Descriptor's
// functions, is recovered as a *SchemaError, so that it is the entity's error
// on every use rather than a crash on the first.
func BuildDescriptor(x any, base unsafe.Pointer, load func() DescriptorView) (info *EntityInfo, err error) {
	t := reflect.TypeOf(x).Elem()
	name := t.String()
	defer func() {
		if p := recover(); p != nil {
			info, err = nil, &SchemaError{Entity: name, Reason: fmt.Sprintf("the generated metadata panicked: %v", p)}
		}
	}()
	v := load()
	d := v.D
	fail := func(field, format string, args ...any) (*EntityInfo, error) {
		return nil, &SchemaError{Entity: name, Field: field, Reason: fmt.Sprintf(format, args...)}
	}
	if v.Addr == nil {
		return fail("", "the generated metadata has no Addr; run go tool velox-gen")
	}
	if d.Table == "" {
		return fail("", "the generated metadata has no Table; run go tool velox-gen")
	}
	if err := unboundMethods(t, d); err != nil {
		return nil, err
	}
	cols := make([]ColumnInfo, len(d.Columns))
	anyImmutable, anyRequired := false, false
	for i, c := range d.Columns {
		off, ft, ok := fieldOf(v.Addr(i), base, t.Size())
		if !ok {
			// The column's offset and type are read from this pointer, and
			// every scan writes through it: anything else is no column.
			return fail(c.Field, "Addr does not return a pointer into the entity for column %d", i)
		}
		ci := ColumnInfo{Field: c.Field, Name: c.Name, Type: ft, Nullable: ft.Kind() == reflect.Pointer || c.Nullable,
			HasDefault: c.Default != nil, DefaultFunc: c.DefaultFunc, UpdateDefault: c.UpdateDefault,
			Immutable: c.Immutable, Sensitive: c.Sensitive, RequiredEdge: c.RequiredEdge, Codec: c.Encode != nil, encode: c.Encode, decode: c.Decode, off: off}
		if (c.Encode == nil) != (c.Decode == nil) {
			return fail(c.Field, "column %s has one of Encode and Decode; a codec needs both", c.Name)
		}
		if k := ft.Kind(); c.Nullable && k != reflect.Pointer && k != reflect.Slice && k != reflect.Map {
			return fail(c.Field, "column %s says Nullable, and its field is a %v, which cannot be nil: make it a pointer", c.Name, ft)
		}
		if c.RequiredEdge != "" && !ci.Nullable {
			return fail(c.Field, "column %s has a RequiredEdge, and cannot be NULL: the database refuses it unset", c.Name)
		}
		if c.Immutable && c.UpdateDefault != nil {
			return fail(c.Field, "Immutable column %s has an UpdateDefault; every update would be refused", c.Field)
		}
		if c.Default != nil {
			if boolOrNumber(ft) {
				// A bool or numeric field cannot have a Default: its zero value is also a
				// real value, and the insert would overwrite an explicit false or 0.
				return fail(c.Field, "Default on bool/numeric field %s would overwrite an explicit false or 0 on insert; make the field a pointer (nil uses the default)", c.Field)
			}
			if ci.Default, ok = defaultOf(c.Default, ft); !ok {
				// FillDefault sets the field to it: a value of another type
				// would panic on the first insert instead of failing here.
				return fail(c.Field, "Default is a %T, not a %v", c.Default, ft)
			}
			ci.copyDefault = holdsRefs(reflect.TypeOf(ci.Default))
		}
		cols[i] = ci
		anyImmutable = anyImmutable || c.Immutable
		anyRequired = anyRequired || c.RequiredEdge != ""
	}
	inRange := func(i int) bool { return i >= 0 && i < len(cols) }
	for _, k := range d.PrimaryKey {
		if !inRange(k) {
			return fail("", "the primary key names column %d, of %d", k, len(cols))
		}
	}
	for _, ix := range d.Indexes {
		for _, k := range ix.Columns {
			if !inRange(k) {
				return fail("", "an index names column %d, of %d", k, len(cols))
			}
		}
	}
	if d.Tenant != -1 && !inRange(d.Tenant) {
		return fail("", "the tenant names column %d, of %d", d.Tenant, len(cols))
	}
	if d.Bidi {
		if err := checkBidi(name, d, cols); err != nil {
			return nil, err
		}
	}
	rels := make([]*relation, len(d.Relations))
	var bidiKeys []bidiKey
	if len(rels) > 0 && v.RelAddr == nil {
		return fail("", "the generated metadata has relations but no EdgeAddr; run go tool velox-gen")
	}
	for i, r := range d.Relations {
		if !r.explicit {
			// Only EdgeTo and EdgeThrough say which columns key the
			// relation; the key must be explicit.
			return fail(r.name, "relation %d was not made by EdgeTo or EdgeThrough; run go tool velox-gen", i)
		}
		off, rt, ok := fieldOf(v.RelAddr(i), base, t.Size())
		if !ok || !IsRelation(rt) {
			return fail(r.name, "EdgeAddr does not return a *One[T] or *Many[T] in the entity for relation %d", i)
		}
		if rt != r.holders[0] && rt != r.holders[1] {
			// The field holds another entity's relation: loading would fill
			// it with the wrong type.
			return fail(r.name, "relation %s is a %v, not a One or Many of %s", r.name, rt, targetName(rtype(r.holders[0])))
		}
		if r.bidi != nil {
			bk, err := checkEdgeBidi(name, t, d, cols, &r, rt)
			if err != nil {
				return nil, err
			}
			bidiKeys = append(bidiKeys, bk)
		}
		rels[i] = &relation{Name: r.name, Many: strings.HasPrefix(rt.Name(), "Many["), Off: off, typ: rt, ownerCol: r.ownerCol, targetCol: r.targetCol, through: r.through}
	}
	refs, err := buildTenantRefs(name, d, cols)
	if err != nil {
		return nil, err
	}
	info = &EntityInfo{Name: name, Table: d.Table, Schema: d.Schema, Columns: cols, PrimaryKey: d.PrimaryKey, Tenant: d.Tenant,
		Indexes: d.Indexes, rels: rels, immutable: anyImmutable, required: anyRequired, bidi: d.Bidi, bidiKeys: bidiKeys, refs: refs}
	if err := validate(info); err != nil {
		return nil, err
	}
	return info, nil
}

// checkBidi refuses the Descriptor of a bidirectional edge's join entity unless its
// two columns, of one Go type, are its primary key, and it has no tenant: the
// mirror of a row swaps them. The type is a byte slice or one that == compares
// without panicking (linkKey), as a self-link is told by comparing the two
// (selfLink).
func checkBidi(name string, d *DescriptorFields, cols []ColumnInfo) error {
	fail := func(format string, args ...any) error {
		return &SchemaError{Entity: name, Reason: "the join entity of a bidirectional edge " + fmt.Sprintf(format, args...)}
	}
	switch {
	case len(cols) != 2:
		return fail("has %d columns; it has two, its primary key", len(cols))
	case len(d.PrimaryKey) != 2 || d.PrimaryKey[0] != 0 || d.PrimaryKey[1] != 1:
		return fail("has the primary key %v; its two columns are its primary key, [0 1]", d.PrimaryKey)
	case cols[0].Type != cols[1].Type:
		return fail("has columns of two types, %v and %v", cols[0].Type, cols[1].Type)
	case !linkKey(rtype(cols[0].Type)):
		return fail("has columns of type %v, which cannot be compared; use a comparable type or a byte slice", cols[0].Type)
	case d.Tenant != -1:
		return fail("cannot have a tenant column")
	}
	return nil
}

// linkKey reports whether t, a bidirectional edge's key type, is a byte
// slice or a comparable type that holds no interface: == on an interface
// panics when its dynamic value is uncomparable, and so does == on a struct
// or an array that holds one (holdsInterface).
func linkKey(t reflect.Type) bool {
	if t.Kind() == reflect.Slice {
		return t.Elem().Kind() == reflect.Uint8
	}
	return t.Comparable() && !holdsInterface(t)
}

// holdsInterface reports whether t is an interface, or a struct or an array
// that holds one, in a field or its elements, at any depth. A pointer or a
// channel is compared by its address, so what it points to does not count.
func holdsInterface(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface:
		return true
	case reflect.Array:
		return holdsInterface(t.Elem())
	case reflect.Struct:
		for field := range t.Fields() {
			if holdsInterface(field.Type) {
				return true
			}
		}
	}
	return false
}

// unboundMethods refuses an entity whose pointer type has a Policy or Hooks
// method that the Descriptor does not bind: velox would never call it. The names
// are constants, so that the linker can still drop the methods nobody calls.
func unboundMethods(t reflect.Type, d *DescriptorFields) error {
	pt := reflect.PointerTo(t)
	if _, ok := pt.MethodByName("Policy"); ok && !d.Policy {
		return &SchemaError{Entity: t.String(), Reason: "has a Policy method, but the generated metadata does not bind it; run go tool velox-gen"}
	}
	if _, ok := pt.MethodByName("Hooks"); ok && !d.Hooks {
		return &SchemaError{Entity: t.String(), Reason: "has a Hooks method, but the generated metadata does not bind it; run go tool velox-gen"}
	}
	return nil
}

// fieldOf returns the offset from base and the type of the field p points
// to, and whether p is a non-nil pointer into the size bytes at base.
func fieldOf(p any, base unsafe.Pointer, size uintptr) (uintptr, reflect.Type, bool) {
	pv := reflect.ValueOf(p)
	if pv.Kind() != reflect.Pointer || pv.IsNil() {
		return 0, nil, false
	}
	at := pv.Pointer()
	if at < uintptr(base) || at-uintptr(base) >= size {
		return 0, nil, false
	}
	return at - uintptr(base), pv.Type().Elem(), true
}

// boolOrNumber reports whether t, a reflect.Type, is a bool or numeric type.
func boolOrNumber(t any) bool {
	k := rtype(t).Kind()
	return k == reflect.Bool || k >= reflect.Int && k <= reflect.Float64
}

// defaultOf is a Descriptor's Default d as ColumnInfo holds it for a field of type
// ft: d itself when it is of type ft, and a pointer to a copy of d when ft is
// a pointer to d's type, which FillDefault copies again for every row. It
// reports false for a value of any other type.
func defaultOf(d any, ft reflect.Type) (any, bool) {
	dv := reflect.ValueOf(d)
	switch {
	case dv.Type() == ft:
		return d, true
	case ft.Kind() == reflect.Pointer && dv.Type() == ft.Elem():
		p := reflect.New(ft.Elem())
		p.Elem().Set(dv)
		return p.Interface(), true
	}
	return nil, false
}

// Through is the join entity of a many-to-many relation and the columns of
// it that hold the owner's and the target's keys.
type Through struct {
	Join      func() (*EntityInfo, error)
	OwnerCol  int
	TargetCol int
}

// NewEdgeDescriptor returns the description of an edge named name, with the columns that
// hold its owner and target keys and the two holders of the relation.
func NewEdgeDescriptor(name string, ownerCol, targetCol int, holders [2]any) EdgeDescriptor {
	return EdgeDescriptor{name: name, explicit: true, ownerCol: ownerCol, targetCol: targetCol, holders: holders}
}

// SetEdgeBidi marks e as bidirectional, with t its edge's type.
func SetEdgeBidi(e *EdgeDescriptor, t any) { e.bidi = t }

// SetEdgeThrough makes e an edge through the join entity t.
func SetEdgeThrough(e *EdgeDescriptor, t *Through) { e.through = t }

// EdgeThroughOf is the join entity of e; nil when e has none.
func EdgeThroughOf(e EdgeDescriptor) *Through { return e.through }
