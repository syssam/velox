# 0081. One entity table per generated package

- Status: accepted, 2026-11-19. Supersedes the linking of 0039 (`velox.Linked`)
  and 0079 (`Descriptor.Links`).

## Context

A condition by name, `velox.HasEdge`, `HasEdgeWith` and `FieldInSelect`, names
no target type, so velox needs the target's metadata from somewhere. Until now
velox-gen linked it selectively: `velox.Linked[T]` on a relation, and
`Descriptor.Links` for an entity, only when it saw a constant name in the
schema package's behaviour. It refused a name it could not read.

That fails the largest consumer's port. Its access rules live in a shared
`rule/` package and take their names from configuration in the schema files
(`cfg.ChildTable`), which velox-gen neither follows nor can read. Both of the
other ways out are worse: following calls into other packages cannot read a
configuration value either, and rewriting the rules changes 13 schema files,
which the port must not do.

## Decision

- velox-gen writes one `velox.EntityTable` in `velox.go` of a package whose
  schema declares behaviour (a Policy, Hooks or Interceptors, its own or a
  mixin's): every entity, by its name in the schema, its table and its Go
  type. Every descriptor of the package names it, `Entities:
  veloxEntityTable`. It is built on first use, once for the package, so its
  cost grows with the entities, not their square.
- Only such a package has the table. A lookup by string links every entry,
  so the table defeats the linker's dead-code elimination: a service that
  uses three entities of a 60-entity package would link all 60
  (`TestServiceLinksOnlyItsEntities` and its GraphQL twin in `bench/scale`
  fail when it is in every package; tried and reverted 2026-11-19). A
  condition by name is a behaviour feature; application code names a
  relation with its typed condition (`Rel.Has`, `Rel.HasWith`), and a by-name
  condition on an entity without a table fails with that advice.
- `FieldInSelect` finds its entity in the table by its name in the schema or,
  failing that, by its table. A name that is one entity's name and another's
  table, or the table of two entities (in two database schemas), is
  ambiguous and fails the statement, as an unknown name does. `HasEdge` and
  `HasEdgeWith` find a relation's target by its Go type.
- velox-gen still checks every constant name, column, value and type, and now
  accepts a table name for `FieldInSelect` as velox does. A relation or an
  entity named by a value it cannot read is a warning, as a column already
  was, and the conditions inside such a call are not checked.
- Nothing is widened. An unknown name fails the statement, the target's Read
  policy and interceptors apply inside the `EXISTS`, and a rule that reaches
  back to its own entity is refused when the statement runs (`cycleError`),
  as velox-gen cannot see a rule outside the schema package.
- `velox.Linked`, `Descriptor.Links` and `engine.SetEdgeTarget` are removed;
  `velox.LinkEntity[T](name, table)` makes an entry of the table, and
  `EntityLink.Type` is the type's package path and name as a string: a
  `reflect.Type` field made every generic velox type about 140 KB larger, as
  every package that reaches `EntityInfo` compiled the descriptors of
  `reflect.Type`'s methods (measured by `TestGenericTypesCompileSmall`).
- `velox.GeneratedCodeIsVersion22` replaces Version21.

## Consequences

- A rule in a shared package works with names from configuration, by entity
  or by table, and the port's schema files do not change.
- A name velox-gen cannot read is checked only when its statement runs. A
  test that runs a read of each entity under a normal context finds a wrong
  name before production does.
- A package that gains or loses its only behaviour regenerates every entity's
  file, as each descriptor names the table or stops naming it.
- A program that uses one entity of a package with behaviour links every
  entity's metadata, through the table.
- A relation is still the better rule where one exists: `HasEdgeWith` names
  the domain, velox-gen checks all of it, and the parent's policy applies.
  The table is for rules whose names come from data.
- `TestEntityTableLookup` and `TestTypeKeyMatchesTargetName` in
  `internal/engine` cover the lookup and its refusals;
  `TestCheckFieldInSelectAccepts` and `TestCheckWarnsUnreadNames` in
  `internal/codegen` cover velox-gen; `TestFieldInSelect` in
  `internal/codegen/testdata/e2e` runs a rule with configured names, by table,
  on SQLite, under the target's policy.
