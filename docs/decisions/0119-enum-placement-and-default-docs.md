# 119. An enum type sits in its entity's file, and a default says what it replaces

- Status: accepted, 2026-11-23. Amends [0082](0082-defaults-before-setters.md)'s
  consequence that velox-gen cannot show a default replacing a zero, and
  places [0086](0086-shared-enum-types.md)'s types.

## Context

A review of velox-gen's output on a 496-entity schema found three costs.

- `velox.go` declared every generated enum type: 1,018 of them, 30k lines,
  in a file no entity's reader opens. Most enum types belong to one entity.
- An enum field of an application's Go type, such as a shared `Currency`
  with 84 values, got its whole `switch` inside every Check that used it:
  132 copies in 125 files. A generated enum type already has `IsValid`,
  which each Check calls.
- A struct insert stores a field's default in place of its zero value
  (0082). Nothing in the generated code said so, and a field with
  `Default(true)` looked like any other bool.

## Decision

- An enum type that the fields of one entity use is declared in that
  entity's file, after its String method. A type that several entities
  share (0086) stays in `velox.go`. File names follow 0042.
- An enum field of an application's Go type is checked by one unexported
  function per Go type and values, `veloxCheck<Type>` (with the package's
  name too when two types have one name). The Check of every field of the
  type calls it. It is declared in the file of the one entity that uses it,
  or in `velox.go` when several do. Its error is the one the inline switch
  returned, `is not one of …`, which the engine wraps in a
  `ValidationError` that names the field.
- A field with a constant default that is not its zero value, or a
  default function, has one more sentence on its struct field and on its
  handle: "An insert of the zero value stores the default, true;
  Xs.Open.Set(false) in the insert stores false." A pointer field's says
  "An insert of nil stores the default, 1.5." An enum's names the default
  constant and no setter, since its zero is none of its values.
- `velox.GeneratedCodeIsVersion28` replaces Version27.

## Consequences

- A reader finds an entity's enum types beside it, as ent's per-entity
  package has them, and `velox.go` holds only what is shared.
- At 496 entities the 132 copies of the 84-value switch, about 77 KB, are
  one function; each Check's case is three lines, not five.
- A Nillable field's sentence makes no claim about `SetNull` in an insert.
  The insert leaves a nil column out (0093), so the column's `DEFAULT`,
  which a constant `Default` declares, applies, not NULL.
