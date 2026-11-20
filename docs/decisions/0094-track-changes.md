# 0094. A hook tracks old and new values: Track, Changes, Diff

- Status: accepted, 2026-11-20.

## Context

An application's change log records which fields a write changed, with
the old and the new value. ent's answer is `OldX(ctx)` on a one-row
update: a query of its own, outside the update's lock, so a concurrent
write can slip between the two. velox's hooks see bulk updates and
deletes too, by column name, and have no per-field getters.

## Decision

- `Mutation.Track(names...)`, called by a hook before next on an update
  or a delete, asks velox to read the rows before it writes them. It
  names the columns to compare. With no name it means the columns the
  update assigns, resolved when next runs: the caller's and the hooks'
  assignments, then the UpdateDefault and Version columns writeCore adds.
  So a hook's `Set` after `Track()` is tracked, and so is `updated_at`.
  On a delete, no name means every column. Two calls add up.
- Track on an insert, after next, or with a name the entity has no column
  of is a misuse, recorded as the other Mutation misuses are.
- A tracked write runs in one transaction: the caller's inside `db.Tx`,
  else one of its own, as `UpdateQuery.One` does. In it velox
  1. reads the key and the tracked columns of the rows the statement
     matches, a Writable SELECT (exactly an UPDATE's scope: interceptors,
     Read and Write policies), in key order, `FOR UPDATE` except on SQLite;
  2. sends the UPDATE or DELETE with its own conditions and `AND key IN
     (…)` of the keys read (one `(a = ? AND b = ?)` group per row for a
     composite key), split into chunks under the parameter limit with
     `byChunks`, as the relation loads are;
  3. for an update reads the new values: `RETURNING` of the key and the
     tracked columns on PostgreSQL and SQLite, in the UPDATE itself; on
     MySQL a `SELECT` by the keys after the write.
  No row read: nothing more is sent, and the write affects 0 rows.
- The MySQL re-read adds no row condition (a private `Statement` mark the
  scope obeys). The update was scoped, and may move a row out of view: a
  soft delete's `deleted_at` hides the row from its own interceptor, which
  `WithoutPolicy` does not lift. RETURNING has no such problem.
- An update that assigns a key column is followed under the key assigned.
  An `Add` or `Sub` on a key column is refused, as One refuses it.
- `Mutation.Changes()` yields, after next returned nil, one
  `velox.Change` per row: `Key` (before the write) and `Fields`, a
  `FieldChange{Name, Old, New, Changed}` per tracked column in column
  order. `Change.Changed()` lists the fields that differ. Values are plain,
  as `Mutation.Value` returns them. A delete's New is nil. An insert's
  Changes need no Track: each row as written, every column, Old nil.
- `velox.Diff[E](before, after *E)` lists the columns of two rows that
  differ, a nil row being all NULL, for code that holds both. The design
  said `old, new`; `new` is a builtin the linter refuses as a name.
- Equality is by value: a type's `Equal(T) bool` (time.Time, decimals),
  `bytes.Equal` for a byte slice (json.RawMessage), else
  `reflect.DeepEqual`.
- `Change` and `FieldChange` are plain data in `veloxtype`, aliased in the
  root and the engine (0020, 0021).
- A Rewrite hook may not change a tracked UPDATE's `Returning`: velox scans
  it by position.

## Where Track reads nothing

- Under `SQL(ctx)`: the statement renders as it would untracked, and
  Changes yields none. A dry run sends no read.
- When a hook skips next or makes the write itself (`SetAffected`): velox
  wrote nothing. The soft-delete mixin is such a hook. Its update runs the
  entity's update hooks, so an application hook that tracks updates
  records the soft delete as `deleted_at` changing from nil. Only a hook
  outside the mixin's, which a mixin listed before softdelete would be,
  sees the delete and gets no Changes.
- An update that sets the key of a bidirectional one-to-one edge is
  refused with Track: velox re-points both partners in statements of its
  own.

## Consequences

- A tracked update is BEGIN, SELECT … FOR UPDATE, UPDATE … RETURNING,
  COMMIT on PostgreSQL; the same without FOR UPDATE on SQLite; and BEGIN,
  SELECT … FOR UPDATE, UPDATE, SELECT, COMMIT on MySQL. A tracked delete
  is BEGIN, SELECT (FOR UPDATE), DELETE, COMMIT everywhere.
- Old values are those the write replaced: the lock holds the rows from
  the read to the commit, so concurrent tracked writes chain. On SQLite
  the database's single writer serializes them: another writer that would
  commit between the read and the UPDATE waits, or one of the two fails
  with SQLITE_BUSY. Neither reports a wrong old value.
- The write changes the rows read, not rows a concurrent insert adds that
  match the conditions afterwards.
- The read is bounded by the DB's MaxRows: a tracked write of more rows
  fails with `ErrTooManyRows` before anything is written.
- Nothing is read unless a hook calls Track.
- Every package with a hook describes Mutation's methods in its export
  data. Track and Changes, with Change and FieldChange, add about 1 KB to
  each (`compilecost_test.go`). The engine's side adds under 100 bytes:
  its work is plain functions, not methods of `MutationState`, and the
  state is one `any` field. As methods, with typed fields, it added 1.4 KB
  to an entity package with no hook.
- `RETURNING` on an UPDATE is rendered for a tracked update only: the
  pipeline sends such an UPDATE as a query and scans the rows it returns,
  as it does an INSERT's.
- A delete could read its old values with `DELETE … RETURNING` on
  PostgreSQL and SQLite, one statement fewer. It reads first instead, the
  same shape on every dialect.
