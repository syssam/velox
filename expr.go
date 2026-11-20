package velox

import (
	"github.com/syssam/velox/internal/engine"
)

// Expr is a condition written as SQL text, for what the column handles'
// methods do not say: a function, an arithmetic expression, a CASE. Each ?
// in text is filled by the next argument: a column handle of E, written as
// the column, a Term of E, written as the term (so a HAVING is an Expr of
// aggregate terms), or any other value, which is bound. It is one more condition
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
// when the statement runs, before anything is sent, as are functions that
// read a table, a file or server state without a subquery, such as
// PostgreSQL's pg_ and query_to_xml families and MySQL's LOAD_FILE. So a condition on another
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
	slots, vals, err := slotsOf[E](text, args)
	if err != nil {
		return Pred[E](engine.NewPredicate(engine.Node{Op: engine.OpInvalid, V: err}))
	}
	return Pred[E](engine.NewPredicate(engine.ExprNode(text, slots, vals)))
}

// SetExpr assigns column c the SQL expression text, written as Expr's text
// is: each ? is filled by the next argument, a column handle or a Term of E
// written as it is, and any other value bound. The same text is refused. It
// is a setter of UpdateQuery.Set, for what Set, Add and Sub do not say:
//
//	db.Update[veloxgen.Post]().Where(P.ID.EQ(id)).
//		Set(velox.SetExpr(P.Views, "? * 2", P.Views))
//
// writes UPDATE "posts" SET "views" = ("views" * 2) WHERE "id" = $1. The
// expression reads the row's own columns, never another table, and the
// update keeps the Write policy, the scope and the interceptors.
//
// velox cannot read the expression's value before the database computes
// it. So the column's validators do not run on it, as on Add and Sub, and a
// hook's Mutation.Value reports false for the column. An Immutable or
// Version column, an encoded (JSON) column, a key that One must read back
// and an insert refuse it, as does an aggregate in the text. On MySQL an
// expression reads the values earlier assignments of the same UPDATE wrote;
// PostgreSQL and SQLite read the row as it was.
func SetExpr[E any](c Column[E], text string, args ...any) Setter[E] {
	slots, vals, err := slotsOf[E](text, args)
	if err != nil {
		return Setter[E]{engine.ExprAssignment(int(c.column(nil)), engine.InvalidTerm(err))}
	}
	return Setter[E]{engine.ExprAssignment(int(c.column(nil)), engine.ExprTerm(text, slots, vals))}
}
