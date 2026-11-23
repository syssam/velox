# 0129. On MySQL a repeated GROUP BY key with a bound value is ANY_VALUE

- Status: accepted, 2026-11-23. Amends 0128, which left MySQL open.

## Context

Decision 0128 writes a repeated term with the same placeholders on
PostgreSQL. MySQL's `?` is positional and cannot be repeated, so a term with
a bound value, selected and grouped by, is two expressions to MySQL:

```sql
SELECT date_format(created_at, ?) ... GROUP BY date_format(created_at, ?)
```

Under `ONLY_FULL_GROUP_BY`, MySQL's server-side prepared statement matches
a grouped expression as it is written, and a parameter matches no other
parameter. The query failed with error 1055. An ordering by the term failed
the same way.

Options considered:

- GROUP BY the ordinal of the selected term (`GROUP BY 1`). MySQL resolves
  it to the select list's own expression, so there is one. It does not help
  a term that is grouped or ordered by but not selected. An ordering by a
  term writes `x IS NULL, x` on MySQL, and `1 IS NULL` is no ordinal. MySQL
  documents column positions as deprecated. 0128 rejected it for the same
  reasons.
- Write the select list's and the ordering's copy as `ANY_VALUE(x)`. Every
  row of a group has the key's value, so ANY_VALUE of the key is the group's
  value. `ANY_VALUE` lifts the check for its argument. 0127 uses the same
  trick for an ordering's `x IS NULL`. It covers selected and unselected
  terms, and orderings.

## Decision

- On MySQL, in a projection with GROUP BY, a result term or an ordering by
  a term that binds a value and repeats a GROUP BY key is written as
  `ANY_VALUE(x)`. A Rollup's keys count as keys. "Repeats" is `sameTerm`
  of 0128: the same text and columns, values of one type equal by `==`,
  the name aside.
  - A term that binds nothing is written as it is. Its copies are the same
    text, which MySQL matches.
  - A term that repeats no key is written as it is. If it is not grouped,
    MySQL's error 1055 is the right answer. ANY_VALUE would hide it.
  - A term with an aggregate never equals a key, so it is never wrapped.
- `ShapeKey` adds `keyRepeatKey` for a grouped MySQL projection: one byte per
  result term and per ordering by a term, whether it repeats a key. The same
  text with another value is another shape.
- Under `WITH ROLLUP`, MySQL makes a rolled-up key NULL in a subtotal row
  where the select list names the key as GROUP BY writes it. A copy with
  its own parameter is not that, so ANY_VALUE of it could show a row's
  value in a subtotal row. velox fails such a statement before it is sent.
  A Rollup of keys that bind nothing is unchanged.
- PostgreSQL and SQLite are unchanged.

## Consequences

- A term built once, with a bound value, and used in Select, GroupBy and
  OrderBy runs on MySQL. MySQL still receives the value once per place.
- A Rollup on MySQL whose key binds a value cannot be selected or ordered
  by. Writing the value into the text, as `DateTrunc` writes its zone, works.
- A HAVING that repeats a grouped term with a bound value is still written
  as it is, as on PostgreSQL (0128).
- SELECT DISTINCT with an ordering by a repeated bound term is not changed.
- Tests: `TestSelectRepeatedTermMySQL` (`internal/apitest`) checks the text,
  the arguments, the shared cache and the Rollup refusal; it failed before
  this change. `TestSelectRepeatedBoundTerm` (`internal/integration`) no
  longer skips MySQL and adds a `date_format(?, ?)` bucket.
