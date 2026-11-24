# 0160. An object element of a JSON array by one member's presence: HasFieldKey

- Status: accepted, 2026-11-24. Adds to 0159; amends nothing.

## Context

`HasField` (0159) asks for an object element whose member has a given
value. A query often asks only whether the member is there, such as the
tasks whose assignees all carry an `employee_id`, `[{"employee_id": 7}, …]`,
and the stored value is whatever the writer put: a number, a string, or a
JSON null where the application cleared it. `@>` cannot say "any value", and
the column's `HasKey` (0058) reads a path of object keys, not the members of
an array's elements.

## Decision

- `JSONArray` has `HasFieldKey(key string) Pred[E]`: true where the array
  holds an object with member `key` whose value is not a JSON null.
  `Array()` with no key is the column's own array, and `Array(keys...)`
  reaches an array inside the document, so the method is on `JSONArray`
  alone.
- A JSON null member counts as absent: `[{"employee_id": null}]` is false.
  A member holding 0, `""`, `false`, an object or an array is present.
  `JSONCol.HasKey` differs: it tests that a path of keys is in the document
  "whatever its value, a JSON null included" (0058), because a path that is
  there with a null is a fact about the document. An element of an array of
  records, written `null` to clear a field, is read as no value, so the two
  methods name the difference in their documentation, and neither is
  changed.
- A scalar or array element, an object without the member, and a missing
  member do not match. It is false for `[]`, where the path is missing and
  where it holds no array, and NULL where the column is NULL, so
  `Not(HasFieldKey(…))` matches the first three and no NULL column, as
  `Not(HasField(…))` does.
- `key` is checked as `HasField`'s is: one member's name, not empty, no
  double quote, backslash or control character; a bad key makes an invalid
  condition, which fails the statement.
- PostgreSQL writes `jsonb_path_exists("col", $1::jsonpath)` with
  `strict $ ? (exists(@."a"."b"[*] ? (@."k" != null)))` (`@[*]` for no path).
  The path is strict because a lax one unwraps an array met on the way and
  wraps a non-array into a one-element array, which would match what is
  not an array at the path. A strict path's structural errors (a missing
  key, a non-array, a scalar element) are unknown inside the filters and
  `exists`, so they drop an element and no error reaches the call; the
  comparison with `null` is true for every value but a JSON null. The
  function is NULL for a NULL column. No GIN index serves it: `@>` only
  asks for equal values, and the jsonpath operators `@?` and `@@` extract
  index entries from equality tests, which `!= null` is not. The
  alternatives were an `EXISTS` over `jsonb_array_elements`, which reads the
  same and is no more indexable, and `@>`, which cannot exclude null.
- MySQL writes Has's CASE, `JSON_TYPE(doc) = 'ARRAY'` then
  `COALESCE(NOT JSON_CONTAINS('[null]', JSON_EXTRACT(col, path)), 0)` with
  the path `$[*]."k"` (`$."a"."b"[*]."k"` at a path). The extraction is the
  array of the member of every element that has one, and NULL when none
  has; an array of nothing but nulls is contained in `[null]`, and any other
  member is not. No `JSON_TABLE`, whose correlated use varies by version.
- SQLite writes Has's CASE over `json_each(col[, path]) AS je` with
  `je.type = 'object' AND json_type(je.value, ?) <> 'null'`; the member is
  missing where `json_type` is NULL, and the comparison drops it.
- The engine adds `JSONHasFieldKeyNode(col, key)`, an `OpJSONHas` whose
  element has kind `JSONKindAny` and the member's name, and `JSONAtPath`
  takes it as it takes `Has`'s. The render cache already keys the kind and
  whether there is a member.
- `HasFieldKey` is a lazy generic method (`Lazy[E]`), so
  `TestGenericTypesCompileSmall` keeps its bounds.

## Verification

SQLite and MySQL 8.4 run the integration test: `[{"employee_id": 7},
{"team_id": 3}]`, `[{"team_id": 3}]`, `[{"employee_id": null}]`, `[]`,
scalar elements, a document that is no array, falsy values, a nested path,
a path that holds an object, a NULL column, and `Not` over each. The
PostgreSQL form is tested by SQL golden and by the same integration test
where `VELOX_TEST_POSTGRES` is set.

## Not done

- A member that is null counted as present: `HasKey` on a path does it.
- The untyped surface and velox-graphql offer no counterpart.
