# 0066. UseTx runs velox calls on a transaction the caller began

- Status: accepted, 2026-11-10.

## Context

A program that already holds a `*sql.Tx`, because other code or raw SQL began
it, had no way to run velox calls on it. `db.Tx` begins its own, so velox's
writes and the caller's raw statements ended up in two transactions.

## Decision

`db.UseTx(ctx, tx *sql.Tx, fn)` calls `fn` with a `*DB` whose statements run
on `tx`, and the DB's own connection is not used for them.

- The caller owns `tx`. `UseTx` never begins, commits or rolls back, and
  returns `fn`'s error unchanged.
- It retries nothing and takes no option. It runs no `TxAround` and no
  `AfterCommit` hook, as it cannot know whether the caller commits.
- A nested `Tx` in `fn` is a savepoint of `tx`.
- The `*DB` that `fn` gets is valid only inside `fn`. When `fn` returns the
  frame is marked ended and broken, so a kept DB's statements fail with
  `ErrOutsideTx`. `tx` is still open, so this is not left to the driver as it
  is for `Tx`.
- With replicas, `tx` is of the primary's `*sql.DB`, as a `Tx` runs on the
  primary.

An executor adopts a `*sql.Tx` by implementing `conn.Adopter`. `conn.SQL` and
the SQLite time wrapper do. `veloxpgx` does not, as it runs on pgx's own
transaction types; `UseTx` refuses it with an error. `UseTx` refuses too a
nil `tx`, a DB that is in a transaction, a context of another transaction, and
a sharded DB, whose transaction runs on one shard that velox chooses.

## Consequences

- The tests (`internal/apitest/usetx_test.go`) run raw SQL and velox in one
  SQLite transaction and end it both ways, nest a failing `Tx`, keep the DB
  past `fn`, and read through a DB with replicas.
- Passing a `tx` of another database than the DB's is not detected: its
  statements run there. The caller is trusted, as with `OpenConn`.
