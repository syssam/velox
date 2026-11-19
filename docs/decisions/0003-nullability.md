# 0003. Optional stays NOT NULL; Nillable makes NULL

- Status: accepted, 2026-10-29; amended by [0063](0063-optional-needs-a-value.md): an `Optional` field has a `Default` or is `Nillable`, and an unset one no longer writes the zero value; [0090](0090-veloxe-defaults.md) superseded 0063: an unset `Optional` field is its zero value, as veloxe wrote it, and an `Optional` enum needs a `Default` or `Nillable`

## Context

ent's `Optional` makes a column NULL-able. veloxe, which velox's schema DSL
follows, keeps it NOT NULL and uses `Nillable` for NULL. One rule had to hold
across the DSL, velox-gen, the plugin model and the runtime.

## Decision

veloxe's rule. `Optional()` means the field need not be given on create: the
column stays NOT NULL, the Go field is a value, and an unset field writes its
`Default` or its zero value. `Nillable()` makes the column NULL-able, the Go
field a pointer, and adds a `Null*` handle. `Optional().Nillable()` is both. An
`Optional` JSON field is the one exception: its column is NULL-able, as a
slice or a map has no zero value to write.

## Consequences

An ent schema that relies on `Optional` for NULL needs `Nillable` added in
velox. velox-gen refuses an `Optional` field whose zero value cannot be stored
or means nothing (an enum, bytes or time with neither a Default nor Nillable;
an edge field that is not Nillable), and a `Unique` `Optional` field that is not
`Nillable` and has no `DefaultFunc` or `SchemaDefault`, whose unset rows would all
hold one value, so that the second fails on the index.

## Evidence

- `Optional` and `Nillable` docs, schema/field/field.go.
- `TestTablesNullable` (internal/codegen/tables_test.go:453): on every dialect
  "optional" is NOT NULL and "nillable" is NULL-able.
- `TestLowerNullable` (internal/codegen/lower_test.go:1151): the plugin model and
  the core agree.
- docs/history/specs/2026-10-20-api-cleanup-design.md §3 ruling 1 (:72), after old
  velox's `compiler/gen/type_field.go`.
