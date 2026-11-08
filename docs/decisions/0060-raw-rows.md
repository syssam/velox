# 60. Rows reads raw SQL that is no entity's

Status: accepted, 2026-11-08.

## Context

`Raw` scans rows into an entity by column name, and `Exec` returns a count.
A report whose rows are no entity's had only `velox.Pool(db)`: nil on a
transaction's DB and on a DB from `OpenConn`, `OpenShards` or `veloxpgx`,
and, on SQLite's one connection, a hang inside a `Tx` or an `Iter` loop. A
review asked for SQL "on the connection the statement uses".

## Decision

- `db.Rows(ctx, reason, query, args...)` returns `RawRows`, a
  `func(yield func(RawRow, error) bool)` to range over, as an
  `iter.Seq2[RawRow, error]` is. `RawRow.Scan(dst...)` reads the row's
  columns in order, as the driver's `Scan` does.
- It runs as `Raw`'s `Iter` does: on the DB's connection, a transaction's
  included; through the transaction check, the Around hooks (OpRawQuery,
  named "Rows"), routing as a read, the SQL comment and the row cap; with a
  reason, as all raw SQL needs.
- Its own type, not `iter.Seq2[RawRow, error]`: a generic instantiation in a
  method of `DB` costs every package that holds a `*DB`, 3382 bytes for one
  variable of `UpdateQuery` (`TestGenericTypesCompileSmall`). A caller that
  wants the `iter.Seq2` converts: `iter.Seq2[velox.RawRow, error](rows)`.
- An iterator, not a callback or `*sql.Rows`: velox's executor is not
  database/sql's on veloxpgx, and the loop body runs inside the hooks, as
  `Iter`'s does.

## Consequences

- `velox.Pool` is for what needs the pool itself, such as migrations.
- A row is valid only in its loop body. A statement in the body fails on a
  pool of one connection, as in `Iter`'s.

## Evidence

`internal/apitest/rows_test.go`: `TestRowsInTx` sees the transaction's
uncommitted row on SQLite's one connection, without hanging; `TestRowsErrors`
(reason, cap, bad SQL); `TestRowsBreakAndHooks` (a break frees the
connection; the hook sees OpRawQuery "Rows" with the loop body's time).
