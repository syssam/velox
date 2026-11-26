# 180. A transaction retries what the database rolled back

Status: accepted, 2026-11-26.

## Context

`TxRetrySerializable(n)` ran a transaction again after a serialization
failure or a deadlock. A server that restarted, or that had no connection
to give, ended the transaction instead, and an application that ran on veloxe
or ent kept its own loop to retry those.

A connection that drops before COMMIT takes the transaction with it: the
server rolls back what it held. Running `fn` again is as safe as after a
deadlock. The one unsafe case is a failure at COMMIT, whose outcome is
unknown (`ErrCommitUnknown`, [0134](0134-commit-unknown.md)).

## Decision

`TxRetrySerializable` is `TxRetryRolledBack`: one option, one count, for
one fact (the database rolled the transaction back before COMMIT), whatever
the cause. `TxRetries` reads it as before. `WithTxDefaults` and
[0133](0133-tx-defaults-and-none.md) apply to it unchanged.

Besides a serialization failure or a deadlock, a transaction runs again when
its BEGIN, or a statement it sent, failed on a lost or refused connection:
`connLost`, the errors replica failover already reads (`failoverable`),
less a failed login (PostgreSQL class 28, MySQL 1045) and a blocked host
(1129, 1130), which no retry mends. A read timeout is not a lost connection.

The loss is recorded where the statement runs (`DB.send`), in a flag the
attempt shares with its savepoints, and not read from the error `fn`
returns: an error of `fn`'s own, an `io.EOF` from a file it read, does not
rerun it. A failure at COMMIT is never retried. What `fn` did outside the
transaction, or under `WithoutTx`, is done again, as for a deadlock.

## Consequences

`TxRetrySerializable` is gone, as nothing before 1.0 is kept; the
[earlier records](0133-tx-defaults-and-none.md) name it.

A statement that bypasses `send` (SAVEPOINT and RELEASE) does not record a
loss: its failure ends the transaction, and is not retried.

The retry waits as before, from 5 ms to a second, within the transaction's
deadline: `TxTimeout` is the time budget for a server that takes seconds to
come back, with a count to match.
