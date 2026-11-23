# 124. An explicit NULL in an insert is stored as NULL

- Status: accepted, 2026-11-23. Amends [0093](0093-lean-inserts.md)'s
  rule that a nil nullable column is left out, and replaces
  [0119](0119-enum-placement-and-default-docs.md)'s silence on `SetNull`.
  Amended by [0136](0136-explicit-zero-and-empty-patch.md): a setter's
  zero is written as its NULL is (`InsertSet` replaces `InsertNulls`).

## Context

A field declared `Nillable().Default(x)` has a nullable column whose DDL
says `DEFAULT x`. An insert with `Xs.F.SetNull()` stored x, not NULL.

The cause was 0093. The setter wrote nil into the field, and the insert
left out a column every row holds nil in. The column's `DEFAULT` then
applied. This held for a constant `Default`, which velox-gen also writes
as the column's `DEFAULT`, and for a `DEFAULT` the schema declares with
`sqlschema.Default` or `field.SchemaDefault`. Only a default function,
which has no `DEFAULT`, stored NULL. 0119 kept the generated sentence for
such a field silent on `SetNull` for this reason.

The user ruled: `SetNull` stores NULL, never the default, in `Insert`,
`InsertAll` and an upsert's inserted rows, whatever default the field
has. An `Input` whose `Setters` give `SetNull` counts the same through
`SetInput`. Two cases were left open: a nil pointer field in the inserted
struct with no setter, and `SetPtr(nil)`.

ent decides both by whether the builder was told. Its create builder
fills a field's default in `defaults()` when `m.X()` reports the field
unset. Only `SetX` sets it. `SetNillableX(nil)` sets nothing, and ent's
create builder has no `ClearX`. So in ent a Nillable field with a
`Default` gets the default unless a value is set. velox has no "set" bit
on a struct field (0093). velox's `fillDefaults` already reads a nil
pointer field as unset and fills the `Default` into it (0082).
`SetPtr(nil)` is ent's `SetNillableX(nil)` (0099), and sets nothing in an
update.

## Decision

- A nil pointer field, or a nil slice or map in a nullable column, that
  no setter names is unset. It gets its default, as in ent: a `Default` or
  default function fills it, and a column the insert leaves out gets its
  `DEFAULT`. Lean inserts (0093) still leave such a column out.
- `SetPtr(nil)` sets nothing in an insert, as in an update and as ent's
  `SetNillableX(nil)`: the field stays unset and gets its default. 0099
  holds unchanged, its upsert rule included. nextapp calls `SetNillableX`
  2,829 times; a meaning that differed between insert and update would
  change those call sites without a word.
- A column setter of the insert that stores nil is an explicit NULL:
  `SetNull()`. The last setter of a column decides, so `SetNull()`
  followed by `Set(v)` stores v.
- An explicit NULL's column is written in the INSERT as `NULL`. It is
  neither left out nor written as the `DEFAULT` keyword in a multi-row
  insert, and it splits no SQLite run (`DefaultRuns`). Nothing is read
  back for it: the entity's nil is what is stored. A hook that then sets
  a value in the field gets that value written.
- The engine carries the columns in the insert's statement
  (`SetExplicitNulls`, from `Setters.InsertNulls`). A column written as
  NULL is in `Statement.Columns`, and the render cache's key holds the
  columns and which of them are the literal NULL, so an insert of the
  column as NULL and one that leaves it out are two shapes.
- velox-gen's sentence for a Nillable field with a default names the
  setter: "An insert of nil stores the default, 1.5;
  Users.Score.SetNull() in the insert stores NULL." It changes comments
  only; `GeneratedCodeIsVersion28` stays.

## Consequences

- `SetNull()` in an insert means what it says on every dialect, for every
  kind of default.
- `SetPtr(p)` keeps one meaning in inserts and updates: a nil `p` is "not
  given". To store NULL from a nil pointer, test it and use `SetNull()`.
- An insert of an explicit NULL sends the column, where it was left out:
  `INSERT INTO t (name, note) VALUES (?, NULL)`.
