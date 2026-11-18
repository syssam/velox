# 0072. An index key may be DESC and have an operator class

- Status: accepted, 2026-11-18. Completes 0070, which left both unheld. The spelling is amended by [0075](0075-sqlschema-annotations.md): `sqlschema.DescColumns("column")` and `sqlschema.OpClassColumn("column", class)`, not `.Desc` and `.OpClass`; the rules below stand.

## Context

0070 gave an index a `Method` and left what else PostgreSQL puts on an index
key unheld: `Inspect` noted DESC and an operator class, and compared the index
without them. That left a gap on each side. A schema could not declare a
`created_at DESC` key, which an `ORDER BY a, created_at DESC` needs: PostgreSQL
reads a single column's btree backwards, but not one that mixes directions. And
a GIN or btree index over a text column with `gin_trgm_ops`,
`text_pattern_ops` or `jsonb_path_ops` could only be made by hand, where it was
drift.

The old velox spells both as annotations (`sqlschema.Desc`, `DescColumns`,
`OpClass`, `OpClassColumn`). Its users call `Desc` three times and `OpClass`
never, so this is the same shape as `Method`: small, and the one thing an index
cannot say.

## Decision

Two more builder methods on `index`, beside `Where` and `Method`:

- `Desc(names...)` makes the named fields or edges of the index sort
  descending.
- `OpClass(name, class)` sets the operator class of one name.

Both name what the index covers, in the words of its `Fields` and `Edges`, and
both are PostgreSQL's.

- velox-gen resolves the names to columns. A name the index does not cover is
  refused (rule 60); `Desc` on a method with no order, which is any but btree,
  is refused (rule 61); a class that is not a name as `CREATE INDEX` writes one,
  or two classes for one name, is refused (rule 62). `Desc` ends in the index's
  column order, however it was called.
- MySQL and SQLite refuse both, in velox-gen and in `migrate`. MySQL 8 and
  SQLite do have descending keys; they wait for a need.
- `migrate.Index` has `Desc []string` and `OpClass map[string]string`, by
  column. They are part of an index's identity: the same columns with another
  direction or class are another index (rule 43 does not repeat), and a change
  is the planner's drop and create, built beside the old index.
- `Inspect` reads both. DESC is `indoption` bit 1. An operator class is read as
  PostgreSQL prints it, schema-qualified only when it is not on the search path;
  a class that is its type's default is read as none, so the model names only
  one that is not. A key that is ASC with NULLS FIRST, or DESC with NULLS LAST,
  stays a note.
- The apply-time guard knows both: they are part of what a statement that
  makes the index again writes, and part of what a `velox:drops` line may name
  (`key-options`).

## Consequences

- A port from the old velox keeps `Annotations(sqlschema.Desc())` and
  `sqlschema.OpClass("gin_trgm_ops")` (0075).
- A class that an extension supplies (`gin_trgm_ops` is pg_trgm's) needs the
  extension, which velox does not create: it goes into the baseline by hand, as
  0070 says of `btree_gin`. Without it the index fails when applied, not in
  velox-gen.
- A schema that names a class that is its column's default (`text_ops` on text)
  differs from the database for ever, as `Inspect` reads it as none.
  PostgreSQL's default of a type is not something velox-gen can see; the
  guide says so.
- The generated code's shape does not change, so `GeneratedCodeIsVersionN`
  stays. `schema.json` and `velox.json` gain optional `desc` and `op_class`
  fields, which an older reader never sees.
- INCLUDE columns, a collation of the key's own, NULLS FIRST or LAST and NULLS
  NOT DISTINCT stay what the model cannot hold.

Enforced by `internal/codegen/indexkeys_test.go`, `migrate/indexkeys_test.go`
(including `TestIndexKeysSwapLive`, which applies the chain of adding DESC,
naming a class and removing both to a table with rows on a real PostgreSQL), and
the round trip in `migrate/inspect_postgres_test.go`.
