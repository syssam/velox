package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// DateUnit is the unit a DateTruncTerm truncates a time to.
type DateUnit uint8

// The units of DateTruncTerm.
const (
	DateYear DateUnit = iota + 1
	DateQuarter
	DateMonth
	DateWeek // ISO weeks: a week starts on Monday
	DateDay
	DateHour
)

var dateUnitSQL = [...]string{DateYear: "year", DateQuarter: "quarter", DateMonth: "month", DateWeek: "week", DateDay: "day", DateHour: "hour"}

// termExt is what a termDate or a termGrouping holds beyond a Term's
// common fields.
type termExt struct {
	unit   DateUnit
	zone   string // a zone's name, an offset such as +08:00, or "" for UTC
	offset int    // the offset's minutes east of UTC, when zone is one
	fiscal int    // the month a year starts in, 1 to 12
	cube   bool   // a termGrouping's: CUBE, not ROLLUP
	set    []Term // a termGrouping's keys
}

// DateTruncTerm is of, a time, truncated to the start of unit in zone and
// read as an instant: the first instant of the year, quarter, month, ISO
// week, day or hour in zone that holds of. zone is "" or UTC for UTC, a
// name of the IANA database, or an offset such as +08:00 or -05:30. fiscal
// is the month a year starts in, 1 for January; another month moves the
// years and quarters, and applies only to DateYear and DateQuarter. Every
// part is written as a literal, never bound, so the same term renders alike
// in a result column, GROUP BY and ORDER BY. A bad unit, zone, month or
// operand is an InvalidTerm.
func DateTruncTerm(unit DateUnit, of Term, zone string, fiscal int) Term {
	if unit < DateYear || unit > DateHour {
		return InvalidTerm(fmt.Errorf("velox: DateTrunc: unit %d is not one of velox's DateUnits", unit))
	}
	if fiscal < 1 || fiscal > 12 {
		return InvalidTerm(fmt.Errorf("velox: DateTrunc: a fiscal year starts in month 1 to 12, not %d", fiscal))
	}
	if fiscal != 1 && unit != DateYear && unit != DateQuarter {
		return InvalidTerm(fmt.Errorf("velox: DateTrunc: FiscalYear moves years and quarters, not %ss", dateUnitSQL[unit]))
	}
	if err := operandErr("DateTrunc", of); err != nil {
		return InvalidTerm(err)
	}
	if of.kind == termInvalid {
		return of
	}
	x := &termExt{unit: unit, fiscal: fiscal}
	if err := x.setZone(zone); err != nil {
		return InvalidTerm(err)
	}
	return Term{kind: termDate, of: &of, ext: x}.seal()
}

// operandErr is the error of a term that what (DateTrunc, Rollup or Cube)
// cannot take: none, a relation's aggregate, a window function or a
// grouping set.
func operandErr(what string, of Term) error {
	switch {
	case of.kind == termNone:
		return fmt.Errorf("velox: %s of no term", what)
	case of.kind == termGrouping:
		return fmt.Errorf("velox: %s of a Rollup or Cube, which is only a key of GroupBy", what)
	case hasRel(of):
		return fmt.Errorf("velox: %s of a relation's aggregate is not allowed", what)
	case hasWindow(of):
		return fmt.Errorf("velox: %s of a window function: SQL computes windows after grouping", what)
	}
	return nil
}

// setZone sets x's zone from s: "" or UTC, an offset ±HH:MM, or a name of
// letters, digits and _ + - /, which are all a literal may hold.
func (x *termExt) setZone(s string) error {
	switch {
	case s == "" || s == "UTC":
		return nil
	case s[0] == '+' || s[0] == '-':
		m, ok := zoneOffset(s)
		if !ok {
			return fmt.Errorf("velox: DateTrunc: the zone offset %q is not ±HH:MM within ±14:00", s)
		}
		if m != 0 {
			x.zone, x.offset = s, m
		}
		return nil
	case !zoneName(s):
		return fmt.Errorf("velox: DateTrunc: %q is not a time zone's name: a letter, then letters, digits and _ + - /, at most 64", s)
	}
	x.zone = s
	return nil
}

// zoneOffset reads s, ±HH:MM, as minutes east of UTC.
func zoneOffset(s string) (int, bool) {
	if len(s) != 6 || s[3] != ':' {
		return 0, false
	}
	h, err1 := strconv.Atoi(s[1:3])
	m, err2 := strconv.Atoi(s[4:6])
	if err1 != nil || err2 != nil || s[1] == '+' || s[4] == '+' || m > 59 || h*60+m > 14*60 {
		return 0, false
	}
	if s[0] == '-' {
		return -(h*60 + m), true
	}
	return h*60 + m, true
}

// zoneName reports whether s may be written as a zone's name in a literal.
func zoneName(s string) bool {
	if len(s) > 64 || !isIdentStart(s[0]) {
		return false
	}
	for i := range len(s) {
		c := s[i]
		if !isIdentStart(c) && (c < '0' || c > '9') && !strings.ContainsRune("+-/", rune(c)) {
			return false
		}
	}
	return true
}

// GroupingTerm is ROLLUP(keys) or, when cube is set, CUBE(keys): a key of
// GROUP BY that adds the subtotals of the keys' prefixes, or of every
// subset of them, and a grand total. It is a key of a Projection's GroupBy
// and nothing else. No keys, or a key that is an aggregate, a relation's
// aggregate, a window or another grouping set, is an InvalidTerm.
func GroupingTerm(cube bool, keys []Term) Term {
	what := "Rollup"
	if cube {
		what = "Cube"
	}
	if len(keys) == 0 {
		return InvalidTerm(fmt.Errorf("velox: %s of no keys", what))
	}
	for _, k := range keys {
		if k.kind == termInvalid {
			return k
		}
		if err := operandErr(what, k); err != nil {
			return InvalidTerm(err)
		}
		if hasAgg(k) {
			return InvalidTerm(fmt.Errorf("velox: %s of an aggregate: a group's key is a value of its rows", what))
		}
	}
	return Term{kind: termGrouping, ext: &termExt{cube: cube, set: keys}}.seal()
}

// TermColumn returns the column t is, when it is a column of the
// statement's entity, named or not.
func TermColumn(t Term) (int32, bool) { return t.col, t.kind == termColumn }

// extKey appends what shapes x to b: every part of a DateTrunc is in its
// text, and a grouping set's keys are.
func extKey(b []byte, x *termExt) []byte {
	b = append(b, byte(x.unit), byte(x.fiscal), bit(x.cube))
	b = binary.AppendUvarint(b, uint64(len(x.zone)))
	b = append(b, x.zone...)
	b = binary.AppendUvarint(b, uint64(len(x.set)))
	for _, k := range x.set {
		b = binary.AppendUvarint(b, uint64(len(k.shape)))
		b = append(b, k.shape...)
	}
	return b
}

// dateTrunc writes the termDate t for the writer's dialect.
func (w *writer) dateTrunc(t Term) {
	x := t.ext
	if o := *t.of; o.kind == termColumn {
		c := w.in.Columns[o.col]
		if typ := GoType(c); typ != nil && !timeLike(typ) {
			w.fail(fmt.Errorf("velox: %s: DateTrunc of column %s, which holds a %v, not a time", w.in.Name, c.Name, typ))
			return
		}
	}
	if w.d == SQLite && x.zone != "" && x.offset == 0 {
		w.fail(fmt.Errorf("velox: %s: DateTrunc in zone %s: SQLite has no time zones; give an offset such as +08:00, or truncate in UTC", w.in.Name, x.zone))
		return
	}
	parts := strings.Split(dateText(w.d, x), "@")
	for i, p := range parts {
		w.str(p)
		if i < len(parts)-1 {
			w.term(*t.of)
		}
	}
}

// timeLike reports whether a column of Go type typ holds a time: a
// time.Time, or a type of one, through a pointer or not.
func timeLike(typ reflect.Type) bool {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ == timeType || typ.Kind() == reflect.Struct && typ.ConvertibleTo(timeType)
}

// dateText is the SQL of a DateTrunc on dialect d, with @ where the operand
// goes. The column holds an instant: a timestamptz on PostgreSQL, a DATETIME
// of UTC on MySQL, velox's UTC text on SQLite. The value is an instant on
// each: a timestamptz, a DATETIME of UTC, and SQLite's text of UTC, which
// velox's SQLite rows parse into a time.Time.
func dateText(d Dialect, x *termExt) string {
	switch d {
	case Postgres:
		return pgDate(x)
	case MySQL:
		return mysqlDate(x)
	}
	return sqliteDate(x)
}

// fiscalStep is the months a year or quarter of x spans, and what to add to
// a month (1 to 12) so that the sum modulo step is the months since the
// start of its year or quarter: (month + 12 - fiscal) % step.
func fiscalStep(x *termExt) (step, add int) {
	step = 12
	if x.unit == DateQuarter {
		step = 3
	}
	return step, 12 - x.fiscal
}

// pgDate is dateText on PostgreSQL: date_trunc of the local time, read back
// as an instant in the same zone.
func pgDate(x *termExt) string {
	zone := "'UTC'"
	switch {
	case x.offset != 0:
		zone = "INTERVAL '" + x.zone + "'" // ISO's sign: + is east of UTC
	case x.zone != "":
		zone = "'" + x.zone + "'"
	}
	local := "(@ AT TIME ZONE " + zone + ")"
	body := "date_trunc('" + dateUnitSQL[x.unit] + "', " + local + ")"
	if x.fiscal != 1 {
		step, add := fiscalStep(x)
		body = fmt.Sprintf("(date_trunc('month', %s) - ((EXTRACT(MONTH FROM %s)::int + %d) %% %d) * INTERVAL '1 month')", local, local, add, step)
	}
	return "(" + body + " AT TIME ZONE " + zone + ")"
}

// mysqlDate is dateText on MySQL. A zone is CONVERT_TZ's, from and back to
// UTC; a zone's name needs MySQL's time zone tables, without which
// CONVERT_TZ is NULL.
func mysqlDate(x *termExt) string {
	local := "@"
	if x.zone != "" {
		local = "CONVERT_TZ(@, '+00:00', '" + x.zone + "')"
	}
	var body string
	switch x.unit {
	case DateYear, DateQuarter:
		if x.unit == DateYear && x.fiscal == 1 {
			body = "CAST(DATE_FORMAT(" + local + ", '%Y-01-01') AS DATETIME)"
			break
		}
		step, add := fiscalStep(x)
		body = fmt.Sprintf("(CAST(DATE_FORMAT(%s, '%%Y-%%m-01') AS DATETIME) - INTERVAL ((MONTH(%s) + %d) %% %d) MONTH)", local, local, add, step)
	case DateMonth:
		body = "CAST(DATE_FORMAT(" + local + ", '%Y-%m-01') AS DATETIME)"
	case DateWeek:
		body = "CAST(DATE(" + local + ") - INTERVAL WEEKDAY(" + local + ") DAY AS DATETIME)"
	case DateDay:
		body = "CAST(DATE(" + local + ") AS DATETIME)"
	case DateHour:
		body = "CAST(DATE_FORMAT(" + local + ", '%Y-%m-%d %H:00:00') AS DATETIME)"
	}
	if x.zone != "" {
		return "CONVERT_TZ(" + body + ", '" + x.zone + "', '+00:00')"
	}
	return body
}

// sqliteDate is dateText on SQLite, whose date functions read velox's text
// of a time. An offset moves the time before the truncation and back after
// it; SQLite has no zone names.
func sqliteDate(x *termExt) string {
	local := "@"
	if x.offset != 0 {
		local = "@, '" + signed(x.offset) + " minutes'"
	}
	var body string
	switch x.unit {
	case DateYear, DateQuarter:
		if x.unit == DateYear && x.fiscal == 1 {
			body = "datetime(" + local + ", 'start of year')"
			break
		}
		step, add := fiscalStep(x)
		body = fmt.Sprintf("datetime(%s, 'start of month', '-' || ((CAST(strftime('%%m', %s) AS INTEGER) + %d) %% %d) || ' months')", local, local, add, step)
	case DateMonth:
		body = "datetime(" + local + ", 'start of month')"
	case DateWeek:
		body = "datetime(" + local + ", 'start of day', '-6 days', 'weekday 1')"
	case DateDay:
		body = "datetime(" + local + ", 'start of day')"
	case DateHour:
		body = "strftime('%Y-%m-%d %H:00:00', " + local + ")"
	}
	if x.offset != 0 {
		return "datetime(" + body + ", '" + signed(-x.offset) + " minutes')"
	}
	return body
}

// signed is n with its sign.
func signed(n int) string {
	if n >= 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// groupBy writes GROUP BY ts, a Rollup or Cube among them as the dialect
// has it: PostgreSQL's ROLLUP and CUBE anywhere among the keys, MySQL's
// WITH ROLLUP after them, which rolls up every key, so only when the Rollup
// is the only key. SQLite has neither.
func (w *writer) groupBy(ts []Term) {
	w.str(" GROUP BY ")
	for i, t := range ts {
		if t.kind != termGrouping {
			continue
		}
		switch {
		case w.d == SQLite:
			w.fail(fmt.Errorf("velox: %s: SQLite has no ROLLUP or CUBE; group by the keys, and add the subtotals in Go or with another query", w.in.Name))
			return
		case w.d == MySQL && t.ext.cube:
			w.fail(fmt.Errorf("velox: %s: MySQL has no CUBE, only WITH ROLLUP; use Rollup, or PostgreSQL", w.in.Name))
			return
		case w.d == MySQL && len(ts) != 1:
			w.fail(fmt.Errorf("velox: %s: MySQL's WITH ROLLUP rolls up every key of GROUP BY, so a Rollup must be GroupBy's only key there; put every key in it", w.in.Name))
			return
		case w.d == MySQL:
			w.terms(ts[i].ext.set)
			w.str(" WITH ROLLUP")
			return
		}
	}
	for i, t := range ts {
		if i > 0 {
			w.str(", ")
		}
		if t.kind == termGrouping {
			w.grouping(t)
			continue
		}
		w.term(t)
	}
}

// grouping writes the termGrouping t as PostgreSQL has it.
func (w *writer) grouping(t Term) {
	if t.ext.cube {
		w.str("CUBE(")
	} else {
		w.str("ROLLUP(")
	}
	w.terms(t.ext.set)
	w.str(")")
}

// errGroupingPlace is the error of a Rollup or Cube anywhere but GroupBy.
var errGroupingPlace = errors.New("velox: a Rollup or Cube is a key of GroupBy, and nothing else: select or order by its keys")
