# 0070. An index has a Method

- Status: accepted, 2026-11-18. The spelling is amended by [0075](0075-sqlschema-annotations.md): `sqlschema.IndexType("GIN")`, not `.Method("gin")`; the rules below stand.

## Context

velox could not make a GIN index. `migrate.Index` held a name, columns,
`Unique` and `Where`, and the PostgreSQL reader noted any other method as
something the model "cannot hold" and compared the index without it. An
application with a jsonb column, whose containment and key tests need GIN,
had no way to declare the index, and a hand-made one was drift.

The need is real. An application ported from the old velox declares 230 GIN
indexes, 229 of them `index.Fields("custom_fields")` on a JSON field. ent
and the old velox spell it as an annotation, `entsql.IndexType("GIN")` and
`sqlschema.IndexType("GIN")`. velox already spells the one index option it
took from them, the predicate, as a builder method: `Where`.

velox-gen also refused every JSON field in an index (rule 42), which a GIN
index over a jsonb column is for.

## Decision

`sqlschema.IndexType("GIN")`, an annotation of the index, sets the access method
(first written as a builder method, `.Method("gin")`, and changed by 0075).

- The methods are PostgreSQL's core ones: btree, hash, gin, gist, spgist and
  brin, in any case in the schema (rule 58).
- It is PostgreSQL's alone. velox-gen refuses a Method on MySQL and on SQLite,
  as it refuses `Where` on MySQL; so does `migrate` for a model that sets one.
- `btree` is the default and is read back as no method: velox-gen writes
  `Method: ""` for it, and `Inspect` reads a btree index as `""`, so the two
  never differ.
- A unique index has Method btree or none (rule 59): PostgreSQL enforces
  uniqueness in btree only.
- A GIN index may cover a JSON field. Any other method over one is still
  refused (rule 42), and its message names `sqlschema.IndexType("GIN")`.
- The method is part of an index's identity. The same columns with another
  method are another index (rule 43 does not repeat), and a change of method
  is the planner's drop and create, built beside the old index and renamed on
  a table that exists.
- `Inspect` reads the method into `Index.Method`. It no longer notes it.

## Consequences

- A port from ent or the old velox keeps `Annotations(sqlschema.IndexType("GIN"))`
  (0075).
- INCLUDE, a collation and NULLS NOT DISTINCT stay what the model cannot hold:
  `Inspect` still notes them. The operator class and DESC were left too, and
  [0072](0072-index-desc-opclass.md) holds them: a GIN index over a column with
  no default operator class, such as text with `gin_trgm_ops`, is
  `sqlschema.OpClass("gin_trgm_ops")`.
- The generated code's shape does not change, so `GeneratedCodeIsVersionN`
  stays. `schema.json` and `velox.json` gain an optional `method` field, which
  an older reader never sees.

Enforced by `internal/codegen/indexmethod_test.go`,
`migrate/indexmethod_test.go`, and the PostgreSQL round trip in
`migrate/inspect_postgres_test.go` (a GIN and a hash index of the hand-made
schema apply, inspect and drift with no change).
