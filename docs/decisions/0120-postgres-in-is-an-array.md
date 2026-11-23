# 120. On PostgreSQL an In list is an array

Status: accepted, 2026-11-23.

## Context

velox wrote an In list as `col IN ($1, …, $n)`, padded to a few lengths
(`inLen`: 1, 2, 4, … 1024, then steps of 1024) so that lists of many
lengths share a few render-cache shapes.

pgx's statement cache prepares each text once, and PostgreSQL moves a
prepared statement to its generic plan after five runs. A generic plan
cannot fold `IN ($1, …, $100)` into a constant array: it keeps
`ANY (ARRAY[$1, …, $100])` and builds and scans the array of parameters for
each row. On the eager load's child query (items whose invoice_id is one of
100 keys, 503 rows, no index) the generic IN list ran in 0.551 ms, and
`invoice_id = ANY($1)` with one bigint[] parameter in 0.122 ms (its custom
plan 0.037 ms). With an index on invoice_id: the IN list 0.058 ms with
0.164 ms of planning, `ANY($1)` 0.036 ms with 0.028 ms. bench/compare's
EagerO2M100 on PostgreSQL through database/sql ran 6% behind ent and 14%
behind veloxe for this; velox through pgx's native interface was faster.

Each padded length was also a shape of its own in the render cache and a
prepared statement of its own in pgx's cache.

## Decision

- On PostgreSQL, In is `col = ANY($n)` and NotIn `col <> ALL($n)`, with one
  argument: the PostgreSQL array literal of the values, such as `{1,2,3}` or
  `{"a","b"}`. An empty list stays `1 = 0` and `1 = 1`.
- The argument is text. Every database/sql driver binds a string, and
  velox.Open takes any `*sql.DB`, including lib/pq's, which cannot encode a
  Go slice; pgx sends a string in text format whatever the parameter's type.
- The parameter has no cast. PostgreSQL types it as an array of the column's
  type, as it types each parameter of an IN list as the column's type. The
  engine knows a column's Go type, not its SQL type, which may be text,
  varchar, uuid, an enum or a domain; a cast from the Go type would compare a
  uuid or an enum column with text and fail.
- A list takes the array form when each value is nil, an int, int8 to int64,
  uint8 to uint32, or a string, or a named type of those kinds that is no
  `driver.Valuer`; PostgreSQL parses each element with the type's input
  function, as it parses a string bound for one value. A string is quoted,
  with each backslash and double quote escaped; a nil is an unquoted NULL. A
  uint or uint64 (database/sql refuses one above MaxInt64), a Valuer (a
  UUID type, a decimal), a time, a float, bytes, any other type, and a list
  that mixes one of them in, keep the IN list. So does a list inside a JSON
  path's condition.
- NULL reads as it does in an IN list: `col = ANY('{1,NULL}')` is true for 1
  and NULL otherwise, and `col <> ALL('{1,NULL}')` is never true.
- A per-owner read's keys (`Each`) take the same form.
- The render cache keys the array form without the list's length, and the
  IN form with its padded length. A Term's shape is made before the dialect
  is known, so an In list in it is keyed by both.
- A log shows the array argument when it shows the column's values, and
  hides it as Sensitive when the column is.

## Consequences

- Under `plan_cache_mode = auto` PostgreSQL keeps an IN list of parameters
  on custom plans (`pg_prepared_statements`: 0 generic, 2060 custom), which
  plan each run and hash a constant array. `= ANY($1)` moves to its generic
  plan, which plans nothing but cannot hash a parameter's array: on the
  items above, 0.006 ms planning and 0.155 ms execution, against 0.169 ms
  and 0.077 ms. Measured on PostgreSQL 18.6 through velox, the eager load
  of 100 invoices' 500 items, with both tables analyzed, ran 4.3% (pgx) and
  4.7% (database/sql) faster without an index on items.invoice_id, and
  21.6% and 14.8% faster with one. internal/integration's EagerO2M100,
  which reads tables never analyzed, ran 4% to 8% slower: without
  statistics the custom plan of the IN list was the cheaper.
- Every length of an eligible In list is one shape and one prepared
  statement on PostgreSQL. `Statement.Args` and a hook see one string where
  they saw each value; the predicates still hold the values.
- A load's chunks are still planned at the parameter limit as if each key
  were a parameter, so on PostgreSQL they err small.
- A load with an array costs two more allocations, the literal's bytes and
  its string in an `any`; `TestLoadAllocationBudgets` was raised by two.
- MySQL and SQLite have no generic plan of this kind and keep the IN list.
- `TestPostgresInIsOneArrayParameter` prepares an In read under
  `plan_cache_mode = force_generic_plan` and checks that its plan reads
  `= ANY ($1)`, and that a text literal compares with varchar, uuid and enum
  columns. `TestInArrayMatchesAnInList` checks quoted strings, the int64
  bounds and NULLs on every dialect. `TestPostgresInIsAnArray` and
  `TestPostgresInShapes` cover the forms and the cache.
