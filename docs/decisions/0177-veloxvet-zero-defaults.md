# 177. veloxvet reports a zero that an insert's default replaces

Status: accepted, 2026-11-25.

## Context

An insert fills a field's `Default`, or its `DefaultFunc`'s value, into
the field when the struct holds the zero value there, before the
insert's setters and hooks ([0082](0082-defaults-before-setters.md)).
So with `field.Bool("active").Default(true)`, inserting
`&gen.User{Active: false}` stores `true`, and `Default(100)` turns a `0`
and `Default("free")` an `""` the same way. Nothing fails: the struct
reads as if it stored the zero. A setter in the insert,
`Set(gen.Users.Active.Set(false))`, stores the zero, and velox-gen's
field comments say so; the compiler cannot.

## Decision

veloxvet gets a second analyzer, `ZeroDefault` (`zerodefault`), in
`veloxvet.Analyzers` and so in `cmd/veloxvet`.

- It reports a constant zero (`false`, `0`, `""`, typed or not) given to
  a field whose column has a constant `Default` that is not zero, or a
  `DefaultFunc`: a key of the entity's composite literal, or an
  assignment `x.F = zero`. The message names the default and the setter
  that stores the zero, `Users.Active.Set(false) in the insert stores
  false`.
- Only when the same function, its literals included, inserts that
  value with `db.Insert` (`&E{…}`, `&x` or a `*E` variable) or
  `db.InsertAll` (a `[]E{…}` literal), alone or as a row of
  `InsertBulk`, and the insert's chain runs `Exec` or `SQL` through
  only `OnConflict`, `Update`, `UpdateAll`, `UpdateSet`, `UpdateWhere`,
  `OnConflictWhere`, `OnConstraint` and `DoNothing`. An insert with `Set`
  or `SetInput`, which can store the zero, is not reported, nor one
  whose query is held in a variable or passed on. `Save` writes the
  columns it names and fills no default, so it is not checked.
- A variable is followed only when it is local, starts as a composite
  literal, `&E{…}`, `new(E)` or its zero value, and the function does
  nothing else with it than read or set its fields, call its value
  methods and insert it. A field the function sets to anything but a
  constant zero, changes with `++` or `op=`, or takes the address of is
  left out. A zero set after the insert is not reported, nor one whose
  store does not reach the insert in the function's SSA form, such as a
  zero set in a branch that returns first. A zero set in a function
  literal is reported: its paths are not followed.
- The defaults come from velox-gen's `Descriptor`: while it runs on a
  package, the analyzer reads every `velox.Descriptor[E]` literal's
  `Columns` and exports a `Defaults` fact on `E`, with the fields, their
  defaults as Go source and the name of `EHandles`' variable. The
  descriptor is already a literal of constants, so generated code does
  not change.
- `//velox:zero-ok` waives a report, by `//velox:unbounded-ok`'s rule
  ([0176](0176-veloxvet-unbounded-reads.md)): on any line of the
  statement that holds the zero, or alone on the line before it. Each
  analyzer has its own directive, so a waiver says which report it
  expects and does not hide the other.

## Consequences

Known false negatives: an entity passed in as a parameter, held in a
field, a slice or a map, or given to any function but the insert; a
query held in a variable; `InsertAll` of a slice variable; a `Default`
that is not a constant; a zero that is not a constant, such as a
variable's `false`; a hand-written insert path such as a hook's
`Mutation.Set`. The untyped `Table` insert is not checked.

Rejected: a field comment alone, which is already generated and which
the code that writes the zero never reads; a run-time warning, which
would cost every insert a check; reporting every zero of a defaulted
field, which would report structs that are never inserted.
