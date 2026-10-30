package engine

import (
	"fmt"
)

// AlwaysTrue reports whether n holds for every row, judged by its structure
// alone, as render writes it: an empty And, or NotIn of nothing, is 1 = 1. An
// OpThrough never is: a row without a join row does not meet it.
func AlwaysTrue(n Node) bool {
	switch n.Op {
	case OpAllow:
		return true
	case OpAnd:
		for _, k := range n.Kids {
			if !AlwaysTrue(k) {
				return false
			}
		}
		return true
	case OpOr:
		for _, k := range n.Kids {
			if AlwaysTrue(k) {
				return true
			}
		}
		return false
	case OpNot:
		return alwaysFalse(n.Kids[0])
	case OpNotIn:
		return len(n.V.([]any)) == 0
	}
	return false
}

// alwaysFalse reports whether n holds for no row, judged by its structure
// alone: an empty Or, or In of nothing, is 1 = 0.
func alwaysFalse(n Node) bool {
	switch n.Op {
	case OpAnd:
		for _, k := range n.Kids {
			if alwaysFalse(k) {
				return true
			}
		}
		return false
	case OpOr:
		for _, k := range n.Kids {
			if !alwaysFalse(k) {
				return false
			}
		}
		return true
	case OpNot:
		return AlwaysTrue(n.Kids[0])
	case OpIn:
		return len(n.V.([]any)) == 0
	case OpThrough:
		// EXISTS on join rows that meet the kid, which Of makes the one
		// condition on them.
		return alwaysFalse(n.Kids[0])
	}
	return false
}

// HasZero reports whether n is, or holds, the zero Predicate, which renders
// as nothing.
func HasZero(n Node) bool {
	if n.Op == OpZero {
		return true
	}
	for _, k := range n.Kids {
		if HasZero(k) {
			return true
		}
	}
	return false
}

func ErrZeroPredicate(entity string) error {
	return fmt.Errorf("velox: %s: a condition is the zero Predicate; make conditions with column handles, And, Or and Not", entity)
}
