package velox

import (
	"context"
	"fmt"
	"reflect"
	"unsafe"
)

// Schemer is implemented by every entity: a struct with a Schema method on
// its pointer type.
type Schemer interface{ Schema(*Schema) }

// Mixer is implemented by a struct meant to be embedded in entities. velox
// calls Mixin on every Mixer embedded directly in an entity, before the
// entity's own Schema.
type Mixer interface{ Mixin(*Schema) }

type field struct {
	name string
	off  uintptr
	typ  any // a reflect.Type (design §3 rule 7)
}

// Schema collects what an entity's Schema method declares. Fields are named
// by pointer; a pointer is matched to a column by its offset in the entity and
// its type.
type Schema struct {
	entity string
	base   unsafe.Pointer
	size   uintptr
	fields []field
	typ    any  // the entity's reflect.Type, to name fields in errors
	pkSet  bool // PrimaryKey was called
	cols   []ColumnInfo
	table  string
	schema string
	pk     []int
	tenant int
	idx    []IndexInfo
	errs   []error
	rels   []*relation

	// mixin is the reflect.Type, as any, of the mixin whose Mixin is running,
	// and mixinOff its offset in the entity; nil and 0 in the entity's Schema.
	mixin    any
	mixinOff uintptr

	policy       *policy
	mixinInsert  []func(ctx context.Context, p unsafe.Pointer) error // the mixins' BeforeInsert hooks
	beforeInsert []func(ctx context.Context, p unsafe.Pointer) error // the entity's
	beforeUpdate []func(ctx context.Context, st *Statement) error
	beforeDelete []func(ctx context.Context, st *Statement) error
}

func (s *Schema) fail(fieldName, format string, args ...any) {
	s.errs = append(s.errs, &SchemaError{Entity: s.entity, Field: fieldName, Reason: fmt.Sprintf(format, args...)})
}

// find returns the column that a pointer into the entity names, or -1 after
// recording an error. t is the field's reflect.Type, held as any (design §3
// rule 7).
func (s *Schema) find(p unsafe.Pointer, t any) int {
	if uintptr(p) < uintptr(s.base) || uintptr(p)-uintptr(s.base) >= s.size {
		s.fail("", "a pointer given to Schema does not point into the entity")
		return -1
	}
	off := uintptr(p) - uintptr(s.base)
	for i, f := range s.fields {
		if f.off == off && f.typ == t {
			return i
		}
	}
	s.fail(fieldAt(rtype(s.typ), off, rtype(t)), "the %v at offset %d is not a column (it is unexported, tagged velox:\"-\", or a relation)", t, off)
	return -1
}

func (s *Schema) findAny(p any) int {
	v := reflect.ValueOf(p)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		s.fail("", "expected a pointer to a field, got %T", p)
		return -1
	}
	return s.find(v.UnsafePointer(), v.Type().Elem())
}

func (s *Schema) indexes(fields []any) []int {
	out := make([]int, 0, len(fields))
	for _, f := range fields {
		if i := s.findAny(f); i >= 0 {
			out = append(out, i)
		}
	}
	return out
}

// Table sets the table name. The default is the snake_case of the type name.
func (s *Schema) Table(name string) { s.table = name }

// TableIn sets the table name and the schema (PostgreSQL) or database
// (MySQL, SQLite) that holds it: SQL names the table schema.table.
func (s *Schema) TableIn(schema, table string) { s.schema, s.table = schema, table }

// Column returns the column that the field pointer p names, to declare its
// options.
func (s *Schema) Column[V any](p *V) *ColumnDef[V] {
	return &ColumnDef[V]{s: s, i: s.find(unsafe.Pointer(p), elemType(p))}
}

// PrimaryKey declares the primary key. The default is the field named ID.
func (s *Schema) PrimaryKey(fields ...any) {
	s.pk, s.pkSet = s.indexes(fields), true
}

// Unique declares a unique index over the fields.
func (s *Schema) Unique(fields ...any) {
	s.idx = append(s.idx, IndexInfo{Columns: s.indexes(fields), Unique: true})
}

// Index declares a non-unique index over the fields.
func (s *Schema) Index(fields ...any) {
	s.idx = append(s.idx, IndexInfo{Columns: s.indexes(fields)})
}

// Tenant declares the column that scopes every row to a tenant.
func (s *Schema) Tenant(field any) {
	if s.tenant >= 0 {
		s.fail("", "Tenant is declared twice")
		return
	}
	s.tenant = s.findAny(field)
}

// ColumnDef declares the options of one column.
type ColumnDef[V any] struct {
	s *Schema
	i int
}

// colInfo returns the column being declared, or a throwaway one when the
// field pointer was invalid (the error is already recorded).
func colInfo(s *Schema, i int) *ColumnInfo {
	if i < 0 {
		return &ColumnInfo{}
	}
	return &s.cols[i]
}

// Name sets the column name. The default is the snake_case of the field name.
func (c *ColumnDef[V]) Name[_ Lazy[V]](name string) *ColumnDef[V] {
	colInfo(c.s, c.i).Name = name
	return c
}

// SQLType sets the column's SQL type, for the migration planner. The default
// follows the field's Go type.
func (c *ColumnDef[V]) SQLType[_ Lazy[V]](t string) *ColumnDef[V] {
	colInfo(c.s, c.i).SQLType = t
	return c
}

// Default sets the value an insert writes when the field holds its zero value
// (nil, for a pointer field).
//
// A bool or numeric field cannot have a Default: its zero value is also a
// real value, and the insert would overwrite an explicit false or 0. Declare
// such a field as a pointer (*bool, *int64), where nil means "use the
// default"; otherwise the schema is a *SchemaError. A string, a time or a
// slice can have one. See DefaultFunc for a field whose zero value means
// "unset" by design.
func (c *ColumnDef[V]) Default[_ Lazy[V]](v V) *ColumnDef[V] {
	if c.i >= 0 && boolOrNumber(c.s.cols[c.i].Type) {
		ci := c.s.cols[c.i]
		c.s.fail(ci.Field, "Default on bool/numeric field %s would overwrite an explicit false or 0 on insert; make the field a pointer (nil uses the default)", ci.Field)
		return c
	}
	ci := colInfo(c.s, c.i)
	ci.Default, ci.HasDefault = v, true
	return c
}

// boolOrNumber reports whether t, a reflect.Type, is a bool or numeric type.
func boolOrNumber(t any) bool {
	k := rtype(t).Kind()
	return k == reflect.Bool || k >= reflect.Int && k <= reflect.Float64
}

// DefaultFunc sets a function whose result an insert writes when the field
// holds its zero value, such as time.Now for a creation time or a generated
// ID. Unlike Default it is allowed on any type, a bool or number included:
// the function says what "unset" means by its own rules, so the zero value of
// such a field is treated as unset.
func (c *ColumnDef[V]) DefaultFunc[_ Lazy[V]](f func() V) *ColumnDef[V] {
	colInfo(c.s, c.i).DefaultFunc = func() any { return f() }
	return c
}

// OnUpdate sets a function whose result every insert and update writes to the column.
func (c *ColumnDef[V]) OnUpdate[_ Lazy[V]](f func() V) *ColumnDef[V] {
	colInfo(c.s, c.i).OnUpdate = func() any { return f() }
	return c
}

// fieldAt names the field of t at offset off with type ft, descending into
// embedded and nested structs ("stamps.UpdatedAt"); "" when there is none.
func fieldAt(t reflect.Type, off uintptr, ft reflect.Type) string {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Offset == off && f.Type == ft {
			return f.Name
		}
		if f.Type.Kind() == reflect.Struct && off >= f.Offset && off < f.Offset+f.Type.Size() {
			if n := fieldAt(f.Type, off-f.Offset, ft); n != "" {
				return f.Name + "." + n
			}
		}
	}
	return ""
}

// Many returns the to-many relation f, to declare its key. Undeclared, the
// key is the target's field named after this entity: InvoiceID on Line for a
// relation of Invoice.
func (s *Schema) Many[T any](f *Many[T]) *RelDef[T] {
	return &RelDef[T]{s: s, r: s.findRel(unsafe.Pointer(f), elemType(f))}
}

// One returns the to-one relation f, to declare its key. Undeclared, the key
// is this entity's field named after the relation (OrgID for a relation Org)
// when there is one, and otherwise the target's field named after this entity.
func (s *Schema) One[T any](f *One[T]) *RelDef[T] {
	return &RelDef[T]{s: s, r: s.findRel(unsafe.Pointer(f), elemType(f))}
}

// findRel returns the relation p points to, or nil after recording an error.
// t is the field's reflect.Type, held as any (design §3 rule 7).
func (s *Schema) findRel(p unsafe.Pointer, t any) *relation {
	if uintptr(p) >= uintptr(s.base) && uintptr(p)-uintptr(s.base) < s.size {
		off := uintptr(p) - uintptr(s.base)
		for _, r := range s.rels {
			if r.off == off && r.typ == t {
				return r
			}
		}
	}
	s.fail("", "a pointer given to Many or One is not a relation field of the entity")
	return nil
}

// RelDef declares the key of one relation.
type RelDef[T any] struct {
	s *Schema
	r *relation // nil when the field pointer was invalid (the error is recorded)
}

// ForeignKey declares the target's column that holds this entity's primary
// key: func(l *Line) *int64 { return &l.InvoiceID }.
//
// The function runs once, here, on a fresh T; it must return a pointer to one
// of T's fields (or of a struct embedded in it) and do nothing else.
func (d *RelDef[T]) ForeignKey[K any](fk func(*T) *K) *RelDef[T] {
	if d.r == nil {
		return d
	}
	switch {
	case d.r.hasFK:
		d.s.fail(d.r.name, "relation %s's key is declared twice", d.r.name)
		return d
	case d.r.ref != -1:
		d.s.fail(d.r.name, "relation %s declares both ForeignKey and References", d.r.name)
		return d
	}
	x := new(T)
	off := uintptr(unsafe.Pointer(fk(x))) - uintptr(unsafe.Pointer(x))
	if off >= unsafe.Sizeof(*x) {
		d.s.fail(d.r.name, "ForeignKey's function must return a field of %s", elemType(x))
		return d
	}
	d.r.fkOff, d.r.fkTyp, d.r.hasFK = off, elemType((*K)(nil)), true
	return d
}

// References declares this entity's column that holds the target's primary
// key, for a One relation to an entity this one belongs to: References(&x.OrgID).
func (d *RelDef[T]) References[V any](p *V) *RelDef[T] {
	if d.r == nil {
		return d
	}
	if d.r.many {
		d.s.fail(d.r.name, "References applies to a One relation; a Many relation's key is on its target: use ForeignKey")
		return d
	}
	if d.r.hasFK {
		d.s.fail(d.r.name, "relation %s declares both ForeignKey and References", d.r.name)
		return d
	}
	if d.r.ref != -1 {
		d.s.fail(d.r.name, "relation %s's key is declared twice", d.r.name)
		return d
	}
	d.r.ref = d.s.find(unsafe.Pointer(p), elemType(p))
	if d.r.ref < 0 {
		d.r.ref = -2 // the error is recorded; keep resolution from guessing
	}
	return d
}

// Policy declares the entity's row-level policy (see Policy). It is declared
// at most once, in the entity's own Schema method: not in a mixin.
func (s *Schema) Policy[E any](p Policy[E]) {
	pol := &policy{}
	if r := p.Read; r != nil {
		pol.read = func(ctx context.Context) (Predicate, error) { q, err := r(ctx); return Predicate(q), err }
	}
	if w := p.Write; w != nil {
		pol.write = func(ctx context.Context) (Predicate, error) { q, err := w(ctx); return Predicate(q), err }
	}
	if ins := p.Insert; ins != nil {
		pol.insert = func(ctx context.Context, x unsafe.Pointer) error { return ins(ctx, (*E)(x)) }
	}
	s.setPolicy(elemType((*E)(nil)), pol)
}

// BeforeInsert declares a hook that runs on every row an insert or upsert
// writes, after the defaults and the tenant are filled; its error refuses the
// statement. In a mixin's Mixin method, f takes the mixin's own struct, and
// is given the one embedded in the row; the mixins' hooks run before the
// entity's, each kind in the order declared.
func (s *Schema) BeforeInsert[E any](f func(ctx context.Context, x *E) error) {
	var h func(context.Context, unsafe.Pointer) error
	if f != nil {
		off := s.mixinOff
		h = func(ctx context.Context, p unsafe.Pointer) error { return f(ctx, (*E)(unsafe.Add(p, off))) }
	}
	s.addBeforeInsert(elemType((*E)(nil)), h)
}

// BeforeUpdate declares a hook that runs on every update and Save of the
// entity, before the statement is scoped and rendered. It may read and change
// the assignments and narrow the rows through m; its error refuses the
// statement. A mixin cannot declare one.
func (s *Schema) BeforeUpdate[E any](f func(ctx context.Context, m *Mutation[E]) error) {
	s.addMutationHook("BeforeUpdate", elemType((*E)(nil)), mutationHook(f), &s.beforeUpdate)
}

// BeforeDelete declares a hook that runs on every delete of the entity,
// before the statement is scoped and rendered. It may narrow the rows through
// m; its error refuses the statement. A mixin cannot declare one.
func (s *Schema) BeforeDelete[E any](f func(ctx context.Context, m *Mutation[E]) error) {
	s.addMutationHook("BeforeDelete", elemType((*E)(nil)), mutationHook(f), &s.beforeDelete)
}

// mutationHook wraps f over a statement: each call gets a fresh Mutation,
// and an error the Mutation recorded fails it once f returns nil. A nil f
// gives nil.
func mutationHook[E any](f func(context.Context, *Mutation[E]) error) func(context.Context, *Statement) error {
	if f == nil {
		return nil
	}
	return func(ctx context.Context, st *Statement) error {
		m := &Mutation[E]{st: st}
		if err := f(ctx, m); err != nil {
			return err
		}
		return m.err
	}
}

// hookOn reports whether a hook declared by what, typed on t (a reflect.Type
// as any), may be declared here: on the entity in its Schema method, on the
// mixin in a Mixin method. Otherwise it records why not.
func (s *Schema) hookOn(what string, t any) bool {
	switch {
	case s.mixin != nil && t != s.mixin:
		s.fail("", "%s in mixin %v is typed on %v; a mixin's hook takes the mixin's own struct", what, s.mixin, t)
	case s.mixin == nil && t != s.typ:
		s.fail("", "%s is typed on %v, not on %v", what, t, s.typ)
	default:
		return true
	}
	return false
}

func (s *Schema) setPolicy(t any, p *policy) {
	switch {
	case s.mixin != nil:
		s.fail("", "Policy is declared in mixin %v; declare it in the entity's Schema", s.mixin)
	case !s.hookOn("Policy", t):
	case s.policy != nil:
		s.fail("", "Policy is declared twice")
	default:
		s.policy = p
	}
}

func (s *Schema) addBeforeInsert(t any, h func(context.Context, unsafe.Pointer) error) {
	switch {
	case !s.hookOn("BeforeInsert", t):
	case h == nil:
		s.fail("", "BeforeInsert is given a nil function")
	case s.mixin != nil:
		s.mixinInsert = append(s.mixinInsert, h)
	default:
		s.beforeInsert = append(s.beforeInsert, h)
	}
}

func (s *Schema) addMutationHook(what string, t any, h func(context.Context, *Statement) error, to *[]func(context.Context, *Statement) error) {
	switch {
	case s.mixin != nil:
		s.fail("", "%s is declared in mixin %v; a mixin may declare only BeforeInsert", what, s.mixin)
	case !s.hookOn(what, t):
	case h == nil:
		s.fail("", "%s is given a nil function", what)
	default:
		*to = append(*to, h)
	}
}
