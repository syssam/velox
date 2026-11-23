# 139. The generated package names each entity in a constant, TypeX

- Status: accepted, 2026-11-23.

## Context

ent's generated `mutation.go` declares one constant per node,
`const ( TypeCustomer = "Customer" … )`. veloxe generated the same in its
package. The nextapp port uses them about 2,760 times: the entity type an
audit row stores, not-found errors, and the `switch entity` of a
`velox.WithFilter` filter. velox-gen wrote none, so each port spelled the
names out as string literals, which nothing checks.

A filter receives an entity's name as `entity`. `velox.HasColumn` takes the
same name, and so does the entity table's `velox.LinkEntity`. It is the
entity's Go type name, a join entity's too.

## Decision

- `velox.go` of the generated package declares, after the version guard,
  one const block with `TypeX = "X"` for each entity X: the schema's
  entities and the many-to-many join entities, sorted by name. The value is
  the name a `velox.Filter` receives for a statement on X, which
  `TestTypeConstantIsFiltersEntity` checks for every entity of
  `internal/testmodel`, its join entity included.
- velox-gen writes one package (decision 0042); its entities share it.
  The constants go in that package, as ent's go in its root package.
- `TypeX` is a top-level name of the package, so velox-gen's check of
  declared names covers it: a schema, enum type, handles variable or other
  generated name equal to `TypeX` is refused, naming both, such as
  `type constant TypeBox collides with schema TypeBox`.
- The constants are untyped strings, as ent's are, so they compare with
  `entity`, `m.Entity()` and an audit column without a conversion.
- Generated code changes shape: `GeneratedCodeIsVersion29`.

## Consequences

- A port keeps `gen.TypeX` where veloxe had it, with the generated
  package's new name.
- A schema whose name is `Type` followed by another entity's name, or an
  entity named `Type` with an enum field named after another entity, is
  refused until one is renamed.
- The block adds one constant per entity to `velox.go`. Constants cost no
  code or data. `scripts/scale-check.sh -n 500 -s 100` on darwin/arm64:
  cold build 5.86x the reference, schema edit 5.23x, within the 2026-11-22
  calibration's runs (5.84 to 6.32, 4.15 to 5.37) and its thresholds.
