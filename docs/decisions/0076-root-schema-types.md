# 0076. The root package has the schema types, and package dialect the dialect names

- Status: accepted, 2026-11-18.

## Context

ent's root package defines the types a schema is written with (`ent.Schema`,
`ent.Field`, `ent.Edge`, `ent.Index`, `ent.Mixin`, `ent.Annotation`), and so did
the old velox's (`velox.Schema` and the rest). velox2 put them in package
`schema` only. A port from the old velox, which says them 4,500 times in 500
files in the largest consumer, therefore compiled none of its schema files
until each was edited.

The 2026-11-04 decision against a re-export facade was about generated code
(`type X = entity.X` in `gen`), where the alias hid the shape of what
velox-gen wrote. These are the hand-written vocabulary of a schema, and ent
has them in its root.

## Decision

Package `velox` declares `Schema`, `Field`, `Edge`, `Index`, `Mixin` and
`Annotation` as aliases of package `schema`'s. They are the same types: a
schema that embeds `velox.Schema` is discovered, loaded and generated as one
that embeds `schema.Schema`, and the two spellings mix in one package.

- Package `dialect` names the dialects, `dialect.Postgres`, `MySQL` and `SQLite`,
  as the old velox's does, for the `SchemaType` map a schema writes (32 uses in
  4 files of the largest consumer). They are the strings `field.Postgres` and the
  others already were, which are now these.
- Nothing is generated from the aliases, and velox-gen's discovery already
  reads through aliases (`types.Unalias`).
- `schema` stays the home: the guide and examples keep saying `schema.Schema`,
  and the aliases are for a port and for a reader who knows ent.

## Consequences

- A port from the old velox compiles these with no edit.
- `GeneratedCodeIsVersionN` stays: the generated code does not change.
- The public API snapshot gains six aliases.

Enforced by `internal/codegen/roottypes_test.go` and the e2e fixture
`testdata/e2e/schema/note.go`, which is written with the root's types and goes
through discovery and generation.
