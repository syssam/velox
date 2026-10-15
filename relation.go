package velox

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
	"unsafe"
)

// kit is the code velox needs to handle an entity whose type it does not
// know at the point of use: loading a relation to it. MetaOnce.Get builds it,
// so it is compiled once, in the entity's own package.
type kit struct {
	value func(p unsafe.Pointer, col int) any       // the value of column col of the entity at p
	scan  func(r rows, cols []int) (any, error)     // every row, into a new []E; cols as in Statement.Columns
	join  func(parts []any) (any, []unsafe.Pointer) // one []E of the parts, and a pointer to each element
	pick  func(all any, idx []int) any              // a new []E of all's elements at idx
	ptr   func(all any, i int) any                  // &all[i], as *E
	empty func() any                                // []E{}
}

// One holds a to-one relation, filled by With. Its zero value means not
// loaded. The value is held as any so that an entity's type does not reach
// the types of the entities it relates to: every package that uses an
// instantiated generic type compiles its type descriptor and everything the
// descriptor reaches (design §3 rule 2).
type One[T any] struct {
	v      any // *T
	loaded bool
}

// The entities loaded through a One are shared by every owner with the same
// key: they point at the same value, so changing one changes it for all.
//
// Get returns the related entity (nil when there is none) and whether the
// relation was loaded.
func (o One[T]) Get[_ Lazy[T]]() (*T, bool) {
	p, _ := o.v.(*T)
	return p, o.loaded
}

// Many holds a to-many relation, filled by With. Its zero value means not
// loaded; the value is held as any for the reason given on One.
type Many[T any] struct {
	v      any // []T
	loaded bool
}

// Get returns the related entities and whether the relation was loaded.
func (m Many[T]) Get[_ Lazy[T]]() ([]T, bool) {
	s, _ := m.v.([]T)
	return s, m.loaded
}

// holder has the layout of One[T] and Many[T], so that velox's non-generic
// loader can fill a relation field through its offset (TestHolderLayout).
type holder struct {
	v      any
	loaded bool
}

// Relation is one relation field of a generated Shape or Desc: its name, how
// to reach its target entity and, when EdgeTo or EdgeThrough made it, the
// columns that key it.
type Relation struct {
	name   string
	target func() (*EntityInfo, error)

	explicit  bool     // EdgeTo or EdgeThrough: the key is ownerCol and targetCol
	ownerCol  int      // an index into the owner's Columns
	targetCol int      // an index into the target's Columns
	through   *through // EdgeThrough's join entity; nil for any other relation
}

// RelationTo is what velox-gen writes for a relation field whose target is
// entity T. Nothing runs until the relation is first used.
func RelationTo[T any, PT Entity[T]](name string) Relation {
	return Relation{name: name, target: func() (*EntityInfo, error) {
		m := PT(nil).VeloxMeta()
		return m.info, m.err
	}}
}

// relation is a relation field of an entity, resolved on first use.
// Resolving reads the target's EntityInfo, never the target's relations, so
// relations that point at each other, or at their own entity, cannot wait on
// each other.
type relation struct {
	name   string
	many   bool
	off    uintptr // the field's offset in the owner
	typ    any     // the reflect.Type of One[T] or Many[T] (design §3 rule 7)
	target func() (*EntityInfo, error)
	ref    int     // References: the owner's column; -1 none, -2 invalid
	fkOff  uintptr // ForeignKey: the target field's offset...
	fkTyp  any     // ...and reflect.Type, computed during Schema
	hasFK  bool

	// A relation of the Desc path names its key (EdgeTo): the inference
	// above, from References, ForeignKey and field names, is the old path's.
	explicit  bool
	ownerCol  int
	targetCol int
	through   *through // EdgeThrough's join entity; nil for any other relation

	once sync.Once
	link link
	err  error
}

// link is a resolved relation: owner.Columns[ownerCol] equals
// target.Columns[targetCol] or, for a many-to-many relation, the owner's and
// the target's columns each equal a column of a row of the join entity.
type link struct {
	target     *EntityInfo
	ownerCol   int
	targetCol  int
	join       *EntityInfo // many-to-many: the join entity; nil otherwise
	joinOwner  int         // join's column equal to owner.Columns[ownerCol]
	joinTarget int         // join's column equal to target.Columns[targetCol]
}

func (r *relation) resolve(owner *EntityInfo) (link, error) {
	r.once.Do(func() { r.link, r.err = r.build(owner) })
	return r.link, r.err
}

func (r *relation) build(owner *EntityInfo) (link, error) {
	fail := func(format string, args ...any) (link, error) {
		return link{}, &SchemaError{Entity: owner.Name, Field: r.name, Reason: fmt.Sprintf(format, args...)}
	}
	if r.target == nil {
		return fail("it has no target in the generated code; run go tool velox-gen")
	}
	t, err := r.target()
	if err != nil {
		return fail("its target %s is invalid: %s", targetName(rtype(r.typ)), strings.TrimPrefix(err.Error(), "velox: "))
	}
	l := link{target: t}
	kind := "One"
	if r.many {
		kind = "Many"
	}
	switch {
	case r.explicit:
		// The indexes are literals in generated code: an index out of range
		// is stale or hand-edited code, and would panic on first use.
		if r.ownerCol < 0 || r.ownerCol >= len(owner.Columns) {
			return fail("its key names column %d of %s, which has %d", r.ownerCol, owner.Name, len(owner.Columns))
		}
		if r.targetCol < 0 || r.targetCol >= len(t.Columns) {
			return fail("its key names column %d of %s, which has %d", r.targetCol, t.Name, len(t.Columns))
		}
		l.ownerCol, l.targetCol = r.ownerCol, r.targetCol
		if r.through != nil {
			return r.buildThrough(owner, l, fail)
		}
	case r.ref == -2:
		return fail("References names no column")
	case r.ref >= 0:
		l.ownerCol = r.ref
		if l.targetCol, err = singleKey(t); err != nil {
			return fail("%v", err)
		}
	case r.hasFK:
		if l.targetCol = columnAt(t, r.fkOff, r.fkTyp); l.targetCol < 0 {
			return fail("ForeignKey's function does not return a column of %s", t.Name)
		}
		if l.ownerCol, err = singleKey(owner); err != nil {
			return fail("%v", err)
		}
	default:
		if c, ok := fieldColumn(owner, r.name+"ID"); ok && !r.many {
			l.ownerCol = c
			if l.targetCol, err = singleKey(t); err != nil {
				return fail("%v", err)
			}
			break
		}
		want := typeName(owner.Name) + "ID"
		c, ok := fieldColumn(t, want)
		if !ok {
			return fail("%s has no field %s; name one, or declare the key with s.%s(&x.%s).ForeignKey", t.Name, want, kind, r.name)
		}
		l.targetCol = c
		if l.ownerCol, err = singleKey(owner); err != nil {
			return fail("%v", err)
		}
	}
	if err := sameKey(owner, l.ownerCol, t, l.targetCol); err != nil {
		return fail("%v", err)
	}
	return l, nil
}

// buildThrough resolves a many-to-many relation, l with its owner's and
// target's columns checked, by adding the join entity and its columns.
func (r *relation) buildThrough(owner *EntityInfo, l link, fail func(string, ...any) (link, error)) (link, error) {
	if !r.many {
		return fail("a many-to-many relation needs a Many field")
	}
	j, err := r.through.join()
	if err != nil {
		return fail("its join entity is invalid: %s", strings.TrimPrefix(err.Error(), "velox: "))
	}
	// Has reads the join rows inside its EXISTS, where nothing scopes them,
	// while loading reads them with a query of their own, which would be
	// scoped: the two would disagree. A generated join entity has neither.
	if j.Tenant != -1 {
		return fail("its join entity %s has a tenant column; a join entity can have none", j.Name)
	}
	if p := j.policy; p != nil && (p.read != nil || p.write != nil || p.insert != nil) {
		return fail("its join entity %s has a policy; a join entity can have none", j.Name)
	}
	for _, c := range [...]int{r.through.ownerCol, r.through.targetCol} {
		if c < 0 || c >= len(j.Columns) {
			return fail("its key names column %d of %s, which has %d", c, j.Name, len(j.Columns))
		}
	}
	l.join, l.joinOwner, l.joinTarget = j, r.through.ownerCol, r.through.targetCol
	if err := sameKey(owner, l.ownerCol, j, l.joinOwner); err != nil {
		return fail("%v", err)
	}
	if err := sameKey(j, l.joinTarget, l.target, l.targetCol); err != nil {
		return fail("%v", err)
	}
	return l, nil
}

// sameKey reports whether a.Columns[ac] and b.Columns[bc] can key a
// relation: their types, without a pointer, are one comparable type.
func sameKey(a *EntityInfo, ac int, b *EntityInfo, bc int) error {
	x, y := a.Columns[ac], b.Columns[bc]
	if base(x.Type) != base(y.Type) {
		return fmt.Errorf("the key types differ: %s.%s is %v and %s.%s is %v", a.Name, x.Field, base(x.Type), b.Name, y.Field, base(y.Type))
	}
	if !base(x.Type).Comparable() {
		return fmt.Errorf("a key of type %v cannot be compared", base(x.Type))
	}
	return nil
}

// singleKey returns an entity's primary key column; relations need one.
func singleKey(in *EntityInfo) (int, error) {
	if len(in.PrimaryKey) != 1 {
		return -1, fmt.Errorf("%s has a composite primary key; a relation needs a single-column key", in.Name)
	}
	return in.PrimaryKey[0], nil
}

// columnAt returns the column of the field at offset off with type typ, a
// reflect.Type.
func columnAt(in *EntityInfo, off uintptr, typ any) int {
	for i, c := range in.Columns {
		if c.off == off && c.Type == typ {
			return i
		}
	}
	return -1
}

// fieldColumn returns the column of the field named field.
func fieldColumn(in *EntityInfo, field string) (int, bool) {
	for i, c := range in.Columns {
		if c.Field == field {
			return i, true
		}
	}
	return -1, false
}

// base is the reflect.Type v holds without one level of pointer: *int64 and
// int64 are the same key.
func base(v any) reflect.Type {
	t := rtype(v)
	if t.Kind() == reflect.Pointer {
		return t.Elem()
	}
	return t
}

// typeName is the type's name without its package, first letter upper-cased:
// "Invoice" for "billing.Invoice" (and "Company" for an unexported company).
func typeName(name string) string {
	n := name[strings.LastIndexByte(name, '.')+1:]
	r, size := utf8.DecodeRuneInString(n)
	return string(unicode.ToUpper(r)) + n[size:]
}

// targetName is the name of T in the relation field type One[T] or Many[T].
func targetName(t reflect.Type) string {
	n := t.Name()
	if i := strings.IndexByte(n, '['); i >= 0 && strings.HasSuffix(n, "]") {
		return n[i+1 : len(n)-1]
	}
	return n
}

// Rel is the handle of a relation of entity E to entity T, written by
// velox-gen: I is the relation's index in E's shape. Passed to Query.With it
// loads the relation; its Where, OrderBy and With say what to load. Like a
// Query it is a value: each method returns a new Rel.
type Rel[E, T any] struct {
	I     int32
	where []Predicate
	order []OrderTerm
	with  []loadSpec
	err   error
}

// Where returns the relation loading only the related entities that meet
// every condition.
func (r Rel[E, T]) Where[_ Lazy[T]](ps ...Pred[T]) Rel[E, T] {
	r.where = slices.Clip(r.where)
	for _, p := range ps {
		if r.err == nil {
			r.err = predErr(p.n)
		}
		r.where = append(r.where, Predicate(p))
	}
	return r
}

// OrderBy returns the relation loading its entities in this order within
// each owner. The default is the target's primary key.
func (r Rel[E, T]) OrderBy[_ Lazy[T]](os ...Order[T]) Rel[E, T] {
	r.order = slices.Clip(r.order)
	for _, o := range os {
		r.order = append(r.order, o.t)
	}
	return r
}

// With returns the relation also loading a relation of each loaded entity.
func (r Rel[E, T]) With[U any](next Rel[T, U]) Rel[E, T] {
	sp := next.spec()
	if r.err == nil {
		r.err = specErr(sp)
	}
	r.with = append(slices.Clip(r.with), sp)
	return r
}

// spec is the relation's load options, without types.
func (r Rel[E, T]) spec[_ Lazy[T]]() loadSpec {
	return loadSpec{rel: r.I, where: r.where, order: r.order, with: r.with, err: r.err}
}

// MarshalJSON writes the related entity, or null when there is none or it
// was not loaded. Tag the field json:",omitzero" to leave a relation that was
// not loaded out. (An ordinary method on a generic type, against design §3
// rule 1, because encoding/json looks for it by name; its body is one call.)
func (o One[T]) MarshalJSON() ([]byte, error) { return json.Marshal(o.v) }

// MarshalJSON writes the related entities, or null when they were not
// loaded; see One.MarshalJSON.
func (m Many[T]) MarshalJSON() ([]byte, error) { return json.Marshal(m.v) }

// relLink resolves relation i of entity E.
func relLink[E any](i int32) (link, error) {
	m, err := metaOf[E]()
	if err != nil {
		return link{}, err
	}
	if i < 0 || int(i) >= len(m.info.rels) {
		return link{}, fmt.Errorf("velox: %s: has no relation %d; run go tool velox-gen", m.info.Name, i)
	}
	return m.info.rels[i].resolve(m.info)
}

// Has is true when the relation holds at least one entity. On MySQL, an UPDATE
// or DELETE whose condition's EXISTS reads the table being changed (a relation
// of an entity to itself) fails with error 1093.
func (r Rel[E, T]) Has[_ Lazy[T]]() Pred[E] { return r.HasWhere() }

// HasWhere is true when at least one related entity meets every condition:
// EXISTS (SELECT 1 FROM target WHERE key = owner's key AND ps…). On MySQL, an
// UPDATE or DELETE whose condition's EXISTS reads the table being changed (a
// relation of an entity to itself) fails with error 1093.
func (r Rel[E, T]) HasWhere[_ Lazy[T]](ps ...Pred[T]) Pred[E] {
	l, err := relLink[E](r.I)
	if err == nil {
		for _, p := range ps {
			if err = predErr(p.n); err != nil {
				break
			}
		}
	}
	if err != nil {
		return Pred[E]{node{op: opInvalid, v: err}}
	}
	return Pred[E]{node{op: opExists, col: r.I, v: l, kids: nodes(ps)}}
}
