# 174. A JSON path's EQ is containment in a PostgreSQL filter

Status: accepted, 2026-11-25. Amends 0058 (PostgreSQL's form of a JSON
path condition) and keeps 0154 (`Contains`) as it is.

## Context

A GIN index on a jsonb column, of the `jsonb_path_ops` class or the
default one, serves `@>`. velox's typed path conditions read the value
with `jsonb_extract_path` under a `CASE` on `jsonb_typeof`, which no
index serves. So nextapp called `JSONCol.Contains` on PostgreSQL, for its
index, and `Text(k).EQ` on SQLite, where `Contains` fails: a switch on
the dialect in application code.

## Decision

- On PostgreSQL, `Text`'s, `Number`'s and `Bool`'s `EQ`, `Text`'s `In`
  with values and `ValueIsNull` are written as `col @> $n::jsonb`. The
  document is the path's keys, nested, holding the value:
  `Text("plan", "tier").EQ("pro")` binds `{"plan":{"tier":"pro"}}`.
  `ValueIsNull` binds a JSON `null`. An `In` is an `Or` of one
  containment per value, padded to the IN list's length (`inLen`), so a
  bitmap scan serves each arm. The document is built when the statement
  binds; the text is cached as before.
- It is written so only where SQL NULL and false keep the same rows: in
  a filter, under an even number of `NOT`s. The writer tracks both
  (`writer.filter`, `writer.neg`). `filterPred` sets a filter, with no
  `NOT`, for a `WHERE` (select, update, delete, a load, a subquery), a
  join's `ON`, a `HAVING`, an upsert's update `WHERE`, and the conditions
  inside `EXISTS`, a many-to-many `EXISTS` and a relation aggregate's
  subquery; `OpNot` flips `neg`; `And`, `Or` and a joined relation's
  conditions keep both. A projection, an ordering and an expression
  hold no predicate; they read the typed form through `JSONTerm`.
- An upsert's conflict target `WHERE` keeps the typed form. PostgreSQL
  matches it against a partial unique index by its form.
- A path with a key that `jsonb_extract_path` reads as an array index
  (spaces, a sign, then digits only) keeps the typed form everywhere.
- The render cache keys an `OpJSON` node by whether any key is such an
  index key. `NOT` and the statement's clauses were keyed already, so a
  condition in a positive and a negated place are two shapes.
- MySQL and SQLite are unchanged. MySQL's `JSON_CONTAINS` uses no
  ordinary index, and it lets an array contain a scalar.

## Equivalence

For a path of object keys and a scalar, `col @> doc` is true exactly
where the typed `EQ` is true. In an object, containment needs each key,
and a value of the same JSON type: a string equal byte for byte, a
number equal as numeric (`1.50` is `1.5`, `1e2` is 100), a boolean, or
`null`. An array or an object never matches a scalar inside an object.
Only at the top level may an array contain a scalar, and the document
is always an object. Where the typed form is false or NULL, containment
is false, or NULL where the column is NULL.

The two differ where the typed form is NULL, and NULL and false differ
under `NOT`. Kleene's logic is monotone in `And` and `Or`, and a formula
true with a NULL leaf is true with that leaf false. So under an even
number of `NOT`s, a filter keeps the same rows either way. Under an odd
number, it would not: `Not(EQ)` would start to match a missing key.

A number: `Number.EQ` takes a float64. pgx binds a float64 to the typed
form's `numeric` as its shortest decimal (`FormatFloat(f, 'f', -1, 64)`).
The document holds the same decimal in `'g'` form, the same numeric.
On PostgreSQL both forms compare the stored number with that decimal,
so `9007199254740993` is not `EQ(9007199254740992)`, as before. NaN and
the infinities equal no JSON number. Their document is SQL NULL, which
keeps no row, as the typed form keeps none. A string or a key that is
not valid UTF-8 fails the statement. PostgreSQL refuses it as text too.

An array on the path: `jsonb_extract_path` reads a key `"0"` as an array
index, while MySQL's and SQLite's `$."0"` and containment read object
keys only. velox's paths are paths of object keys, so the object-key
reading is the portable one. A path with an index key keeps the typed
form, so on PostgreSQL its `EQ` and `Not(EQ)` stay complements over the
rows that hold a value, at the cost of the index. A non-numeric key
through an array is NULL in the typed form and false in containment, the
same in a filter.

## Consequences

- Applications use the typed handles on every dialect. A jsonb GIN index
  serves `EQ`, `In` and `ValueIsNull`, and an `Or` of them with
  `Array(k).Has(v)`, in a filter.
- A negated typed condition, `NEQ`, `NotIn`, the comparisons and
  `IsNull` are still not indexed. Neither is a path with an integer key.
- `Contains` stays the database's containment, refused on SQLite.
- Rejected: `col @> ANY($1::jsonb[])` for `In`, which binds one argument
  but leans on bitmap scans of array keys, and rewriting the typed form
  to skip arrays at integer keys, which costs a guard per key.
