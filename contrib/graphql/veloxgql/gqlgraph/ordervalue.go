package gqlgraph

import (
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql"
	"github.com/syssam/velox/untyped"
)

// A connection's query may be ordered (decision 0166): by expressions, a
// computed key such as a number parsed from a JSON member, and by the
// node's own columns, in any order. They come first in the connection's
// order, before its orderBy argument and the key. The page reads each one's
// value beside the row (TableQuery.OrderValues), a cursor carries it, and
// the next page compares the same term with it (AfterValues). A column
// ordering of q takes that path too, as the term of its column, so a q
// with any ordering never reads or writes a msgpack cursor: entgql's
// cursors hold the orderBy field alone. A cursor's value of a column, or
// of an expression whose kind velox knows (a JSON path's Number, a Calc
// marked velox.Text, Integer, Time, Bool or Decimal), is checked against
// that kind (untyped.ErrOrderValue, here ErrBadCursor).
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

// exprTerms is the terms of q's orderings that come first in c's
// connection, each read beside the row; nil for an unordered q. An ordering
// by the node's own column is the term of the column (columnOrder), and
// one that order, the orderBy argument, also names is the client's error.
// An ordering by a relation's aggregate, or by a related row's column, is
// the application's error.
func exprTerms(q untyped.TableQuery, c *Type, order []Order) ([]term, error) {
	if !q.Ordered() {
		return nil, nil
	}
	own := q.Table()
	os := q.Order()
	terms := make([]term, len(os))
	for i := range os {
		o := os[i]
		if o.Expr == nil {
			var err error
			if o, err = columnOrder(own, c, o, order); err != nil {
				return nil, err
			}
		} else if !untyped.OrderValueTerm(o) {
			// The application's bug, not the client's: an uncoded error.
			return nil, fmt.Errorf("veloxgql: %s: q may be ordered by expressions (Calc) and the node's own columns, not by a relation's aggregate; a count comes from the orderBy argument", newConnFunc(c))
		}
		dir := veloxgql.OrderDirectionAsc
		if o.Desc {
			dir = veloxgql.OrderDirectionDesc
		}
		terms[i] = term{field: "expr:" + own.OrderSig(o), dir: dir, col: -1, tbl: own, expr: &o}
	}
	return terms, nil
}

// columnOrder is o, an ordering of q by a column of its own entity, as the
// ordering by the column's term, whose value the page reads beside the
// row, with o's direction and NULL placement. The column must be of a type
// whose kind velox knows (knownKind): a cursor's value of it is checked as
// that kind. A related row's column, a column of another type and one the
// orderBy argument names too are refused.
func columnOrder(own untyped.Table, c *Type, o velox.OrderTerm, order []Order) (velox.OrderTerm, error) {
	fn := newConnFunc(c)
	info := own.Info()
	if o.Rel != 0 {
		return o, fmt.Errorf("veloxgql: %s: q may be ordered by the node's own columns and by expressions; a related row's column comes from the orderBy argument", fn)
	}
	if o.Column < 0 || o.Column >= len(info.Columns) {
		return o, fmt.Errorf("veloxgql: %s: q is ordered by column %d, which %s lacks", fn, o.Column, info.Name)
	}
	col := info.Columns[o.Column]
	if !knownKind(col) {
		return o, fmt.Errorf("veloxgql: %s: q is ordered by %s.%s, of the Go type %v; a column of q's order must be a string, a bool, a number or a time, or a pointer to one", fn, info.Name, col.Field, velox.GoType(col))
	}
	for _, x := range order {
		if f := c.find(x.Field); f != nil && f.Edge == 0 && !f.Count && f.Index == o.Column {
			return o, InputErrorf("%s connection: %w: orderBy %s orders by a column the connection is ordered by already", c.Name, veloxgql.ErrBadPagination, x.Field)
		}
	}
	return velox.OrderTerm{Expr: untyped.Col(o.Column), Desc: o.Desc, Nulls: o.Nulls}, nil
}

// knownKind reports whether a cursor's value of column col can be checked
// against its kind: its Go type, or the type it points to, is a time or
// has a string, bool, integer or float kind with no Value or Scan method,
// and the column is not encoded.
func knownKind(col velox.ColumnInfo) bool {
	ft := velox.GoType(col)
	if ft == nil || col.Codec {
		return false
	}
	if ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	if baseType(ft) == timeType {
		return true
	}
	if ft.Implements(reflect.TypeFor[driver.Valuer]()) || reflect.PointerTo(ft).Implements(reflect.TypeFor[sql.Scanner]()) {
		return false
	}
	switch ft.Kind() {
	case reflect.String, reflect.Bool, reflect.Float32, reflect.Float64,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return true
	}
	return false
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
