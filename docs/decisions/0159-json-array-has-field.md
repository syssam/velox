# 0159. An object element of a JSON array by one member: HasField

- Status: accepted, 2026-11-24. Adds to 0080, 0141 and 0154; amends
  nothing.

## Context

A column often holds a JSON array of objects, such as a task's assignees,
`[{"employee_id": 7, "role": "owner"}, …]`, and a query asks for the rows
whose array has an element with `employee_id = 7`. `Has` (0080, 0141) finds
scalar elements only. `Contains` (0154) answers it on PostgreSQL and MySQL
with `[{"employee_id": 7}]`, but refuses SQLite, where applications run
their tests. ent writes it as `sqljson.ValueContains` with an object, which
reads differently on each dialect and compares `json_each` values as text
on SQLite.

## Decision

- `JSONArray` has `HasField(key string, v any) Pred[E]`: true where the
  array holds an object whose member `key` is `v`. `v` is what `Has` takes
  (a string, a number or a boolean of any Go type of those kinds) and
  compares alike: 7 matches 7.0, `"7"` is not 7, `true` is not 1. An element
  that is no object, or lacks the member, or holds an object there, does not
  match. `c.Array()` with no key is the column's own array, and
  `Array(keys...)` reaches an array inside the document, so the method is on
  `JSONArray` alone and no handle method is added.
- `key` is one member's name, checked as a path key is (0058): not empty, no
  double quote, backslash or control character. A bad key or value makes an
  invalid condition, which fails the statement.
- It is false where the path is missing or holds no array, and for `[]`;
  NULL where the column is NULL. So `Not(HasField(…))` matches an empty
  array, a missing path and a document that is no array, and no NULL
  column, as `Not(Has(…))` does.
- PostgreSQL writes `"col" @> $1::jsonb` with `[{"key": v}]`, wrapped in
  the path's objects (`{"a":{"b":[{"key": v}]}}`), the column bare on the
  left so that a GIN index on it serves the condition. Only an array
  contains an array, so no `jsonb_typeof` conjunct is needed.
- MySQL writes `Has`'s form with the object `{"key": v}` for the element:
  `JSON_TYPE(col) = 'ARRAY' AND JSON_CONTAINS(col, ?)`, or at a path the
  CASE with `JSON_CONTAINS(col, ?, path)`. Its containment also finds `v` in
  a member that is an array (`{"key": [7]}`), and an object in an array
  nested in the array; PostgreSQL and SQLite do not. That difference is
  documented and tested rather than paid for with a second, unindexable
  form.
- SQLite writes `Has`'s CASE over `json_each(col[, path]) AS je` with
  `je.type = 'object' AND json_type(je.value, ?) <type> AND
  json_extract(je.value, ?) = ?`, the member's path `$."key"` an argument;
  a boolean is tested by its JSON type alone, as `Has` does.
- The engine adds `JSONHasFieldNode(col, key, v)`, an `OpJSONHas` whose
  element carries the member's name. `JSONAtPath` takes it as it takes
  `Has`'s. The render cache keys whether there is a member, not its name,
  which is an argument on every dialect.
- `HasField` is a lazy generic method (`Lazy[E]`), so
  `TestGenericTypesCompileSmall` keeps its bounds.

## Not done

- `HasSomeField`/`HasEveryField` and several members at once: `Or` and
  `And` of `HasField` write them; one element holding two given members is
  `Contains` on PostgreSQL and MySQL.
- The untyped surface and velox-graphql offer no counterpart.
