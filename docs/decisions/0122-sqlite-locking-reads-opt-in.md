# 0122. WithSQLiteLockingReads runs a locking read on SQLite without its clause

- Status: accepted, 2026-11-23.

## Context

`ForUpdate` and `ForShare` fail on SQLite: "SQLite has no locking reads;
its transactions lock the whole database". SQLite has no `FOR UPDATE`.
ent and old velox drop the clause on SQLite and run the read. nextapp's
services lock rows with `ForUpdate` on PostgreSQL, and their tests run on
SQLite. The port had to wrap every locking read in a helper that skips
`ForUpdate` when the dialect is SQLite.

The refusal is right by default. A read without its lock is a different
statement, and code that relies on the lock should not run silently
without it.

What a dropped clause still guarantees depends on how the transaction
begins:

- velox's `Tx` begins through database/sql's `BeginTx`. modernc.org/sqlite
  then sends a plain `BEGIN`, which is DEFERRED, unless the DSN sets
  `_txlock=immediate` or `exclusive`.
  database/sql has no per-transaction choice of the mode. velox does not
  pick one.
- A DEFERRED transaction takes SQLite's write lock at its first write. Two
  transactions on two connections can both read a row "for update". When one
  has written and committed, the other's write fails with SQLITE_BUSY. In WAL
  mode the snapshot is stale, so no busy timeout helps. No update is lost.
  The second transaction fails where a row lock would have made it wait.
- Under `_txlock=immediate` every transaction takes the write lock at its
  `BEGIN`. A second writer waits, up to the busy timeout, before it reads,
  as it would behind a row lock. So does every other transaction, read-only
  ones included.
- `velox.Connect` gives SQLite a pool of one connection. In one process,
  transactions then take turns for the connection, whatever the BEGIN.

Making the opt-in force IMMEDIATE was considered. velox would have to begin
its own transactions with a raw `BEGIN IMMEDIATE` on a held `*sql.Conn`,
outside database/sql's `Tx`. It cannot rewrite the DSN of an `*sql.DB` given
to `Open`. It would also make every `TxReadOnly` take the write lock. The DSN
already gives that choice, and the driver honours it.

## Decision

- `velox.WithSQLiteLockingReads()` is an `Option`. Under it, `guardLock` does
  not refuse a locking read on SQLite, and the renderer leaves out
  SQLite's lock clause, `SKIP LOCKED` and `NOWAIT` included. `SkipLocked` then
  skips no row, and `NoWait` does not stop a wait.
- Every other check of a locking read holds under it. It runs only inside
  `Tx`. `Count` and the aggregates cannot lock. `SkipLocked` and `NoWait` need
  `ForUpdate` or `ForShare`. A locking read loads no relations.
- It does not change how a `Tx` begins. Its doc and the guide say what the
  DEFERRED default does and recommend `_txlock=immediate` for the wait
  semantics of a row lock.
- Without the option the refusal stays. Its error names the option.
- PostgreSQL and MySQL are unchanged.

## Consequences

- A service whose tests run on SQLite keeps its `ForUpdate` calls as written.
  It opens the test database with the option.
- `TestSQLiteLockingReadsOption` (`internal/apitest`) checks what is sent,
  what `SQL` renders, and that the other refusals stay.
- `TestSQLiteLockingReadSemantics` checks each case on a WAL file:
  - On one connection, four concurrent read-modify-writes all count.
  - On two DEFERRED connections, the second writer fails with SQLITE_BUSY,
    keeping the first one's write, and `TxRetrySerializable(1)` gets both
    writes in.
  - Under `_txlock=immediate`, the second transaction waits and reads the
    first one's value.
