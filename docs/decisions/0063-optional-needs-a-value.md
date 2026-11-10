# 0063. An Optional field has a Default or is Nillable

- Status: accepted, 2026-11-10. Amends [0003](0003-nullability.md): `Optional`
  stays NOT NULL and `Nillable` makes NULL, but an unset field no longer
  writes the Go zero value.

## Context

Each option should mean one thing. `Nillable` is NULL. `Default` is the value
an unset field takes. `Optional` is that the field need not be given on
create. 0003 let `Optional` carry a second meaning: an unset field with no
`Default` wrote its Go zero value (`""`, `0`, `false`) and the migration
planner gave its column a `DEFAULT` of the same. velox-gen then needed a
refusal for each type whose zero is no value (an enum, bytes, time, an edge
field) and one for a `Unique` field, whose unset rows would all hold it.

Old velox does not write the zero. Its `FeatureAutoDefault` does, is off by
default and is Alpha: without it an `Optional` field with no `Default` is left
out of the INSERT, which a NOT NULL column refuses at run time, on the
dialects that enforce it.

## Decision

velox-gen refuses an `Optional` field that is not `Nillable` and has no
`Default`, `DefaultFunc` or `SchemaDefault` (rule 37, which now covers every
type; rules 39 and 40 are gone). An `Optional` JSON field is the exception, as
in 0003: its column is NULL-able. Rule 54 stays for a `Unique` `Optional` field
whose only value is a literal `Default`.

A string that may be empty is `Optional().Default("")`, a number
`Optional().Default(0)`, a bool `Optional().Default(false)`. A `Default` that
is the zero value is allowed on a bool or number, as rule 36 says. The
migration planner writes a column `DEFAULT` from a `Default` or a
`SchemaDefault` only, no longer from the zero value of an `Optional` field.

The runtime is unchanged: a zero field still means unset, and an insert fills
its `Default` or `DefaultFunc`.

## Consequences

An ent schema that has `field.String("bio").Optional()` writes
`Optional().Nillable()` for NULL, or `Optional().Default("")` for an empty
string. A field that has an `Optional` and a `DefaultFunc` has no column
`DEFAULT`; a migration that adds it to a table with rows needs one, as for a
required field.

## Evidence

- `TestOptionalNeedsAValue` (internal/codegen/build_test.go): one message for
  a string, number, bool, enum, bytes and time field; passes for a `Nillable`,
  a `Default`, a `DefaultFunc` and a JSON field.
- `TestBuildUniqueOptionalRefused` (internal/codegen/build_refusals_test.go):
  rule 37 for a bare `Unique` `Optional`, rule 54 with a `Default`.
- `TestTablesDefaults` (internal/codegen/tables_test.go): a column `DEFAULT`
  comes from a `Default`, as before.
- Old velox's `FeatureAutoDefault` (compiler/gen/feature.go): the zero fill, off
  by default.
