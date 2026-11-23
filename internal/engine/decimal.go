package engine

import (
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"strconv"
)

// SQLite has no decimal type (decision 0126). A decimal column's NUMERIC
// affinity stores a decimal's text as an INTEGER when it is a whole number
// that fits, and otherwise as a REAL: the double nearest it, which keeps 15
// significant digits of any decimal and no more. Two things follow, which
// velox mends for a decimal column (ColumnInfo.decimal set, as velox-gen sets
// it on an Other field held in a decimal column).
//
// An argument of a decimal Go type, such as decimal.Decimal, is a Valuer
// of text, which SQLite binds as TEXT. A column comparison converts it by the
// column's affinity, but an expression has none, so SUM(x) > '1' compared a
// number with text, which SQLite orders after every number: false for every
// group. In an Expr, velox binds such a value as the number the column would
// store (sqliteNumber).
//
// SUM and AVG of REALs are computed in floating point, so 0.1 + 0.2 is
// 0.30000000000000004. velox rounds a decimal column's SUM and AVG to 15
// significant digits (sqliteDecimalAgg), which makes them the decimal sum
// whenever it has at most 15 significant digits, as the values it adds do.

// decimalColumnType reports whether v's type, or the type v points to, is
// the Go type of a decimal column of in.
func decimalColumnType(in *EntityInfo, v any) (any, bool) {
	if v == nil {
		return nil, false
	}
	t := reflect.TypeOf(v)
	if t.Kind() == reflect.Pointer {
		rv := reflect.ValueOf(v)
		if rv.IsNil() {
			return nil, false
		}
		t, v = t.Elem(), rv.Elem().Interface()
	}
	for i := range in.Columns {
		if c := &in.Columns[i]; c.decimal[0] != 0 && c.val == any(t) {
			return v, true
		}
	}
	return nil, false
}

// exprArg is the value an Expr binds for v: on SQLite, a value of a decimal
// column's Go type as sqliteNumber makes it; anything else as it is.
func (w *writer) exprArg(v any) any {
	if w.d != SQLite {
		return v
	}
	if x, ok := decimalColumnType(w.in, v); ok {
		return sqliteNumber(x)
	}
	return v
}

// sqliteNumber is the decimal value v as SQLite's NUMERIC affinity would
// store its text: an int64 for a whole number that fits, else the nearest
// float64, which is what SQLite's own conversion of the text gives for a
// decimal of up to 15 significant digits. v is read through its Value
// method, or as a string when it is one. A value that is not a finite
// number's text is returned as it is, for the driver to bind or refuse.
func sqliteNumber(v any) any {
	x := v
	if vr, ok := v.(driver.Valuer); ok {
		var err error
		if x, err = vr.Value(); err != nil {
			return v
		}
	}
	var s string
	switch y := x.(type) {
	case int64, float64:
		return y
	case string:
		s = y
	case []byte:
		s = string(y)
	default:
		rv := reflect.ValueOf(x)
		if rv.Kind() != reflect.String {
			return v
		}
		s = rv.String()
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && !math.IsInf(f, 0) && !math.IsNaN(f) && plainNumber(s) {
		return f
	}
	return v
}

// plainNumber reports whether s is a decimal number's text: a sign, digits
// with at most one point, and an exponent, as SQLite reads one. ParseFloat
// also takes hexadecimal, underscores, Inf and NaN.
func plainNumber(s string) bool {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digits, point := 0, false
	for ; i < len(s); i++ {
		switch c := s[i]; {
		case c >= '0' && c <= '9':
			digits++
		case c == '.' && !point:
			point = true
		default:
			if digits == 0 || c != 'e' && c != 'E' {
				return false
			}
			return exponent(s[i+1:])
		}
	}
	return digits > 0
}

// exponent reports whether s is an exponent's digits, after an optional sign.
func exponent(s string) bool {
	if s != "" && (s[0] == '+' || s[0] == '-') {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// decimalAgg reports whether an aggregate of column col of in, a SUM or AVG
// when sumOrAvg says so, is one SQLite computes in floating point over a
// decimal column.
func decimalAgg(d Dialect, in *EntityInfo, sumOrAvg bool, col int32) bool {
	return d == SQLite && sumOrAvg && col >= 0 && int(col) < len(in.Columns) && in.Columns[col].decimal[0] != 0
}

// decimalTermAgg reports whether fn of the term of is a SUM or AVG that
// SQLite computes in floating point over decimals: of is a decimal column,
// or an expression DecimalTerm marked (decision 0131).
func decimalTermAgg(d Dialect, in *EntityInfo, fn TermFn, of Term) bool {
	sumOrAvg := fn == TermSum || fn == TermAvg
	switch of.kind {
	case termColumn:
		return decimalAgg(d, in, sumOrAvg, of.col)
	case termExpr:
		return d == SQLite && sumOrAvg && of.dec
	}
	return false
}

// DecimalTerm is t, an expression, marked as holding decimals: on SQLite
// its SUM and AVG are rounded as a decimal column's are (decision 0131).
// The mark changes nothing else. Any other term is an InvalidTerm: a
// column is decimal by its descriptor, and an aggregate by its operand.
func DecimalTerm(t Term) Term {
	switch t.kind {
	case termInvalid:
		return t
	case termExpr:
		t.dec = true
		return t.seal()
	}
	return InvalidTerm(errors.New("velox: Decimal marks an expression, such as a Calc, as holding decimals; a column is decimal by its schema, and an aggregate by what it aggregates"))
}

// sqliteDecimalAgg writes agg, a SUM or AVG of a decimal column on SQLite,
// rounded to 15 significant digits when it is a REAL:
//
//	CASE WHEN typeof(agg) = 'real' THEN CAST(printf('%!.15g', agg) AS REAL) ELSE agg END
//
// A sum of INTEGERs stays the exact integer, and NULL stays NULL. The value
// is a number still, so a HAVING or an ordering compares it as one. SQLite
// computes the one aggregate once, however often the text names it.
func (w *writer) sqliteDecimalAgg(agg func()) {
	w.str("CASE WHEN typeof(")
	agg()
	w.str(") = 'real' THEN CAST(printf('%!.15g', ")
	agg()
	w.str(") AS REAL) ELSE ")
	agg()
	w.str(" END")
}
