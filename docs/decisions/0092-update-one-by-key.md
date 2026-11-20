# 0092. UpdateQuery.One by key sends no key read

- Status: accepted, 2026-11-20. Amends [0048](0048-update-one.md).

## Context

0048's `UpdateQuery.One` reads the row's key, locked, then sends the UPDATE
with that key as its only condition, then reads the row back. Most updates
name the row by its key, `Where(X.ID.EQ(id))`, and then the key read finds
nothing the UPDATE does not. A create-read-update-delete round over
nextapp's 496 entities sent 1467 SELECTs through the typed API, old velox
981: one key read per update. Its updates took 1170 ms on PostgreSQL
against old velox's 1059.

Two things made the key read look necessary. It applies the Read policy as
well as the Write policy, but so does every UPDATE: scope gives an UPDATE
the interceptors and both policies, the scope of the Writable read. It
locks the row, but so does the UPDATE, inside the same transaction.

Writing this found a bug. An update that assigns a key column wrote the row,
read it back by the old key, found nothing, and rolled back with
`ErrNotFound`.

## Decision

- When the conditions hold, at their top level, `<column> = v` with a
  non-nil v for every primary-key column, and the update assigns something
  and writes no edge, One sends the UPDATE with the caller's conditions
  and reads the row back by key, in one transaction (none of its own
  inside the caller's). The hooks see the caller's conditions, as Exec's
  hooks do.
- The count decides the rest. One row: read back by key without the Read
  policy, as before. None: a hook skipped the write, or no row matched or
  the policies hid it. One then reads the row under the conditions and
  both policies and returns it, or `ErrNotFound`. A hook that skips next
  still gets the row, and MySQL without `clientFoundRows=true` still finds
  a row the update leaves unchanged. More than one, which only a hook's
  `SetAffected` or the driver can report: `ErrNotSingular`, rolled back.
- Every other One reads the key first, as 0048 says.
- Either way the row is read back by its key as the update left it: a key
  column the update assigns (a hook's assignment included) holds the
  value assigned. An `Add` or `Sub` on a key column is refused before
  anything is sent, as One could not tell the new key.

## Consequences

- One by key is BEGIN, UPDATE, SELECT, COMMIT, as old velox's
  `UpdateOneID(id).Save` is. In the CRUD round velox sends old velox's
  statements, count for count.
- An UPDATE by key that matches no row costs a second statement, the read
  that tells a skipped write from a missing row.
- The update of a key column through One works.
- `RETURNING` would make One by key one statement on PostgreSQL and
  SQLite. It is not done: the hooks, the Rewrite hooks and the pipeline
  run an UPDATE as an exec, and MySQL has no `RETURNING`.
