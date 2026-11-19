package engine

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"unsafe"
)

// Kit is the code velox needs to handle an entity whose type it does not
// know at the point of use: loading a relation to it. MetaOnce.Load builds
// it, so it is compiled once, in the entity's own package.
type Kit struct {
	Value func(p unsafe.Pointer, col int) any                       // the value of column col of the entity at p
	Scan  func(r Rows, cols []int) (any, error)                     // every row, into a new *blocks; cols as in Statement.Columns
	Pick  func(all []any, ptrs []unsafe.Pointer, idx [][]int) []any // for each idx, a []E of the rows at ptrs[idx...], which JoinBlocks gave of all; see InPlace
	Ptr   func(p unsafe.Pointer) any                                // p as *E
	Empty func() any                                                // []E{}
}

// Holder has the layout of One[T] and Many[T], so that velox's non-generic
// loader can fill a relation field through its offset (TestHolderLayout).
type Holder struct {
	V      any
	Loaded bool
}

// EdgeDescriptor is one relation field of a generated Desc: its name and the columns
// that key it. For generated code. EdgeTo, EdgeBidi and EdgeThrough make it. It carries
// no target entity: the use site supplies that (see EdgeTo).
type EdgeDescriptor struct {
	name string

	explicit  bool     // EdgeTo or EdgeThrough made it; a literal EdgeDescriptor is refused
	ownerCol  int      // an index into the owner's Columns
	targetCol int      // an index into the target's Columns
	through   *Through // EdgeThrough's join entity; nil for any other relation
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
	Name string
	Many bool
	Off  uintptr // the field's offset in the owner
	typ  any     // the reflect.Type of One[T] or Many[T]

	// The key, as EdgeTo and EdgeThrough wrote it.
	ownerCol  int
	targetCol int
	through   *Through // EdgeThrough's join entity; nil for any other relation
	once      sync.Once
	link      Link
	err       error
}

// Link is a resolved relation: owner.Columns[ownerCol] equals
// target.Columns[targetCol] or, for a many-to-many relation, the owner's and
// the target's columns each equal a column of a row of the join entity.
type Link struct {
	Name       string // the relation's field in the owner
	Many       bool   // a Many field: the relation can hold several rows, and cannot be joined
	Target     *EntityInfo
	OwnerCol   int
	TargetCol  int
	Join       *EntityInfo // many-to-many: the join entity; nil otherwise
	JoinOwner  int         // join's column equal to owner.Columns[ownerCol]
	JoinTarget int         // join's column equal to target.Columns[targetCol]
}

// RelTarget is a relation's target as the use site names it: the getter of
// the target's metadata, and the reflect.Types of One[T] and Many[T], which
// the owner's field must be one of. The types are type descriptors only
// (One and Many hold their value as any), so naming them does not link T's
// metadata or its code. The zero value names no target.
type RelTarget struct {
	Get       func() (*EntityInfo, error)
	One, Many any
}

// Resolve returns the relation's link. A relation has no target of its own,
// so that the owner's metadata does not keep the target's code in every
// program that links the owner: the use site, which names the target's type,
// passes use. Every resolution checks that use names the entity the field
// holds, before the cached link is consulted: the link is built from the
// owner's and the target's columns, never from the handle, so a handle that
// fails the check neither gets the good handle's link nor spoils it. The check
// is two comparisons and allocates nothing.
func (r *relation) Resolve(owner *EntityInfo, use RelTarget) (Link, error) {
	if use.Get == nil {
		return Link{}, r.fail(owner, "it has no target in the generated code; run go tool velox-gen")
	}
	// The handle's T must be the entity the field holds, or loading would
	// fill the field with rows of another type and Get would find none.
	if ft := rtype(r.typ); ft != rtype(use.One) && ft != rtype(use.Many) {
		return Link{}, r.fail(owner, "the field is a %v, but the Rel handle names %s; run go tool velox-gen", ft, targetName(rtype(use.One)))
	}
	r.once.Do(func() { r.link, r.err = r.build(owner, use) })
	return r.link, r.err
}

// fail is a *SchemaError about r, an error of owner's relation.
func (r *relation) fail(owner *EntityInfo, format string, args ...any) error {
	return &SchemaError{Entity: owner.Name, Field: r.Name, Reason: fmt.Sprintf(format, args...)}
}

// build resolves the relation against use, whose handle resolve has checked.
func (r *relation) build(owner *EntityInfo, use RelTarget) (Link, error) {
	fail := func(format string, args ...any) (Link, error) {
		return Link{}, r.fail(owner, format, args...)
	}
	t, err := use.Get()
	if err != nil {
		return fail("its target %s is invalid: %s", targetName(rtype(r.typ)), strings.TrimPrefix(err.Error(), "velox: "))
	}
	l := Link{Target: t, Name: r.Name, Many: r.Many}
	// The indexes are literals in generated code: an index out of range
	// is stale or hand-edited code, and would panic on first use.
	if r.ownerCol < 0 || r.ownerCol >= len(owner.Columns) {
		return fail("its key names column %d of %s, which has %d", r.ownerCol, owner.Name, len(owner.Columns))
	}
	if r.targetCol < 0 || r.targetCol >= len(t.Columns) {
		return fail("its key names column %d of %s, which has %d", r.targetCol, t.Name, len(t.Columns))
	}
	l.OwnerCol, l.TargetCol = r.ownerCol, r.targetCol
	if r.through != nil {
		return r.buildThrough(owner, l, fail)
	}
	if err := sameKey(owner, l.OwnerCol, t, l.TargetCol); err != nil {
		return fail("%v", err)
	}
	return l, nil
}

// buildThrough resolves a many-to-many relation, l with its owner's and
// target's columns checked, by adding the join entity and its columns.
func (r *relation) buildThrough(owner *EntityInfo, l Link, fail func(string, ...any) (Link, error)) (Link, error) {
	if !r.Many {
		return fail("a many-to-many relation needs a Many field")
	}
	j, err := r.through.Join()
	if err != nil {
		return fail("its join entity is invalid: %s", strings.TrimPrefix(err.Error(), "velox: "))
	}
	// Has reads the join rows inside its EXISTS, bound to the owner and the target,
	// which are scoped, while a query of their own would need a condition: the two
	// could disagree. So a join entity cannot have a Read policy or an
	// interceptor. Its Insert and Write policies apply only to writes of its
	// rows, which they may check.
	if j.policy.reads() {
		return fail("its join entity %s has a Read policy; a join entity is read through its two sides, whose Read policies apply, and cannot have one of its own", j.Name)
	}
	if len(j.filters) > 0 {
		return fail("its join entity %s has interceptors; a join entity is read through its two sides, whose interceptors apply, and cannot have its own", j.Name)
	}
	for _, c := range [...]int{r.through.OwnerCol, r.through.TargetCol} {
		if c < 0 || c >= len(j.Columns) {
			return fail("its key names column %d of %s, which has %d", c, j.Name, len(j.Columns))
		}
	}
	l.Join, l.JoinOwner, l.JoinTarget = j, r.through.OwnerCol, r.through.TargetCol
	if err := sameKey(owner, l.OwnerCol, j, l.JoinOwner); err != nil {
		return fail("%v", err)
	}
	if err := sameKey(j, l.JoinTarget, l.Target, l.TargetCol); err != nil {
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

// RelRef names relation i of an entity without reading the entity's
// metadata: the owner function reads it, and runs only when the statement
// that holds the condition does (resolve). Building a condition inside an
// entity's Policy or Hooks, which Load calls while it builds that entity's
// metadata, then does not wait on itself.
type RelRef struct {
	Owner  func() (*EntityInfo, error)
	Target RelTarget // the relation's target, named by the use site
	I      int32
}

// Link resolves the relation.
func (r RelRef) Link() (Link, error) {
	in, err := r.Owner()
	if err != nil {
		return Link{}, err
	}
	if r.I < 0 || int(r.I) >= len(in.rels) {
		return Link{}, fmt.Errorf("velox: %s: has no relation %d; run go tool velox-gen", in.Name, r.I)
	}
	return in.rels[r.I].Resolve(in, r.Target)
}

// ColRef is the correlation of a subquery made by a column handle's Select
// and InSelect: the owner's column OwnerCol equals the target's column
// TargetCol. Like RelRef it reads no metadata until the statement that holds
// it runs. Its EXISTS node has Col -1, so render's cache keys tell it from a
// relation's.
type ColRef struct {
	Owner, Target       func() (*EntityInfo, error)
	OwnerCol, TargetCol int32
}

// Link resolves the correlation to a link with no join.
func (r ColRef) Link() (Link, error) {
	owner, err := r.Owner()
	if err != nil {
		return Link{}, err
	}
	target, err := r.Target()
	if err != nil {
		return Link{}, err
	}
	if r.OwnerCol < 0 || int(r.OwnerCol) >= len(owner.Columns) {
		return Link{}, fmt.Errorf("velox: %s: has no column %d; run go tool velox-gen", owner.Name, r.OwnerCol)
	}
	if r.TargetCol < 0 || int(r.TargetCol) >= len(target.Columns) {
		return Link{}, fmt.Errorf("velox: %s: has no column %d; run go tool velox-gen", target.Name, r.TargetCol)
	}
	return Link{Target: target, OwnerCol: int(r.OwnerCol), TargetCol: int(r.TargetCol)}, nil
}

// owner is the entity whose column the correlation names.
func (r ColRef) owner() (*EntityInfo, error) { return r.Owner() }

// owner is the entity whose relation r names.
func (r RelRef) owner() (*EntityInfo, error) { return r.Owner() }

// linkRef is an EXISTS's correlation not yet resolved: a RelRef or a ColRef.
type linkRef interface {
	Link() (Link, error)
	owner() (*EntityInfo, error)
}

// OfNode is Rel.Of's condition for the relation ref names, with its Where
// conditions where and their error err, for the owners at ps.
func OfNode(ref RelRef, where []Predicate, err error, ps []unsafe.Pointer) Node {
	if err != nil {
		return Node{Op: OpInvalid, V: err}
	}
	l, err := ref.Link()
	if err != nil {
		return Node{Op: OpInvalid, V: err}
	}
	owner, _ := ref.Owner() // link read it without an error
	keys := make([]any, 0, len(ps))
	for _, p := range ps {
		if p == nil {
			return Node{Op: OpInvalid, V: fmt.Errorf("velox: %s: Of was given a nil owner", owner.Name)}
		}
		if k, ok := keyOf(owner.kit.Value(p, l.OwnerCol)); ok {
			keys = append(keys, k)
		}
	}
	n := Node{Op: OpIn, Col: int32(l.TargetCol), V: keys, W: l.Target}
	if l.Join != nil {
		// The target's own scope applies to the statement that holds the
		// condition.
		n = Node{Op: OpThrough, Col: int32(l.TargetCol), V: l.Join, W: l.JoinTarget,
			Kids: []Node{{Op: OpIn, Col: int32(l.JoinOwner), V: keys, W: l.Join}}}
	}
	if len(where) == 0 {
		return n
	}
	kids := make([]Node, 0, 1+len(where))
	kids = append(kids, n)
	for _, p := range where {
		kids = append(kids, p.n)
	}
	return Group(OpAnd, kids)
}

// RelOffset is the offset of relation i's field in the owner's struct, where a
// joined row is stored, or why the relation is not a field of the entity.
func RelOffset(in *EntityInfo, i int32) (uintptr, error) {
	if i < 0 || int(i) >= len(in.rels) {
		return 0, fmt.Errorf("velox: %s: has no relation %d; run go tool velox-gen", in.Name, i)
	}
	return in.rels[i].Off, nil
}
