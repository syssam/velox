# 138. HAVING repeats a key as GROUP BY writes it; MySQL's DISTINCT orders by ANY_VALUE

- Status: accepted, 2026-11-23. Amends
  [0128](0128-repeated-term-placeholders.md) and
  [0129](0129-mysql-repeated-key-any-value.md), closing what they left
  open.

## Context

0128 and 0129 left two cases open: a HAVING that repeats a grouped term
with a bound value, and, on MySQL, SELECT DISTINCT ordered by a repeated
bound term. Both were run against MySQL 8.4 (a local server, with
`ONLY_FULL_GROUP_BY`, server-side prepared statements) before this change:

- PostgreSQL wrote the HAVING's copy with its own placeholders, binding the
  value again. To PostgreSQL that is another expression whose column is not
  grouped: 42803, as 0128's case was.
- MySQL's HAVING refused the copy with error 1054, "Unknown column
  'org_id' in 'having clause'". So did a key expression that binds nothing:
  `GROUP BY (org_id + 1) HAVING (org_id + 1) > 2` fails. MySQL's HAVING
  names a column outside an aggregate only when it is a key column or a
  select-list alias. `ANY_VALUE`, 0129's answer, does not lift this: it is
  1054 too. An aggregate does: `HAVING MIN((org_id * ?)) > ?` runs.
- MySQL's DISTINCT refused an ordering by the copy with error 3065 (3066
  for an aggregate): an ordering must be found in the select list, and a
  copy with its own `?` is not. An ordering by an expression that binds
  nothing failed as well, through the `x IS NULL` velox writes first on
  MySQL, which is not in the select list either:
  `SELECT DISTINCT (LOWER(a)) ... ORDER BY (LOWER(a)) IS NULL` is 3065.
  `ANY_VALUE` lifts the check, in both places.

## Decision

- A HAVING term that repeats a GROUP BY key, outside any aggregate in it,
  is written as the key is:
  - On PostgreSQL, a term with a bound value is written with the
    placeholders of the result term or key it repeats, binding nothing,
    0128's `repeatSpan`. A term inside an aggregate binds anew: there it
    need not be grouped.
  - On MySQL, a term that is a key and not a column, bound or not, is
    written as `MIN(x)`. Every row of a group has the key's value, so MIN
    of it is that value, NULL for a NULL key. A key column is written as it
    is, which MySQL accepts. Under `WITH ROLLUP` a subtotal row's key is
    NULL and MIN of a copy is not, so such a statement fails before it is
    sent, as 0129's Rollup case does.
  - "Repeats" is `sameTerm` (0128). The writer checks each term of each
    Expr in HAVING and the terms inside it, stopping at an aggregate.
- On MySQL, a DISTINCT projection's ordering by a term that is one of its
  result terms, not a column, is written as `ANY_VALUE(x)` in its `IS NULL`
  and in itself. A DISTINCT row's value of a result term is the value of
  every copy of it. The guard already refuses an ordering by a term not
  among the result terms (`distinctOrdered`), with the same equality
  (`resultTerm`). An ordering by a column is unchanged.
- `ShapeKey` adds `havingKey`: on PostgreSQL, for each HAVING term the
  writer checks, 1 plus the index of the term it repeats (`repeated`), or
  0; on MySQL, whether each is a key; and for a DISTINCT MySQL projection,
  whether each ordering term is a result term. A term the writer does not
  reach, under a repeated one, still adds its part, which keeps the key
  exact.
- Rejected: HAVING by a select-list alias on MySQL. It works only for a
  selected term with a name, and velox names none of its own. GROUP BY or
  ORDER BY ordinals were rejected in 0128 and 0129.

## Consequences

- A report that groups by a bucket with a bound value and filters the
  buckets in HAVING runs on PostgreSQL and MySQL. On MySQL so does one
  whose key binds nothing.
- On MySQL, SELECT DISTINCT ordered by a selected expression runs, bound or
  not.
- A MySQL `WITH ROLLUP` whose HAVING tests an expression key outside an
  aggregate fails; test it in Where, or use PostgreSQL.
- SQLite is unchanged.

## Evidence

- `internal/apitest/select_test.go`: `TestHavingRepeatedTerm` (the exact
  text and arguments on PostgreSQL, MySQL and SQLite, two values of one
  text sharing the cache, a key that binds nothing, a key column, the
  Rollup refusal) and `TestDistinctOrderedByTerm`.
- `internal/integration/select_test.go`: `TestHavingRepeatedTerm` and
  `TestDistinctOrderedByTerm` failed on MySQL 8.4 before this change, with
  1054 and 3065, and pass after; on SQLite they pass before and after.
  PostgreSQL runs them in the merge gate (`VELOX_TEST_POSTGRES`); they were
  not run against PostgreSQL locally.
