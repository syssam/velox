# 58. A JSON path reads one kind of value, alike on every dialect

Status: accepted, 2026-11-08.

## Context

A JSON field's handle could set the column and test it for NULL, nothing
more: equality and order of whole documents differ by dialect, so the engine
refuses them (`errCodecCondition`). A review found that a common need, "rows
whose attrs.plan.tier is pro", then needed `Raw`. ent has `sqljson.ValueEQ`
with a `sqljson.Path`.

The dialects disagree on the value at a path. MySQL's `JSON_UNQUOTE` gives
the string `null` for a JSON null; SQLite's `json_extract` gives an integer
for a number and 1 for true; PostgreSQL's `->>` gives text for all of them.
A comparison of that value with a string would then match different rows on
each.

## Decision

- A JSON handle's `Text(keys...)` is the string at the path, and
  `Number(keys...)` the number, as a float64. Each is SQL NULL where the path
  is missing or holds a value of another kind. velox writes that with each
  dialect's own type test, so the same rows match everywhere: PostgreSQL
  `CASE WHEN jsonb_typeof(jsonb_extract_path(c, k…)) = 'string' THEN
  jsonb_extract_path_text(c, k…) END`, MySQL `JSON_TYPE(JSON_EXTRACT(c, p))`
  and `JSON_UNQUOTE`, SQLite `json_type(c, p)` and `json_extract`.
- `HasKey(keys...)` is true where the path is there, whatever its value, a
  JSON null included: `jsonb_extract_path`, `JSON_EXTRACT` and `json_type`
  are SQL NULL only for a missing path.
- `Bool(keys...)` is the boolean, NULL for a string "true" or a number 1:
  `jsonb_typeof` 'boolean', `JSON_TYPE` 'BOOLEAN', `json_type` 'true' or
  'false'.
- `JSONText` has EQ, NEQ, In, NotIn, IsNull and NotNull; `JSONNumber` has
  the comparisons, Between, IsNull and NotNull; `JSONBool` has EQ, IsNull
  and NotNull. Strings compare with case on
  every dialect: MySQL's `JSON_UNQUOTE` returns utf8mb4_bin text.
- Every key, or MySQL's and SQLite's quoted path `$."a"."b"`, is an
  argument, never SQL text. A key may not be empty or hold `"`, `\` or a
  control character, as SQLite's quoted path has no escape; such a path
  fails the statement.
- It is one node, `OpJSON`, whose kid is an ordinary leaf; the writer renders
  the leaf with the path's value in place of the column. The render cache
  keys the kind and the number of keys.

## Consequences

- A path is object keys only: an array index, a wildcard and containment
  (`@>`) stay in `Raw`.
- A number beyond 2^53 loses precision as a float64.
- PostgreSQL's column must be jsonb, as velox-gen writes it.
- `TestJSONPaths` in `internal/integration` runs every condition on
  PostgreSQL (pgx and database/sql), MySQL (with and without
  interpolateParams) and SQLite; `TestJSONPath*` in `internal/apitest` checks
  the SQL, the cache and the refusals.
