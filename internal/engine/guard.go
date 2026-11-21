package engine

import (
	"fmt"
	"slices"
)

// alwaysTrue reports whether n holds for every row, judged by its structure
// alone, as render writes it: an empty And, or NotIn of nothing, is 1 = 1. An
// OpThrough never is: a row without a join row does not meet it.
func alwaysTrue(n Node) bool {
	switch n.Op {
	case OpAllow:
		return true
	case OpAnd:
		for _, k := range n.Kids {
			if !alwaysTrue(k) {
				return false
			}
		}
		return true
	case OpOr:
		return slices.ContainsFunc(n.Kids, alwaysTrue)
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
		return slices.ContainsFunc(n.Kids, alwaysFalse)
	case OpOr:
		for _, k := range n.Kids {
			if !alwaysFalse(k) {
				return false
			}
		}
		return true
	case OpNot:
		return alwaysTrue(n.Kids[0])
	case OpIn:
		return len(n.V.([]any)) == 0
	case OpThrough:
		// EXISTS on join rows that meet the kid, which Of makes the one
		// condition on them.
		return alwaysFalse(n.Kids[0])
	}
	return false
}

// hasZero reports whether n is, or holds, the zero Predicate, which renders
// as nothing.
func hasZero(n Node) bool {
	if n.Op == opZero {
		return true
	}
	return slices.ContainsFunc(n.Kids, hasZero)
}

func errZeroPredicate(entity string) error {
	return fmt.Errorf("velox: %s: a condition is the zero Predicate; make conditions with column handles, And, Or and Not", entity)
}

// guard checks a statement as the caller built it, before any hook sees it:
// conditions velox or a hook adds later never count as the caller's.
func (db *DB) guard(st *Statement) error {
	for _, p := range st.Where {
		if hasZero(PredNode(p)) {
			return errZeroPredicate(st.Entity.Name)
		}
	}
	if err := checkProject(st, "a projection names"); err != nil {
		return err
	}
	if err := checkOrderExprs(st, "an ordering names"); err != nil {
		return err
	}
	if err := checkSetExprs(st, "an assigned expression names"); err != nil {
		return err
	}
	if err := guardJoins(st); err != nil {
		return err
	}
	if st.Distinct && !distinctReads(st) {
		return fmt.Errorf("velox: %s: Distinct applies to a projection (Select, Calc, Rows) or Values; a read of entities, Count, Exists or an aggregate is refused, as its rows are distinct by key (count distinct values with CountDistinct)", st.Entity.Name)
	}
	if st.Op == OpSelect && !st.Count && !st.Exists && st.Aggregate == 0 && st.Columns != nil && len(st.Columns) == 0 {
		return fmt.Errorf("velox: %s: the query selects no columns", st.Entity.Name)
	}
	if st.Op == OpSelect && (st.Lock != 0 || st.LockWait != 0) {
		if err := db.guardLock(st); err != nil {
			return err
		}
	}
	switch st.Op {
	case OpSelect:
		if st.GroupBy != 0 && !st.Unbounded {
			// A key may be unique, so the groups are as many as the rows.
			return fmt.Errorf("velox: %s: %w: a grouped aggregate ignores Limit; add AllowUnbounded to read every group", st.Entity.Name, ErrUnbounded)
		}
		if st.Count || st.Exists || st.Aggregate != 0 {
			break
		}
		if st.Limit <= 0 && !st.Unbounded {
			return fmt.Errorf("velox: %s: %w: add Limit, or AllowUnbounded to read every row", st.Entity.Name, ErrUnbounded)
		}
		if m := db.Cfg.MaxLimit; m > 0 && st.Limit > m {
			return fmt.Errorf("velox: %s: %w: Limit %d, the maximum is %d", st.Entity.Name, ErrLimitTooLarge, st.Limit, m)
		}
	case OpUpdate, OpDelete:
		if st.AllRows {
			break
		}
		if len(st.Where) == 0 {
			return fmt.Errorf("velox: %s: %w: add Where, or All to change every row", st.Entity.Name, ErrNoWhere)
		}
		every := true
		for _, p := range st.Where {
			every = every && alwaysTrue(PredNode(p))
		}
		if every {
			// And(conds...) of an empty conds renders WHERE 1 = 1.
			return fmt.Errorf("velox: %s: %w: its conditions match every row; add a condition, or All to change every row", st.Entity.Name, ErrNoWhere)
		}
	}
	return nil
}

// distinctReads reports whether st may be a SELECT DISTINCT: a projection,
// or a read of one column (Values, IDs).
func distinctReads(st *Statement) bool {
	return st.Op == OpSelect && st.Each == nil && !st.Count && !st.Exists && st.Aggregate == 0 &&
		(st.Project != nil || st.columnRead)
}

// guardLock checks a locking read: it needs a lock to wait on, a database
// with locking reads, a transaction to hold the locks, and rows to lock.
func (db *DB) guardLock(st *Statement) error {
	name := st.Entity.Name
	switch {
	case st.Lock == 0:
		return fmt.Errorf("velox: %s: SkipLocked and NoWait need ForUpdate or ForShare", name)
	case db.D == SQLite:
		return fmt.Errorf("velox: %s: SQLite has no locking reads; its transactions lock the whole database", name)
	case !st.InTx:
		return fmt.Errorf("velox: %s: a locking read (ForUpdate or ForShare) runs only inside Tx", name)
	case st.Count || st.Aggregate != 0:
		return fmt.Errorf("velox: %s: Count, Sum, Avg, Min and Max cannot lock rows; lock them with All, One, First, Iter or Exists", name)
	}
	return nil
}
