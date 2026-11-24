package gqlgraph

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

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
// a cursor taken under another expression, other bound values or another
// NULL placement is taken under another order. Its value is the value the
// driver read, written so that it reads back as the same Go type:
//
//   - NULL is null, a bool is a bool and an integer is a JSON integer;
//   - a finite float is a JSON number with a fraction or an exponent
//     ("3.0"), and NaN and the infinities are "f:NaN", "f:+Inf", "f:-Inf";
//   - text, and bytes, are "s:" and the text: a decimal the driver reads as
//     text stays text, and the database converts it back;
//   - a time is "t:" and RFC 3339 with nanoseconds.

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
			// Only an expression may come before the orderBy argument.
			return nil, fmt.Errorf("veloxgql: %s: q has an OrderBy; the connection orders by its orderBy argument", newConnFunc(c))
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
// expression, as a cursor holds them (cursorValue).
func cursorValues(vs []any) ([]any, error) {
	out := make([]any, len(vs))
	for i, v := range vs {
		var err error
		if out[i], err = cursorValue(v); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// cursorValue is v, the value the driver read of an ordering by an
// expression, as a cursor holds it.
func cursorValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case float32:
		return floatValue(float64(x)), nil
	case float64:
		return floatValue(x), nil
	case string:
		return "s:" + x, nil
	case []byte:
		return "s:" + string(x), nil
	case time.Time:
		return "t:" + x.Format(time.RFC3339Nano), nil
	}
	return nil, fmt.Errorf("veloxgql: an ordering by an expression read a value of the Go type %T, which a cursor cannot hold", v)
}

// floatValue is f as a cursor holds it: a JSON number that does not read
// back as an integer, or "f:" and NaN, +Inf or -Inf.
func floatValue(f float64) any {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "f:" + s
	}
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return json.Number(s)
}

// orderValue is v, a cursor's value of an ordering by an expression, as
// cursorValue wrote it, back as the value the driver read; a value
// cursorValue never writes fails with ErrBadCursor.
func orderValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, int64:
		return x, nil
	case json.Number:
		if n, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return n, nil
		}
		if f, err := strconv.ParseFloat(string(x), 64); err == nil {
			return f, nil
		}
	case string:
		tag, rest, ok := strings.Cut(x, ":")
		switch {
		case !ok:
		case tag == "s":
			return rest, nil
		case tag == "t":
			if t, err := time.Parse(time.RFC3339Nano, rest); err == nil {
				return t, nil
			}
		case tag == "f":
			if f, err := strconv.ParseFloat(rest, 64); err == nil && (math.IsNaN(f) || math.IsInf(f, 0)) {
				return f, nil
			}
		}
	}
	return nil, InputErrorf("%w: a value of an ordering by an expression is not one a page wrote", veloxgql.ErrBadCursor)
}
