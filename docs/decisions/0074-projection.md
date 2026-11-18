# 74. Project reads terms, and keeps the policies

Status: accepted, 2026-11-18.

## Context

`SumBy` and its kin (0022) group by one key and return one aggregate. A
report needs more: several keys, several aggregates, `HAVING`, an ordering by
an aggregate or by an expression, `COUNT(DISTINCT x)`. Those went to `Raw`,
which skips every Read policy, scope and interceptor (0060, 0069). nextapp's
aggregate service, business reports and custom-object sort are all of this
shape.

`Expr` (0069) is a condition. A report also needs the same text as a result
column, a group key and an ordering, and an aggregate to compare in `HAVING`.

## Decision

- One value, `velox.Term[E]`, is a column (`Pick`), an expression (`Calc`,
  written as `Expr`'s text is) or an aggregate of either (`Sum`, `Avg`,
  `Min`, `Max`, `Count`, `CountDistinct` as methods, and `CountAll`). It is
  a result column, a group key, an operand of `Expr` (a `HAVING` is an
  `Expr` of aggregate terms) and, through `Asc` and `Desc`, an ordering.
- `Query.Project(terms...)` makes the query read terms in place of the
  entity's columns, `GroupBy(terms...)` groups them and `Having(preds...)`
  filters the groups. `Query.Rows(ctx)` reads the rows by position as
  `RawRows` (0060): the entity cannot be built from them.
- A projection is a read of E like any other. `Where`, the Read policy, the
  scope and the interceptors apply to it in the same statement, so a grouped
  read counts only the rows the caller may see. `Limit` or `AllowUnbounded`
  is needed, and the row cap holds.
- A projection refuses what reads entities or rows of the entity: `All`,
  `One`, `First`, `Iter`, `Count`, `Exists`, the aggregates, `Columns`, `Omit`,
  `With`, `Join`, `After`, a lock and a per-owner read. `GroupBy` and
  `Having` without `Project` are errors. An aggregate of an aggregate
  (`SUM(MAX(x))`), the zero `Term` and a term of another entity's column fail
  the statement before anything is sent.
- `Statement.Project` carries a `*Projection{Terms, GroupBy, Having}`. A
  Rewrite hook may not change it, as it may not change `Each`. Typed and
  untyped statements are guarded alike, in `guard`, since a typed query does
  not pass `checkColumns`.
- An ordering by a term is an `OrderTerm.Expr`. It sorts NULL last,
  ascending, as every ordering does: PostgreSQL does so, and elsewhere a
  `term IS NULL` goes first. `After` and cursors refuse it, as they read each
  ordering term's value from a row.
- The render cache keys a term by its shape, sealed when it is made: kind,
  function, column and text pieces. The key builder appends the string and
  does not recurse, because a recursive function over terms and expressions
  made the key buffer escape to the heap and cost an allocation on every
  statement (`TestLoadAllocationBudgets`). Values are arguments, so one shape
  serves every value.
- `Expr`'s slots now hold a column, a value or a term (`ExprSlot`).

## Cost

An entity's package carries about 3.4% more bytes (`entityDescUse` from
781242 to 808034): the four new methods of `Query` and the larger types its
generic bodies reach. `Term`'s methods are generic with `[_ Lazy[E]]`, as the
others are, so a package that does not use projections pays only their
declarations. The bounds in `compilecost_test.go` were raised, and the
allocation budgets were not.

## Not covered

- A subquery on another table by name, with that table's policies: `HasWith`
  and `InSelect` (0057) cover tables with a relation or a handle.
- Window functions (`COUNT(*) OVER`), `UNION` and CTEs.
- Scanning rows into a struct: `Rows` returns positions, scanned by the caller.
- An untyped (`untyped.TableQuery`) form of `Project`.
