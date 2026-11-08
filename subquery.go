package velox

import (
	"errors"

	"github.com/syssam/velox/internal/engine"
)

// Subquery is the values of one column of an entity's rows that meet some
// conditions, made by a column handle's Select. A handle's InSelect and
// NotInSelect test a column against it. V is the column's type, so only a
// column of the same type can be tested: an int64 key against an int64
// foreign key, an enum against the same enum.
//
// Like HasWith, a Subquery reads no metadata, so it may be made anywhere, an
// entity's Policy and Hooks bodies included. Its zero value fails the
// statement that holds a condition made from it.
type Subquery[V any] struct {
	target func() (*EntityInfo, error)
	col    int32
	kids   []engine.Node
	err    error
}

var errZeroSubquery = errors.New("velox: a condition tests the zero Subquery; make one with a column handle's Select")

// selectOf is the Subquery of column col of entity T's rows that meet ps.
func selectOf[T, V any](col int32, ps []Pred[T]) Subquery[V] {
	for _, p := range ps {
		if err := engine.PredErr(engine.PredNode(Predicate(p))); err != nil {
			return Subquery[V]{err: err}
		}
	}
	return Subquery[V]{target: ownerOf[T], col: col, kids: nodes(ps)}
}

// inSelect is the condition that entity E's column col is among s's values.
// It is an EXISTS correlated on the two columns, as HasWith's is on a
// relation's keys, so the subquery's entity gets its Read policy and its
// interceptors' conditions as HasWith's target does.
func inSelect[E, V any](col int32, s Subquery[V]) Pred[E] {
	switch {
	case s.err != nil:
		return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpInvalid, V: s.err}))
	case s.target == nil:
		return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpInvalid, V: errZeroSubquery}))
	}
	ref := engine.ColRef{Owner: ownerOf[E], Target: s.target, OwnerCol: col, TargetCol: s.col}
	return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpExists, Col: -1, V: ref, Kids: s.kids}))
}
