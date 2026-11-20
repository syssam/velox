package engine

import (
	"fmt"
	"reflect"
)

// The untyped entry points take column indexes, conditions and assignments
// that no type checks, so they check them before anything runs: an index
// out of range, a condition made by another entity's Table, or an
// assignment whose value is not of its column's Go type is an error, never
// a panic and never a statement on another column. A typed handle's
// condition carries no entity, so one converted to a Predicate is checked
// for its range only: a tag on it would cost every entity's package code.

// checkStatement checks st, a statement of t's entity built by an untyped
// entry point.
func (t *table) checkStatement(st *Statement) error {
	if err := checkColumns(st, "a statement names"); err != nil {
		return err
	}
	conds := func(ps []Predicate) error {
		for _, p := range ps {
			if err := checkTags(p.n, t.info); err != nil {
				return err
			}
		}
		return nil
	}
	if err := conds(st.Where); err != nil {
		return err
	}
	if st.Conflict != nil {
		if err := conds(st.Conflict.Where); err != nil {
			return err
		}
	}
	for _, a := range st.Set {
		if err := t.checkAssign(a); err != nil {
			return err
		}
	}
	return nil
}

// checkTags refuses a condition in n on another entity than in, the entity
// the statement or the EXISTS that holds it reads, and a column out of
// range in an EXISTS's conditions.
func checkTags(n Node, in *EntityInfo) error {
	switch n.Op {
	case OpAnd, OpOr, OpNot:
		for _, k := range n.Kids {
			if err := checkTags(k, in); err != nil {
				return err
			}
		}
		return nil
	case OpInvalid:
		return n.V.(error)
	case OpAllow, opZero:
		return nil
	case OpExists:
		if r, ok := n.V.(linkRef); ok {
			if owner, err := r.owner(); err == nil && owner != in {
				what := "a relation"
				if _, col := r.(ColRef); col {
					what = "a column"
				}
				return fmt.Errorf("velox: %s: a condition on %s of %s", in.Name, what, owner.Name)
			}
		}
		l, err := existsLink(n)
		if err != nil {
			return err
		}
		for _, k := range n.Kids {
			if err := checkTags(k, l.Target); err != nil {
				return err
			}
		}
		return nil
	case OpThrough:
		j := n.V.(*EntityInfo)
		for _, k := range n.Kids {
			if err := checkTags(k, j); err != nil {
				return err
			}
		}
		return checkCol(in, int(n.Col))
	case OpExpr:
		return checkExprCols(n.V.(*exprData), func(c int) error { return checkCol(in, c) })
	case OpJoined:
		return nil // the joined target's: no untyped entry point makes one
	case opNamed, opNamedEdge, opNamedSub:
		return nil // resolved against the statement's entity when it runs
	}
	if tag, ok := n.W.(*EntityInfo); ok && n.Op != OpBetween && tag != in {
		return fmt.Errorf("velox: %s: a condition on a column of %s", in.Name, tag.Name)
	}
	return checkCol(in, int(n.Col))
}

// checkCol refuses c, a column index, unless in has it.
func checkCol(in *EntityInfo, c int) error {
	if c < 0 || c >= len(in.Columns) {
		return fmt.Errorf("velox: %s: a statement names column %d, the entity has %d columns", in.Name, c, len(in.Columns))
	}
	return nil
}

// checkAssign refuses an assignment whose value is not of its column's Go
// type: the type the column's handle takes, or a pointer to it. A nil value
// is NULL, which the column's nullability decides. An assignment of another
// entity's column of the same Go type is not told from one of this
// entity's: an Assignment carries no entity. An expression's value is
// checked with the statement (checkSetExprs).
func (t *table) checkAssign(a Assignment) error {
	want, _ := t.vals[a.Column].(reflect.Type)
	if a.Value == nil || want == nil || a.Op == AssignExpr {
		return nil
	}
	got := reflect.TypeOf(a.Value)
	if got == want || a.Op == AssignSet && got == reflect.PointerTo(want) {
		return nil
	}
	return fmt.Errorf("velox: %s: an assignment to %s takes a %v, not a %T", t.info.Name, t.info.Columns[a.Column].Field, want, a.Value)
}

// checkRows refuses rows of another entity than t's.
func (t *table) checkRows(r TableRows) error {
	if r.n == 0 {
		return nil
	}
	if r.info != t.info || r.size != t.size {
		name := "no entity"
		if r.info != nil {
			name = r.info.Name
		}
		return fmt.Errorf("velox: %s: rows of %s", t.info.Name, name)
	}
	return nil
}
