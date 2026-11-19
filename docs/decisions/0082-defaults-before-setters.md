# 0082. Defaults are filled before setters and hooks; any Default is allowed

- Status: accepted, 2026-11-19. Amends [0032](0032-ent-refusals.md): its
  refusal of a non-zero Default on a bool or number (rule 36) and its
  2026-11-18 amendment (`ColumnDescriptor.ZeroInvalid`) are withdrawn.

## Context

An insert takes a struct, and a zero field there means unset. Until now the
insert filled each `Default` into a zero field after the insert's setters and
the hooks had run, just before rendering. So nothing could store a zero over a
default, not even a setter: `Insert(&g).Set(Groups.Open.Set(false))` stored
`true`. velox-gen therefore refused a non-zero `Default` or any `DefaultFunc` on
a bool, and on a number unless a check refused 0, and pointed at
`Nillable().Default(true)`, which turns the column NULL-able and the Go field
into a `*bool`.

The largest consumer's schemas, ported from ent, declare
`field.Bool(...).Default(true)` on at least 46 fields. Each would have become
a schema change with a `*bool` rippling through its code, and the port must
not change its schemas. A string already had the same trade-off without a
refusal: `Default("free")` turns a struct's `""` into `"free"`.

ent fills its defaults in `Save` before running its hooks, and a builder's
`SetActive(false)` is stored because the default goes in first.

## Decision

- An insert fills the defaults of every row first, in `Exec` and `SQL`, before
  the insert's setters (`Set`, `SetInput`) and the hooks. A value a setter, a
  hook's `Mutation.Set` or a `Row.Set` writes is stored over the default, zero
  or not. The untyped `Table` insert does the same.
- velox-gen, the engine's descriptor check and velox-graphql's model check
  accept any `Default` or `DefaultFunc` on a bool or number. A constant one is
  the column's `DEFAULT`, as in ent: `NOT NULL DEFAULT true`. Rule 36 is
  retired, and so is `ColumnDescriptor.ZeroInvalid`, which only existed to let
  the engine allow some of these defaults.
- `engine.FillDefaults` fills one row, visiting only the columns that have a
  default. It is not generic, so an entity's insert compiles no loop of its own.
- `velox.GeneratedCodeIsVersion23` replaces Version22, as `ColumnDescriptor`
  lost a field.

## Consequences

- `field.Bool("active").Default(true)` ports from ent unchanged and keeps its
  NOT NULL column. To store `false`, write
  `Insert(&x).Set(Xs.Active.Set(false))`, as ent's `SetActive(false)`.
- A struct's `Active: false` with no setter stores `true`. This is the
  struct-insert rule, and it now holds for bools and numbers as it already did
  for strings. velox-gen cannot see it.
- A GraphQL create input leaves a defaulted field optional and sets it only
  when given, so `active: false` is stored and a missing `active` takes the
  default. `graphql.CreateDefault` stays, for a field with no schema default.
- Hooks see the defaults already filled, as in ent. A hook's `Mutation.Fill`
  of a column that has a `Default` therefore does nothing; `Mutation.Set`
  stores its value. A `DefaultFunc` runs before the hooks.
- `TestDefaultUnderExplicitZero` (`internal/apitest`) covers a setter, an
  `InsertAll`'s setter, a hook's `Set` and `Fill`, and `SQL`. It fails with the
  old order. `TestBoolDefaultTrue` in `internal/codegen/testdata/e2e` reads the
  rows back from SQLite. `TestBuildBoolNumberDefaults` checks the columns'
  `DEFAULT` on every dialect, and `TestDefaultOnBoolOrNumber` checks the plugin.
