# 136. A setter's zero is stored, and an empty patch runs its hooks

- Status: accepted, 2026-11-23. Amends [0093](0093-lean-inserts.md),
  [0099](0099-keep-defaults-and-set-ptr.md) and
  [0124](0124-explicit-null-in-inserts.md). Both rules follow ent.

## Context

Two of velox's write rules differed from ent's, and nextapp, the veloxe
application being ported, met both.

- **A setter's zero (N-V27).** 0093 left out of an INSERT a zero value in
  a column with a declared database default, so the default replaced it.
  velox read the value, not who set it, so `Xs.Rate.Set(0)` was left out
  too. velox-gen warned of every NOT NULL field whose declared default is
  a non-zero literal: "the zero value cannot be stored through Insert".
  nextapp got the warning on 53 fields (`exchange_rate`,
  `conversion_factor`, ...). 0124 had already made `SetNull()` an explicit
  NULL. ent's create builder writes every field `SetX` was called for, and
  fills a default only into a field it was not told about (`defaults()`
  checks `m.X()`).
- **An empty patch (N-V29).** 0099 made an update whose setters were all
  skipped (`SetPtr(nil)`) send nothing: no hook ran, no `UpdateDefault`
  fired, so `updated_at` did not move. ent's `Save` runs the hooks and
  `defaults()` whatever was set, so `UpdateOne` with nothing set still
  writes `updated_at`. ent's `sqlgraph` then sends no UPDATE when the
  statement is empty: `Update` returns 0, `UpdateOne` reads the node.

## Decision

The maintainer ruled that velox follows ent in both.

### A setter's value is stored, its zero included

- Every column of an insert's column setter is written as the entity holds
  it: `Set(v)` for any v, the zero included, and `SetNull()`. Neither the
  column's database `DEFAULT` nor a `Default` or default function replaces
  it. `SetInput` counts as its setters do: a field the `Input` gives is a
  `Set`.
- `Setters.InsertSet` names the columns, which replaces 0124's
  `InsertNulls`. The statement carries them as 0124's explicit NULLs were
  carried (`SetExplicit`). `LeanInsert` keeps each, `defaultCell` writes no
  `DEFAULT` in one, SQLite's `DefaultRuns` does not split on one, and
  nothing is read back for one.
- A column a setter names is validated, as a value is: `ValidateInsert`
  skips the validators only of a cell the insert leaves to the database.
- This holds for `Insert`, `InsertAll` and an upsert's inserted rows. The
  last setter of a column decides its value.
- A zero or nil field that no setter names is unset, as before (0093,
  0124). It gets its `Default` (0082) or, in a column with a declared
  database default, is left out for the database to fill, and read back.
  So a struct passed to `db.Insert(&x)` with no setter behaves as before:
  the struct has no "set" bit, and a zero field is ent's field never set.
- A hook's `Mutation.Set(col, v)` on an insert names the column as a setter
  does, as ent's `m.SetX` in a hook does: its zero is stored
  (`MutationState.Given`, merged by `GivenColumns`), in the typed and the
  untyped insert. A zero assigned straight into the struct, by a hook or
  anyone, stays unset: velox cannot tell it from a field never set.
- `SetPtr(nil)` still sets nothing (0099, 0124). An insert cannot take
  `SetExpr`, `Add` or `Sub`, as before.
- The render cache keys an insert by its columns, so an insert of
  `Set(0)` and one that leaves the column out are two shapes, each cached
  (`TestSetterZeroIsStored` runs both twice).
- velox-gen no longer warns "the zero value cannot be stored through
  Insert": the setter stores it. `codegen.DefaultWarnings` is removed. The
  generated sentences already said `Xs.F.Set(0) in the insert stores 0`
  for a field with a Go default, which is now true for every default. The
  generated code does not change.

### An empty patch runs its hooks and its UpdateDefaults

- An update given a `Set` that leaves nothing to assign is an empty patch:
  setters that were all skipped, a `Set()` of none, or a `SetInput` whose
  `Input` gives nothing. `UpdateQuery.One` with no setter is one too. Its
  hooks run and every `UpdateDefault` fires that `KeepDefaults` does not
  name, as in ent. What they assign is written: `updated_at`, and a column
  a hook sets, such as `updated_by`.
- When the hooks and the defaults assign nothing either, no statement is
  sent. `Exec` returns 0, as ent's `Update` does, and `SQL` returns "". The
  guard still applies (`ErrNoWhere`). `One` reads the row under the Read
  and Write policies as `Writable`, as ent's `UpdateOne` reads the node,
  and fails with `ErrNotFound` when there is none.
- An update never given a `Set` still fails with "Update has no Set" (0099).
  That is a builder mistake, as a missing `Where` is.
- An update of only edge setters runs its hooks and its `UpdateDefault`s
  too, as ent's does when a relation changes: `updated_at` moves. Its
  UPDATE is sent when they assign something; `Exec` still returns the rows
  whose edges it wrote.
- **Write policy (0008).** The UPDATE of an empty patch is scoped by the
  policy as any other: a row it hides is not written. With nothing to
  send, no policy is asked, as nothing is written. `One` still reads its row
  as `Writable`, so a row the Write policy hides is `ErrNotFound`
  (`TestEmptyPatchOneHidden`).
- **Version (0056).** The Version column gets its `+ 1` when an UPDATE is
  sent, so an empty patch that writes `updated_at` adds 1, and one that
  sends nothing adds nothing. ent has no Version field.
- **Recording (0094).** An empty patch that sends an UPDATE is recorded as
  any update is. One that sends nothing records nothing.
- **Save.** `db.Save(&x, cols...)` takes at least one column, so it has no
  empty form.

## Consequences

- `Xs.Rate.Set(0)` in an insert stores 0 on every dialect. A schema need
  not make a field Nillable, nor insert and then update, to store a zero.
- An INSERT lists every column a setter names, where it left a zero out:
  `INSERT INTO t (name, rate) VALUES (?, ?)`.
- A GraphQL update whose input fields are all absent moves `updated_at` and
  runs the hooks, where it changed nothing. To write nothing, call no
  update, or name the column in `KeepDefaults`.
- No change to generated code; `GeneratedCodeIsVersion` stays.
