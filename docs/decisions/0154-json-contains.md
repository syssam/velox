# 0154. A JSON document that contains another: Contains

- Status: accepted, 2026-11-24. Adds to 0080 and 0141; amends nothing.

## Context

A JSON handle tests the elements of an array (0080, 0141) and the value at a
path (0058). It cannot ask whether an object holds some keys and values, such
as `{"plan": "pro"}`, short of `Raw`. PostgreSQL's `@>` and MySQL's
`JSON_CONTAINS` answer exactly that, and ent's `sqljson.ValueContains` is
written for it. A `Raw` or an `Expr` would also leave out the handle's checks:
a column that is no JSON column, and the dialects.

## Decision

- A JSON handle (`JSONCol`, `NullJSONCol`) has `Contains(doc any) Pred[E]`:
  true where the column's document contains `doc` as the database's JSON
  containment says. An object contains an object whose every key and value it
  contains, recursively; a scalar an equal scalar; an array per the database's
  rule, which is not restated or adjusted here.
- `doc` is encoded with `encoding/json` once, when `Contains` is called, and
  bound as one argument, a string. A `doc` that does not encode (a channel, a
  function, NaN) makes an invalid condition, which fails the statement as
  `Has`'s bad element does. Encoding is the standard library's, so a struct
  with `json` tags reads as it is stored.
- PostgreSQL writes `"col" @> $1::jsonb` and MySQL `JSON_CONTAINS(col, ?)`.
  The column stands bare on the left: no function wraps it and no `->`
  reaches into it, so a GIN index on a jsonb column serves `@>`. Unlike `Has`
  there is no `jsonb_typeof` conjunct, since a scalar document contains an
  equal scalar and an object an object, which is the rule asked for. A NULL
  column is NULL on both, so neither `Contains` nor its `Not` matches it.
- SQLite has no JSON containment, and its `json_each` emulation of `Has` does
  not generalize to nested objects. `Contains` fails the statement there:
  `SQLite has no JSON containment, which Contains needs`. An emulation that
  answered some documents right and others wrong would be worse than no
  answer; an application that runs on SQLite in tests uses `Has` and `Text`,
  which all three dialects read alike, or branches on the dialect.
- The engine has `OpJSONContains` and `JSONContainsNode(col, doc)`. The
  document is an argument, so the render cache keys nothing of it.
- `Contains` is a lazy generic method like `Has` (`Lazy[V]`), so a package
  pays for it only where it is called; `TestGenericTypesCompileSmall` keeps
  its bounds. It is not added to `JSONArray`, nor given a path: a document
  at a path is `Array(keys...)` for arrays, and `Text`, `Number` and `Bool`
  for scalars.

## Not done

- The untyped surface (`untyped.PredJSONHas` and its kin) has no
  `PredJSONContains`. velox-graphql offers no `contains` input for a JSON
  field.
- A path: ent's `ValueContains(f, v, sqljson.Path(...))` with an object `v`
  has no counterpart.
