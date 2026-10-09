package orm

// This file is for code written once for several entities: a filter
// translated from a request for whichever entity it names, a rule about who
// owns a row that many entities share. The generated code has no generic
// types (a package pays for the methods of every generic type it can reach,
// instantiated or not), so what such code is generic over is declared here:
// the predicate types as a constraint, and a column as an interface of the
// methods the column types have in common.

// Predicate is the predicate type of any entity (predicate.User,
// predicate.Post): the constraint of a function that builds predicates for
// whichever entity it is used with. Under it a predicate converts to P and
// back, so such a function can use everything here:
//
//	func recent[T orm.Predicate](created orm.OrderedColumn[time.Time, T], since time.Time) T {
//		return orm.And(created.GTE(since), T(orm.Raw(notArchived)))
//	}
//
//	db.Post.Query(conn).Where(recent(&post.CreatedAt, since))
//
// A column is passed by its address: the methods of a column type have
// pointer receivers.
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

// Column is a column of values of type T, of an entity whose predicate type
// is P, as code generic over entities sees it: &user.Role is a
// Column[user.RoleEnum, predicate.User]. Every column type has these.
type Column[T, P any] interface {
	EQ(T) P
	NEQ(T) P
	In(...T) P
	NotIn(...T) P
}

// OrderedColumn is a column whose values compare: numbers, times, strings.
type OrderedColumn[T, P any] interface {
	Column[T, P]
	GT(T) P
	GTE(T) P
	LT(T) P
	LTE(T) P
}

// TextColumn is a string column.
type TextColumn[P any] interface {
	OrderedColumn[string, P]
	Contains(string) P
	HasPrefix(string) P
	HasSuffix(string) P
	EqualFold(string) P
	ContainsFold(string) P
}

// NullableColumn is what a column that may be NULL has besides.
type NullableColumn[P any] interface {
	IsNil() P
	NotNil() P
}
