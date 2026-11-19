# 0080. The elements of a JSON array: Has, HasSome, HasEvery and IsEmpty

- Status: accepted, 2026-11-19.

## Context

A JSON handle tested a value at a path of object keys (ADR 0058) and left
containment of an array to `Raw`. The old velox's `OpsJSONArray` gave a JSON
slice field four where predicates, `has`, `hasSome`, `hasEvery` and `isEmpty`,
which the largest consumer annotates on 40 fields in 33 files. Its own hook
strips every where input a field does not ask for with `Filterable()`, so no
such filter ever reached its generated SDL, and ADR 0077 left the feature out
as unused. The old velox's published module has it (`v0.3.0`,
`contrib/graphql`: `OpsJSONArray = OpHas | OpHasSome | OpHasEvery |
OpIsEmpty`), so a port that keeps the annotation expects it to work.

## Decision

- A JSON handle, `JSONCol` and `NullJSONCol`, has `Has(v)`, `HasSome(vs...)`,
  `HasEvery(vs...)` and `IsEmpty()`. An element is a string, a number or a
  boolean, of any Go type of those kinds; anything else, and NaN, an infinity,
  an unsigned number above 2^63-1 and a string that is not UTF-8, fails the
  statement. The element is a parameter of type `any`, not of the slice's
  element type: the handle's `V` is the slice, and a type parameter for its
  element would give every JSON column a second one.
- `Has` is `jsonb_typeof(c) = 'array' AND c @> $1::jsonb` on PostgreSQL,
  `JSON_TYPE(c) = 'ARRAY' AND JSON_CONTAINS(c, ?)` on MySQL, and on SQLite
  `CASE WHEN json_type(c) = 'array' THEN EXISTS (SELECT 1 FROM json_each(c) AS
  je WHERE je.type = … AND je.value = ?) …`. The element is an argument, as
  JSON on the first two, and as the value on SQLite, where the JSON type of the
  element is compared too, so that the boolean `true` is not the number 1 and
  the string `"1"` is not either, as on the other two. The bare `@>` stays a
  conjunct, so a GIN index on a jsonb column serves it.
- Every form first asks for an array, because the dialects part there:
  `@>` and `JSON_CONTAINS` match a document that is that very scalar, and
  `json_each` walks the values of an object. So a document that is no array
  holds no element on all three, and is not empty.
- `IsEmpty` is true for `[]` only, as a `CASE` that asks `jsonb_typeof`,
  `JSON_TYPE` or `json_type` first, since PostgreSQL's `jsonb_array_length`
  refuses a scalar.
- A NULL column is NULL in all four, on every dialect, as a comparison is: it
  matches neither the condition nor its negation. A document that is no array
  is false, and so `Not(Has(x))` and `Not(IsEmpty())` match it.
- `HasSome` is an Or of `Has` and `HasEvery` an And of it; with no values they
  are false and true, as `In` and `NotIn` are with none. That is also what an
  empty list in a GraphQL where input means (the client sent a list of
  nothing), where the old velox left the field out.
- `untyped.PredJSONHas` and `PredJSONEmpty` are the untyped forms, for
  velox-graphql, which composes `HasSome` and `HasEvery` itself.
- velox-graphql: `OpHas`, `OpHasSome`, `OpHasEvery`, `OpIsEmpty` and
  `OpsJSONArray`. A JSON field of `[]string`, `[]int`, `[]int64`, `[]float64` or
  `[]bool` (the types whose list the SDL already types) offers
  `<field>Has: T`, `<field>HasSome: [T!]`, `<field>HasEvery: [T!]` and
  `<field>IsEmpty: Boolean`, T the element's scalar; `WhereOps()` with no
  argument, and so `Filterable()`, gives it all four, and a Nillable one adds
  `IsNil` and `NotNil` as a scalar's defaults do, which explicit ops do not.
  Any other JSON field still has no predicates and `WhereOps` on it is an
  error. The generated code calls `gqlgraph.ArrayField`, so
  `veloxgql.GeneratedCodeIsVersion24` replaces Version23.
- The cost model charges each of the four as a scan: a containment or length
  test reads the table unless an index of the database's own kind serves it.

## Consequences

- A port that keeps `WhereOps(OpsJSONArray)` on its 40 fields, and that has no
  hook like the old velox's whitelist, has 160 more fields in its where inputs
  than the old SDL, which that hook stripped. Drop the annotation where the
  filter is not wanted.
- A GIN index serves `Has` and `HasEvery` on PostgreSQL (`jsonb_ops` or
  `jsonb_path_ops`); `HasSome` is an Or of them. MySQL needs a multi-valued
  index on the path to serve `JSON_CONTAINS`, and SQLite has none.
- `internal/integration` `TestJSONArrays` runs every condition on PostgreSQL
  (pgx and database/sql), MySQL (with and without interpolateParams) and
  SQLite, over strings with escapes, `true` against 1, `"1"` against 1, 1.0
  against 1, an object where an array is expected, an empty array and a NULL
  column; `TestJSONArray*` in `internal/apitest` checks the SQL, the cache, the
  untyped forms and the refusals.

## Not done

- An element that is an object or an array, and a path into a nested array:
  containment of a structure differs by dialect (`@>` is structural on
  PostgreSQL, `JSON_CONTAINS` too on MySQL, and SQLite has neither).
- Typed elements: `Has("x")` on a `[]int` column is a valid condition that
  matches nothing, as the dialects say; only GraphQL's types make it typed.
- A by-name form for a policy (`velox.FieldHas`): no consumer needs one yet.
