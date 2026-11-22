# 115. JSON: Append, Len, text patterns and ordering

Status: accepted, 2026-11-22.

## Context

ent's `sqljson` has four helpers that velox's JSON handles lacked:
`sqljson.Append` (the generated `AppendX` of a JSON slice field),
`sqljson.LenEQ` and its kin, `sqljson.StringContains`, `StringHasPrefix` and
`StringHasSuffix`, and `sqljson.OrderValue`. A port from ent wrote them as
`Raw`, which skips the policies, or could not write them. ADR 0058 gave a
JSON path a value of one kind alike on every dialect, and ADR 0080 the
elements of an array; this record extends both.

## Decision

- **Append.** `JSONCol.Append(v V)` and `NullJSONCol.Append(v V)` take the
  handle's own slice type and make an assignment of the new operator
  `AssignAppend`. An update writes:
  - PostgreSQL: `CASE WHEN c IS NULL OR jsonb_typeof(c) = 'null' THEN
    '[]'::jsonb ELSE c END || $1::jsonb`;
  - MySQL: `JSON_MERGE_PRESERVE(CASE WHEN c IS NULL OR JSON_TYPE(c) = 'NULL'
    THEN JSON_ARRAY() ELSE c END, ?)`;
  - SQLite: `json_insert(CASE … WHEN json_type(c) = 'array' THEN c ELSE
    json_array(json(c)) END, '$[#]', json(?), …)`, one `json(?)` per
    element, since SQLite has no concatenation of arrays; `json(…)` alone
    when there is none.

  A NULL column and the JSON null become the elements, as ent's Append
  makes them. A document that is no array becomes the first element of the
  result: that is what PostgreSQL's `||` and MySQL's `JSON_MERGE_PRESERVE` do,
  and SQLite is written to match. The value is encoded by the column's codec
  and must be a JSON array (nil and `null` append nothing); anything else
  fails the statement before it is sent. An insert refuses it, as it refuses
  Add and Sub. Hooks see the operator in `Statement.Set`; `Mutation.Value`
  reports no value, as for Add. The render cache keys SQLite's text by the
  slice's length; PostgreSQL's and MySQL's take one argument.
- **Len.** `Len(keys...)` on a JSON handle is a `JSONNumber`: the length of
  the array at the path, or of the column's own array with no key. It is
  NULL where the path is missing or holds no array, as
  `jsonb_array_length` refuses a scalar and MySQL's `JSON_LENGTH` counts an
  object's keys. It is a new `JSONKind`, so every `JSONNumber` condition,
  ordering and `Term` applies. PostgreSQL's length is cast to numeric, as a
  `Number` is, so that the float64 argument binds the same way.
- **Patterns.** `JSONText` has `Contains`, `HasPrefix` and `HasSuffix`, the
  string taken literally and compared with case on every dialect, as
  `JSONText`'s EQ is: LIKE with the `!` escape on PostgreSQL and on MySQL
  (whose `JSON_UNQUOTE` is utf8mb4_bin), and a GLOB on SQLite, whose LIKE
  ignores the case of ASCII letters; `*`, `?` and `[` are escaped in a
  class of their own. It is one leaf, `opJSONLike`, under `OpJSON`.
- **Ordering and terms.** `JSONNumber` and `JSONText` have `Asc` and `Desc`;
  all three path kinds have `Term()`, which Select reads, Scan scans (0116),
  GroupBy groups and an aggregate takes. It is one more term kind,
  `termJSON`, written as the conditions write the path. Text sorts in the
  database's collation, which differs by dialect, as for any string column.
- A JSON column itself is still no condition, ordering, group key or
  DISTINCT result: only a result column of a projection, read whole (0116).

## Cost

Every new method is generic with `[_ Lazy[...]]`, on JSON handles and the
path types, so a handle's own row in `TestGenericTypesCompileSmall` did
not change. With 0116, entityDescUse grew from 776888 to 777942 bytes,
though it has no JSON column and calls no Scan: the engine types its package
reaches, `Term` among them, grew. Its bound was raised to 778240; the other
bounds and the buildcost tests hold.

## Not done

- A path into `Append` (ent's `sqljson.Path` option): no consumer.
- A key of an array index or a wildcard: still `Raw` (0058).
- `Len` of a JSON text column on PostgreSQL that is `json`, not `jsonb`:
  velox-gen writes jsonb, as 0058 assumes.

## Evidence

- `internal/apitest/jsonsql_test.go`: the SQL of Append, Len, the patterns,
  the orderings and the terms on each dialect, the render cache and the
  refusals.
- `internal/integration/jsonparity_test.go`: `TestJSONLenAndPatterns` and
  `TestJSONAppend` on SQLite, and on PostgreSQL (pgx and database/sql) and
  MySQL (with and without interpolateParams) in the gate.
