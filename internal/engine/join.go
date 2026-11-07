package engine

import (
	"fmt"
	"slices"
	"strconv"
)

// joinAlias is the alias of the n-th join of a SELECT (from 1): the target's
// table under it, so that a relation to the statement's own table is told apart.
func joinAlias(n int) string { return "vxj" + strconv.Itoa(n) }

// joinOf returns the position in joins of the join of relation rel, or false.
func joinOf(joins []JoinClause, rel int32) (int, bool) {
	for i := range joins {
		if joins[i].Ref.I == rel {
			return i, true
		}
	}
	return 0, false
}

// JoinWidth is how many columns st's joins add to each row: the columns of
// every target.
func JoinWidth(st *Statement) int {
	n := 0
	for i := range st.Joins {
		n += len(st.Joins[i].Link.Target.Columns)
	}
	return n
}

// resolveJoins resolves the relation of each of st's joins, on a copy of the
// joins: the query's are shared with its other runs. A relation that holds
// several rows cannot be joined, as every owner would be read once for each.
func resolveJoins(st *Statement) error {
	if len(st.Joins) == 0 {
		return nil
	}
	if st.Op != OpSelect {
		return fmt.Errorf("velox: %s: only a SELECT joins a relation", st.Entity.Name)
	}
	joins := slices.Clone(st.Joins)
	for i := range joins {
		l, err := joins[i].Ref.Link()
		if err != nil {
			return err
		}
		if l.Many || l.Join != nil {
			return fmt.Errorf("velox: %s: %s is a to-many relation, and Join reads a to-one relation; load it with With, or test it with Has or HasWith", st.Entity.Name, l.Name)
		}
		joins[i].Link = l
	}
	st.Joins = joins
	return nil
}

// guardJoins checks the joins of a SELECT as the caller built it. A join adds
// columns to every row, which an aggregate's rows have none to hold, and a
// per-owner read through a join table puts the owner's key after them; a locking
// read would lock the joined rows, and PostgreSQL refuses to lock the nullable
// side of a left join.
func guardJoins(st *Statement) error {
	if len(st.Joins) == 0 {
		return nil
	}
	name := st.Entity.Name
	switch {
	case st.Aggregate != 0 || st.GroupBy != 0:
		return fmt.Errorf("velox: %s: an aggregate reads no joined relation; Count and Exists do", name)
	case st.Each != nil && st.Each.Join != nil:
		return fmt.Errorf("velox: %s: a per-owner read through a join table joins no relation", name)
	case st.Lock != 0 || st.LockWait != 0:
		return fmt.Errorf("velox: %s: a locking read joins no relation; lock the rows in one query and read the relation in another", name)
	}
	return nil
}

// sameJoins reports whether a and b join the same relations in the same way:
// what a Rewrite hook may not change, as the rows are read by it.
func sameJoins(a, b []JoinClause) bool {
	return slices.EqualFunc(a, b, func(x, y JoinClause) bool {
		return x.Ref.I == y.Ref.I && x.Left == y.Left && x.Link.Target == y.Link.Target
	})
}
