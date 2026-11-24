# 0164. The entities of a generated package: velox.Entities

- Status: accepted, 2026-11-24. Builds on 0081; amends nothing.

## Context

An application's dynamic aggregate layer maps a physical table name to its
primary-key columns, for every entity: 72 of its tables do not follow
`<table>_id`. An ent application reads the generated `migrate.Tables`.
velox had no public list: `EntityTable` (0081) is for generated code and
keeps its entries private, and `EntityInfo` was outside the 1.0 promise.

## Decision

- `velox.Entities[E any, PE Entity[E]]() ([]*EntityInfo, error)` returns
  the metadata of every entity of E's generated package, read from the
  table E's descriptor names (`Descriptor.Entities`). Any one entity of the
  package names it. No generated code changes.
- The entities are sorted by `Name`, the Go type, so the order does not
  depend on velox-gen's listing.
- Each call returns copies: the `EntityInfo`, its `Columns`, `PrimaryKey`
  and `Indexes` (and each index's columns), and a `Default` that holds
  references, deep-copied as an insert copies it. A caller may change them;
  velox's shared metadata stays as it was.
- E's own build error is returned as it is. Any other entity's error fails
  the call, every such error joined, and no entities are returned with
  them: a partial list would silently drop a table.
- A package whose schema declares no behaviour has no table (0081 keeps it
  out of such packages for dead-code elimination), nor does an entity
  written by hand: `Entities` returns the error that says so. The table is
  not added to every package for this, as 0081's measurement still holds.
- `EntityInfo`'s `Name`, `Table`, `Schema`, `Columns`, `PrimaryKey` and
  `Indexes`, and `ColumnInfo`'s `Field`, `Name`, `SchemaName` and
  `Nullable`, are inside the 1.0 compatibility promise. The rest of both
  types stays outside it. The primary key is reached as indexes into
  `Columns`, which were public already; no new method.
- No lookup by name is added: a caller builds its own map from the slice,
  by table or by name, as it needs.

## Consequences

- An application's table-to-key map is a loop over one call, with no
  hand-kept list.
- `Entities` is generic only in its constraint; its body calls the
  non-generic `engine.PackageEntities`, so a package that calls it compiles
  no more than one small instantiation.
- `TestEntitiesListsThePackage`, `TestEntitiesReturnsCopies`,
  `TestEntitiesCopiesDefaults` and `TestEntitiesRefuses` in
  `internal/apitest` cover the list, a composite key of non-`id` columns
  (an edge schema's), the copies and the errors.
- A package whose schemas declare no behaviour has no entity table, so
  `Entities` returns an error there. Writing the table into every package
  was built and measured, and is not done: in bench/scale at 500 entities
  and 100 services, alternated with its base, it raised cold build from
  7.00x and 6.82x to 9.51x and 9.86x, and schema edit and rebuild from
  6.46x and 6.14x to 9.07x and 9.24x, about 40% more build CPU. Each
  entity's link instantiates the generic metadata reader, which a package
  without behaviour otherwise never compiles.
- If a behaviour-free package needs the list, the cheaper way is for
  velox-gen to write a plain table of string literals (entity, table, key
  columns) into velox.go, read through one non-generic function, with no
  per-entity instantiation. It changes generated code's shape, so it needs
  a version bump and the same alternated bench/scale measurement first.
