package engine

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
)

// An ordering by an expression pages by its values (decision 0166). A row
// does not hold an expression's value, so a read that pages reads it beside
// the row (Statement.OrderValues), and After takes it back with the row
// (AddAfterValues), bound into the same expression in the keyset
// condition.

// OrderValueTerm reports whether o orders by an expression whose value a
// keyset reads beside the row: an expression term (Calc, a JSON member, a
// joined column's term, a date bucket) that is not a relation's aggregate,
// which After computes from the row's key instead.
func OrderValueTerm(o OrderTerm) bool {
	t, ok := o.Expr.(Term)
	return ok && t.kind != termRel
}

// orderValueCount is the number of the values st reads beside each row:
// one per OrderValueTerm of its OrderBy, or -1 when it reads none.
func orderValueCount(st *Statement) int {
	if !st.OrderValues {
		return -1
	}
	n := 0
	for _, o := range st.OrderBy {
		if OrderValueTerm(o) {
			n++
		}
	}
	return n
}

// OrderSig is the signature of o, an ordering of the Table's rows: see
// OrderSig.
func (t Table) OrderSig(o OrderTerm) string { return OrderSig(t.Info(), o) }

// OrderSig is the signature of o, an ordering of in's rows: a short hex
// hash of the term as SQLite writes it in ORDER BY, its direction and NULL
// placement included, and of each value it binds with its Go type. Two
// orderings with the same signature sort alike; a cursor names its
// expression terms by it, so a cursor taken under another expression, or
// the same one with other bound values, is refused.
func OrderSig(in *EntityInfo, o OrderTerm) string {
	w := writer{d: SQLite, in: in}
	if o.Expr != nil {
		w.orderTerm(o)
	} else {
		w.orderBy([]OrderTerm{o}, false)
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(w.b.String()))
	for _, a := range w.args {
		_, _ = fmt.Fprintf(h, "\x00%T\x00%v", a, a)
	}
	if w.err != nil {
		_, _ = fmt.Fprintf(h, "\x00%v", w.err)
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

// checkOrderValueTerm refuses of an OrderValueTerm that a keyset cannot
// page over: an aggregate or a window, whose value is not the row's own.
func checkOrderValueTerm(in *EntityInfo, o OrderTerm) error {
	t := o.Expr.(Term)
	if hasAgg(t) || hasWindow(t) {
		return fmt.Errorf("velox: %s: After cannot page over an ordering by an aggregate or a window function, whose value is not the row's own", in.Name)
	}
	return nil
}

// valueSource is the afterSource of an OrderValueTerm: the row's value of
// it, read beside the row. A value of another kind than the term gives
// (checkValueKind) is refused.
func valueSource(in *EntityInfo, o OrderTerm, v any, d Dialect) (afterSource, error) {
	if err := checkOrderValueTerm(in, o); err != nil {
		return afterSource{}, err
	}
	if err := checkValueKind(d, in, o.Expr.(Term), v); err != nil {
		return afterSource{}, fmt.Errorf("velox: %s: %w", in.Name, err)
	}
	return afterSource{info: in, value: true, v: v, d: d}, nil
}

// valueKind is the kind of value a term gives, as far as velox knows it
// without the database: kindUnknown for an expression whose text velox
// does not read. It is an alias of uint8, and Term holds its mark as a
// uint8: a field of a defined type would name that type in every entity
// package's export data, about 230 bytes.
type valueKind = uint8

const (
	kindUnknown valueKind = iota
	kindNumber
	kindTime
	kindText
	kindBool
	kindBytes
	kindInteger
	// kindDecimal is a Term's mark alone (DecimalTerm): its values are
	// numbers, and SQLite rounds its SUM and AVG.
	kindDecimal
)

// kindName is k's name in an error.
func kindName(k valueKind) string {
	switch k {
	case kindNumber:
		return "a number"
	case kindTime:
		return "a time"
	case kindText:
		return "text"
	case kindBool:
		return "a bool"
	case kindBytes:
		return "bytes"
	case kindInteger:
		return "an integer"
	case kindDecimal:
		return "a decimal (Decimal)"
	}
	return "any value"
}

// TextTerm is t, an expression, marked as giving text: AfterValues takes a
// string for its value, as for a string column. See markKind.
func TextTerm(t Term) Term { return markKind(t, kindText) }

// IntegerTerm is t, an expression, marked as giving integers: AfterValues
// takes a Go integer for its value, or a DecimalText with no fraction, as
// MySQL reads FLOOR of a decimal. See markKind.
func IntegerTerm(t Term) Term { return markKind(t, kindInteger) }

// TimeTerm is t, an expression, marked as giving times: AfterValues takes
// for its value what it takes for a time column. See markKind.
func TimeTerm(t Term) Term { return markKind(t, kindTime) }

// BoolTerm is t, an expression, marked as giving bools: AfterValues takes
// for its value what it takes for a bool column. See markKind.
func BoolTerm(t Term) Term { return markKind(t, kindBool) }

// markKind is t, an expression, marked as giving values of kind k
// (termValueKind), so that AfterValues checks a cursor's value of it
// (checkValueKind). The mark changes no SQL, so neither the render cache's
// key nor OrderSig holds it. A term already marked with another kind,
// Decimal included, and any other term than an expression are
// InvalidTerms: a column's kind is its Go type's.
func markKind(t Term, k valueKind) Term {
	switch {
	case t.kind == termInvalid:
		return t
	case t.kind != termExpr:
		return InvalidTerm(fmt.Errorf("velox: a kind mark (%s) is for an expression, such as a Calc; a column's kind is its Go type's", kindName(k)))
	case t.mark != kindUnknown && t.mark != k:
		return InvalidTerm(fmt.Errorf("velox: an expression marked as giving %s cannot also give %s", kindName(t.mark), kindName(k)))
	}
	t.mark = k
	return t
}

// termValueKind is the kind of value t, a term of in's rows, gives: a
// column's by its Go type (columnKind); a JSON path's by its kind, a
// number for Number, Len and Text's Number, a time for Text's Time, text
// for Text and a bool for Bool; a time for a date bucket; a number for an
// expression marked Decimal, and the kind markKind gave one; a joined
// term's by its term in the joined entity. Any other expression (an
// unmarked Calc, a JSON path's raw value) is kindUnknown.
func termValueKind(in *EntityInfo, t Term) valueKind {
	switch t.kind {
	case termColumn:
		if in != nil && t.col >= 0 && int(t.col) < len(in.Columns) {
			return columnKind(in.Columns[t.col])
		}
	case termExpr:
		if t.mark == kindDecimal {
			return kindNumber
		}
		return t.mark
	case termJSON:
		switch t.path.Kind {
		case JSONKindNumber, JSONKindLen, JSONKindTextNumber:
			return kindNumber
		case JSONKindTextTime:
			return kindTime
		case JSONKindText:
			return kindText
		case JSONKindBool:
			return kindBool
		}
	case termDate:
		return kindTime
	case termJoined:
		if ref, ok := t.r.n.V.(RelRef); ok {
			if l, err := ref.Link(); err == nil {
				return termValueKind(l.Target, *t.of)
			}
		}
	}
	return kindUnknown
}

// columnKind is the kind of value column c gives, by its Go type: a time
// for a time.Time or a type of one; kindUnknown for a column velox encodes
// and any other type with a Value or Scan method, such as a decimal type;
// a number for a column with a MySQL decimal type, an integer or a float;
// text for a string; a bool for a bool; bytes for a []byte.
func columnKind(c ColumnInfo) valueKind {
	typ := GoType(c)
	if typ == nil || c.Codec {
		return kindUnknown
	}
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if timeLike(typ) {
		return kindTime
	}
	if typ.Implements(reflect.TypeFor[driver.Valuer]()) || reflect.PointerTo(typ).Implements(scannerType) {
		return kindUnknown
	}
	if c.decimal != [2]uint8{} {
		return kindNumber
	}
	switch typ.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return kindNumber
	case reflect.String:
		return kindText
	case reflect.Bool:
		return kindBool
	case reflect.Slice:
		if typ.Elem().Kind() == reflect.Uint8 {
			return kindBytes
		}
	}
	return kindUnknown
}

// checkValueKind refuses v, an order value given to AfterValues for the
// term t, when t's kind is known (termValueKind) and no read of t on
// dialect d gives a value of v's Go type, as a cursor edited by its client
// may hold: bound as it is, PostgreSQL would fail the statement and SQLite
// page from the wrong place. NULL is every kind's. A number is an integer,
// a float or a DecimalText (PostgreSQL's numeric, MySQL's decimal, read as
// text); a time a time.Time or a TimeText (MySQL's without parseTime), and
// on SQLite, whose times are text, a string; text a string; a bool a bool,
// and on MySQL and SQLite, whose booleans are integers, an integer; bytes a
// string, as OrderValues copies them, or a []byte. A value of a term of
// unknown kind is bound as it is.
func checkValueKind(d Dialect, in *EntityInfo, t Term, v any) error {
	if v == nil {
		return nil
	}
	k := termValueKind(in, t)
	ok := true
	switch k {
	case kindNumber:
		_, dec := v.(DecimalText)
		ok = dec || numberValue(v)
	case kindTime:
		switch v.(type) {
		case time.Time, TimeText:
		case string:
			ok = d == SQLite
		default:
			ok = false
		}
	case kindInteger:
		x, dec := v.(DecimalText)
		ok = integerValue(v) || dec && decimalText(string(x)) && strings.IndexByte(string(x), '.') < 0
	case kindText:
		_, ok = v.(string)
	case kindBool:
		_, ok = v.(bool)
		ok = ok || d != Postgres && integerValue(v)
	case kindBytes:
		switch v.(type) {
		case string, []byte:
		default:
			ok = false
		}
	}
	if !ok {
		return fmt.Errorf("%w: %#v for an ordering by %s on %v", ErrOrderValue, v, kindName(k), d)
	}
	return nil
}

// integerValue reports whether v is a Go integer of any width.
func integerValue(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return true
	}
	return false
}

// numberValue reports whether v is a Go integer or float of any width.
func numberValue(v any) bool {
	switch v.(type) {
	case float32, float64:
		return true
	}
	return integerValue(v)
}

// valueEqual is the condition that the expression o orders by has the value
// v, bound on dialect d (boundValue): NULL equals NULL.
func valueEqual(o OrderTerm, v any, d Dialect) Node {
	t := o.Expr.(Term)
	if nullValue(v) {
		return valueExpr("? IS NULL", t)
	}
	if zeroDate(d, v) {
		return valueExpr(zeroDateText, t, t, t)
	}
	b, arg := boundValue(d, v)
	return valueExpr("? = "+b, t, arg)
}

// zeroDateText is the condition that a MySQL date or time is the zero date,
// 0000-00-00, written with no literal of it: under NO_ZERO_DATE MySQL casts
// the literal to NULL.
const zeroDateText = "(YEAR(?) = 0 AND MONTH(?) = 0 AND DAY(?) = 0)"

// zeroDate reports whether v, an order value on dialect d, is MySQL's zero
// date: a TimeText of it, or the zero time.Time, which the driver reads it
// as with parseTime. It sorts before every other date.
func zeroDate(d Dialect, v any) bool {
	if d != MySQL {
		return false
	}
	switch x := v.(type) {
	case TimeText:
		return zeroTimeText(string(x))
	case time.Time:
		return x.IsZero()
	}
	return false
}

// ErrOrderValue is the error of AfterValues given an order value that no
// read on the statement's dialect gives, or that is not of the kind its
// ordering's term gives: a cursor's that was forged, or written on another
// dialect.
var ErrOrderValue = errors.New("velox: an order value this dialect never reads")

// checkOrderValue refuses v, an order value given to AfterValues, when no
// read on dialect d gives it, since d would cast it to another value or
// fail with a driver's error. A NaN, Infinity or -Infinity decimal is
// PostgreSQL's alone; the zero date MySQL's alone; a NaN float
// PostgreSQL's alone, an infinite one not MySQL's; an unsigned integer past
// int64 not SQLite's, whose integers are signed (PostgreSQL's numeric(20,0)
// and MySQL's BIGINT UNSIGNED hold one); text holding a NUL byte not
// PostgreSQL's, whose text cannot hold one.
func checkOrderValue(d Dialect, v any) error {
	ok := true
	switch x := v.(type) {
	case string:
		ok = d != Postgres || strings.IndexByte(x, 0) < 0
	case DecimalText:
		ok = d == Postgres || !specialDecimal(string(x))
	case TimeText:
		ok = d == MySQL || !zeroTimeText(string(x))
	case float64:
		switch {
		case math.IsNaN(x):
			ok = d == Postgres
		case math.IsInf(x, 0):
			ok = d != MySQL
		}
	case uint64:
		ok = d != SQLite || x <= math.MaxInt64
	}
	if !ok {
		return fmt.Errorf("%w: %#v on %v", ErrOrderValue, v, d)
	}
	return nil
}

// boundValue is how v, an order value, is bound on dialect d: the text that
// stands for it and the argument. A DecimalText is cast to a decimal (on
// MySQL DECIMAL(65,30)), so that it compares as a decimal: MySQL compares a
// decimal with text as a double. A TimeText, and a time.Time, are cast to
// DATETIME(6) on MySQL. Anything else is bound as it is. On PostgreSQL a
// text argument takes the type of the expression it is compared with, so a
// numeric that pgx read as text compares exactly too.
func boundValue(d Dialect, v any) (string, any) {
	switch x := v.(type) {
	case DecimalText:
		if d == MySQL {
			return "CAST(? AS DECIMAL(65,30))", string(x)
		}
		return "CAST(? AS NUMERIC)", string(x)
	case TimeText:
		if d == MySQL {
			return "CAST(? AS DATETIME(6))", string(x)
		}
		return "?", string(x)
	case time.Time:
		if d == MySQL {
			return "CAST(? AS DATETIME(6))", x
		}
	}
	return "?", v
}

// valuePast is the condition that the expression o orders by is past v,
// and false when no row is: as pastValue's for a nullable column, since an
// expression can be NULL.
func valuePast(o OrderTerm, v any, d Dialect) (Node, bool) {
	t := o.Expr.(Term)
	cmp := " > "
	if o.Desc {
		cmp = " < "
	}
	null, first := nullValue(v), nullsFirst(o)
	switch {
	case null && !first:
		return Node{}, false
	case null:
		return valueExpr("? IS NOT NULL", t), true
	}
	if zeroDate(d, v) {
		// Past the zero date ascending is every other date; descending,
		// nothing but NULL where NULL sorts last.
		switch {
		case !o.Desc && first:
			return valueExpr("(? IS NOT NULL AND NOT "+zeroDateText+")", t, t, t, t), true
		case !o.Desc:
			return valueExpr("(? IS NULL OR NOT "+zeroDateText+")", t, t, t, t), true
		case first:
			return Node{}, false
		}
		return valueExpr("? IS NULL", t), true
	}
	b, arg := boundValue(d, v)
	if o.Desc && mysqlTime(d, v) {
		// The zero date sorts before every date, but under NO_ZERO_DATE
		// MySQL does not compare it as less than one: name it.
		if first {
			return valueExpr("(?"+cmp+b+" OR "+zeroDateText+")", t, arg, t, t, t), true
		}
		return valueExpr("(?"+cmp+b+" OR "+zeroDateText+" OR ? IS NULL)", t, arg, t, t, t, t), true
	}
	if first {
		return valueExpr("?"+cmp+b, t, arg), true
	}
	return valueExpr("(?"+cmp+b+" OR ? IS NULL)", t, arg, t), true
}

// mysqlTime reports whether v, an order value on dialect d, is a MySQL
// date or time, which the zero date may sort before.
func mysqlTime(d Dialect, v any) bool {
	switch v.(type) {
	case TimeText, time.Time:
		return d == MySQL
	}
	return false
}

// valueExpr is the condition text with each ? filled by the next of xs: a
// Term written as it is, anything else bound.
func valueExpr(text string, xs ...any) Node {
	slots := make([]ExprSlot, len(xs))
	var args []any
	for i, x := range xs {
		if t, ok := x.(Term); ok {
			slots[i] = ExprSlot{Col: -1, Term: &t}
			continue
		}
		slots[i] = ExprSlot{Col: -1}
		args = append(args, x)
	}
	return ExprNode(text, slots, args)
}

// orderValueScan is the destinations of the values a row's read scans
// beside its columns, and where they are kept: n per row, in vals.
type orderValueScan struct {
	n    int
	dst  []any
	cell []any
	vals []any
	// types are the database type names of the value columns
	// (sql.ColumnType.DatabaseTypeName), read at the first row when the
	// rows tell them; nil otherwise.
	types []string
	typed bool // types was looked for
}

// newOrderValueScan is the scan of st's order values; nil when it reads
// none.
func newOrderValueScan(st *Statement) *orderValueScan {
	n := orderValueCount(st)
	if n <= 0 {
		return nil
	}
	s := &orderValueScan{n: n, dst: make([]any, n), cell: make([]any, n)}
	for i := range s.cell {
		s.dst[i] = &s.cell[i]
	}
	return s
}

// columnTyper is rows that tell their columns' types: *sql.Rows.
type columnTyper interface {
	ColumnTypes() ([]*sql.ColumnType, error)
}

// keep appends the values the last row of r scanned, each as orderValue
// makes it.
func (s *orderValueScan) keep(r Rows) {
	if !s.typed {
		s.typed = true
		if ct, ok := r.(columnTyper); ok {
			if cts, err := ct.ColumnTypes(); err == nil && len(cts) >= s.n {
				for _, c := range cts[len(cts)-s.n:] {
					s.types = append(s.types, strings.ToUpper(c.DatabaseTypeName()))
				}
			}
		}
	}
	for i, v := range s.cell {
		typ := ""
		if s.types != nil {
			typ = s.types[i]
		}
		s.vals = append(s.vals, orderValue(v, typ))
		s.cell[i] = nil
	}
}

// DecimalText is an order value that is a decimal the driver read as text:
// MySQL's DECIMAL, or a NUMERIC through pgx's database/sql driver.
// AfterValues binds it cast to a decimal (boundValue).
type DecimalText string

// TimeText is an order value that is a date or a time the driver read as
// text: MySQL's DATE, DATETIME or TIMESTAMP without parseTime. AfterValues
// binds it cast to DATETIME(6) on MySQL.
type TimeText string

// orderValue is v, a value the driver read into an any, as an order value:
// a driver.Valuer, such as pgx's pgtype.Numeric, as its value; text, or a
// []byte copied as text since the driver may reuse its buffer, as a
// DecimalText in a column whose database type typ is a decimal, a TimeText
// in a date or time column, and a string otherwise. A pgtype.Numeric, from
// pgx's own rows, which tell no column types, is a DecimalText.
func orderValue(v any, typ string) any {
	if v != nil && typ == "" {
		// pgx's own rows tell no column types; its pgtype.Numeric, a
		// numeric read into an any, says what it is by this method.
		if _, ok := reflect.TypeOf(v).MethodByName("NumericValue"); ok {
			typ = "NUMERIC"
		}
	}
	if dv, ok := v.(driver.Valuer); ok {
		if x, err := dv.Value(); err == nil {
			v = x
		}
	}
	var text string
	switch x := v.(type) {
	case []byte:
		text = string(x)
	case string:
		text = x
	default:
		return v
	}
	switch typ {
	case "DECIMAL", "NUMERIC", "NEWDECIMAL":
		return DecimalText(text)
	case "DATE", "DATETIME", "TIMESTAMP":
		return TimeText(text)
	}
	return text
}

// EncodeOrderValue is v, an order value (TableRows.OrderValues), as a JSON
// value that DecodeOrderValue reads back as the same value: a cursor holds
// it. NULL is nil and a bool a bool; an integer is an int64, or "u:" and
// its digits past the int64 range; a finite float is a json.Number with a
// fraction or an exponent ("3.0"), NaN and the infinities "f:NaN", "f:+Inf"
// and "f:-Inf"; text is "s:" and the text, a DecimalText "d:" and its text,
// a TimeText "dt:" and its text, and a time.Time "t:" and RFC 3339 with
// nanoseconds. Any other value is an error, and so is a DecimalText or a
// TimeText that is not as the drivers print one (decimalText and
// PostgreSQL's NaN, Infinity and -Infinity; validTimeText and MySQL's zero
// date).
func EncodeOrderValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, int64:
		return x, nil
	case int:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case uint64:
		if x > math.MaxInt64 {
			return "u:" + strconv.FormatUint(x, 10), nil
		}
		return int64(x), nil
	case uint:
		return EncodeOrderValue(uint64(x))
	case uint32:
		return int64(x), nil
	case uint16:
		return int64(x), nil
	case uint8:
		return int64(x), nil
	case float32:
		return floatOrderValue(float64(x)), nil
	case float64:
		return floatOrderValue(x), nil
	case string:
		return "s:" + x, nil
	case []byte:
		return "s:" + string(x), nil
	case DecimalText:
		if !decimalText(string(x)) && !specialDecimal(string(x)) {
			return nil, fmt.Errorf("velox: an ordering by an expression read the decimal %q, which a cursor cannot hold", string(x))
		}
		return "d:" + string(x), nil
	case TimeText:
		if !validTimeText(string(x)) && !zeroTimeText(string(x)) {
			return nil, fmt.Errorf("velox: an ordering by an expression read the time %q, which a cursor cannot hold", string(x))
		}
		return "dt:" + string(x), nil
	case time.Time:
		return "t:" + x.Format(time.RFC3339Nano), nil
	}
	return nil, fmt.Errorf("velox: an ordering by an expression read a value of the Go type %T, which a cursor cannot hold", v)
}

// floatOrderValue is f as EncodeOrderValue writes it.
func floatOrderValue(f float64) any {
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return "f:" + s
	}
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return json.Number(s)
}

// What DecodeOrderValue takes is what EncodeOrderValue writes: a cursor is
// the client's, and a value the database would cast to something else (a
// hex or NaN decimal MySQL casts to 0, a time it casts to NULL) would page
// from the wrong place. The checks are written out, not regexps, so that a
// program that never pages by an expression links no regexp package.

// digits is the count of ASCII digits s begins with.
func digits(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

// decimalText reports whether s is a decimal as MySQL and PostgreSQL print
// one: an optional minus, 1 to 200 digits, and optionally a point and 1 to
// 100 digits; no exponent.
func decimalText(s string) bool {
	s = strings.TrimPrefix(s, "-")
	n := digits(s)
	if n < 1 || n > 200 {
		return false
	}
	s = s[n:]
	if s == "" {
		return true
	}
	if s[0] != '.' {
		return false
	}
	n = digits(s[1:])
	return n >= 1 && n <= 100 && n == len(s)-1
}

// timeShape reports whether s is shaped as MySQL's DATE (2006-01-02), or
// DATETIME and TIMESTAMP (2006-01-02 15:04:05) with up to six fraction
// digits, with digit and separator bytes in place.
func timeShape(s string) bool {
	const layout = "0000-00-00 00:00:00"
	if len(s) != 10 && len(s) < len(layout) {
		return false
	}
	for i := 0; i < len(s) && i < len(layout); i++ {
		if layout[i] == '0' {
			if s[i] < '0' || s[i] > '9' {
				return false
			}
		} else if s[i] != layout[i] {
			return false
		}
	}
	if len(s) <= len(layout) {
		return true
	}
	frac := s[len(layout):]
	n := digits(frac[1:])
	return frac[0] == '.' && n >= 1 && n <= 6 && n == len(frac)-1
}

// zeroTimeText reports whether s is MySQL's zero date as it prints it:
// 0000-00-00, with 00:00:00 and up to six zero fraction digits.
func zeroTimeText(s string) bool {
	if !timeShape(s) {
		return false
	}
	for i := range len(s) {
		if c := s[i]; c != '0' && c != '-' && c != ' ' && c != ':' && c != '.' {
			return false
		}
	}
	return true
}

// jsonNumber reports whether s is a number as JSON spells one.
func jsonNumber(s string) bool {
	s = strings.TrimPrefix(s, "-")
	n := digits(s)
	if n == 0 || (n > 1 && s[0] == '0') {
		return false
	}
	s = s[n:]
	if s != "" && s[0] == '.' {
		n = digits(s[1:])
		if n == 0 {
			return false
		}
		s = s[1+n:]
	}
	if s != "" && (s[0] == 'e' || s[0] == 'E') {
		s = s[1:]
		if s != "" && (s[0] == '+' || s[0] == '-') {
			s = s[1:]
		}
		n = digits(s)
		if n == 0 {
			return false
		}
		s = s[n:]
	}
	return s == ""
}

// specialDecimal reports whether s is a PostgreSQL numeric that is not a
// number, exactly as it prints it: NaN, which sorts after every number
// and equals itself, Infinity or -Infinity.
func specialDecimal(s string) bool {
	return s == "NaN" || s == "Infinity" || s == "-Infinity"
}

// validTimeText reports whether s is a date or a time as MySQL prints one,
// and a real date and time.
func validTimeText(s string) bool {
	if !timeShape(s) {
		return false
	}
	layout := "2006-01-02"
	if len(s) > len(layout) {
		layout += " 15:04:05"
	}
	_, err := time.Parse(layout, s)
	return err == nil
}

// errOrderValue is DecodeOrderValue's error.
var errOrderValue = errors.New("velox: a value of an ordering by an expression is not one EncodeOrderValue wrote")

// DecodeOrderValue is v, a value EncodeOrderValue wrote, after a JSON round
// trip that reads a number as a json.Number, back as the order value; an
// int64 is taken as it is. Any other value is an error.
func DecodeOrderValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, int64:
		return x, nil
	case json.Number:
		if !jsonNumber(string(x)) {
			break
		}
		if n, err := strconv.ParseInt(string(x), 10, 64); err == nil {
			return n, nil
		}
		if strings.ContainsAny(string(x), ".eE") {
			if f, err := strconv.ParseFloat(string(x), 64); err == nil {
				return f, nil
			}
		}
	case string:
		return decodeTagged(x)
	}
	return nil, errOrderValue
}

// decodeTagged is DecodeOrderValue of a string: "<tag>:<text>".
func decodeTagged(x string) (any, error) {
	tag, rest, ok := strings.Cut(x, ":")
	switch {
	case !ok:
	case tag == "s":
		return rest, nil
	case tag == "d":
		if decimalText(rest) || specialDecimal(rest) {
			return DecimalText(rest), nil
		}
	case tag == "dt":
		if validTimeText(rest) || zeroTimeText(rest) {
			return TimeText(rest), nil
		}
	case tag == "u":
		if rest == "" || rest[0] < '1' || rest[0] > '9' || strings.Trim(rest, "0123456789") != "" {
			break
		}
		if n, err := strconv.ParseUint(rest, 10, 64); err == nil && n > math.MaxInt64 {
			return n, nil
		}
	case tag == "t":
		if t, err := time.Parse(time.RFC3339Nano, rest); err == nil {
			return t, nil
		}
	case tag == "f":
		switch rest {
		case "NaN":
			return math.NaN(), nil
		case "+Inf":
			return math.Inf(1), nil
		case "-Inf":
			return math.Inf(-1), nil
		}
	}
	return nil, errOrderValue
}

// width is the number of values per row, 0 for none.
func (s *orderValueScan) width() int {
	if s == nil {
		return 0
	}
	return s.n
}

// Order returns a copy of the query's orderings, in order.
func (q TableQuery) Order() []OrderTerm { return slices.Clone(q.st.OrderBy) }

// Unordered returns the query with no ordering: a caller that builds its
// own order from Order, such as a paginator, starts from it.
func (q TableQuery) Unordered() TableQuery {
	q.st.OrderBy = nil
	return q
}

// OrderValues returns the query whose All also reads the value of each
// ordering that is an expression (OrderValueTerm), beside each row, as
// AfterValues takes them back: TableRows.OrderValues. They are the values
// the driver reads into an any, a []byte copied as a string. One, First,
// Count and Exists read none.
func (q TableQuery) OrderValues() TableQuery {
	q.orderVals = true
	return q
}

// AfterValues is After for a query ordered by expressions: vals are row's
// values of the orderings that are expressions (OrderValueTerm), in order,
// as OrderValues read them with the row. Each expression is compared with
// its value in the keyset condition, the expression written there again
// with its own bound values; a nil value is NULL, placed as the ordering
// places it. The other orderings read row as After does, so the primary
// key must still end the order. The number of vals must be the number of
// such orderings, or the query fails when it runs.
func (q TableQuery) AfterValues(row any, vals []any) TableQuery {
	q = q.After(row)
	q.afterVals = slices.Clone(vals)
	if q.afterVals == nil {
		q.afterVals = []any{}
	}
	return q
}

// OrderValues returns row i's values of the query's orderings that are
// expressions, as TableQuery.OrderValues read them; nil when the query did
// not read them. The slice is the rows' own.
func (r TableRows) OrderValues(i int) []any {
	if r.nv == 0 {
		return nil
	}
	return r.vals[i*r.nv : (i+1)*r.nv : (i+1)*r.nv]
}

// scanWithValues scans the current row into dst, whose last s.width()
// destinations are the order values', and keeps them.
func scanWithValues(r Rows, dst []any, s *orderValueScan) error {
	if err := r.Scan(dst...); err != nil {
		return err
	}
	if s != nil {
		s.keep(r)
	}
	return nil
}
