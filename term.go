package velox

import (
	"fmt"
	"reflect"

	"github.com/syssam/velox/internal/engine"
)

// Term is one SQL value over entity E: a column, an expression, or an
// aggregate of either. Made by Pick, Calc and CountAll, and by the Sum, Avg,
// Min, Max, Count and CountDistinct methods, a Term is a result column of
// Query.Project, a key of GroupBy, an operand of Expr (a HAVING is an Expr
// of aggregate terms), and, through Asc and Desc, an ordering.
//
// Every use goes through the query's statement, so the Read policy, scope
// and interceptors of E apply, and a term can name only E's columns.
type Term[E any] struct{ t engine.Term }

// Pick is the column c of E as a Term.
func Pick[E any](c Column[E]) Term[E] { return Term[E]{engine.ColumnTerm(c.column(nil))} }

// Calc is the SQL expression text as a Term, written as Expr's text is: a ?
// is filled by the next argument, a column handle of E or a Term of E written
// as it is, any other value bound, and the same text is refused. See Expr
// for what the text may hold.
//
//	velox.Calc[veloxgen.Invoice]("EXTRACT(MONTH FROM ?)", I.CreatedAt)
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

// Asc orders by t, ascending, NULL last as every ordering puts it. An entity
// query ordered by an expression cannot be read After a row or by a cursor.
func (t Term[E]) Asc[_ Lazy[E]]() Order[E] { return Order[E]{OrderTerm{Expr: t.t}} }

// Desc orders by t, descending, NULL first as every descending ordering
// puts it.
func (t Term[E]) Desc[_ Lazy[E]]() Order[E] { return Order[E]{OrderTerm{Expr: t.t, Desc: true}} }

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
