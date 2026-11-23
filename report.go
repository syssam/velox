package velox

import (
	"errors"
	"fmt"
	"time"

	"github.com/syssam/velox/internal/engine"
)

// ColumnByName returns the column of entity E whose name is name, as the
// schema's column names it ("customer_id"), as a Term: a result column of
// Select, a key of GroupBy, an operand of DateTrunc, Calc and Expr, and,
// through Asc and Desc, an ordering by the column, which After and a cursor
// can follow. It is false when E has no such column, or is not an entity.
// It reads E's metadata, so code that is given a field's name, such as a
// report driven by a GraphQL request, needs no table of handles. A
// condition on a column by name is FieldEQ and the rest. E's columns, with
// their names and Go types, are untyped.TableOf[E]().Info().Columns.
func ColumnByName[E any](name string) (Term[E], bool) {
	m, err := metaOf[E]()
	if err != nil {
		return Term[E]{engine.InvalidTerm(err)}, false
	}
	i, ok := engine.ColumnNamed(m.Info, name)
	if !ok {
		return Term[E]{engine.InvalidTerm(fmt.Errorf("velox: %s: no column %q", m.Info.Name, name))}, false
	}
	return Term[E]{engine.ColumnTerm(i)}, true
}

// Column returns the name of the column t is, when t is a column of E (a
// handle given to Select, ColumnByName's term, or an ordering's Term), named
// with As or not. It is false for any other term.
func (t Term[E]) Column[_ Lazy[E]]() (string, bool) {
	i, ok := engine.TermColumn(t.t)
	if !ok {
		return "", false
	}
	m, err := metaOf[E]()
	if err != nil || int(i) >= len(m.Info.Columns) {
		return "", false
	}
	return m.Info.Columns[i].Name, true
}

// Equal reports whether t and o are the same term: the same kind, function,
// columns and text, at every depth, with bound values of one type equal by
// ==. A name from As is ignored. So an ordering's Term is equal to the
// bucket a report groups by when it orders by that bucket. A value that ==
// cannot compare, such as a slice, is equal to no other. A relation's
// aggregate (Rel's Count…), the zero Term and an invalid term are equal to
// no term. It is the rule by which velox writes a repeated term once in a
// statement.
func (t Term[E]) Equal[_ Lazy[E]](o Term[E]) bool { return engine.SameTerm(t.t, o.t) }

// Term returns what o orders by: the column, as a Term, for a handle's Asc
// or Desc, or the term of a Term's. An ordering by a column of a joined
// relation has no term of E: its Term fails the statement that holds it.
func (o Order[E]) Term[_ Lazy[E]]() Term[E] {
	switch {
	case o.t.Expr != nil:
		if t, ok := o.t.Expr.(engine.Term); ok {
			return Term[E]{t}
		}
		return Term[E]{engine.InvalidTerm(errors.New("velox: an ordering by an expression that is not a term"))}
	case o.t.Rel != 0:
		return Term[E]{engine.InvalidTerm(errors.New("velox: an ordering by a column of a joined relation has no term of the query's entity"))}
	}
	return Term[E]{engine.ColumnTerm(int32(o.t.Column))}
}

// Descending reports whether o orders from the greatest value down.
func (o Order[E]) Descending[_ Lazy[E]]() bool { return o.t.Desc }

// DateUnit is the unit DateTrunc truncates a time to.
type DateUnit = engine.DateUnit

// The units of DateTrunc.
const (
	UnitYear    = engine.DateYear    // January 1, or the fiscal year's first day
	UnitQuarter = engine.DateQuarter // January, April, July or October 1, or the fiscal quarter's
	UnitMonth   = engine.DateMonth   // the month's first day
	UnitWeek    = engine.DateWeek    // the ISO week's Monday
	UnitDay     = engine.DateDay     // midnight
	UnitHour    = engine.DateHour    // the hour's start
)

// DateOption changes what DateTrunc truncates in: InZone or FiscalYear.
type DateOption struct {
	zone   *string
	fiscal time.Month
}

// InZone truncates in the time zone name: a name of the IANA database, such
// as Europe/London, or an offset east of UTC, such as +08:00 or -05:30. ""
// and UTC are UTC, DateTrunc's zone without it. The zone is written as a
// literal, so a name may hold only letters, digits and _ + - /, and an offset
// is ±HH:MM within ±14:00; any other fails the statement. PostgreSQL takes
// both. MySQL takes both, but a name needs MySQL's time zone tables, without
// which CONVERT_TZ is NULL. SQLite has no zones: an offset is taken, and a
// name fails the statement.
func InZone(name string) DateOption { return DateOption{zone: &name} }

// FiscalYear starts DateTrunc's years and quarters in month start: with
// time.April, UnitYear truncates 2026-03-15 to 2025-04-01, and UnitQuarter
// to 2026-01-01. It applies to UnitYear and UnitQuarter only; with another
// unit it fails the statement.
func FiscalYear(start time.Month) DateOption { return DateOption{fiscal: start} }

// DateTrunc is the time t, a time column of E or a Term of one, truncated to
// the start of unit: the first instant of its year, quarter, month, ISO week,
// day or hour, in UTC unless InZone gives another zone. It is an instant
// (PostgreSQL's timestamptz, MySQL's DATETIME of UTC, SQLite's text of UTC)
// that scans into a time.Time on every dialect, and sorts and groups by
// time:
//
//	month := velox.DateTrunc[veloxgen.Invoice](velox.UnitMonth, I.CreatedAt, velox.InZone("Asia/Hong_Kong"))
//	rows := db.Query[veloxgen.Invoice]().
//		Select(month, I.Total.Sum()).GroupBy(month).OrderBy(month.Asc()).
//		Limit(24).Rows(ctx)
//
// The column must hold an instant, as velox-gen's time columns do. The
// unit, the zone and the fiscal year are written as literals, never bound,
// so the same DateTrunc is the same text in Select, GroupBy and OrderBy.
// A bad unit, zone or month, or a t that is not a time column or term of E,
// fails the statement.
func DateTrunc[E any](unit DateUnit, t Selectable[E], opts ...DateOption) Term[E] {
	zone, fiscal := "", 1
	for _, o := range opts {
		if o.zone != nil {
			zone = *o.zone
		}
		if o.fiscal != 0 {
			fiscal = int(o.fiscal)
		}
	}
	return Term[E]{engine.DateTruncTerm(unit, termOf[E](t), zone, fiscal)}
}

// GroupingSet is a key of GroupBy that adds subtotal rows: Rollup or Cube.
// It is nothing else: Select, an ordering and an Expr refuse it.
type GroupingSet[E any] struct{ t engine.Term }

// Rollup is GROUP BY ROLLUP(ts): the groups of all of ts, then a subtotal
// row for each prefix of them, ts[0] alone and so on, and a grand total. In
// a subtotal row the keys it rolls up are NULL; Calc("GROUPING(?)", key) is
// 1 there and 0 in a group's row, which tells such a NULL from a key's own.
//
//	q.Select(I.OrgID, I.Status, I.Total.Sum()).GroupBy(velox.Rollup[veloxgen.Invoice](I.OrgID, I.Status))
//
// PostgreSQL takes it among other keys of GroupBy. MySQL writes it as WITH
// ROLLUP, which rolls up every key, so there it must be GroupBy's only key.
// SQLite has no ROLLUP: the statement fails.
func Rollup[E any](ts ...Selectable[E]) GroupingSet[E] { return groupingSet[E](false, ts) }

// Cube is GROUP BY CUBE(ts): the groups of all of ts, a subtotal row for
// every subset of them, and a grand total. Only PostgreSQL has CUBE: on
// MySQL and SQLite the statement fails.
func Cube[E any](ts ...Selectable[E]) GroupingSet[E] { return groupingSet[E](true, ts) }

// groupingSet is Rollup's and Cube's work.
func groupingSet[E any](cube bool, ts []Selectable[E]) GroupingSet[E] {
	keys := make([]engine.Term, len(ts))
	for i, t := range ts {
		keys[i] = termOf[E](t)
	}
	return GroupingSet[E]{engine.GroupingTerm(cube, keys)}
}
