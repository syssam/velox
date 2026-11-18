# 0075. SQL-only options are `sqlschema` annotations

- Status: accepted, 2026-11-18. Amends [0070](0070-index-method.md),
  [0072](0072-index-desc-opclass.md) and [0073](0073-db-default.md).

## Context

0070, 0072 and 0073, written the same day, spelled an index's access method, its
key order, its operator classes and a column's SQL default as builder methods:
`index.Fields(...).Method("gin")`, `.Desc(...)`, `.OpClass(...)` and
`field.Int(...).DBDefault("100")`. A schema is the source of more than SQL: the
same declaration feeds GraphQL, and gRPC and others after it. A builder method
on `index` or `field` that only PostgreSQL reads puts one backend's word in the
portable vocabulary, and a reader of `Method("gin")` cannot tell it is SQL.
The old velox and ent spell these as annotations of a package named for the
backend (`sqlschema.IndexType("GIN")`, `entsql.IndexType("GIN")`), as velox's
GraphQL options are annotations of package `graphql`.

None of the three builders had been released.

## Decision

Package `github.com/syssam/velox/dialect/sqlschema` holds the options only a
SQL database reads, with the old velox's names and fields, and the builders are
removed.

- Index: `IndexAnnotation` and `IndexType`, `IndexTypes`, `IndexWhere`, `Desc`,
  `DescColumns`, `OpClass`, `OpClassColumn`. Several on one index merge, a later
  non-zero field winning.
- Field: `Annotation` and `Default`, `DefaultExpr`, `DefaultExprs` (one SQL text
  for the three dialects, or one for each by name; `sqlite3` is read as
  `sqlite`).
- `StorageParams`, `Prefix`, `PrefixColumn` and `IncludeColumns` exist so that
  a port compiles. velox-gen refuses them, naming the index, because velox
  holds none of them.
- The loader, `schema/load`, lowers the annotations into the descriptor values
  that rules 42, 43 and 58 to 62 and the migrate model already read, so
  none of those changed except their messages, which name the annotation. `Desc()` and `OpClass(class)` apply
  to every key the index covers, and a per-key `OpClassColumn` wins over the
  all-keys one. An `IndexTypes` key other than `postgres` is refused.
- `.Where` stays a builder method, as it is a property of the index in every
  dialect that has partial indexes. `IndexWhere` is the same predicate for a
  port; setting both is refused.
- A `SchemaDefault` and a sqlschema default on one field are refused: give one.
- `Annotation` is the old velox's one type for schemas and fields, so a port
  compiles: `sqlschema.Annotation{Table: "users"}` and `sqlschema.Table("users")`
  (the consumers' commonest call, 450 times in three) lower on a schema to
  `schema.TableIn`; `Collation` and `Checks` lower to `schema.Collation` and
  `schema.Check`. `Schema` without a `Table` and the unnamed `Check` are refused:
  velox names a table with its schema, and every constraint.
- The rest of the old `Annotation` (`Skip`, `Size`, `ColumnType`, `OnDelete`,
  `OnUpdate`, `NonUniqueFK`, `Charset`, `IncrementStart`, `WithComments`) is
  declared and refused, naming velox's spelling where it has one (`field.MaxLen`,
  `field.SchemaType`, `edge.OnDelete`). `View` and `ViewFor` are not declared.
- An `IndexAnnotation` on a field, or an `Annotation` on an index, is refused,
  naming where it belongs. The old velox ignored one without a word: nextapp's
  `IDDesc` puts `sqlschema.Desc()` on a field, where it never did anything, and
  a port deletes that line.

## Consequences

- A port from the old velox changes nothing for these: `Annotations(
  sqlschema.IndexType("GIN"))` and `Annotations(sqlschema.Default("100"))` are
  what it already writes, with the import path `.../velox/dialect/sqlschema`.
- `DBDefault`'s type restriction (a number or a string) is gone: the
  annotation applies to any field. velox-gen's MySQL literal-on-text refusal
  (0073) still holds for any default that reaches the column, and a new one
  covers what the restriction used to keep out: a MySQL `datetime(6)` or
  `timestamp(6)` whose default is `CURRENT_TIMESTAMP` or `now()` without the
  column's precision, which MySQL refuses at apply, is refused in velox-gen with
  `CURRENT_TIMESTAMP(6)` named.
- velox-gen refuses a column default or an index predicate, whichever way it was
  written (`SchemaDefault`, `sqlschema.Default`, `Where`, `IndexWhere`), that
  holds `;`, `--` or `/*` outside its string literals, as the old velox refused
  them. It catches a pasted statement or a trailing comment that swallows the
  rest of a DDL line; the text is the application's, so it is not an attack
  surface.
- The `OpClass` duplicate check (rule 62, "two operator classes") no longer has
  a spelling that reaches it; it stays for a model another producer builds.
- `GeneratedCodeIsVersionN` stays: the generated code does not change.

Enforced by `internal/codegen/sqlschema_test.go`, with the earlier tests of
0070, 0072 and 0073 moved to the new spelling.
