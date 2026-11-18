package engine

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unsafe"
)

// DescriptorFields are the fields of a Descriptor that do not depend on E, so that
// BuildDescriptor, which uses reflect, need not be generic.
type DescriptorFields struct {
	Table       string
	TableSchema string
	Columns     []ColumnDescriptor
	PrimaryKey  []int
	Bidi        bool
	Indexes     []IndexInfo
	Relations   []EdgeDescriptor
	Links       []EntityLink
	Check       func(col int, v any) error
	// Schema is Descriptor.Schema: the value whose methods declare the
	// entity's behaviour, nil when it has none.
	Schema any
}

// DescriptorView is a Descriptor with E erased: its fields, and its FieldPtr and EdgeFieldPtr
// bound to one new entity. Each is nil when the descriptor's is.
type DescriptorView struct {
	D            *DescriptorFields
	FieldPtr     func(i int) any // d.FieldPtr(x, i)
	EdgeFieldPtr func(i int) any // d.EdgeFieldPtr(x, i)
}

// BuildDescriptor builds the metadata of the entity x, a new *E at base, from the
// Descriptor that load returns with E erased. A panic in load, or in the Descriptor's
// functions, is recovered as a *SchemaError, so that it is the entity's error
// on every use rather than a crash on the first.
func BuildDescriptor(x any, base unsafe.Pointer, load func() DescriptorView) (info *EntityInfo, err error) {
	t := reflect.TypeOf(x).Elem()
	defer func() {
		if p := recover(); p != nil {
			info, err = nil, &SchemaError{Entity: t.String(), Reason: fmt.Sprintf("the generated metadata panicked: %v", p)}
		}
	}()
	return buildDescriptor(t, base, load)
}

// buildDescriptor is BuildDescriptor for the entity type t, without its
// recover.
func buildDescriptor(t reflect.Type, base unsafe.Pointer, load func() DescriptorView) (*EntityInfo, error) {
	name := t.String()
	v := load()
	d := v.D
	if v.FieldPtr == nil {
		return nil, schemaError(name, "", "the generated metadata has no FieldPtr; run go tool velox-gen")
	}
	if d.Table == "" {
		return nil, schemaError(name, "", "the generated metadata has no Table; run go tool velox-gen")
	}
	if err := unboundMethods(t, name, d); err != nil {
		return nil, err
	}
	cols := make([]ColumnInfo, len(d.Columns))
	anyImmutable, anyRequired, version := false, false, -1
	for i, c := range d.Columns {
		ci, err := columnInfo(t, base, v.FieldPtr(i), i, c)
		if err != nil {
			return nil, err
		}
		cols[i] = ci
		anyImmutable = anyImmutable || c.Immutable
		anyRequired = anyRequired || c.RequiredEdge != ""
		if c.Version {
			if version >= 0 {
				return nil, schemaError(name, c.Field, "column %s is a second Version column", c.Name)
			}
			if slices.Contains(d.PrimaryKey, i) {
				return nil, schemaError(name, c.Field, "column %s is in the primary key and cannot be the Version column", c.Name)
			}
			version = i
		}
	}
	if err := checkKeyColumns(name, d, len(cols)); err != nil {
		return nil, err
	}
	if d.Bidi {
		if err := checkBidi(name, d, cols); err != nil {
			return nil, err
		}
	}
	rels, bidiKeys, err := relationFields(t, base, v, cols)
	if err != nil {
		return nil, err
	}
	info := &EntityInfo{Name: name, Table: d.Table, Schema: d.TableSchema, Columns: cols, PrimaryKey: d.PrimaryKey,
		Indexes: d.Indexes, rels: rels, links: d.Links, immutable: anyImmutable, version: version + 1, required: anyRequired, bidi: d.Bidi, bidiKeys: bidiKeys}
	if err := validate(info); err != nil {
		return nil, err
	}
	info.names = nameIndex(cols)
	return info, nil
}

// schemaError returns the *SchemaError of entity's field whose reason is
// format applied to args.
func schemaError(entity, field, format string, args ...any) error {
	return &SchemaError{Entity: entity, Field: field, Reason: fmt.Sprintf(format, args...)}
}

// columnInfo returns the metadata of column i of the entity t at base, from
// its descriptor c and the pointer ptr that FieldPtr returns for it.
func columnInfo(t reflect.Type, base unsafe.Pointer, ptr any, i int, c ColumnDescriptor) (ColumnInfo, error) {
	name := t.String()
	fail := func(format string, args ...any) (ColumnInfo, error) {
		return ColumnInfo{}, schemaError(name, c.Field, format, args...)
	}
	off, ft, ok := fieldOf(ptr, base, t.Size())
	if !ok {
		// The column's offset and type are read from this pointer, and
		// every scan writes through it: anything else is no column.
		return fail("FieldPtr does not return a pointer into the entity for column %d", i)
	}
	ci := ColumnInfo{Field: c.Field, Name: c.Name, Type: ft, Nullable: ft.Kind() == reflect.Pointer || c.Nullable,
		HasDefault: c.Default != nil, DefaultFunc: c.DefaultFunc, UpdateDefault: c.UpdateDefault,
		Immutable: c.Immutable, Version: c.Version, Sensitive: c.Sensitive, RequiredEdge: c.RequiredEdge, Codec: c.Encode != nil, encode: c.Encode, decode: c.Decode, off: off}
	ci.val, ci.fast = valType(ft), fastOf(ft)
	if (c.Encode == nil) != (c.Decode == nil) {
		return fail("column %s has one of Encode and Decode; a codec needs both", c.Name)
	}
	if k := ft.Kind(); c.Nullable && k != reflect.Pointer && k != reflect.Slice && k != reflect.Map {
		return fail("column %s says Nullable, and its field is a %v, which cannot be nil: make it a pointer", c.Name, ft)
	}
	if c.RequiredEdge != "" && !ci.Nullable {
		return fail("column %s has a RequiredEdge, and cannot be NULL: the database refuses it unset", c.Name)
	}
	if c.Immutable && c.UpdateDefault != nil {
		return fail("Immutable column %s has an UpdateDefault; every update would be refused", c.Field)
	}
	if c.Version {
		switch k := ft.Kind(); {
		case k < reflect.Int || k > reflect.Uint64 || k == reflect.Uintptr:
			return fail("Version column %s is a %v; it must be an integer", c.Name, ft)
		case c.Immutable || c.UpdateDefault != nil || c.Encode != nil:
			return fail("Version column %s cannot be Immutable, have an UpdateDefault or a codec", c.Name)
		}
	}
	if c.Default == nil {
		return ci, nil
	}
	if ci.Default, ok = defaultOf(c.Default, ft); !ok {
		// FillDefault sets the field to it: a value of another type
		// would panic on the first insert instead of failing here.
		return fail("Default is a %T, not a %v", c.Default, ft)
	}
	if boolOrNumber(ft) && !zeroNumber(reflect.ValueOf(ci.Default)) && (!c.ZeroInvalid || rtype(ft).Kind() == reflect.Bool) {
		// A bool or numeric field cannot have a Default: its zero value is also a
		// real value, and the insert would overwrite an explicit false or 0.
		// The zero value itself stores what unset does, and so does any when
		// ZeroInvalid says the field never stores 0.
		return fail("Default on bool/numeric field %s would overwrite an explicit false or 0 on insert; make the field a pointer (nil uses the default)", c.Field)
	}
	ci.copyDefault = holdsRefs(reflect.TypeOf(ci.Default))
	return ci, nil
}

// checkKeyColumns refuses a primary key or an index of d that names a
// column outside the n columns of the entity name.
func checkKeyColumns(name string, d *DescriptorFields, n int) error {
	inRange := func(i int) bool { return i >= 0 && i < n }
	for _, k := range d.PrimaryKey {
		if !inRange(k) {
			return schemaError(name, "", "the primary key names column %d, of %d", k, n)
		}
	}
	for _, ix := range d.Indexes {
		for _, k := range ix.Columns {
			if !inRange(k) {
				return schemaError(name, "", "an index names column %d, of %d", k, n)
			}
		}
	}
	return nil
}

// relationFields returns the relation fields of the entity t at base, which v
// describes and whose columns are cols, and the keys of its bidirectional
// edges.
func relationFields(t reflect.Type, base unsafe.Pointer, v DescriptorView, cols []ColumnInfo) ([]*relation, []bidiKey, error) {
	name, d := t.String(), v.D
	rels := make([]*relation, len(d.Relations))
	var bidiKeys []bidiKey
	if len(rels) > 0 && v.EdgeFieldPtr == nil {
		return nil, nil, schemaError(name, "", "the generated metadata has relations but no EdgeFieldPtr; run go tool velox-gen")
	}
	for i, r := range d.Relations {
		if !r.explicit {
			// Only EdgeTo and EdgeThrough say which columns key the
			// relation; the key must be explicit.
			return nil, nil, schemaError(name, r.name, "relation %d was not made by EdgeTo or EdgeThrough; run go tool velox-gen", i)
		}
		off, rt, ok := fieldOf(v.EdgeFieldPtr(i), base, t.Size())
		if !ok || !IsRelation(rt) {
			return nil, nil, schemaError(name, r.name, "EdgeFieldPtr does not return a *One[T] or *Many[T] in the entity for relation %d", i)
		}
		if rt != r.holders[0] && rt != r.holders[1] {
			// The field holds another entity's relation: loading would fill
			// it with the wrong type.
			return nil, nil, schemaError(name, r.name, "relation %s is a %v, not a One or Many of %s", r.name, rt, targetName(rtype(r.holders[0])))
		}
		if r.bidi != nil {
			bk, err := checkEdgeBidi(name, t, d, cols, &r, rt)
			if err != nil {
				return nil, nil, err
			}
			bidiKeys = append(bidiKeys, bk)
		}
		rels[i] = &relation{Name: r.name, Many: strings.HasPrefix(rt.Name(), "Many["), Off: off, typ: rt, ownerCol: r.ownerCol, targetCol: r.targetCol, through: r.through, target: r.target}
	}
	return rels, bidiKeys, nil
}

// checkBidi refuses the Descriptor of a bidirectional edge's join entity unless its
// two columns, of one Go type, are its primary key: the
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

// unboundMethods refuses an entity whose pointer type has a Policy, Hooks or
// Interceptors method that the Descriptor does not bind, as its Schema is
// not a *E: velox would never call it. The names are constants, so that the
// linker can still drop the methods nobody calls. name is the entity's Go
// type.
func unboundMethods(t reflect.Type, name string, d *DescriptorFields) error {
	pt := reflect.PointerTo(t)
	if reflect.TypeOf(d.Schema) == pt {
		return nil
	}
	for _, m := range [...]struct{ name, article string }{{"Policy", "a"}, {"Hooks", "a"}, {"Interceptors", "an"}} {
		var ok bool
		switch m.name {
		case "Policy":
			_, ok = pt.MethodByName("Policy")
		case "Hooks":
			_, ok = pt.MethodByName("Hooks")
		default:
			_, ok = pt.MethodByName("Interceptors")
		}
		if ok {
			return &SchemaError{Entity: name, Reason: fmt.Sprintf("has %s %s method, but the metadata does not bind it: "+
				"velox-gen binds the schema's, and an entity written by hand names itself, Schema: (*E)(nil)", m.article, m.name)}
		}
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

// zeroNumber reports whether v, a bool or a number, is false or equal to 0.
func zeroNumber(v reflect.Value) bool {
	switch {
	case v.Kind() == reflect.Bool:
		return !v.Bool()
	case v.CanInt():
		return v.Int() == 0
	case v.CanUint():
		return v.Uint() == 0
	case v.CanFloat():
		return v.Float() == 0
	}
	return false
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

// SetEdgeTarget links e's target: get returns its metadata. A condition
// by the relation's name (Edge) resolves only on a linked relation, since
// it names no target type of its own.
func SetEdgeTarget(e *EdgeDescriptor, get func() (*EntityInfo, error)) { e.target = get }

// EdgeThroughOf is the join entity of e; nil when e has none.
func EdgeThroughOf(e EdgeDescriptor) *Through { return e.through }
