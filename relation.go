package velox

import (
	"encoding/json"
	"slices"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

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

// targetOf is the use site's target for a relation to entity T. It calls
// ElemType with a nil pointer where non-generic code would use reflect.TypeFor,
// so that no reflect.Type is compiled into the generic code.
func targetOf[T any]() engine.RelTarget {
	return engine.RelTarget{Get: ownerOf[T], One: engine.ElemType((*One[T])(nil)), Many: engine.ElemType((*Many[T])(nil))}
}

// Rel is the handle of a relation of entity E to entity T, written by
// velox-gen: I is the index of its edge in E's Descriptor Edges. Passed to Query.With
// it loads the relation; its Where, OrderBy and With say what to load. Like a
// Query it is a value: each method returns a new Rel.
type Rel[E, T any] struct {
	I       int32
	where   []Predicate
	order   []OrderTerm
	with    []engine.LoadSpec
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
			r.err = engine.PredErr(engine.PredNode(Predicate(p)))
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
// primary key. The Read policy, a scope included, applies before the ranking,
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
		r.err = engine.SpecErr(sp)
	}
	r.with = append(slices.Clip(r.with), sp)
	return r
}

// spec is the relation's load options, without types.
func (r Rel[E, T]) spec[_ Lazy[T]]() engine.LoadSpec {
	return engine.LoadSpec{Rel: r.I, Target: targetOf[T](), Where: r.where, Order: r.order, With: r.with, Each: r.each, MaxRows: r.maxRows, Err: r.err}
}

// MarshalJSON writes the related entity, or null when there is none or it was
// not loaded. Tag the field json:",omitzero" to leave a relation that was not
// loaded out. (An ordinary method on a generic type, not a generic one, because
// encoding/json looks for it by name; its body is one call.)
//
// JSON goes one way: One and Many have no UnmarshalJSON, as decoding would need T,
// which they do not reach (see One), so decoding an entity whose relations were
// loaded fails. Decode into a struct of your own, or marshal the entity before
// loading them.
func (o One[T]) MarshalJSON() ([]byte, error) { return json.Marshal(o.v) }

// MarshalJSON writes the related entities, or null when they were not
// loaded; see One.MarshalJSON.
func (m Many[T]) MarshalJSON() ([]byte, error) { return json.Marshal(m.v) }

// ownerOf is RelRef.owner for entity E, and the getter in RelTarget for the
// target E of a relation.
func ownerOf[E any]() (*EntityInfo, error) {
	m, err := metaOf[E]()
	if err != nil {
		return nil, err
	}
	return m.info, nil
}

// CheckRelation resolves the relation that r names and returns what is wrong
// with it: its target is not an entity or has invalid metadata, or its key is
// missing or mistyped. CheckEntity cannot do this, because a relation of
// generated code names its target only where it is used. velox-gen verify
// calls it for every relation: velox.CheckRelation(gen.Users.Posts).
func CheckRelation[E, T any](r Rel[E, T]) error {
	_, err := engine.RelRef{Owner: ownerOf[E], Target: targetOf[T](), I: r.I}.Link()
	return err
}

// Has is true when the relation holds at least one entity. On MySQL, an UPDATE
// or DELETE whose condition's EXISTS reads the table being changed (a relation
// of an entity to itself) fails with error 1093.
func (r Rel[E, T]) Has[_ Lazy[T]]() Pred[E] { return r.HasWith() }

// HasWith is true when at least one related entity meets every condition:
// EXISTS (SELECT 1 FROM target WHERE key = owner's key AND ps…). On MySQL, an
// UPDATE or DELETE whose condition's EXISTS reads the table being changed (a
// relation of an entity to itself) fails with error 1093.
//
// HasWith reads no metadata, so it may be called anywhere, an entity's
// Policy and Hooks bodies included. A relation that cannot be resolved fails
// the statement that holds the condition, not HasWith.
func (r Rel[E, T]) HasWith[_ Lazy[T]](ps ...Pred[T]) Pred[E] {
	for _, p := range ps {
		if err := engine.PredErr(engine.PredNode(Predicate(p))); err != nil {
			return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpInvalid, V: err}))
		}
	}
	// The relation resolves when a statement holding the condition runs, not
	// here: see RelRef.
	return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpExists, Col: r.I, V: engine.RelRef{Owner: ownerOf[E], Target: targetOf[T](), I: r.I}, Kids: nodes(ps)}))
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
// or Hooks body, outside the functions it returns, use HasWith. A nil
// owner fails the statement that holds the condition; an owner whose key is
// NULL relates to nothing; with no owners Of is false. Each owner is one
// parameter, under the dialect's limit: a statement with more owners than
// that is refused.
func (r Rel[E, T]) Of[_ Lazy[T]](owners ...*E) Pred[T] {
	// A []*E has the layout of a []unsafe.Pointer; the work is not generic,
	// so that it is compiled once, not into every package that uses a Rel.
	ps := unsafe.Slice((*unsafe.Pointer)(unsafe.Pointer(unsafe.SliceData(owners))), len(owners))
	return Pred[T](engine.NewPredicate(engine.OfNode(engine.RelRef{Owner: ownerOf[E], Target: targetOf[T](), I: r.I}, r.where, r.err, ps)))
}

// Set is the setter that links the owner to the row of T whose key is key,
// for Insert's and Update's Set, through an edge whose foreign key is on
// the owner's row (a many-to-one edge, or the side of a one-to-one edge
// that holds the key):
//
//	db.Insert(&post).Set(gen.Posts.Author.Set(id)).Exec(ctx)
//
// The write first checks, in its transaction, that key names a row of T the
// caller may read, under T's Read policy; one it does not fails with an
// *EdgeError of ErrNotFound, and nothing is written. Then it sets the
// foreign key, as that column's handle's Set does: the column counts as
// assigned, once. An update checks the key once, for every row it matches.
// key is converted as Add converts its keys. Set of any other edge, or of a
// nil key, fails the statement; the key column's SetNull clears the edge.
func (r Rel[E, T]) Set[_ Lazy[T]](key any) Setter[E] {
	return r.edgeSetter(engine.EdgeSetKey, []any{key})
}

// Add is the setter that links the owner to the rows of T whose keys are
// keys, for Insert's and Update's Set:
//
//	db.Update[gen.Post]().Set(gen.Posts.Tags.Add(1, 2)).Where(gen.Posts.ID.EQ(id)).Exec(ctx)
//
// A key is a value of the target's key column, converted to its Go type as
// Table.Convert converts; repeated keys count once. r's Where, OrderBy and
// With do not apply. Add applies to a many-to-many edge (EdgeThrough) and to
// an edge whose foreign key the target holds; an edge whose key is on the
// owner's row is set by Set, or that column's handle, and its Add fails the
// statement. InsertQuery.Set says when and how the edge is written.
func (r Rel[E, T]) Add[_ Lazy[T]](keys ...any) Setter[E] {
	return r.edgeSetter(engine.EdgeAdd, keys)
}

// Remove is the setter that unlinks the rows of T whose keys are keys from
// the owner, as Add links them: it applies to the same edges, and a key that
// is not linked is left alone.
func (r Rel[E, T]) Remove[_ Lazy[T]](keys ...any) Setter[E] {
	return r.edgeSetter(engine.EdgeRemove, keys)
}

// Clear is the setter that unlinks every row of T from the owner that the
// caller may read, or may change for an edge whose foreign key the target
// holds. It applies to the edges Add does.
func (r Rel[E, T]) Clear[_ Lazy[T]]() Setter[E] {
	return r.edgeSetter(engine.EdgeClear, nil)
}

// edgeSetter is the setter of op on r of keys, copied.
func (r Rel[E, T]) edgeSetter[_ Lazy[T]](op engine.EdgeOp, keys []any) Setter[E] {
	ref := engine.RelRef{Owner: ownerOf[E], Target: targetOf[T](), I: r.I}
	return Setter[E]{a: engine.EdgeAssignment(ref, op, slices.Clone(keys))}
}

// EdgePage is the most links of a many-to-many edge that a Clear setter
// reads in one statement.
const EdgePage = engine.EdgePage
