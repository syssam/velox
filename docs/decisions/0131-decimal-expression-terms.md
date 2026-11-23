# 0131. An expression is decimal when Term.Decimal marks it

- Status: accepted, 2026-11-23. Amends 0126.

## Context

Decision 0126 rounds, on SQLite, the SUM and AVG of a decimal column to 15
significant digits, the digits SQLite stores of a decimal. It left the sum
of an expression unrounded: `Calc("? * ?", qty, price).Sum()` over 0.1 and
0.2 times 3 gave 0.9000000000000001. velox could not tell that the
expression is a decimal.

Options considered:

- Infer from the operands: an expression whose column operands are all
  decimal columns. The usual line total multiplies an integer quantity by a
  decimal price, so the rule would miss the case it is for.
- Infer from any decimal operand. It would catch `qty * price`, but also
  `length(?)` or `? > 0` of a decimal, whose values are no decimals. The
  rounding then depends on which functions the text calls, which velox does
  not parse. That is magic a reader cannot see.
- Let the expression say so: `Term.Decimal()`. The caller knows its type.
  A column is decimal by its descriptor (`Decimal`); an expression is by
  this mark. Nothing changes for a term without it.

## Decision

- `Term[E].Decimal()` marks an expression (a `Calc`, or an Expr term) as
  holding decimals. On SQLite, its SUM and AVG are then written as 0126
  writes a decimal column's: `CASE WHEN typeof(agg) = 'real' THEN
  CAST(printf('%!.15g', agg) AS REAL) ELSE agg END`. That covers its
  `Sum()` and `Avg()` term wherever it stands.
- The mark does nothing else. The expression's own value per row, its MIN
  and MAX, and every other dialect are written as before.
- Decimal of a term that is not an expression fails the statement: a column
  is decimal by its schema, and an aggregate by what it aggregates.
- velox infers nothing from an expression's operands.
- The mark is part of the term's shape, so the render cache keys it, and
  `sameTerm` (`Term.Equal`) tells a marked term from an unmarked one: on
  SQLite their sums are other text.

## Consequences

- A report on SQLite sums `Calc("? * ?", X.Qty, X.Price).Decimal()` exactly
  for values of up to 15 significant digits.
- A caller who forgets the mark gets the floating-point sum, as before.
- Per-row arithmetic on SQLite is still floating point; `0.1 * 3` selected
  without an aggregate is 0.30000000000000004.
- Tests: `TestDecimalSumExact` (`internal/integration`) sums and averages a
  marked expression; on SQLite it failed without the rounding, with
  0.9000000000000001 and 0.45000000000000007. `TestDecimalTermSQL`
  (`internal/apitest`) checks the text, the shared cache, the refusal and
  Equal.
