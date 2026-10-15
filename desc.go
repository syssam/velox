// This file is the metadata path velox-gen writes (schema design §2); the
// struct-scanning path in meta.go and schema.go remains until schema plan 3.

package velox

import (
	"fmt"
	"reflect"
	"strings"
	"unsafe"
)

// Desc is an entity's metadata as velox-gen writes it: literals, read on the
// entity's first use. Its column order is the scan order.
type Desc[E any] struct {
	Table      string
	Schema     string // the schema (PostgreSQL) or database holding Table; "" for the default
	Columns    []ColumnDesc
	PrimaryKey []int // indexes into Columns
	Tenant     int   // index into Columns; -1 when the entity has none
	Indexes    []IndexInfo
	Relations  []Relation                 // in the order of the generated relation handles
	Addr       func(x *E, i int) any      // &x.<field of column i>
	RelAddr    func(x *E, i int) any      // &x.<field of relation i>: a *One[T] or *Many[T]
	Check      func(col int, v any) error // the validators of column col, given its value (never a nil pointer); nil when none
}

// ColumnDesc describes one column of a Desc.
type ColumnDesc struct {
	Field         string // the Go field name
	Name          string // the column name
	SQLType       string // empty: the dialect's default
	Default       any    // a value of the field's type (or of its element type, for a pointer field); nil when none
	DefaultFunc   func() any
	UpdateDefault func() any
	Immutable     bool
	Sensitive     bool
}

// descFields are the fields of a Desc that do not depend on E, so that
// buildDesc, which uses reflect, need not be generic (design §3 rule 7).
type descFields struct {
	table      string
	schema     string
	columns    []ColumnDesc
	primaryKey []int
	tenant     int
	indexes    []IndexInfo
	relations  []Relation
	check      func(col int, v any) error
}

// descView is a Desc with E erased: its fields, and its Addr and RelAddr
// bound to one new entity. addr and relAddr are nil when Addr and RelAddr
// are.
type descView struct {
	d       *descFields
	addr    func(i int) any // d.Addr(x, i)
	relAddr func(i int) any // d.RelAddr(x, i)
}

// Load returns the entity's Meta, building it from desc the first time. Like
// Get, a panic in desc, Policy or Hooks becomes the Meta's *SchemaError.
//
// It does no more than call desc and bind its functions to a new entity: the
// build, which uses reflect, is buildDesc's, so that Load, compiled in every
// entity's package, holds no reflect.Type (design §3 rule 7).
func (o *MetaOnce[E]) Load[_ Lazy[E]](desc func() Desc[E]) *Meta[E] {
	o.once.Do(func() {
		x := new(E)
		var d Desc[E]
		info, err := buildDesc(x, unsafe.Pointer(x), func() descView {
			d = desc()
			v := descView{d: &descFields{table: d.Table, schema: d.Schema, columns: d.Columns, primaryKey: d.PrimaryKey,
				tenant: d.Tenant, indexes: d.Indexes, relations: d.Relations, check: d.Check}}
			if a := d.Addr; a != nil {
				v.addr = func(i int) any { return a(x, i) }
			}
			if a := d.RelAddr; a != nil {
				v.relAddr = func(i int) any { return a(x, i) }
			}
			return v
		})
		addr := d.Addr
		if err == nil {
			err = entityMethods[E](info)
		}
		if err != nil {
			info, addr = nil, nil // an error Meta has no scan plan, as in Get
		}
		o.m = &Meta[E]{info: info, addr: addr, err: err}
		if info != nil {
			info.kit = kitOf(info, addr)
			info.check = d.Check
		}
	})
	return o.m
}

// buildDesc builds the metadata of the entity x, a new *E at base, from the
// Desc that load returns with E erased. A panic in load, or in the Desc's
// functions, is recovered as a *SchemaError, so that it is the entity's error
// on every use rather than a crash on the first.
func buildDesc(x any, base unsafe.Pointer, load func() descView) (info *EntityInfo, err error) {
	t := reflect.TypeOf(x).Elem()
	name := t.String()
	defer func() {
		if p := recover(); p != nil {
			info, err = nil, &SchemaError{Entity: name, Reason: fmt.Sprintf("the generated metadata panicked: %v", p)}
		}
	}()
	v := load()
	d := v.d
	fail := func(field, format string, args ...any) (*EntityInfo, error) {
		return nil, &SchemaError{Entity: name, Field: field, Reason: fmt.Sprintf(format, args...)}
	}
	if v.addr == nil {
		return fail("", "the generated metadata has no Addr; run go tool velox-gen")
	}
	cols := make([]ColumnInfo, len(d.columns))
	anyImmutable := false
	for i, c := range d.columns {
		off, ft, ok := fieldOf(v.addr(i), base, t.Size())
		if !ok {
			// The column's offset and type are read from this pointer, and
			// every scan writes through it: anything else is no column.
			return fail(c.Field, "Addr does not return a pointer into the entity for column %d", i)
		}
		ci := ColumnInfo{Field: c.Field, Name: c.Name, Type: ft, Nullable: ft.Kind() == reflect.Pointer, SQLType: c.SQLType,
			HasDefault: c.Default != nil, DefaultFunc: c.DefaultFunc, UpdateDefault: c.UpdateDefault,
			Immutable: c.Immutable, Sensitive: c.Sensitive, off: off}
		if c.Immutable && c.UpdateDefault != nil {
			return fail(c.Field, "Immutable column %s has an UpdateDefault; every update would be refused", c.Field)
		}
		if c.Default != nil {
			if boolOrNumber(ft) {
				// The rule ColumnDef.Default enforces, for the reason given there.
				return fail(c.Field, "Default on bool/numeric field %s would overwrite an explicit false or 0 on insert; make the field a pointer (nil uses the default)", c.Field)
			}
			if ci.Default, ok = defaultOf(c.Default, ft); !ok {
				// fillDefault sets the field to it: a value of another type
				// would panic on the first insert instead of failing here.
				return fail(c.Field, "Default is a %T, not a %v", c.Default, ft)
			}
		}
		cols[i] = ci
		anyImmutable = anyImmutable || c.Immutable
	}
	inRange := func(i int) bool { return i >= 0 && i < len(cols) }
	for _, k := range d.primaryKey {
		if !inRange(k) {
			return fail("", "the primary key names column %d, of %d", k, len(cols))
		}
	}
	for _, ix := range d.indexes {
		for _, k := range ix.Columns {
			if !inRange(k) {
				return fail("", "an index names column %d, of %d", k, len(cols))
			}
		}
	}
	if d.tenant != -1 && !inRange(d.tenant) {
		return fail("", "the tenant names column %d, of %d", d.tenant, len(cols))
	}
	rels := make([]*relation, len(d.relations))
	if len(rels) > 0 && v.relAddr == nil {
		return fail("", "the generated metadata has relations but no RelAddr; run go tool velox-gen")
	}
	for i, r := range d.relations {
		if !r.explicit {
			// Only EdgeTo and EdgeThrough say which columns key the
			// relation; the old path's inference does not apply here.
			return fail(r.name, "relation %d was not made by EdgeTo or EdgeThrough; run go tool velox-gen", i)
		}
		off, rt, ok := fieldOf(v.relAddr(i), base, t.Size())
		if !ok || !isRelation(rt) {
			return fail(r.name, "RelAddr does not return a *One[T] or *Many[T] in the entity for relation %d", i)
		}
		rels[i] = &relation{name: r.name, many: strings.HasPrefix(rt.Name(), "Many["), off: off, typ: rt, target: r.target,
			explicit: true, ownerCol: r.ownerCol, targetCol: r.targetCol, through: r.through}
	}
	info = &EntityInfo{Name: name, Table: d.table, Schema: d.schema, Columns: cols, PrimaryKey: d.primaryKey, Tenant: d.tenant,
		Indexes: d.indexes, rels: rels, immutable: anyImmutable}
	if err := validate(info); err != nil {
		return nil, err
	}
	return info, nil
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

// defaultOf is a Desc's Default d as ColumnInfo holds it for a field of type
// ft: d itself when it is of type ft, and a pointer to a copy of d when ft is
// a pointer to d's type, which fillDefault copies again for every row. It
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

// EdgeTo is a relation to entity T keyed owner.Columns[ownerCol] =
// T.Columns[targetCol].
func EdgeTo[T any, PT Entity[T]](name string, ownerCol, targetCol int) Relation {
	r := RelationTo[T, PT](name)
	r.explicit, r.ownerCol, r.targetCol = true, ownerCol, targetCol
	return r
}

// EdgeThrough is a many-to-many relation to T through the join entity J:
// owner.Columns[ownerCol] = J.Columns[joinOwner] and J.Columns[joinTarget] =
// T.Columns[targetCol]. Task 4 implements it.
func EdgeThrough[J, T any, PJ Entity[J], PT Entity[T]](name string, ownerCol, joinOwner, joinTarget, targetCol int) Relation {
	r := EdgeTo[T, PT](name, ownerCol, targetCol)
	r.through = &through{
		join: func() (*EntityInfo, error) {
			m := PJ(nil).VeloxMeta()
			return m.info, m.err
		},
		ownerCol:  joinOwner,
		targetCol: joinTarget,
	}
	return r
}

// through is the join entity of a many-to-many relation and the columns of
// it that hold the owner's and the target's keys.
type through struct {
	join      func() (*EntityInfo, error)
	ownerCol  int
	targetCol int
}
