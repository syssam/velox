package gqlgraph

import (
	"fmt"

	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/untyped"
)

// A connection's query may be ordered by expressions (decision 0166): a
// computed key, such as a number parsed from a JSON member. They come first
// in the connection's order, before its orderBy argument and the key. The
// page reads each one's value beside the row (TableQuery.OrderValues), a
// cursor carries it, and the next page compares the same expression with it
// (AfterValues).
//
// Such a term of a cursor is named "expr:<signature>" (Table.OrderSig), so
// a cursor taken under another expression, another NULL placement or other
// bound values is taken under another order. The signature hashes the
// values the expression binds: an expression bound to a value that changes
// from request to request, such as the time now or a search term, makes
// every cursor taken before it another order's. Bind such a value in a
// filter, not in the ordering, or accept that the cursors last one request.
//
// The term's value is the value the driver read, as untyped.EncodeOrderValue
// writes it so that it reads back as the same Go value: null, a bool, an
// integer ("u:<digits>" past int64), a float with a fraction ("3.0"),
// "s:<text>", "d:<decimal text>", "dt:<time text>" or "t:<RFC 3339>". A
// decimal or a time the driver read as text is bound back cast to its type,
// so it compares as the expression does.

// exprTerms is the terms of q's orderings, each an expression, that come
// first in c's connection; nil for an unordered q. An ordering by a column
// or by a relation's aggregate is the application's error.
func exprTerms(q untyped.TableQuery, c *Type) ([]term, error) {
	if !q.Ordered() {
		return nil, nil
	}
	own := q.Table()
	os := q.Order()
	terms := make([]term, len(os))
	for i := range os {
		o := os[i]
		if !untyped.OrderValueTerm(o) {
			// The application's bug, not the client's: an uncoded error.
			return nil, fmt.Errorf("veloxgql: %s: q may be ordered only by expressions (Calc); a column ordering comes from the orderBy argument", newConnFunc(c))
		}
		dir := veloxgql.OrderDirectionAsc
		if o.Desc {
			dir = veloxgql.OrderDirectionDesc
		}
		terms[i] = term{field: "expr:" + own.OrderSig(o), dir: dir, col: -1, tbl: own, expr: &o}
	}
	return terms, nil
}

// cursorValues is a row's values of the query's orderings by an
// expression, as a cursor holds them (untyped.EncodeOrderValue).
func cursorValues(vs []any) ([]any, error) {
	out := make([]any, len(vs))
	for i, v := range vs {
		var err error
		if out[i], err = untyped.EncodeOrderValue(v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// orderValue is v, a cursor's value of an ordering by an expression, back
// as the value the driver read (untyped.DecodeOrderValue); a value no page
// wrote fails with ErrBadCursor.
func orderValue(v any) (any, error) {
	x, err := untyped.DecodeOrderValue(v)
	if err != nil {
		return nil, InputErrorf("%w: a value of an ordering by an expression is not one a page wrote", veloxgql.ErrBadCursor)
	}
	return x, nil
}
