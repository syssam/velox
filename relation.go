package velox

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"unsafe"
)

// kit is the code velox needs to handle an entity whose type it does not
// know at the point of use: loading a relation to it. MetaOnce.Load builds
// it, so it is compiled once, in the entity's own package.
type kit struct {
	value func(p unsafe.Pointer, col int) any       // the value of column col of the entity at p
	scan  func(r rows, cols []int) (any, error)     // every row, into a new []E; cols as in Statement.Columns
	join  func(parts []any) (any, []unsafe.Pointer) // one []E of the parts, and a pointer to each element
	pick  func(all any, idx []int) any              // a new []E of all's elements at idx
	ptr   func(all any, i int) any                  // &all[i], as *E
	empty func() any                                // []E{}
}

// One holds a to-one relation, filled by With. Its zero value means not loaded.
// Every to-one edge velox-gen writes matches at most one row: its key is the
// target's primary key, or, for a one-to-one edge, a unique column. A loaded
// One holds that row, or nil when there is none.
//
// The value is held as any so that an entity's type does not reach the types of
// the entities it relates to: every package that uses an instantiated generic
// type compiles its type descriptor and everything the descriptor reaches.
type One[T any] struct {
	v      any // *T
	loaded bool
}

// Get returns the related entity (nil when there is none) and whether the
// relation was loaded. The entities loaded through a One are shared by every
// owner with the same key: they point at the same value, so changing one
// changes it for all.
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

// EdgeDesc is one relation field of a generated Desc: its name and the columns
// that key it. For generated code. EdgeTo, EdgeBidi and EdgeThrough make it. It carries
// no target entity: the use site supplies that (see EdgeTo).
type EdgeDesc struct {
	name string

	explicit  bool     // EdgeTo or EdgeThrough made it; a literal EdgeDesc is refused
	ownerCol  int      // an index into the owner's Columns
	targetCol int      // an index into the target's Columns
	through   *through // EdgeThrough's join entity; nil for any other relation
	bidi      any      // EdgeBidi made it: the reflect.Type of T, which must be the owner; nil otherwise

	// holders are the reflect.Types of One[T] and Many[T] for the target T:
	// the types the relation's field may have.
	holders [2]any
}

// relation is a relation field of an entity, resolved on first use.
// Resolving reads the target's EntityInfo, never the target's relations, so
// relations that point at each other, or at their own entity, cannot wait on
// each other.
type relation struct {
	name string
	many bool
	off  uintptr // the field's offset in the owner
	typ  any     // the reflect.Type of One[T] or Many[T]

	// The key, as EdgeTo and EdgeThrough wrote it.
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

// useTarget is a relation's target as the use site names it: the getter of
// the target's metadata, and the reflect.Types of One[T] and Many[T], which
// the owner's field must be one of. The types are type descriptors only
// (One and Many hold their value as any), so naming them does not link T's
// metadata or its code. The zero value names no target.
type useTarget struct {
	get       func() (*EntityInfo, error)
	one, many any
}

// targetOf is the use site's target for a relation to entity T. It calls
// elemType with a nil pointer where non-generic code would use reflect.TypeFor,
// so that no reflect.Type is compiled into the generic code.
func targetOf[T any]() useTarget {
	return useTarget{get: ownerOf[T], one: elemType((*One[T])(nil)), many: elemType((*Many[T])(nil))}
}

// resolve returns the relation's link. A relation has no target of its own,
// so that the owner's metadata does not keep the target's code in every
// program that links the owner: the use site, which names the target's type,
// passes use. Every resolution checks that use names the entity the field
// holds, before the cached link is consulted: the link is built from the
// owner's and the target's columns, never from the handle, so a handle that
// fails the check neither gets the good handle's link nor spoils it. The check
// is two comparisons and allocates nothing.
func (r *relation) resolve(owner *EntityInfo, use useTarget) (link, error) {
	if use.get == nil {
		return link{}, r.fail(owner, "it has no target in the generated code; run go tool velox-gen")
	}
	// The handle's T must be the entity the field holds, or loading would
	// fill the field with rows of another type and Get would find none.
	if ft := rtype(r.typ); ft != rtype(use.one) && ft != rtype(use.many) {
		return link{}, r.fail(owner, "the field is a %v, but the Rel handle names %s; run go tool velox-gen", ft, targetName(rtype(use.one)))
	}
	r.once.Do(func() { r.link, r.err = r.build(owner, use) })
	return r.link, r.err
}

// fail is a *SchemaError about r, an error of owner's relation.
func (r *relation) fail(owner *EntityInfo, format string, args ...any) error {
	return &SchemaError{Entity: owner.Name, Field: r.name, Reason: fmt.Sprintf(format, args...)}
}

// build resolves the relation against use, whose handle resolve has checked.
func (r *relation) build(owner *EntityInfo, use useTarget) (link, error) {
	fail := func(format string, args ...any) (link, error) {
		return link{}, r.fail(owner, format, args...)
	}
	t, err := use.get()
	if err != nil {
		return fail("its target %s is invalid: %s", targetName(rtype(r.typ)), strings.TrimPrefix(err.Error(), "velox: "))
	}
	l := link{target: t}
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
	// scoped: the two would disagree. So a join entity can have neither a
	// tenant column nor a Read policy. Its Insert and Write policies apply
	// only to writes of its rows, which they may check: join rows are not
	// tenant-scoped, so a link's endpoints are checked there or in a hook.
	if j.Tenant != -1 {
		return fail("its join entity %s has a tenant column; join rows are not scoped, so a join entity can have neither a tenant column nor a Read policy", j.Name)
	}
	if p := j.policy; p != nil && p.read != nil {
		return fail("its join entity %s has a Read policy; join rows are not scoped, so a join entity can have neither a tenant column nor a Read policy", j.Name)
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
// relation: neither is encoded, and their types, without a pointer, are one
// comparable type.
func sameKey(a *EntityInfo, ac int, b *EntityInfo, bc int) error {
	x, y := a.Columns[ac], b.Columns[bc]
	for _, k := range [...]struct {
		e *EntityInfo
		c ColumnInfo
	}{{a, x}, {b, y}} {
		if k.c.Codec {
			return fmt.Errorf("the key %s.%s is an encoded column, which cannot be compared", k.e.Name, k.c.Field)
		}
	}
	if base(x.Type) != base(y.Type) {
		return fmt.Errorf("the key types differ: %s.%s is %v and %s.%s is %v", a.Name, x.Field, base(x.Type), b.Name, y.Field, base(y.Type))
	}
	if !base(x.Type).Comparable() {
		return fmt.Errorf("a key of type %v cannot be compared", base(x.Type))
	}
	return nil
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

// targetName is the name of T in the relation field type One[T] or Many[T].
func targetName(t reflect.Type) string {
	n := t.Name()
	if i := strings.IndexByte(n, '['); i >= 0 && strings.HasSuffix(n, "]") {
		return n[i+1 : len(n)-1]
	}
	return n
}

// Rel is the handle of a relation of entity E to entity T, written by
// velox-gen: I is the index of its edge in E's Desc Edges. Passed to Query.With
// it loads the relation; its Where, OrderBy and With say what to load. Like a
// Query it is a value: each method returns a new Rel.
type Rel[E, T any] struct {
	I       int32
	where   []Predicate
	order   []OrderTerm
	with    []loadSpec
	each    int // LimitEach's n; 0: no limit
	maxRows int // MaxRows's n; 0: the owner's cap
	err     error
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

// LimitEach returns the relation loading at most n entities for each owner,
// the first in its order: each owner's own page. n <= 0 removes the limit.
//
// The load is one statement for every owner (per chunk of owners, under the
// dialect's parameter limit), which ranks each owner's rows with
// ROW_NUMBER() on every dialect; ties in the order are broken by the target's
// primary key. The tenant scope and the Read policy apply before the ranking,
// so a row they hide takes no place in a page. A to-one relation read by the
// target's one-column primary key has at most one entity per owner, and
// needs no ranking.
//
// A many-to-many relation is read through its join entity in that one
// statement, which is the target's. A load without LimitEach reads the join
// rows in a statement of the join entity's own; with it, no statement is sent
// for the join entity, so a Rewrite or Around hook that acts on the join
// entity's statements does not see the read. A link the join table holds
// twice relates its target to the owner once, as a plain load reads it.
//
// The ranking names its rank vx_rn and, through a join, the owner's key
// vx_owner: a target column of either name cannot be read with LimitEach.
func (r Rel[E, T]) LimitEach[_ Lazy[T]](n int) Rel[E, T] {
	r.each = max(n, 0)
	return r
}

// MaxRows returns the relation's load allowed up to n rows per statement, in
// place of the owner query's MaxRows and the DB's cap, for its nested loads
// too. n <= 0 restores the owner's.
//
// Without it, a load with LimitEach(n) may return n rows for each owner (one,
// when it reads by the target's one-column primary key), whatever the owner's
// cap: it is bounded by construction. Any other load shares the owner
// query's cap.
func (r Rel[E, T]) MaxRows[_ Lazy[T]](n int) Rel[E, T] {
	r.maxRows = max(n, 0)
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
	return loadSpec{rel: r.I, target: targetOf[T](), where: r.where, order: r.order, with: r.with, each: r.each, maxRows: r.maxRows, err: r.err}
}

// MarshalJSON writes the related entity, or null when there is none or it was
// not loaded. Tag the field json:",omitzero" to leave a relation that was not
// loaded out. (An ordinary method on a generic type, not a generic one, because
// encoding/json looks for it by name; its body is one call.)
func (o One[T]) MarshalJSON() ([]byte, error) { return json.Marshal(o.v) }

// MarshalJSON writes the related entities, or null when they were not
// loaded; see One.MarshalJSON.
func (m Many[T]) MarshalJSON() ([]byte, error) { return json.Marshal(m.v) }

// relRef names relation i of an entity without reading the entity's
// metadata: the owner function reads it, and runs only when the statement
// that holds the condition does (resolve). Building a condition inside an
// entity's Policy or Hooks, which Load calls while it builds that entity's
// metadata, then does not wait on itself.
type relRef struct {
	owner  func() (*EntityInfo, error)
	target useTarget // the relation's target, named by the use site
	i      int32
}

// ownerOf is relRef.owner for entity E, and the getter in useTarget for the
// target E of a relation.
func ownerOf[E any]() (*EntityInfo, error) {
	m, err := metaOf[E]()
	if err != nil {
		return nil, err
	}
	return m.info, nil
}

// link resolves the relation.
func (r relRef) link() (link, error) {
	in, err := r.owner()
	if err != nil {
		return link{}, err
	}
	if r.i < 0 || int(r.i) >= len(in.rels) {
		return link{}, fmt.Errorf("velox: %s: has no relation %d; run go tool velox-gen", in.Name, r.i)
	}
	return in.rels[r.i].resolve(in, r.target)
}

// CheckRelation resolves the relation that r names and returns what is wrong
// with it: its target is not an entity or has invalid metadata, or its key is
// missing or mistyped. CheckEntity cannot do this, because a relation of
// generated code names its target only where it is used. velox-gen verify
// calls it for every relation: velox.CheckRelation(gen.Users.Posts).
func CheckRelation[E, T any](r Rel[E, T]) error {
	_, err := relRef{owner: ownerOf[E], target: targetOf[T](), i: r.I}.link()
	return err
}

// Has is true when the relation holds at least one entity. On MySQL, an UPDATE
// or DELETE whose condition's EXISTS reads the table being changed (a relation
// of an entity to itself) fails with error 1093.
func (r Rel[E, T]) Has[_ Lazy[T]]() Pred[E] { return r.HasWhere() }

// HasWhere is true when at least one related entity meets every condition:
// EXISTS (SELECT 1 FROM target WHERE key = owner's key AND ps…). On MySQL, an
// UPDATE or DELETE whose condition's EXISTS reads the table being changed (a
// relation of an entity to itself) fails with error 1093.
//
// HasWhere reads no metadata, so it may be called anywhere, an entity's
// Policy and Hooks bodies included. A relation that cannot be resolved fails
// the statement that holds the condition, not HasWhere.
func (r Rel[E, T]) HasWhere[_ Lazy[T]](ps ...Pred[T]) Pred[E] {
	for _, p := range ps {
		if err := predErr(p.n); err != nil {
			return Pred[E]{node{op: opInvalid, v: err}}
		}
	}
	// The relation resolves when a statement holding the condition runs, not
	// here: see relRef.
	return Pred[E]{node{op: opExists, col: r.I, v: relRef{owner: ownerOf[E], target: targetOf[T](), i: r.I}, kids: nodes(ps)}}
}

// Of is true for the entities related through r to any of owners: the
// rows of T that loading r for owners would load, without r's ordering.
// r's Where conditions apply too; its OrderBy and With, which say how to
// load, do not.
//
//	db.Query[gen.Post]().Where(gen.Users.Posts.Of(&ada)).Limit(20).All(ctx)
//
// Of reads the owners' keys when it is called, so later changes to them do
// not change the condition. It reads E's and T's metadata: inside a Policy
// or Hooks body, outside the functions it returns, use HasWhere. A nil
// owner fails the statement that holds the condition; an owner whose key is
// NULL relates to nothing; with no owners Of is false. Each owner is one
// parameter, under the dialect's limit: a statement with more owners than
// that is refused.
func (r Rel[E, T]) Of[_ Lazy[T]](owners ...*E) Pred[T] {
	// A []*E has the layout of a []unsafe.Pointer; the work is not generic,
	// so that it is compiled once, not into every package that uses a Rel.
	ps := unsafe.Slice((*unsafe.Pointer)(unsafe.Pointer(unsafe.SliceData(owners))), len(owners))
	return Pred[T]{ofNode(relRef{owner: ownerOf[E], target: targetOf[T](), i: r.I}, r.where, r.err, ps)}
}

// ofNode is Rel.Of's condition for the relation ref names, with its Where
// conditions where and their error err, for the owners at ps.
func ofNode(ref relRef, where []Predicate, err error, ps []unsafe.Pointer) node {
	if err != nil {
		return node{op: opInvalid, v: err}
	}
	l, err := ref.link()
	if err != nil {
		return node{op: opInvalid, v: err}
	}
	owner, _ := ref.owner() // link read it without an error
	keys := make([]any, 0, len(ps))
	for _, p := range ps {
		if p == nil {
			return node{op: opInvalid, v: fmt.Errorf("velox: %s: Of was given a nil owner", owner.Name)}
		}
		if k, ok := keyOf(owner.kit.value(p, l.ownerCol)); ok {
			keys = append(keys, k)
		}
	}
	n := node{op: opIn, col: int32(l.targetCol), v: keys}
	if l.join != nil {
		// The join rows are not scoped: a join entity has neither a tenant
		// column nor a Read policy (buildThrough). The target's own scope
		// applies to the statement that holds the condition.
		n = node{op: opThrough, col: int32(l.targetCol), v: l.join, w: l.joinTarget,
			kids: []node{{op: opIn, col: int32(l.joinOwner), v: keys}}}
	}
	if len(where) == 0 {
		return n
	}
	kids := make([]node, 0, 1+len(where))
	kids = append(kids, n)
	for _, p := range where {
		kids = append(kids, p.n)
	}
	return group(opAnd, kids)
}
