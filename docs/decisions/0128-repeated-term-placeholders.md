# 0128. On PostgreSQL a repeated projection term reuses its placeholders

- Status: accepted, 2026-11-23. MySQL's case is decision 0129. HAVING is
  decision [0138](0138-having-and-distinct-repeat-terms.md).

## Context

The port of nextapp's aggregate reports groups by a date bucket with a bound
time zone:

```go
month := velox.Calc[E]("date_trunc('month', ? AT TIME ZONE ?)", X.CreatedAt, zone)
q.Select(month, velox.CountAll[E]()).GroupBy(month).OrderBy(month.Asc())
```

velox bound the zone at each place: `$1` in the select list, `$2` in GROUP
BY, `$3` in ORDER BY. PostgreSQL matches a grouped expression by its text,
and to it `$1` and `$2` are two parameters. The query failed with 42803,
"column must appear in the GROUP BY clause". SELECT DISTINCT has the same
rule for ORDER BY.

Options considered:

- GROUP BY and ORDER BY the ordinal of the selected term (`GROUP BY 1`).
  It does not help a term that is grouped or ordered by but not selected. On
  MySQL and SQLite an ordering by a term writes `term IS NULL` first, and
  `1 IS NULL` is no ordinal. MySQL has deprecated column positions.
- Reuse the selected term's placeholder numbers. PostgreSQL's `$n` can stand
  at several places, and the repeat is then the same text. It covers GROUP
  BY and ORDER BY, selected or not. MySQL's and SQLite's `?` are
  positional, so it is PostgreSQL only.

Either way the text depends on whether two terms' values are equal. The
render cache keys no values, so that equality has to be in the key.

## Decision

- On PostgreSQL, in a projection, a GROUP BY term that repeats a result term
  or an earlier GROUP BY term is written with that term's placeholders and
  binds nothing. So is an ordering by a term that repeats a result term or a
  GROUP BY term. A `Rollup`'s or `Cube`'s keys count as GROUP BY terms, in
  their place. A `DateTrunc` of a term with bound values is the same as
  another of the same unit, zone and fiscal month. PostgreSQL then receives
  the value once.
- A term repeats another when `sameTerm` says so. Both have the same kind,
  function, columns and text, recursively. Their bound values are of one type
  and equal by `==`. A name from `As` is ignored. A value `==` cannot compare,
  such as a slice, is never the same as another. A relation's aggregate is
  never the same as another. Only a term that binds a value is matched.
- `ShapeKey` adds `repeatKey` for a PostgreSQL projection. For each GROUP BY
  term and each ordering by a term, it appends 1 plus the index of the
  earlier term that the term repeats, or 0. The same text with another value
  is another shape. `Fingerprint` stays a hash of the text.
- The writer's `reuse` makes `arg` write the next placeholder number of the
  earlier span without appending an argument. The cached path, which only
  collects arguments, then collects the same list.
- MySQL and SQLite are unchanged. SQLite does not check grouped expressions.
  MySQL still fails: under only_full_group_by its server-side prepared
  statement refuses the repeat with error 1055 (run against MySQL in the CI
  compose). That is open; `TestSelectRepeatedBoundTerm` skips MySQL.

## Consequences

- A term built once and used in Select, GroupBy and OrderBy runs on
  PostgreSQL.
- Two terms with equal text and values written separately, such as two calls
  to a function that builds the term, are matched too.
- A HAVING that repeats a grouped term with its own bound values still binds
  them anew. Neither the key nor the writer walks conditions for this.
- Tests: `TestSelectRepeatedBoundTerm` (`internal/integration`, PostgreSQL
  and SQLite) failed on PostgreSQL with 42803 before this change.
  `TestSelectRepeatedTermPlaceholders` (`internal/apitest`) checks the text,
  the arguments and the shared cache.
