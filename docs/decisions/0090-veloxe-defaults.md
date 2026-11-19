# 0090. veloxe's defaults are velox-gen's

- Status: accepted, 2026-11-19. Supersedes [0063](0063-optional-needs-a-value.md).
  Amends [0003](0003-nullability.md) and [0083](0083-package-index-names.md).
  The user chose that veloxe's behaviour is
  velox-gen's default for every application, with no option to ask for it.

## Context

The largest consumer's veloxe schema package must pass velox-gen unchanged,
its `mixin.go` byte for byte, and its database must show no diff. An earlier
plan hung veloxe's rules on `schema.Package` (0083). The user rejected an
option: a veloxe application writes nothing extra, and velox has one
behaviour. veloxe's DDL is ent's (its `dialect/sql/schema` copies ent
v0.14.6's), so the column types and defaults below are also ent's where ent
has one. The index names are veloxe's applications', not ent's: an ent
database's first diff renames its indexes, and a foreign key whose name was
too long, which ent capped with MD5 and velox caps with veloxe's FNV, and
sets the zero default of its NOT NULL string columns
(cmd/velox-gen's TestMigrateBaselineEnt pins these changes on all three
dialects).

What the consumer's schemas need, and what velox-gen refused or did
otherwise before:

- About 58 `Optional` fields with no `Default` and not `Nillable`: about 29
  scalars, which rule 37 refused (0063), and about 29 JSON structs, which rule
  48 refused. veloxe wrote an unset one as its zero value.
- Enums stored as veloxe and ent store them. velox-gen made a PostgreSQL enum
  type for each, and a `CHECK` on SQLite and on a MySQL string `SchemaType`.
- `sqlschema.NonUniqueFK` on 10 to-one edges to their own schema, which the
  loader refused. veloxe dropped the key's `UNIQUE`.
- `MaintenanceOrder`'s `ForeignIDOptionalImmutable` key on an edge that is
  not `Immutable`, which rule 34 refused.
- `AuditLog.changed_fields`, `field.JSON` of an unnamed `[]struct{...}`, which
  the loader refused as a type velox-gen could not write (N-V25).
- Index names prefixed with the table and capped with FNV-1 128, which 0083
  made an option.
- A string, bool or number column that is NOT NULL and has no `Default` had
  no column `DEFAULT`; veloxe's `FeatureAutoDefault`, which the consumer
  enabled, gave it its zero.

## Decision

- **An unset `Optional` field is its zero value.** Rule 37 holds only for an
  enum, whose zero `""` is none of its values: an `Optional` enum still needs
  a `Default` or `Nillable`, as veloxe left it unset. Every other `Optional`
  field, not `Nillable`, with no `Default`, is written as its Go zero, `""`,
  `0`, `false`, a zero time or nil bytes. velox's insert already writes every
  column, and a validator lets an `Optional` field's zero pass.
- **An `Optional` JSON field of any Go type is accepted** (rule 48 is gone). Its
  column stays NULL-able, as ent's and veloxe's are. Unset, a slice or a map
  is written empty, `[]` or `{}`, as veloxe zero-filled it: velox-gen gives
  its column descriptor a `DefaultFunc` that returns the empty value. An
  update that sets nil writes NULL, which reads back nil. A struct is written
  as its zero value's JSON; NULL reads back the zero value. A pointer is
  written as NULL when nil.
- **A NOT NULL column gets its zero as its `DEFAULT`**, as `FeatureAutoDefault`
  gave it: a string, bool or number field that is not `Nillable`, has no
  `Default` or `DefaultFunc`, and is not the ID or an implicit foreign key,
  required or `Optional`. `''`, `false` (`0` on MySQL and SQLite), `0`. An
  enum, JSON, time, UUID, bytes or `Other` column gets none, as in veloxe.
  `migrate diff` can then add such a column to a table that has rows.
- **An unsized string is `character varying` on PostgreSQL**, as in ent and
  veloxe; `field.Text` and a `MaxLen` over 10485760 are `text`. MySQL keeps
  `varchar(255)`, SQLite `text`.
- **An enum is stored as ent and veloxe store it**:
  - PostgreSQL: `character varying`, with no `CHECK` and no enum type. ent
    keeps varchar "for backwards compatibility with previous versions of ent"
    (veloxe `dialect/sql/schema/postgres.go:136`).
  - MySQL: a native `enum('a','b',...)` of the field's values (`mysql.go:196`).
  - SQLite: `text`, with no `CHECK` (`sqlite.go:118`).

  A PostgreSQL enum type is made only when the field's Postgres `SchemaType`
  names a type that is not a string type, as veloxe's Atlas made one for such
  a `SchemaType`; a string `SchemaType` on MySQL gives that type. Removed:
  the derived PostgreSQL enum types (`<table>_<column>`, or the snake case of
  an `EnumOf` type's name, shared by two fields of one type), and every enum
  `CHECK` (`<table>_<column>_check`) on SQLite and on a MySQL string
  `SchemaType`. A changed value list is an `ALTER` of the column's `enum(...)`
  on MySQL and nothing on PostgreSQL or SQLite, as with ent: there the values
  live only in Go. The migrate package, inspection and drift are unchanged:
  a database ent or veloxe created inspects as varchar and text with no
  check, which is what velox-gen now expects, so it shows no diff.
- **`sqlschema.NonUniqueFK` is accepted on an edge**, with veloxe's meaning:
  the edge's foreign key has no `UNIQUE`. On a unidirectional to-one edge to
  its own schema, which velox otherwise makes a bidirectional one-to-one
  relation whose partner is re-pointed, it makes the relation many-to-one:
  a plain key many rows may hold, as veloxe wrote a key bound to a field. On
  a one-to-one pair, on either edge, the pair stays one-to-one and its key is
  not unique. The loader refuses it on a field, an index or a schema.
- **An `Immutable` key on an edge that is not makes the edge `Immutable`**
  (rule 34's second case is gone): the key cannot change, so neither can the
  edge. An `Immutable` edge on a mutable key is still refused.
- **`field.JSON` of an unnamed struct is written inline** (N-V25), its tags
  included, if each field is exported and of a type velox-gen can write,
  checked recursively; an unexported field is refused by name.
- **Index names are prefixed and capped as veloxe's were, always**: 0083's
  rule without its option. Each index is `<table>_<name>` unless its name
  starts with `<table>_`, and every derived or prefixed name over the
  dialect's limit is capped with veloxe's FNV-1 128 suffix. `schema.Package`,
  `schema.VeloxeNames`, `load.MarshalPackage`, `ddl.VeloxeSymbol` and
  `loadjson.Options` are gone; `loadjson.Version` is 8, for the edge's
  `non_unique_fk`.

## Alternatives

- Keep the rules behind `schema.Package`: rejected by the user; a veloxe
  application would need a file it did not have, and velox two behaviours.
- Zero-fill an `Optional` enum with its first value: veloxe did not, and a
  value the schema never named would be stored.
- Keep PostgreSQL enum types: an ent or veloxe database has varchar columns,
  and every one would diff as an `alter-type`.

## Consequences

- The consumer's schema package passes velox-gen unchanged (about 505
  schemas), and the DDL matches veloxe's: varchar enums, zero `DEFAULT`s,
  prefixed index names, non-unique `NonUniqueFK` keys.
- velox-gen's own goldens changed: index names, `character varying`, zero
  defaults, no enum types or checks. Each example has a new migration,
  `veloxe_defaults`, that brings an existing database to them; on
  PostgreSQL, tracker's changes its two enum columns to varchar under
  `-allow alter-type`.
- An application that wants the old refusal of an `Optional` field with no
  value has none; it gives the field `Nillable` to tell unset from zero.
- Generated code differs only in the `DefaultFunc` of an `Optional` JSON
  slice or map and in an inline struct type; the runtime is unchanged, so
  `GeneratedCodeIsVersion` is not bumped.
