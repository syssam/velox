package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"slices"
)

// Projection is what a statement reads in place of its entity's columns: a
// list of terms, one result column each, grouped and filtered by terms and
// conditions of the same entity. The rows are read by position (Query.Rows).
// The entity's Read policy, scope and interceptors still filter them, as
// they filter every statement.
type Projection struct {
	Terms   []Term      // the result columns, in order
	GroupBy []Term      // GROUP BY, when not empty
	Having  []Predicate // HAVING: all must hold
}

type termKind uint8

const (
	termNone termKind = iota
	termColumn
	termExpr
	termAgg
	termInvalid
)

// TermFn is an aggregate function of a term.
type TermFn uint8

// The aggregate functions of a term. TermCountAll is COUNT(*), of no term.
const (
	TermSum TermFn = iota + 1
	TermAvg
	TermMin
	TermMax
	TermCount
	TermCountDistinct
	TermCountAll
)

var termFnSQL = [...]string{TermSum: "SUM", TermAvg: "AVG", TermMin: "MIN", TermMax: "MAX", TermCount: "COUNT", TermCountDistinct: "COUNT", TermCountAll: "COUNT"}

// Term is one SQL value of a statement's entity: a column, an expression
// (ExprTerm) or an aggregate of either (AggTerm). It is a result column of a
// Projection, a GROUP BY key, an operand of an Expr, or an ordering. The
// zero Term is no term.
type Term struct {
	kind  termKind
	col   int32
	x     *exprData
	fn    TermFn
	of    *Term
	err   error
	shape string // what shapes it in the render cache's key: see seal
}

// seal sets t's shape from its kind, function, column and the shapes of what
// it holds, which are sealed already. The key builder appends it as it is, so
// that it does not recurse through terms and expressions: a cycle of
// functions that pass the key along makes it escape to the heap, and an
// allocation on every statement.
func (t Term) seal() Term {
	b := append(make([]byte, 0, 8+len(t.shape)), byte(t.kind), byte(t.fn))
	b = binary.AppendUvarint(b, uint64(t.col+1))
	switch t.kind {
	case termExpr:
		b = append(b, t.x.shape...)
	case termAgg:
		if t.of != nil {
			b = append(b, 1)
			b = append(b, t.of.shape...)
		} else {
			b = append(b, 0)
		}
	}
	t.shape = string(b)
	return t
}

// ColumnTerm is column col of the statement's entity.
func ColumnTerm(col int32) Term { return Term{kind: termColumn, col: col}.seal() }

// InvalidTerm is a term that fails the statement holding it with err.
func InvalidTerm(err error) Term { return Term{kind: termInvalid, err: err}.seal() }

// ExprTerm is the expression text, in which each ? is filled by the next of
// slots, as ExprNode's. A bad text is an InvalidTerm.
func ExprTerm(text string, slots []ExprSlot, args []any) Term {
	d, err := newExpr(text, slots, args)
	if err != nil {
		return InvalidTerm(err)
	}
	return Term{kind: termExpr, x: d}.seal()
}

// AggTerm is fn applied to of; for TermCountAll, of is ignored. An aggregate
// of an aggregate, or of no term, is an InvalidTerm.
func AggTerm(fn TermFn, of Term) Term {
	if fn == TermCountAll {
		return Term{kind: termAgg, fn: fn}.seal()
	}
	switch of.kind {
	case termNone:
		return InvalidTerm(errors.New("velox: an aggregate of no term"))
	case termInvalid:
		return of
	case termAgg:
		return InvalidTerm(errors.New("velox: an aggregate of an aggregate: SUM(MAX(x)) is not allowed in one statement"))
	}
	return Term{kind: termAgg, fn: fn, of: &of}.seal()
}

// problem is the error t carries: an invalid expression, an aggregate of an
// aggregate, or the zero Term used as one, or nil. It is not exported: an
// exported method of Term is in the type information of every package that
// mentions a Statement.
func (t Term) problem() error {
	switch t.kind {
	case termNone:
		return errors.New("velox: the zero Term; make terms with Pick, Calc and CountAll")
	case termInvalid:
		return t.err
	case termAgg:
		if t.of != nil {
			return t.of.problem()
		}
	case termExpr:
		for _, s := range t.x.slots {
			if s.Term != nil {
				if err := s.Term.problem(); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// term writes t: a column as a condition writes it, an expression in
// parentheses, an aggregate as FN(x).
func (w *writer) term(t Term) {
	switch t.kind {
	case termColumn:
		if w.in.Columns[t.col].Codec {
			w.fail(errCodecExpr(w.in, int(t.col)))
		}
		w.predCol(t.col)
	case termExpr:
		w.exprBody(t.x)
	case termAgg:
		w.str(termFnSQL[t.fn])
		w.str("(")
		switch t.fn {
		case TermCountAll:
			w.str("*")
		default:
			if t.fn == TermCountDistinct {
				w.str("DISTINCT ")
			}
			w.term(*t.of)
		}
		w.str(")")
	default:
		w.fail(t.problem())
	}
}

// terms writes ts, separated by commas.
func (w *writer) terms(ts []Term) {
	for i, t := range ts {
		if i > 0 {
			w.str(", ")
		}
		w.term(t)
	}
}

// projectStmt writes a SELECT of st's Projection.
func (w *writer) projectStmt(st *Statement) {
	p := st.Project
	w.str("SELECT ")
	w.terms(p.Terms)
	w.str(" FROM ")
	w.table()
	w.where(st.Where)
	if len(p.GroupBy) > 0 {
		w.str(" GROUP BY ")
		w.terms(p.GroupBy)
	}
	for i, h := range p.Having {
		if i == 0 {
			w.str(" HAVING ")
		} else {
			w.str(" AND ")
		}
		w.pred(h.n)
	}
	w.orderBy(st.OrderBy, false)
	w.qualify = false
	w.selectTail(st)
}

// orderTerm writes the ordering o by an expression term. NULL sorts after
// every value, ascending, as orderBy puts it: PostgreSQL sorts so, and where
// the database sorts it first, a term of IS NULL goes before. A term may be
// NULL whatever its columns are, so there it always does.
func (w *writer) orderTerm(o OrderTerm) {
	t, _ := o.Expr.(Term)
	for k := range 2 {
		if k == 0 && w.d == Postgres {
			continue
		}
		w.term(t)
		if k == 0 {
			w.str(" IS NULL")
		}
		if o.Desc {
			w.str(" DESC")
		}
		if k == 0 {
			w.str(", ")
		}
	}
}

// termKey appends what shapes t to the render cache's key: its kind, its
// function, its column and its text pieces, sealed with it. Values are
// arguments.
func termKey(k []byte, t Term) []byte { return append(k, t.shape...) }

// projectionKey appends a Projection's shape to the render cache's key; the
// key's flags say whether a statement has one.
func projectionKey(k []byte, p *Projection) []byte {
	k = binary.AppendUvarint(k, uint64(len(p.Terms)))
	for _, t := range p.Terms {
		k = termKey(k, t)
	}
	k = binary.AppendUvarint(k, uint64(len(p.GroupBy)))
	for _, t := range p.GroupBy {
		k = termKey(k, t)
	}
	k = binary.AppendUvarint(k, uint64(len(p.Having)))
	for _, h := range p.Having {
		k = nodeKey(k, h.n)
	}
	return k
}

// checkTermCols calls bad for each column t names, and returns the first
// error: bad's, or t's own.
func checkTermCols(t Term, bad func(int) error) error {
	if err := t.problem(); err != nil {
		return err
	}
	switch t.kind {
	case termColumn:
		return bad(int(t.col))
	case termExpr:
		return checkExprCols(t.x, bad)
	case termAgg:
		if t.of != nil {
			return checkTermCols(*t.of, bad)
		}
	}
	return nil
}

// swapTerm is swapCols of a term: columns 0 and 1 trade places.
func swapTerm(t Term) Term {
	switch t.kind {
	case termColumn:
		t.col = swapCol(t.col)
	case termExpr:
		t.x = swapExprData(t.x)
	case termAgg:
		if t.of != nil {
			o := swapTerm(*t.of)
			t.of = &o
		}
	}
	return t.seal()
}

func swapCol(c int32) int32 {
	switch c {
	case 0:
		return 1
	case 1:
		return 0
	}
	return c
}

// checkProject checks st's Projection: it is a plain SELECT, its terms and
// conditions name only columns of the entity, and none is invalid. who says
// who made the statement, as in checkColumns.
func checkProject(st *Statement, who string) error {
	p := st.Project
	if p == nil {
		return nil
	}
	name := st.Entity.Name
	switch {
	case st.Op != OpSelect:
		return fmt.Errorf("velox: %s: a projection is a SELECT", name)
	case st.Count || st.Exists || st.Aggregate != 0 || st.GroupBy != 0:
		return fmt.Errorf("velox: %s: a projection is not Count, Exists or an aggregate; read its terms with Rows", name)
	case st.Each != nil || len(st.Joins) > 0:
		return fmt.Errorf("velox: %s: a projection cannot be read with Join, With or LimitEach", name)
	case st.Columns != nil:
		return fmt.Errorf("velox: %s: a projection cannot be read with Columns or Omit; its terms are its columns", name)
	case st.Lock != 0 || st.LockWait != 0:
		return fmt.Errorf("velox: %s: a projection cannot take a lock; GROUP BY and a lock do not mix", name)
	case len(p.Terms) == 0:
		return fmt.Errorf("velox: %s: a projection has no terms", name)
	}
	bad := func(c int) error { return badColumn(st, who, c) }
	for _, ts := range [][]Term{p.Terms, p.GroupBy} {
		for _, t := range ts {
			if err := checkTermCols(t, bad); err != nil {
				return err
			}
		}
	}
	return checkConds(st, who, p.Having)
}

// checkOrderExprs checks the ordering terms of st that are expressions.
func checkOrderExprs(st *Statement, who string) error {
	for _, o := range st.OrderBy {
		if o.Expr == nil {
			continue
		}
		t, ok := o.Expr.(Term)
		if !ok {
			return fmt.Errorf("velox: %s: %s an ordering by an expression that is not a term", st.Entity.Name, who)
		}
		if err := checkTermCols(t, func(c int) error { return badColumn(st, who, c) }); err != nil {
			return err
		}
	}
	return nil
}

// cloneProject is a copy of p whose slices may be edited.
func cloneProject(p *Projection) *Projection {
	if p == nil {
		return nil
	}
	c := *p
	c.Terms, c.GroupBy, c.Having = slices.Clone(p.Terms), slices.Clone(p.GroupBy), slices.Clone(p.Having)
	return &c
}

// sameProject reports whether a and b are the same projection.
func sameProject(a, b *Projection) bool {
	if a == nil || b == nil {
		return a == b
	}
	return reflect.DeepEqual(a, b)
}

// errAfterExpr is the refusal of After, and of a cursor, on an ordering by an
// expression: they read the value of each ordering term from a row.
func errAfterExpr(entity string) error {
	return fmt.Errorf("velox: %s: After and a cursor cannot follow an ordering by an expression, which is not a column of a row; order by columns, or page with Limit and Offset", entity)
}
