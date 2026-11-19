# 0090. veloxe's defaults are velox-gen's

- Status: accepted, 2026-11-19. Supersedes [0063](0063-optional-needs-a-value.md).
  Amends [0003](0003-nullability.md), [0083](0083-package-index-names.md)
  and [0001](0001-ent-names.md). Amended 2026-11-19: foreign-key indexes
  and JSON pointers.
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
  as its zero value's JSON; NULL reads back the zero value. Unset, a
  pointer is written as its element's zero value's JSON, by a `DefaultFunc`
  that returns `new(T)`; see "Amendment" below.
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

## Amendment, 2026-11-19: foreign-key indexes and JSON pointers

The user ruled that foreign-key indexes follow veloxe.

- **velox-gen declares no index of a foreign key.** Before, it added one on
  PostgreSQL and SQLite, `<table>_<column>`, to each foreign-key column that
  neither the primary key nor a full index led. ent and veloxe never
  declared one, so an ent or veloxe database's first diff added an index
  per such key: hundreds for the consumer's about 1,325 edges. Removed with
  it: the objects that listed the index and its `_velox_new` helper for name
  clashes, and the rule that gave a partial index the hash of its `Where`
  when its name was that of such an index.
- **MySQL is unchanged.** InnoDB makes an index for a key that no index
  leads, named as the key, and an inspection of an ent or veloxe database
  shows it. velox-gen's MySQL tables keep that index, as before: it is what
  the database holds, and the migrate package wants each MySQL key indexed,
  so that it never drops a key's last index (error 1553).
- **An index the schema does not declare is dropped, as before.** The
  migrate package is unchanged. A database velox created earlier holds the
  old indexes: the next `migrate diff` from its directory drops them,
  `migrate drift` against a newer snapshot reports each as a `drop-index`,
  and each example has a new migration, `drop_fk_indexes`, that drops them.
  On PostgreSQL it runs online, `DROP INDEX CONCURRENTLY`.
- **An unset `Optional` JSON pointer is written as its element's zero.**
  veloxe's generator dropped the pointer of `field.JSON("x", &T{})`: its
  field held a `T`, and `FeatureAutoDefault` set an unset one to `T{}`
  (veloxe `compiler/gen/sql/create.go`, genCreateAutoDefaults, and
  `entity_helpers.go`, jsonFieldZeroValue). velox keeps the field a `*T`
  and gives its column descriptor a `DefaultFunc` that returns `new(T)`, so
  the stored JSON is veloxe's. NULL, which an update can set, reads back
  nil.

Evidence: cmd/velox-gen's `TestMigrateBaselineEnt` holds no `add-index` on
any dialect; `TestTablesIndexes` and `TestDDLNames` in internal/codegen;
the "nil JSON pointer" case of internal/integration's
`TestBindingRoundTrip`.

An index's type (`sqlschema.IndexType`) is ignored on SQLite, which has one
kind of index: ent's and veloxe's SQLite migration never read it, so an
application whose JSON indexes are GIN on PostgreSQL gets plain indexes on
SQLite. velox-gen refused it before. MySQL still refuses it: veloxe wrote
`USING GIN`, which MySQL rejects when the migration runs. Evidence:
`TestIndexMethodOutsidePostgres` in internal/codegen; the consumer's 252 GIN
indexes no longer stop its SQLite DDL.

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
  slice, map or pointer and in an inline struct type; the runtime is unchanged, so
  `GeneratedCodeIsVersion` is not bumped.
