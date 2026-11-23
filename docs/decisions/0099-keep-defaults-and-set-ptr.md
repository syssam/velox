# 99. KeepDefaults, SetPtr and ent's error helpers

Status: accepted, 2026-11-21. Amended by
[0124](0124-explicit-null-in-inserts.md): in an insert, `SetPtr(nil)` on a
nullable column stores NULL.

## Context

nextapp, the veloxe application being ported, calls `SkipDefault(updated_at)`
36 times, `SetNillableX(p)` 2,829 times and `IsConstraintError` 109 times.
The gap analysis (`docs/history/superpowers/specs/2026-11-21-veloxe-gap-analysis.md`
§2.1 items 4 to 6) asked for each. velox had none of them.

A test first answered the open question of item 4: an update that assigns
an `UpdateDefault` column already writes the assigned value, and the
`UpdateDefault` does not fire (`TestExplicitSetWinsOverUpdateDefault`).
`Set(U.UpdatedAt, current)` therefore already kept the column's value. But
it writes a value, and needs the current one. `SkipDefault` writes nothing
to the column.

## Decision

- `UpdateQuery.KeepDefaults(cols ...Column[E])` and
  `SaveQuery.KeepDefaults(cols ...Column[E])`, and
  `untyped.TableUpdate.KeepDefaults(cols ...int)`. The named columns'
  `UpdateDefault` does not fire, so nothing is written to them. A column
  the update assigns is still written as assigned. The columns are kept in
  an unexported field of the `Statement`, which the core reads where it
  adds the `UpdateDefault` assignments (`writeCore`). Hooks do not see the
  field, and the public `Statement` does not change.
- `KeepDefaults` fails closed. A column without an `UpdateDefault`, or a nil
  column, fails `Exec`, `One` and `SQL` before anything is sent, and the
  error names the column. An upsert's update branch never fires an
  `UpdateDefault` (0017), so it needs no `KeepDefaults`.
- `SetPtr(p *V) Setter[E]` on every handle kind that has `Set`, written by
  `internal/handlegen`. It is a lazy generic method like the others (0022),
  so a handle that never calls it costs nothing (`TestGenericTypesCompileSmall`
  measured no growth on any handle). `SetPtr` takes a pointer to the type
  `Set` takes: the element type on a Null kind, an enum's or a JSON
  field's own type on theirs. It reads `*p` when called.
- A nil `p` gives a setter whose `Op` is `engine.AssignSkip`, an internal
  operator like `AssignEdge`. `InsertQuery.Set`, `UpdateQuery.Set`,
  `SetInput` and `TableUpdate.Assign` drop it, so no `Statement`, hook or
  Rewrite ever holds it. The zero `Setter` stays what it was, and is not
  a skip.
- What skipping means:
  - **Update:** the column is not assigned. An update given setters that
    were all skipped, with no edge setter, sends nothing. `Exec` and
    `TableUpdate.Exec` return (0, nil) and `SQL` returns "", once the guard
    (a Where or All) and the KeepDefaults columns are checked. No hook
    runs, no `UpdateDefault` fires and nothing is recorded. This differs
    from ent, whose `UpdateOne` with nothing set still runs its hooks and
    sets `updated_at`: a patch whose fields are all absent changes nothing
    here. `One` reads the row as `Writable`, as for an update with no
    setter. An update given no setter at all still fails with "Update has
    no Set": that is a mistake, not an empty patch.
  - **Insert:** the field stays as the caller left it. A zero field whose
    column has a declared database default is left to the database, and
    skips its validators, as 0093 says.
  - **Upsert:** a column `Update` names, whose setters were all skipped, is
    not updated. With no column left, velox sets the new exported field
    `ConflictClause.Ignore`, ent's `Ignore()`. The update branch assigns
    the first conflict column its own value (`SET org_id =
    invoices.org_id`; on MySQL `ON DUPLICATE KEY UPDATE`, the same text as
    its keep path). So the conflicting row is returned and its key read
    back as after an update, where `DoNothing` would read nothing. It is an
    update branch in every other respect: it gets the policy's Read and
    Write conditions, and MySQL refuses it with `ErrPolicyUpsert` where a
    policy or filter would condition it. It adds 1 to no Version column
    and writes no column but that key. The field is exported, not hidden,
    because `ConflictClause` keeps no state a hook's copy could lose
    (`TestUpsertPolicyFlagIsTheStatements`).
  - **Null kinds:** a nil `p` skips too, as ent's `SetNillableX` does.
    `SetNull()` is how to write NULL, and the godoc says so.
- A skipped setter never reaches a statement. The builders drop it;
  `ValidateSetters` (so `velox.ValidateSet` and `Table.ValidateSet`) passes
  over it, so a nil `SetPtr` of an Immutable column is not refused;
  velox-graphql's create-input check builds its row from Set values only.
  Rendering refuses one that a Rewrite hook puts in an UPDATE.
- `KeepDefaults`' columns are checked whether or not an UPDATE is sent: a
  `One` with no setter, an update of only edge setters, and one of only
  skipped setters fail on a column without an `UpdateDefault` too.
- `Save` takes columns, not setters, so it has no `SetPtr`. A caller names
  only the columns it changed.
- `velox.IsConstraintError(err)` is true when err holds a
  `*ConstraintError`, or a driver error that `internal/dberr` reads as a
  unique, foreign key, CHECK or NOT NULL violation (0053). This holds on
  PostgreSQL, MySQL and SQLite; `internal/integration` tests each kind on
  each dialect. `velox.IsValidationError(err)` is true when err holds a
  `*ValidationError`. `velox.IsNotSingular(err)` is true for
  `ErrNotSingular`, which `One`, raw `One` and `UpdateQuery.One` already
  returned when they matched more than one row. All three walk the error
  tree as `IsNotFound` does, so a typed nil cannot make them panic.
- velox adds no `OpCreate`, `OpUpdateOne` or `OpDeleteOne`. It keeps one
  name per op, and phase D's migration guide maps them.

## Consequences

- `SetNillableX(p)` becomes `X.SetPtr(p)`, `SkipDefault(c)` becomes
  `KeepDefaults(c)`, and `ent.IsConstraintError` becomes
  `velox.IsConstraintError`.
- An entity package that runs `Insert`, `Save` and `Update` is smaller than
  before: `entityDescUse` went from 773,616 to 770,386 bytes. The insert's
  checks and the update's setter handling moved into non-generic engine
  functions (`CheckInsertQuery`, `UpdateSet`, `CheckUpdateSet`).
- No change to generated code. `handles.go` is velox's own file.
