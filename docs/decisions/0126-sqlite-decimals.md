# 0126. SQLite rounds a decimal sum to 15 digits and binds a decimal in an Expr as a number

- Status: accepted, 2026-11-23.
  Amended by [0131](0131-decimal-expression-terms.md).

## Context

The port of nextapp's aggregate reports found two SQLite bugs. Both come from
SQLite having no decimal type.

A decimal column on SQLite is `NUMERIC`. Its affinity stores a decimal's text
as an INTEGER when it is a whole number that fits. Otherwise it stores a REAL,
the double nearest the text. A double keeps 15 significant digits of any
decimal (DBL_DIG). `TestDecimalNumberCol` already skips 18 digits on SQLite.

1. A `decimal.Decimal` is a Valuer of text, so SQLite binds it as TEXT. A
   comparison with a column converts the text by the column's affinity. An
   aggregate or an arithmetic expression has no affinity. SQLite orders TEXT
   after every number, so `SUM(x) > '1'` was false for every group, with no
   error. `x * 2 > '200'` failed the same way.
2. SQLite computes `SUM` and `AVG` of REALs in floating point. `Sum` over 0.1
   and 0.2 returned 0.30000000000000004. veloxe summed the loaded rows in Go
   and got 0.3.

Options considered for 1:

- Write `CAST(? AS NUMERIC)` around a decimal argument. The text then depends
  on the argument's Go type. The render cache keys no values or types of an
  Expr's arguments. Expressions inside a `Term` are sealed before any dialect
  or entity is known, and the key builder does not recurse through them.
  Keying it would touch every key path.
- Bind the value as the number SQLite would store. The text does not change,
  so the cache, `ShapeKey` and `Fingerprint` stay as they are. A whole number
  becomes an int64, which is exact. Any other number becomes a float64. That
  is what the column stores for the same text, for any decimal of up to 15
  significant digits. Beyond 15, SQLite stores no more digits either.

Options considered for 2:

- Sum in Go over the rows. That works for `Sum` only, not for a `Sum()` term
  in a projection, `SumBy` or a HAVING, and it reads every row.
- SQLite's `decimal` extension (`decimal_sum`). modernc.org/sqlite does not
  build it: "no such function: decimal_sum".
- Round to the column's scale. The descriptor's `Decimal` holds MySQL's
  precision and scale, `{65, 30}` when MySQL names none. SQLite's DDL holds
  no scale.
- Round the REAL to 15 significant digits in SQL. `printf('%!.15g', x)`
  prints them, and `CAST(... AS REAL)` makes the text a number again. This is
  core SQLite in every driver. A sum or average of decimals with at most 15
  significant digits is then exact. SQLite's `SUM` uses Kahan-Babuska-Neumaier
  summation (3.43 and later), so its error stays near one unit in the last
  place.

## Decision

- On SQLite, the SUM and AVG of a decimal column are written as
  `CASE WHEN typeof(agg) = 'real' THEN CAST(printf('%!.15g', agg) AS REAL)
  ELSE agg END`. A decimal column is one whose descriptor sets `Decimal`.
  velox-gen sets it on every Other field held in a decimal column. The rule
  covers `Sum`, `Avg`, `SumBy`, `AvgBy`, their `Null` forms, a column's
  `Sum()` and `Avg()` term wherever it stands, and a relation's `Sum` and
  `Avg`.
  - A sum of INTEGERs stays the exact integer. NULL stays NULL.
  - The value is still a number. A HAVING compares it and an ordering sorts
    it as one. `HAVING SUM(x) = 0.3` holds.
  - The scan gets a float64, as for any REAL column on SQLite. A decimal
    type's Scan already reads one there.
  - A sum of an expression, such as `Calc("? * ?", qty, price).Sum()`, is
    not rounded. velox cannot tell that it is a decimal; `Term.Decimal`
    marks one (decision 0131).
- On SQLite, an argument of an `Expr`, `Calc` or `SetExpr` whose Go type is
  that of a decimal column of the entity being written (or a non-nil pointer
  to it) is bound as `sqliteNumber` makes it:
  - an int64 for a whole number that fits;
  - otherwise a float64, for text that is a plain decimal number (sign,
    digits, a point, an exponent);
  - anything else as it was: Inf, NaN, hexadecimal, or text that is no
    number.
  - The value is read through its `Value` method, or as a string kind.
- A value of a type no decimal column of the entity holds is bound as given.
  So is every value outside an Expr. A column's own comparisons, `In` and
  writes already convert by the column's affinity.
- PostgreSQL and MySQL are unchanged.

## Consequences

- nextapp's port can drop its `CAST(? AS NUMERIC)` in HAVING and its Go-side
  sum in `invoicedQuantities`.
- A sum or average that needs more than 15 significant digits is rounded
  there on SQLite. The values it adds were stored with 15, so SQLite never
  held those digits exactly.
- A hook or log sees the converted number, not the decimal, for such an
  argument on SQLite.
- Tests: `TestDecimalSumExact` and `TestDecimalExprArgument`
  (`internal/integration`, every dialect) both failed on SQLite before this
  change. `TestDecimalSQLiteSQL` and `TestSQLiteNumber` (`internal/apitest`)
  check the text and the bound values.
