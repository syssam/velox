// This file is the only metadata path: the one velox-gen writes (schema
// packages).

package velox

import (
	"fmt"
	"reflect"
	"strings"
	"unsafe"
)

// For generated code. Desc is an entity's metadata as velox-gen writes it:
// literals, read on the entity's first use. Its column order is the scan order.
type Desc[E any] struct {
	Table      string
	Schema     string // the schema (PostgreSQL) or database holding Table; "" for the default
	Columns    []ColumnDesc
	PrimaryKey []int // indexes into Columns
	Tenant     int   // index into Columns; -1 when the entity has none (so velox-gen always writes it: 0 is column 0)
	Indexes    []IndexInfo
	Edges      []EdgeDesc            // in the order of the generated relation handles
	Addr       func(x *E, i int) any // &x.<field of column i>
	EdgeAddr   func(x *E, i int) any // &x.<field of edge i>: a *One[T] or *Many[T]

	// Check runs the validators of column col on its value v, and is nil
	// when no column has any. For a pointer field, v is the value the
	// pointer points to: velox never calls Check for a nil pointer, which is
	// the absence of a value, nor with a pointer.
	Check func(col int, v any) error

	// Policy and Hooks bind the entity's Policy and Hooks methods, when it
	// declares them. velox-gen writes
	//
	//	Policy: func() velox.BoundPolicy { return velox.BindPolicy((*E)(nil).Policy()) },
	//	Hooks:  func() velox.BoundHooks  { return velox.BindHooks((*E)(nil).Hooks()) },
	//
	// and leaves the field nil when the entity has no such method, so that an
	// entity without them instantiates none of the policy and hook generics.
	// A method with another signature does not compile (BindPolicy takes a
	// Policy[E], BindHooks a []Hook[E]), which is why no test checks it at
	// run time; and an entity whose pointer type has a Policy or Hooks method
	// that its Desc does not bind is refused at first use with a
	// *SchemaError, so a policy or a hook is never silently left out.
	//
	// Load calls each function once, when it builds the metadata: the
	// methods run on a nil *E, so neither may read its receiver, and a panic
	// in either becomes the entity's *SchemaError. Neither method may read the
	// entity's own metadata in its body, outside the functions it returns:
	// anything that calls its VeloxMeta waits on the build in progress and
	// deadlocks. A Has or HasWhere does not: it names its relation, and the
	// statement that holds the condition resolves it, so the body may build
	// them.
	Policy func() BoundPolicy
	Hooks  func() BoundHooks
}

// For generated code. ColumnDesc describes one column of a Desc.
type ColumnDesc struct {
	Field         string     // the Go field name
	Name          string     // the column name
	Default       any        // a value of the field's type (or of its element type, for a pointer field); nil when none
	DefaultFunc   func() any // returns a value of the field's type (or of its element type, for a pointer field)
	UpdateDefault func() any
	Immutable     bool
	Sensitive     bool
	// Nullable says the column holds NULL though the field is no pointer:
	// a slice or a map, whose nil velox binds as NULL and reads NULL back
	// into. velox-gen sets it on a json field of an Optional or Nillable
	// schema field that is a slice or a map, and on a Nillable bytes field.
	// Without it, a nil slice or map in an encoded column binds what Encode
	// makes of it (json.Marshal's null), and a nil []byte is handed to the
	// driver as it is. A pointer field is nullable without it; on a field of
	// any other type it is refused.
	Nullable bool
	// Encode and Decode are the column's codec, both or neither: with them,
	// velox binds the bytes Encode returns for the field's value, and decodes
	// what it reads with Decode, into a pointer to the field. velox-gen sets
	// them to json.Marshal and json.Unmarshal for every json field, so the
	// generated file, not the core, imports encoding/json, and a program
	// whose entities hold no JSON does not link it. A hand-written Desc may
	// plug in another codec. Decode's data is valid only during the call: it
	// must neither change nor keep it. velox may hand it the bytes of a string
	// the driver returned, read in place, so a Decode that writes to them
	// writes to memory that may be read-only. Encode's result is kept in
	// Statement.Args until the statement has run, so Encode must return a new
	// slice on every call, never a buffer it reuses. A primary-key, tenant or
	// relation-key column cannot have a codec: conditions on it are refused,
	// so the metadata is. velox never guesses a codec from the Go type, as a
	// []byte field holds bytes, not JSON.
	Encode func(v any) ([]byte, error)
	Decode func(data []byte, v any) error
}

// descFields are the fields of a Desc that do not depend on E, so that
// buildDesc, which uses reflect, need not be generic.
type descFields struct {
	table      string
	schema     string
	columns    []ColumnDesc
	primaryKey []int
	tenant     int
	indexes    []IndexInfo
	relations  []EdgeDesc
	check      func(col int, v any) error
	policy     bool // Desc.Policy is set
	hooks      bool // Desc.Hooks is set
}

// descView is a Desc with E erased: its fields, and its Addr and EdgeAddr
// bound to one new entity. addr and relAddr are nil when Addr and EdgeAddr
// are.
type descView struct {
	d       *descFields
	addr    func(i int) any // d.Addr(x, i)
	relAddr func(i int) any // d.EdgeAddr(x, i)
}

// Load returns the entity's Meta, building it from desc the first time. A
// panic in desc, or in the Desc's Policy or Hooks, becomes the Meta's
// *SchemaError.
//
// It does no more than call desc and bind its functions to a new entity: the
// build, which uses reflect, is buildDesc's, and the binding of Policy and
// Hooks is not generic, so that Load, compiled in every entity's package, holds
// no reflect.Type and instantiates nothing for an entity that has no policy or
// hook.
func (o *MetaOnce[E]) Load[_ Lazy[E]](desc func() Desc[E]) *Meta[E] {
	o.once.Do(func() {
		x := new(E)
		var d Desc[E]
		info, err := buildDesc(x, unsafe.Pointer(x), func() descView {
			d = desc()
			v := descView{d: &descFields{table: d.Table, schema: d.Schema, columns: d.Columns, primaryKey: d.PrimaryKey,
				tenant: d.Tenant, indexes: d.Indexes, relations: d.Edges, check: d.Check,
				policy: d.Policy != nil, hooks: d.Hooks != nil}}
			if a := d.Addr; a != nil {
				v.addr = func(i int) any { return a(x, i) }
			}
			if a := d.EdgeAddr; a != nil {
				v.relAddr = func(i int) any { return a(x, i) }
			}
			return v
		})
		addr := d.Addr
		// Desc.Policy and Desc.Hooks have E erased (BoundPolicy, BoundHooks):
		// the generated code instantiates BindPolicy and BindHooks, so that
		// an entity that has neither compiles no Policy or Hook generic.
		if err == nil && d.Policy != nil {
			err = bindPolicy(info, d.Policy)
		}
		if err == nil && d.Hooks != nil {
			err = bindHooks(info, d.Hooks)
		}
		if err != nil {
			info, addr = nil, nil // an error Meta has no scan plan
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
	if d.table == "" {
		return fail("", "the generated metadata has no Table; run go tool velox-gen")
	}
	if err := unboundMethods(t, d); err != nil {
		return nil, err
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
		ci := ColumnInfo{Field: c.Field, Name: c.Name, Type: ft, Nullable: ft.Kind() == reflect.Pointer || c.Nullable,
			HasDefault: c.Default != nil, DefaultFunc: c.DefaultFunc, UpdateDefault: c.UpdateDefault,
			Immutable: c.Immutable, Sensitive: c.Sensitive, Codec: c.Encode != nil, encode: c.Encode, decode: c.Decode, off: off}
		if (c.Encode == nil) != (c.Decode == nil) {
			return fail(c.Field, "column %s has one of Encode and Decode; a codec needs both", c.Name)
		}
		if k := ft.Kind(); c.Nullable && k != reflect.Pointer && k != reflect.Slice && k != reflect.Map {
			return fail(c.Field, "column %s says Nullable, and its field is a %v, which cannot be nil: make it a pointer", c.Name, ft)
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
		return fail("", "the generated metadata has relations but no EdgeAddr; run go tool velox-gen")
	}
	for i, r := range d.relations {
		if !r.explicit {
			// Only EdgeTo and EdgeThrough say which columns key the
			// relation; the key must be explicit.
			return fail(r.name, "relation %d was not made by EdgeTo or EdgeThrough; run go tool velox-gen", i)
		}
		off, rt, ok := fieldOf(v.relAddr(i), base, t.Size())
		if !ok || !isRelation(rt) {
			return fail(r.name, "EdgeAddr does not return a *One[T] or *Many[T] in the entity for relation %d", i)
		}
		if rt != r.holders[0] && rt != r.holders[1] {
			// The field holds another entity's relation: loading would fill
			// it with the wrong type.
			return fail(r.name, "relation %s is a %v, not a One or Many of %s", r.name, rt, targetName(rtype(r.holders[0])))
		}
		rels[i] = &relation{name: r.name, many: strings.HasPrefix(rt.Name(), "Many["), off: off, typ: rt, ownerCol: r.ownerCol, targetCol: r.targetCol, through: r.through}
	}
	info = &EntityInfo{Name: name, Table: d.table, Schema: d.schema, Columns: cols, PrimaryKey: d.primaryKey, Tenant: d.tenant,
		Indexes: d.indexes, rels: rels, immutable: anyImmutable}
	if err := validate(info); err != nil {
		return nil, err
	}
	return info, nil
}

// unboundMethods refuses an entity whose pointer type has a Policy or Hooks
// method that the Desc does not bind: velox would never call it. The names
// are constants, so that the linker can still drop the methods nobody calls.
func unboundMethods(t reflect.Type, d *descFields) error {
	pt := reflect.PointerTo(t)
	if _, ok := pt.MethodByName("Policy"); ok && !d.policy {
		return &SchemaError{Entity: t.String(), Reason: "has a Policy method, but the generated metadata does not bind it; run go tool velox-gen"}
	}
	if _, ok := pt.MethodByName("Hooks"); ok && !d.hooks {
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

// For generated code. EdgeTo is a relation to entity T keyed
// owner.Columns[ownerCol] = T.Columns[targetCol]. Its field must be a One[T] or
// a Many[T]: the metadata is refused at first use when EdgeAddr returns another
// type, such as the holder of another entity, as stale or hand-edited code
// would.
//
// The relation does not name T's metadata: if it did, every program that links
// the owner would link T, and through T's own relations every entity of the
// connected model. Where the relation is used, the handle Rel[E, T] says T, and
// CheckRelation checks the target.
func EdgeTo[T any, PT Entity[T]](name string, ownerCol, targetCol int) EdgeDesc {
	return EdgeDesc{
		name: name, explicit: true, ownerCol: ownerCol, targetCol: targetCol,
		holders: [2]any{elemType((*One[T])(nil)), elemType((*Many[T])(nil))},
	}
}

// For generated code. EdgeThrough is a many-to-many relation to T through the
// join entity J: owner.Columns[ownerCol] = J.Columns[joinOwner] and
// J.Columns[joinTarget] = T.Columns[targetCol]. Its field is a Many[T]. J's
// rows are written like any entity's, with Insert and Delete.
//
// Loading it reads J's rows for the owners' keys, then T's rows for the keys
// they hold, and Has is an EXISTS on J holding an EXISTS on T. T's tenant and
// Read policy apply, as they do to any relation's target. J can have neither
// a tenant column nor a Read policy: the relation is refused when it
// resolves. Join rows are therefore not tenant-scoped, and velox does not
// check that a link's two endpoints are rows the caller may see or change:
// an application that writes links checks both endpoints itself, in J's
// Policy (Insert, and Write for updates and deletes) or in a hook on J.
func EdgeThrough[J, T any, PJ Entity[J], PT Entity[T]](name string, ownerCol, joinOwner, joinTarget, targetCol int) EdgeDesc {
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
