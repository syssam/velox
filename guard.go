package velox

import "fmt"

// guard checks a statement as the caller built it, before any hook sees it
// (policy design §1): conditions velox or a hook adds later never count as
// the caller's.
func (db *DB) guard(st *Statement) error {
	for _, p := range st.Where {
		if hasZero(p.n) {
			return errZeroPredicate(st.Entity.Name)
		}
	}
	if st.Op == OpSelect && !st.Count && st.Columns != nil && len(st.Columns) == 0 {
		return fmt.Errorf("velox: %s: the query selects no columns", st.Entity.Name)
	}
	return nil
}

// hasZero reports whether n is, or holds, the zero Predicate, which renders
// as nothing.
func hasZero(n node) bool {
	if n.op == 0 {
		return true
	}
	for _, k := range n.kids {
		if hasZero(k) {
			return true
		}
	}
	return false
}

func errZeroPredicate(entity string) error {
	return fmt.Errorf("velox: %s: a condition is the zero Predicate; make conditions with column handles, And, Or and Not", entity)
}
