# 0066. UseTx returns a DB on a transaction the caller began

- Status: accepted, 2026-11-10.

## Context

A program that already holds a `*sql.Tx`, because other code or raw SQL began
it, had no way to run velox calls on it. `db.Tx` begins its own, so velox's
writes and the caller's raw statements ended up in two transactions.

## Decision

`vtx, err := velox.UseTx(db, tx *sql.Tx)` returns a `*DB` whose statements run
on `tx`; the DB's own connection is not used for them. It is the shape of
sqlc's `queries.WithTx(tx)`, not of `db.Tx`'s callback.

- The caller owns `tx`. `vtx` never begins, commits or rolls back. When the
  caller ends `tx`, `vtx`'s statements fail with `sql.ErrTxDone`, from
  database/sql.
- It retries nothing and takes no option. It runs no `TxAround` hook, which
  wraps a `Tx` call that `UseTx` is not.
- A `Tx` on `vtx` is a savepoint of `tx`.
- With replicas, `tx` is of the primary's `*sql.DB`, as a `Tx` runs on the
  primary.
- `vtx` is not safe for concurrent use, as a `Tx`'s DB is not.

An executor adopts a `*sql.Tx` by implementing `conn.Adopter`. `conn.SQL` and
the SQLite time wrapper do. `veloxpgx` does not, as it runs on pgx's own
transaction types; `UseTx` refuses it with an error. `UseTx` refuses too a
nil `tx`, a DB that is in a transaction, and a sharded DB, whose transaction
runs on one shard that velox chooses.

A first version took a callback, `UseTx(ctx, db, tx, fn)`, as `Tx` does. It
was dropped: the callback's only work was to bound the DB's life, which the
caller's `tx` bounds already, and it made a user expect `Tx`'s hooks and retry
that it could not give.

A function, not a method of `DB`: with `*sql.Tx` in a method's signature, every
package that uses a `Query[E]` compiled 175 KB more (the compile-cost test,
`TestGenericTypesCompileSmall`, failed at 175,166 bytes for `Query` against
10,240 recorded), as `Pool` found for `*sql.DB`. The engine's `UseTx` is a
function too: the root's `DB` holds an `engine.DB`, whose method set is part of
the same cost.

## Consequences

- The tests (`internal/apitest/usetx_test.go`, `internal/integration/usetx_test.go`)
  run raw SQL and velox in one transaction and end it both ways on SQLite,
  PostgreSQL and MySQL, nest a failing `Tx`, use the DB after Commit, read
  through a DB with replicas, and check each refusal sends nothing.
- A `tx` of another database than the DB's is not detected: its statements run
  there. The caller is trusted, as with `OpenConn`.
