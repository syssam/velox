# 0141. The elements of a JSON array at a path: Array(keys...)

- Status: accepted, 2026-11-23. Does what 0080 left under Not done for a
  path; amends nothing else in it.

## Context

ADR 0080 tests the elements of a column that is an array. The nextapp port
has two `sqljson.ValueContains(col, v, sqljson.Path(k))` calls whose array
sits inside an object: `landedcost/targets.go:665`
(`allocation_details -> receiptIds` holds a string id, OR'd with a column
test) and `promotion/offered_at.go:139` (`conditions -> include_org_ids`
holds an int, one OR term per ancestor organization). A PostgreSQL-only
`Expr` (`#> … @>`) would break the SQLite tests the application runs, and
`Len(keys...)` already takes a path but `Has` did not.

## Decision

- A JSON handle (`JSONCol`, `NullJSONCol`) has `Array(keys ...string)
  JSONArray[E]`. `JSONArray` has `Has(v)`, `HasSome(vs...)`,
  `HasEvery(vs...)` and `IsEmpty()`, as the handle's own, on the array at
  the path. `Array()` with no key is the column's array and writes exactly
  what the handle's methods write. A key is checked as every JSON path's is
  (0058): not empty, no double quote, backslash or control character.
  Elements are 0080's: a string, a number or a boolean.
- A handle method that returns a value type with the conditions, not four
  more methods taking a path: `Has(v any)` already takes one argument of
  type `any`, so `Has(v, keys...)` would read ambiguously, and one lazy
  method costs a package nothing until it is called
  (`TestGenericTypesCompileSmall`).
- The engine keeps the `OpJSONHas` and `OpJSONEmpty` nodes and puts the
  keys in the node's `W` (`engine.JSONAtPath`). The render cache keys the
  number of keys, which shapes the text; the keys are arguments.
- PostgreSQL: `(jsonb_typeof(jsonb_extract_path(c, $k…)) = 'array' AND c
  @> $doc::jsonb)`, `$doc` being `{"k1":{"k2":[elem]}}`. The `@>` of the
  whole column against a document that holds the element at the path is
  what a GIN index on the column serves, and it is false where the path is
  missing, so the conjunction is false there, and NULL only for a NULL
  column. The type test states the rule whatever `@>` answers: where a
  scalar or an object is at the path the conjunction is false, and where
  an array of objects is on the way `jsonb_extract_path` is NULL and `@>`
  false, as an object is not contained in an array.
- MySQL: `CASE WHEN JSON_TYPE(JSON_EXTRACT(c, path)) = 'ARRAY' THEN
  JSON_CONTAINS(c, elem, path) WHEN c IS NULL THEN NULL ELSE 1 = 0 END`;
  SQLite: the same `CASE` over `json_type(c, path)` and `json_each(c,
  path)`. Their tests at a missing path are NULL, so the `CASE` makes it
  false. `IsEmpty` is the `CASE` of 0080 with the path on every dialect.
- So, as in 0080: a missing path, a scalar or an object at the path, and
  an array on the way hold no element and are not empty, and `Not` matches
  them; a NULL column matches neither.

## Consequences

- The two nextapp sites port as
  `L.AllocationDetails.Array("receiptIds").Has(id)` and
  `P.Conditions.Array("include_org_ids").Has(orgID)` (or `HasSome(ids...)`
  for the OR over the ancestor chain), and run on SQLite.
- `TestJSONArrayAtPathSQL` and `TestJSONArrayAtPathForms` in
  `internal/apitest` check the text of every dialect, the render cache and
  the refusals; `TestJSONArrayAtPath` in `internal/integration` runs the
  conditions on every dialect the run has (SQLite always), over a missing
  path, a scalar and an object at the path, an array of objects on the way,
  `true` against 1 against `"1"`, an empty array and a NULL column.

## Not done

- An element that is an object or an array (0080's Not done still holds).
- A by-name form for a policy (`velox.FieldHas`); no consumer asks.
- velox-graphql where inputs on an array at a path: a GraphQL field is a
  column, not a path.
