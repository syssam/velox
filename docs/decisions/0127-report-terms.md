# 0127. Report terms: ColumnByName, DateTrunc, Rollup and Cube, a readable Order

- Status: accepted, 2026-11-23.

## Context

nextapp's report engine answers GraphQL aggregate queries: a group by
field names, a date bucket per field, an optional `ROLLUP` or `CUBE`, an
ordering by fields or aggregates, and OFFSET paging that needs a total
order. Its port onto velox (`aggregate/engine.go` in the private bed)
needed four things velox lacked:

- A table, per entity, from each field's name to its handle
  (`sales_order.go`). velox already resolves names at run time for
  conditions and mutations (`FieldEQ`, `Mutation.Set`, decision 0040), but
  had no term for a column by name.
- About 150 lines of date bucket SQL per dialect, built with `Calc`. The
  zone had to be a literal: a bound zone is `$1` in SELECT and `$2` in
  GROUP BY, two expressions to PostgreSQL.
- `ROLLUP(?, ?)` as a `Calc` over the keys, PostgreSQL only.
- Field names carried beside each `velox.Order`, because an Order is
  opaque: nothing reads its column or direction back.

## Decision

**`velox.ColumnByName[E](name) (Term[E], bool)`** returns the column whose
schema name is `name` as a `Term`. It reads E's metadata, as a condition by
name does, so it is one generic function in the root and adds nothing to
generated code (a generated `XHandles.ByName` would cost every entity a
map and a method). The name is the column's, as `FieldEQ` takes it: a
GraphQL name maps to it by the engine's own rule. False for an unknown
name or a type that is no entity, whose term fails the statement.

A Term was chosen over a handle: a handle's type depends on the column's Go
type, which a name does not say. A Term is a result column, a key, an
operand of `DateTrunc`, `Calc` and `Expr`, and an ordering. A term that is a
column now orders as its handle does (`Asc` makes the column's
`OrderTerm`), so After and a cursor follow an ordering by name, and
`org.Desc() == I.OrgID.Desc()`.

**`velox.DateTrunc[E](unit, t, opts...) Term[E]`** truncates a time to the
start of `UnitYear`, `UnitQuarter`, `UnitMonth`, `UnitWeek` (ISO, Monday),
`UnitDay` or `UnitHour`, in UTC or `InZone(name)`, with
`FiscalYear(start)` moving years and quarters. Its value is an instant: a
timestamptz on PostgreSQL, a DATETIME of UTC on MySQL, SQLite's text of
UTC, which velox's SQLite rows parse. So it scans into a `time.Time` on
every dialect, sorts and groups by time, and the caller formats labels in
its own zone. A text label per dialect (`2026-Q1`) was rejected: its format
would be velox's, not the report's.

Of nextapp's sixteen buckets, these six and the fiscal year are the
truncations. The rest are parts of a date (month of the year, day of the
week, hour of the day, week of the month), whose values are integers, not
instants; they stay a `Calc` (`EXTRACT` on PostgreSQL and MySQL,
`strftime` on SQLite). The fiscal year's naming by its first or last year
is a label, so the application's.

Every part is a literal, never bound: the unit from a closed enum, the
zone validated (a name of letters, digits and `_ + - /`, at most 64; or
`±HH:MM` within `±14:00`), the fiscal month a number. The same DateTrunc is
then the same text in Select, GroupBy and OrderBy whatever the binding of
arguments does. A bound zone would wait on the fix of repeated parameters
in a PostgreSQL GROUP BY.

Per dialect:

- PostgreSQL: `date_trunc(unit, t AT TIME ZONE z) AT TIME ZONE z`, with
  `z` `'UTC'`, the name, or `INTERVAL '+08:00'` for an offset (a text
  offset has POSIX's inverted sign). The column must be a timestamptz, as
  velox-gen's are.
- MySQL: the truncation of `CONVERT_TZ(t, '+00:00', z)`, cast to DATETIME,
  then `CONVERT_TZ` back. A zone's name needs MySQL's time zone tables,
  without which `CONVERT_TZ` is NULL; velox does not check them, as a
  check would cost a round trip, and the official image loads them.
- SQLite: `datetime()` and `strftime()` modifiers. An offset is a
  `'+480 minutes'` modifier before and after. A zone's name fails the
  statement: SQLite has no zone database.

A fiscal year or quarter starting in month `m` is the start of its month
less `(month + 12 - m) % 12` (or `% 3`) months, the same arithmetic on
each dialect.

**`velox.Rollup[E](keys...)` and `velox.Cube[E](keys...)`** return a
`GroupingSet[E]`, a key of `GroupBy` and nothing else: Select, an ordering
and an Expr refuse it. PostgreSQL writes `ROLLUP(…)` and `CUBE(…)` among
other keys. MySQL has only `WITH ROLLUP`, which rolls up every key of the
GROUP BY, so there a Rollup must be the only key, and a Cube fails.
SQLite has neither and fails. `GROUPING(key)` is a `Calc`, as `GROUPING`
is plain SQL on both. A distinct type was chosen over a Term, so that no
`Sum` or `Asc` is offered on it.

**`Order[E].Term()` and `Descending()`, and `Term[E].Column()`** read an
ordering back: its term (the column's term for a handle's ordering), its
direction, and the name of the column a term is. An ordering by a joined
relation's column has no term of E; its Term fails the statement.

**`RawRow.Columns()`** returns the result's column names, as `Raw` reads
them: a term named with `As` by its name, a column by its own.

## Consequences

The port's field table, bucket SQL, ROLLUP text and field names beside each
Order can go. The engine still maps GraphQL names to column names.

`untyped` gains none of these; its `Term` is engine's, so an untyped
DateTrunc is a small addition when a caller needs one.

On MySQL, an ordering by an expression puts NULL last with `x IS NULL, x`.
ONLY_FULL_GROUP_BY refuses `x IS NULL` when x is a GROUP BY key that is an
expression: only the key as GROUP BY writes it may appear. In a grouped
projection the NULL test of a term with no aggregate is therefore
`ANY_VALUE(x) IS NULL`, which is the group's value. Grouping by a `Calc`
and ordering by it had the same failure before this decision.

No generated code changes; the root's `Term` and `Order` gain generic
methods.
