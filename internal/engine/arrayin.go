package engine

import (
	"database/sql/driver"
	"reflect"
	"strconv"
	"unsafe"
)

// On PostgreSQL an In list of integers or strings is one argument, the
// array literal of its values: col = ANY($1) and col <> ALL($1) (decision
// 0120). PostgreSQL's generic plan, which pgx's statement cache reaches
// after five runs, cannot fold col IN ($1, …, $n) into a constant array and
// tests each parameter in turn on every row; one array parameter it reads
// as a constant. It also makes every length of a list one shape.
//
// The parameter has no cast: PostgreSQL gives it the array type of the
// column's own type, as it gives each parameter of an IN list the column's
// type, so a varchar, uuid or enum column compares as it does with IN. The literal is text, which every database/sql driver binds and
// PostgreSQL parses with the element type's input function, as it parses a
// string bound for one element.

// arrayIn reports whether w writes the In list vs as an array: on
// PostgreSQL, outside a JSON path, for a list that arrayElems takes.
func (w *writer) arrayIn(vs []any) bool {
	return w.d == Postgres && w.path == nil && arrayElems(vs)
}

// arrayElems reports whether vs is not empty and each of its values has a
// text form that PostgreSQL reads back as the value a driver would have
// bound: nil, an integer of at most 63 bits' magnitude (int, int8 to
// int64, uint8 to uint32) or a string, or a named type of those kinds that
// is no driver.Valuer. A uint or uint64, which database/sql refuses above
// MaxInt64, a Valuer, and every other type keep the IN list.
func arrayElems(vs []any) bool {
	if len(vs) == 0 {
		return false
	}
	for _, v := range vs {
		if !arrayElemOK(v) {
			return false
		}
	}
	return true
}

// arrayElemOK reports whether arrayElem writes v.
func arrayElemOK(v any) bool {
	switch v.(type) {
	case nil, int64, int, int32, int16, int8, uint32, uint16, uint8, string:
		return true
	case driver.Valuer:
		return false
	}
	switch reflect.ValueOf(v).Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.String:
		return true
	}
	return false
}

// arrayElem appends v, which arrayElemOK takes, to b as an element of a
// PostgreSQL array literal: NULL for nil, an integer in decimal, a string in
// double quotes with each backslash and double quote escaped.
func arrayElem(b []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(b, "NULL"...)
	case int64:
		return strconv.AppendInt(b, x, 10)
	case int:
		return strconv.AppendInt(b, int64(x), 10)
	case int32:
		return strconv.AppendInt(b, int64(x), 10)
	case int16:
		return strconv.AppendInt(b, int64(x), 10)
	case int8:
		return strconv.AppendInt(b, int64(x), 10)
	case uint32:
		return strconv.AppendUint(b, uint64(x), 10)
	case uint16:
		return strconv.AppendUint(b, uint64(x), 10)
	case uint8:
		return strconv.AppendUint(b, uint64(x), 10)
	case string:
		return quoteElem(b, x)
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return strconv.AppendUint(b, rv.Uint(), 10)
	case reflect.String:
		return quoteElem(b, rv.String())
	default: // a signed integer
		return strconv.AppendInt(b, rv.Int(), 10)
	}
}

// quoteElem appends s to b as a quoted element of an array literal.
func quoteElem(b []byte, s string) []byte {
	b = append(b, '"')
	for i := range len(s) {
		if c := s[i]; c == '"' || c == '\\' {
			b = append(b, '\\')
		}
		b = append(b, s[i])
	}
	return append(b, '"')
}

// arrayLiteral is the PostgreSQL array literal of vs, which arrayElems
// takes: {1,2,NULL} or {"a","b"}. The string is the bytes it was built in,
// which nothing else holds.
func arrayLiteral(vs []any) string {
	b := make([]byte, 0, 2+8*len(vs))
	b = append(b, '{')
	for i, v := range vs {
		if i > 0 {
			b = append(b, ',')
		}
		b = arrayElem(b, v)
	}
	b = append(b, '}')
	return unsafe.String(unsafe.SliceData(b), len(b))
}

// arrayArg writes = ANY(?) or, for NOT IN, <> ALL(?), binding vs's array
// literal as a value of column c: a log shows it when it shows c's values.
func (w *writer) arrayArg(not bool, vs []any, c *ColumnInfo) {
	if not {
		w.str(" <> ALL(")
	} else {
		w.str(" = ANY(")
	}
	if w.track {
		w.next = showOf(c)
	}
	w.arg(arrayLiteral(vs))
	w.str(")")
}
