# 0003. Optional stays NOT NULL; Nillable makes NULL

- Status: accepted, 2026-10-29

## Context

ent's `Optional` makes a column NULL-able. Old velox, which velox2's schema DSL
follows, keeps it NOT NULL and uses `Nillable` for NULL. One rule had to hold
across the DSL, velox-gen, the plugin model and the runtime.

## Decision

Old velox's rule. `Optional()` means the field need not be given on create: the
column stays NOT NULL, the Go field is a value, and an unset field writes its
`Default` or its zero value. `Nillable()` makes the column NULL-able, the Go
field a pointer, and adds a `Null*` handle. `Optional().Nillable()` is both. An
`Optional` JSON field is the one exception: its column is NULL-able, as a
slice or a map has no zero value to write.

## Consequences

An ent schema that relies on `Optional` for NULL needs `Nillable` added in
velox2. velox-gen refuses an `Optional` field whose zero value cannot be stored
or means nothing (an enum, bytes or time with neither a Default nor Nillable;
an edge field that is not Nillable).

## Evidence

- `Optional` and `Nillable` docs, schema/field/field.go.
- `TestTablesNullable` (internal/gen/tables_test.go:453): on every dialect
  "optional" is NOT NULL and "nillable" is NULL-able.
- `TestLowerNullable` (internal/gen/lower_test.go:1151): the plugin model and
  the core agree.
- docs/specs/2026-10-20-api-cleanup-design.md §3 ruling 1 (:72), after old
  velox's `compiler/gen/type_field.go`.
