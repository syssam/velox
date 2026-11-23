package engine

import (
	"errors"
	"fmt"
)

// JoinedTerm is of, a term of the target of the to-one relation ref, read
// from the statement's join of that relation: a column, an expression, a
// JSON value or a date bucket of the target, as a term of the owner. It is a
// value of each row, as a column is, so a projection selects it, groups by
// it and aggregates it (AggTerm of it); an aggregate inside it, a relation's
// aggregate, another joined term, a ROLLUP and an upsert's EXCLUDED are
// refused. The statement must join the relation, or it fails when it is
// written. The target's Read policy is in the join's ON clause (scopeJoins),
// so a joined term reads nothing a query on the target would not.
func JoinedTerm(ref RelRef, of Term) Term {
	switch of.kind {
	case termNone:
		return InvalidTerm(errors.New("velox: a joined term of no term"))
	case termInvalid:
		return of
	}
	if err := joinedInner(of); err != nil {
		return InvalidTerm(err)
	}
	return Term{kind: termJoined, col: ref.I, of: &of, r: &relAgg{n: Node{V: ref}}}.seal()
}

// joinedInner refuses what a joined term may not hold, at any depth.
func joinedInner(t Term) error {
	switch t.kind {
	case termAgg:
		return errors.New("velox: a joined term holds an aggregate; aggregate the joined term instead: velox.Joined(r, x).Sum()")
	case termRel:
		return errors.New("velox: a joined term holds a relation's aggregate, which is a subquery of the target's own")
	case termJoined:
		return errors.New("velox: a joined term holds a joined term; join each relation of the query's entity and take its terms")
	case termGrouping:
		return errGroupingPlace
	case termExcluded:
		return errors.New("velox: a joined term holds Excluded, which only an upsert's update may hold")
	case termDate:
		return joinedInner(*t.of)
	case termExpr:
		for _, s := range t.x.slots {
			if s.Term != nil {
				if err := joinedInner(*s.Term); err != nil {
					return err
				}
			}
		}
		if t.x.agg {
			return joinedInner(Term{kind: termAgg})
		}
	}
	return nil
}

// joinedTarget is the target of a joined term's relation.
func joinedTarget(t Term) (*EntityInfo, error) {
	l, err := t.r.n.V.(RelRef).Link()
	if err != nil {
		return nil, err
	}
	if l.Many || l.Join != nil {
		return nil, fmt.Errorf("velox: %s is a to-many relation, and a joined term reads a to-one relation's join", l.Name)
	}
	return l.Target, nil
}

// joinedTerm writes the termJoined t: its term, under the alias of the
// statement's join of its relation, with the target's columns.
func (w *writer) joinedTerm(t Term) {
	i, ok := joinOf(w.joins, t.col)
	if !ok || w.depth != 0 || w.joinAlias != "" {
		w.fail(fmt.Errorf("velox: %s: a joined term (velox.Joined) needs Join or LeftJoin of its relation in the same query, and cannot be inside a subquery or a joined condition", w.in.Name))
		return
	}
	in, alias, having := w.in, w.alias, w.having
	w.in, w.joinAlias, w.alias, w.having = w.joins[i].Link.Target, joinAlias(i+1), joinAlias(i+1), false
	w.term(*t.of)
	w.in, w.joinAlias, w.alias, w.having = in, "", alias, having
}
