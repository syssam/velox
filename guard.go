package velox

import (
	"fmt"

	"github.com/syssam/velox2/internal/engine"
)

// guard checks a statement as the caller built it, before any hook sees it:
// conditions velox or a hook adds later never count as the caller's.
func (db *DB) guard(st *Statement) error {
	for _, p := range st.Where {
		if engine.HasZero(engine.PredNode(p)) {
			return engine.ErrZeroPredicate(st.Entity.Name)
		}
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
		if st.Count || st.Exists || st.Aggregate != 0 {
			break
		}
		if st.Limit <= 0 && !st.Unbounded {
			return fmt.Errorf("velox: %s: %w: add Limit, or Unbounded to read every row", st.Entity.Name, ErrUnbounded)
		}
		if m := db.cfg.MaxLimit; m > 0 && st.Limit > m {
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
			every = every && engine.AlwaysTrue(engine.PredNode(p))
		}
		if every {
			// And(conds...) of an empty conds renders WHERE 1 = 1.
			return fmt.Errorf("velox: %s: %w: its conditions match every row; add a condition, or All to change every row", st.Entity.Name, ErrNoWhere)
		}
	}
	return nil
}

// guardLock checks a locking read: it needs a lock to wait on, a database
// with locking reads, a transaction to hold the locks, and rows to lock.
func (db *DB) guardLock(st *Statement) error {
	name := st.Entity.Name
	switch {
	case st.Lock == 0:
		return fmt.Errorf("velox: %s: SkipLocked and NoWait need ForUpdate or ForShare", name)
	case db.d == SQLite:
		return fmt.Errorf("velox: %s: SQLite has no locking reads; its transactions lock the whole database", name)
	case !st.InTx:
		return fmt.Errorf("velox: %s: a locking read (ForUpdate or ForShare) runs only inside Tx", name)
	case st.Count || st.Aggregate != 0:
		return fmt.Errorf("velox: %s: Count, Sum, Avg, Min and Max cannot lock rows; lock them with All, One, First, Iter or Exists", name)
	}
	return nil
}
