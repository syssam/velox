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
	"regexp"
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
// it, read beside the row.
func valueSource(in *EntityInfo, o OrderTerm, v any, d Dialect) (afterSource, error) {
	if err := checkOrderValueTerm(in, o); err != nil {
		return afterSource{}, err
	}
	return afterSource{info: in, value: true, v: v, d: d}, nil
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
		return zeroTimeText.MatchString(string(x))
	case time.Time:
		return x.IsZero()
	}
	return false
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
	if first {
		return valueExpr("?"+cmp+b, t, arg), true
	}
	return valueExpr("(?"+cmp+b+" OR ? IS NULL)", t, arg, t), true
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
		if !decimalText.MatchString(string(x)) && !specialDecimal(string(x)) {
			return nil, fmt.Errorf("velox: an ordering by an expression read the decimal %q, which a cursor cannot hold", string(x))
		}
		return "d:" + string(x), nil
	case TimeText:
		if !validTimeText(string(x)) && !zeroTimeText.MatchString(string(x)) {
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

// What DecodeOrderValue takes, as EncodeOrderValue writes it: a cursor is
// the client's, and a value the database would cast to something else (a
// hex or NaN decimal MySQL casts to 0, a time it casts to NULL) would page
// from the wrong place.
var (
	// decimalText is a decimal as MySQL and PostgreSQL print one: digits,
	// a sign and a point, no exponent.
	decimalText = regexp.MustCompile(`^-?[0-9]{1,200}(\.[0-9]{1,100})?$`)
	// timeText is MySQL's DATE, or DATETIME and TIMESTAMP with up to six
	// fraction digits.
	timeText = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}( [0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]{1,6})?)?$`)
	// zeroTimeText is MySQL's zero date as it prints it, with up to six
	// zero fraction digits.
	zeroTimeText = regexp.MustCompile(`^0000-00-00( 00:00:00(\.0{1,6})?)?$`)
	// jsonNumber is a JSON number.
	jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)
)

// specialDecimal reports whether s is a PostgreSQL numeric that is not a
// number, exactly as it prints it: NaN, which sorts after every number
// and equals itself, Infinity or -Infinity.
func specialDecimal(s string) bool {
	return s == "NaN" || s == "Infinity" || s == "-Infinity"
}

// validTimeText reports whether s is a date or a time as MySQL prints one,
// and a real date and time.
func validTimeText(s string) bool {
	if !timeText.MatchString(s) {
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
		if !jsonNumber.MatchString(string(x)) {
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
		if decimalText.MatchString(rest) || specialDecimal(rest) {
			return DecimalText(rest), nil
		}
	case tag == "dt":
		if validTimeText(rest) || zeroTimeText.MatchString(rest) {
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
