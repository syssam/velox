package velox

import (
	"fmt"
	"reflect"

	"github.com/syssam/velox/internal/engine"
)

// Expr is a condition written as SQL text, for what the column handles'
// methods do not say: a function, an arithmetic expression, a CASE. Each ?
// in text is filled by the next argument: a column handle of E, written as
// the column, or any other value, which is bound. It is one more condition
// in Where, ANDed with the Read policy, the scope and the interceptors, so a
// query with an Expr keeps them all.
//
//	db.Query[veloxgen.Invoice]().Where(
//		velox.Expr[veloxgen.Invoice]("LOWER(?) = LOWER(?)", I.Customer, name))
//
// The text is written as given, in parentheses, and may hold a function, an
// operator, a cast, CASE, and string literals such as '$.id' or an empty one, and
// nothing that could name another table or end the condition. A literal is a
// quote, plain characters and a quote: with a backslash, a doubled quote, a ?
// or a control character inside, pass the value as an argument instead.
// Outside a literal, any other quote, a semicolon, a comment, a backslash, a
// $, an unbalanced parenthesis, and the words SELECT, WITH, TABLE, VALUES,
// UNION, INTERSECT, EXCEPT, INTO, RETURNING, PRAGMA and ATTACH are refused
// when the statement runs, before anything is sent. So a condition on another
// table is HasWith or InSelect, which apply that table's policies. The words
// are matched whatever their case, and FROM is allowed for EXTRACT and TRIM.
//
// A ? outside a literal is always an argument's place, so PostgreSQL's jsonb
// ? operator is written jsonb_exists. A wrong number of arguments, a column
// of another entity and an encoded (JSON) column fail the statement. The
// arguments are bound as given: no codec encodes them.
//
// The refusals guard against a policy bypass by mistake. They are not a
// defence against hostile text: Expr takes text from the program, never from
// a user. A function that runs SQL from a string argument still would.
func Expr[E any](text string, args ...any) Pred[E] {
	cols := make([]int32, len(args))
	vals := make([]any, 0, len(args))
	for i, a := range args {
		switch h := a.(type) {
		case Column[E]:
			cols[i] = h.column(nil)
		default:
			if foreignHandle(a) {
				return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpInvalid, V: fmt.Errorf(
					"velox: Expr %q: argument %d is %T, a column of another entity than the query's", text, i+1, a)}))
			}
			cols[i] = -1
			vals = append(vals, a)
		}
	}
	return Pred[E](engine.NewPredicate(engine.ExprNode(text, cols, vals)))
}

// foreignHandle reports a column handle of some entity, one that Expr's E
// did not match: a struct of this package that holds a column index I.
func foreignHandle(a any) bool {
	t := reflect.TypeOf(a)
	if t == nil || t.Kind() != reflect.Struct || t.PkgPath() != reflect.TypeFor[DB]().PkgPath() {
		return false
	}
	f, ok := t.FieldByName("I")
	return ok && f.Type.Kind() == reflect.Int32
}
