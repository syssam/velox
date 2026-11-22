package engine

import (
	"errors"
	"fmt"
)

// ExprAssignment is the assignment of the expression t to column col: an
// UPDATE's col = t. A t that is not an expression fails the statement.
func ExprAssignment(col int, t Term) Assignment {
	return Assignment{Column: col, Op: AssignExpr, Value: t}
}

// exprAssigned is the term an AssignExpr holds, or the zero Term.
func exprAssigned(a Assignment) Term {
	t, _ := a.Value.(Term)
	return t
}

// checkSetExprs checks the expressions st assigns. Each is a Term that names
// only columns of the entity and holds no aggregate, and its column is not
// encoded. who says who made the statement, as in checkColumns.
func checkSetExprs(st *Statement, who string) error {
	for _, a := range st.Set {
		if a.Op != AssignExpr {
			continue
		}
		if err := checkSetExpr(st, who, a); err != nil {
			return err
		}
	}
	return nil
}

// checkSetExpr checks one AssignExpr of st, as checkSetExprs.
func checkSetExpr(st *Statement, who string, a Assignment) error {
	in := st.Entity
	if err := badColumn(st, who, a.Column); err != nil {
		return err
	}
	if st.Op != OpUpdate {
		return fmt.Errorf("velox: %s: an expression assigns a column in an UPDATE; an insert's values are Go values", in.Name)
	}
	t, ok := a.Value.(Term)
	if !ok {
		return fmt.Errorf("velox: %s: %s an expression assignment to %s that holds a %T, not a term", in.Name, who, in.Columns[a.Column].Field, a.Value)
	}
	if err := checkTermCols(t, func(c int) error { return badColumn(st, who, c) }); err != nil {
		return err
	}
	if t.kind != termExpr {
		return fmt.Errorf("velox: %s: an assignment to %s is not an expression", in.Name, in.Columns[a.Column].Field)
	}
	if hasAgg(t) {
		return fmt.Errorf("velox: %s: the expression assigned to %s holds an aggregate, which an UPDATE cannot compute", in.Name, in.Columns[a.Column].Field)
	}
	if hasWindow(t) {
		return fmt.Errorf("velox: %s: the expression assigned to %s holds a window function, which an UPDATE cannot compute", in.Name, in.Columns[a.Column].Field)
	}
	if hasRel(t) {
		return fmt.Errorf("velox: %s: the expression assigned to %s holds a relation's aggregate; read it with a query, then set the value", in.Name, in.Columns[a.Column].Field)
	}
	if in.Columns[a.Column].Codec {
		return errCodecSetExpr(in, a.Column)
	}
	return nil
}

// hasAgg reports whether t is or holds an aggregate.
func hasAgg(t Term) bool {
	switch t.kind {
	case termAgg:
		return true
	case termExpr:
		for _, s := range t.x.slots {
			if s.Term != nil && hasAgg(*s.Term) {
				return true
			}
		}
	}
	return false
}

// hasWindow reports whether t is or holds a window function: an expression
// whose text holds OVER.
func hasWindow(t Term) bool {
	switch t.kind {
	case termExpr:
		return exprHasWindow(t.x)
	case termAgg:
		return t.of != nil && hasWindow(*t.of)
	}
	return false
}

// errCodecSetExpr is the error of an expression assigned to the encoded
// column col of in: velox encodes the column's values, and an expression's
// result is not its encoding.
func errCodecSetExpr(in *EntityInfo, col int) error {
	return fmt.Errorf("velox: %s: column %s is an encoded column (JSON in velox-gen), which an expression cannot assign; set it to a Go value", in.Name, in.Columns[col].Name)
}

// errSetExprInsert is the refusal of an expression among an insert's setters.
var errSetExprInsert = errors.New("an expression assigns a column of a row that exists; set an insert's columns to Go values")

// setExpr writes the expression a assigns, in parentheses. A Rewrite hook's
// encoded column or term that is no expression fails the statement.
func (w *writer) setExpr(a Assignment) {
	t := exprAssigned(a)
	switch {
	case w.in.Columns[a.Column].Codec:
		w.fail(errCodecSetExpr(w.in, a.Column))
	case t.kind != termExpr:
		w.fail(fmt.Errorf("velox: %s: an assignment to %s is not an expression", w.in.Name, w.in.Columns[a.Column].Field))
	}
	w.term(t)
}
