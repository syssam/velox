package velox

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/syssam/velox/internal/engine"
)

// Term is one SQL value over entity E: a column, an expression, an
// aggregate of either, or an aggregate of a relation's rows (Rel's Count). A column handle's Sum, Avg, Min, Max, Count and
// CountDistinct make one, as do Calc and CountAll, and the same methods on a
// Term take an aggregate of an expression. A Term is a result column of
// Query.Select, a key of GroupBy, an operand of Expr (a HAVING is an Expr of
// aggregate terms), and, through Asc and Desc, an ordering.
//
// Every use goes through the query's statement, so the Read policy, scope
// and interceptors of E apply, and a term can name only E's columns.
type Term[E any] struct{ t engine.Term }

// Selectable is what Select and GroupBy take: a column handle of E, read as
// the column, or a Term of E. It is the empty interface, named for what it
// holds: a method on every handle type would cost each entity's package
// about 10 KB per handle type (TestGenericTypesCompileSmall), so a string, a
// handle of another entity or any other value is refused when the statement
// runs, before anything is sent, as an Expr's arguments are.
type Selectable[E any] any

// termOf is s as a term of the statement's entity.
func termOf[E any](s Selectable[E]) engine.Term {
	switch h := s.(type) {
	case Term[E]:
		return h.t
	case Column[E]:
		return engine.ColumnTerm(h.column(nil))
	case GroupingSet[E]:
		return engine.InvalidTerm(errors.New("velox: a Rollup or Cube is a key of GroupBy, and nothing else: select or order by its keys"))
	}
	if foreignHandle(s) {
		return engine.InvalidTerm(fmt.Errorf("velox: %T is a column or term of another entity than the query's", s))
	}
	return engine.InvalidTerm(fmt.Errorf("velox: %T is not a column handle or a Term of the query's entity", s))
}

// Calc is the SQL expression text as a Term, written as Expr's text is: a ?
// is filled by the next argument, a column handle of E or a Term of E written
// as it is, any other value bound, and the same text is refused. See Expr
// for what the text may hold.
//
//	velox.Calc[veloxgen.Invoice]("EXTRACT(MONTH FROM ?)", I.CreatedAt)
//
// It may be a window function, such as COUNT(*) OVER () or ROW_NUMBER()
// OVER (PARTITION BY ? ORDER BY ?), as a result column or an ordering.
// Beside GroupBy and Limit, COUNT(*) OVER () counts every group, not only
// the page's. A window function in an Expr (a Where or a Having), in
// GroupBy, under an aggregate or in SetExpr fails the statement, as SQL
// computes windows after them.
func Calc[E any](text string, args ...any) Term[E] {
	slots, vals, err := slotsOf[E](text, args)
	if err != nil {
		return Term[E]{engine.InvalidTerm(err)}
	}
	return Term[E]{engine.ExprTerm(text, slots, vals)}
}

// CountAll is COUNT(*) as a Term.
func CountAll[E any]() Term[E] { return Term[E]{engine.AggTerm(engine.TermCountAll, engine.Term{})} }

// Sum is SUM of t. NULL when no row has a value.
func (t Term[E]) Sum[_ Lazy[E]]() Term[E] { return Term[E]{engine.AggTerm(engine.TermSum, t.t)} }

// Avg is AVG of t. NULL when no row has a value.
func (t Term[E]) Avg[_ Lazy[E]]() Term[E] { return Term[E]{engine.AggTerm(engine.TermAvg, t.t)} }

// Min is MIN of t.
func (t Term[E]) Min[_ Lazy[E]]() Term[E] { return Term[E]{engine.AggTerm(engine.TermMin, t.t)} }

// Max is MAX of t.
func (t Term[E]) Max[_ Lazy[E]]() Term[E] { return Term[E]{engine.AggTerm(engine.TermMax, t.t)} }

// Count is COUNT of t: the rows where it is not NULL.
func (t Term[E]) Count[_ Lazy[E]]() Term[E] { return Term[E]{engine.AggTerm(engine.TermCount, t.t)} }

// CountDistinct is COUNT(DISTINCT t).
func (t Term[E]) CountDistinct[_ Lazy[E]]() Term[E] {
	return Term[E]{engine.AggTerm(engine.TermCountDistinct, t.t)}
}

// Decimal marks t, an expression such as Calc("? * ?", X.Qty, X.Price), as
// holding decimals. On SQLite, whose decimals are REALs, its Sum and Avg are
// then rounded to 15 significant digits, as a decimal column's are: 0.1*3 +
// 0.2*3 sums to 0.9, not 0.9000000000000001. velox does not infer it from
// the operands. PostgreSQL and MySQL compute decimals exactly, so there it
// changes nothing. Decimal of a term that is not an expression, such as an
// aggregate, fails the statement: a column is decimal by its schema.
func (t Term[E]) Decimal[_ Lazy[E]]() Term[E] { return Term[E]{engine.DecimalTerm(t.t)} }

// Agg marks t, an expression such as Calc("PERCENTILE_CONT(0.5) WITHIN
// GROUP (ORDER BY ?)", X.Price), as an aggregate: it computes one value of
// each group. It is then placed as an aggregate Term is: beside GroupBy
// without being a key, in Having, and on MySQL in an ordering as written,
// not as ANY_VALUE of it. It cannot be grouped by, aggregated or assigned.
// velox does not read the text to find an aggregate, so a Calc that calls
// one, SUM(?) too, needs Agg, and one that does not, such as SQLite's
// MAX(?, ?), does not. Agg of a term that is not an expression, or of a
// window function, fails the statement.
func Agg[E any](t Term[E]) Term[E] { return Term[E]{engine.AggMarkTerm(t.t)} }

// As names t as a result column of Select: SELECT t AS name. Scan reads
// the column into the field of that name. A name is a letter or an
// underscore, then letters, digits and underscores, at most 63 of them; any
// other fails the statement. The name is written only in Select's result
// columns: in GroupBy, Having, an Expr or an ordering, t is written as it
// is.
func (t Term[E]) As[_ Lazy[E]](name string) Term[E] { return Term[E]{engine.NamedTerm(t.t, name)} }

// Asc orders by t, ascending, NULL last as every ordering puts it. An entity
// query ordered by an expression cannot be read After a row or by a cursor,
// except by a relation's aggregate, Rel's Count, Sum, Avg, Min or Max. A
// term that is a column (ColumnByName's) orders as the column's handle
// does, and After and a cursor follow it.
func (t Term[E]) Asc[_ Lazy[E]]() Order[E] { return t.order(false) }

// Desc orders by t, descending, NULL first as every descending ordering
// puts it.
func (t Term[E]) Desc[_ Lazy[E]]() Order[E] { return t.order(true) }

// order is Asc's and Desc's work.
func (t Term[E]) order[_ Lazy[E]](desc bool) Order[E] {
	if c, ok := engine.TermColumn(t.t); ok {
		return Order[E]{OrderTerm{Column: int(c), Desc: desc}}
	}
	return Order[E]{OrderTerm{Expr: t.t, Desc: desc}}
}

// slotsOf classifies the arguments of an Expr or a Calc of E: a column
// handle of E fills its ? as the column, a Term of E as the term, and any
// other value is bound. A handle or a Term of another entity is an error.
func slotsOf[E any](text string, args []any) ([]engine.ExprSlot, []any, error) {
	slots := make([]engine.ExprSlot, len(args))
	vals := make([]any, 0, len(args))
	for i, a := range args {
		switch h := a.(type) {
		case Column[E]:
			slots[i] = engine.ExprSlot{Col: h.column(nil)}
		case Term[E]:
			t := h.t
			slots[i] = engine.ExprSlot{Col: -1, Term: &t}
		case GroupingSet[E]:
			return nil, nil, fmt.Errorf("velox: Expr %q: argument %d is a Rollup or Cube, which is a key of GroupBy, and nothing else", text, i+1)
		default:
			if foreignHandle(a) {
				return nil, nil, fmt.Errorf("velox: Expr %q: argument %d is %T, a column or term of another entity than the query's", text, i+1, a)
			}
			slots[i] = engine.ExprSlot{Col: -1}
			vals = append(vals, a)
		}
	}
	return slots, vals, nil
}

// foreignHandle reports a column handle or a Term of some entity, one that
// Expr's E did not match: a struct of this package that holds a column
// index I, or is a Term.
func foreignHandle(a any) bool {
	t := reflect.TypeOf(a)
	if t == nil || t.Kind() != reflect.Struct || t.PkgPath() != reflect.TypeFor[DB]().PkgPath() {
		return false
	}
	if len(t.Name()) > 5 && t.Name()[:5] == "Term[" {
		return true
	}
	f, ok := t.FieldByName("I")
	return ok && f.Type.Kind() == reflect.Int32
}
