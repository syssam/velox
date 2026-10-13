package orm

// This file is for code written once for several entities: a filter
// translated from a request for whichever entity it names, a rule about who
// owns a row that many entities share. Such code is generic over the
// predicate type of an entity, under the constraint Predicate, and takes a
// column as one of the column types (columns.go).

// Predicate is the predicate type of any entity (predicate.User,
// predicate.Post): the constraint of a function that builds predicates for
// whichever entity it is used with. Under it a predicate converts to P and
// back, so such a function can use everything here:
//
//	func recent[E orm.Predicate](created *orm.OrderedColumn[time.Time, E], since time.Time) E {
//		return orm.And(created.GTE(since), E(orm.Raw(notArchived)))
//	}
//
//	velox.Post.Query(conn).Where(recent(post.UpdatedAt.AsOrdered(), since))
//
// A column is given as the plain type of the level the function needs
// (AsOrdered, AsString, ...): the type a column is declared with also says
// whether it can be cleared and whether it has an update default.
type Predicate interface {
	~struct {
		Op   Op
		Col  string
		Val  any
		Subs []P
		Edge *EdgeMeta
		raw  any
	}
}

// The constraint spells out P's fields; this stops compiling when they
// differ.
var _ = And[P]

func group[T Predicate](op Op, ps []T) T {
	subs := make([]P, len(ps))
	for i, p := range ps {
		subs[i] = P(p)
	}
	return T(P{Op: op, Subs: subs})
}

// And matches the rows every one of ps matches: an entity package's And,
// for any entity.
func And[T Predicate](ps ...T) T { return group(OpAnd, ps) }

// Or matches the rows at least one of ps matches; without any, none.
func Or[T Predicate](ps ...T) T { return group(OpOr, ps) }

// Not matches the rows that do not match all of ps; without any, none.
func Not[T Predicate](ps ...T) T { return group(OpNot, ps) }

// IsZero reports whether p is the zero predicate: no condition, what a
// function returns when it has nothing to add.
//
//	if p := olderThan(age); !orm.IsZero(p) { q.Where(p) }
func IsZero[T Predicate](p T) bool { return P(p).IsZero() }
