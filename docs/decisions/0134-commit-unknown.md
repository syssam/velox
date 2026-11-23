# 134. A failed COMMIT whose outcome is unknown matches ErrCommitUnknown

- Status: accepted, 2026-11-23.

## Context

nextapp's `WithTx` retries a transaction on its own, on a serialization
failure, a deadlock or a lost connection, and must never retry once COMMIT
has been sent and failed: the server may have committed and only the
acknowledgement was lost, so running fn again applies its writes twice (a
second journal entry, a second payment allocation). It counts such commits
and reports them as indeterminate. velox's `Tx` returned a failed COMMIT as
`velox: commit: <driver error>`, with nothing to tell it from a failure
before COMMIT. The port (`internal/infra/database`) queued a `BeforeCommit`
that set a flag, then treated every error after it as a failed COMMIT
unless it was a `*ConstraintError`, `ErrTimeout` or the context's: a guess
from the outside, and a wrong one for a deadline that passes while COMMIT
is on the wire, which it counted as a rollback.

## Decision

- `velox.ErrCommitUnknown` is matched (`errors.Is`) by a `Tx`'s error when
  its COMMIT failed with an error that does not say the transaction rolled
  back: a driver error, a lost connection, a context that ended while
  COMMIT was sent.
- An error that says the transaction rolled back does not match.
  `dberr.RolledBack` decides, per driver; when unsure, the answer is
  unknown:
  - every dialect: a violated constraint that velox reads (a deferred one,
    a `*ConstraintError`, 0053);
  - PostgreSQL: the server's ROLLBACK answer to the COMMIT of an aborted
    transaction, which pgx returns as `ErrTxCommitRollback`, natively and
    through database/sql (its stdlib Tx commits through pgx's); an
    integrity violation (class 23, 23P01 included); a transaction rollback
    (40000, 40001, 40002, 40P01). 40003, statement_completion_unknown, a
    canceled COMMIT (57014) and a shutdown (57P01) are unknown;
  - MySQL: a deadlock (1213), which rolls the whole transaction back, and
    3101, a rollback the server decided during COMMIT (Group Replication).
    A lock wait timeout (1205) rolls back only its statement unless
    `innodb_rollback_on_timeout`, and 1180, "Got error during COMMIT",
    does not say what the engine kept: both are unknown;
  - SQLite: SQLITE_BUSY and SQLITE_LOCKED (extended codes too) and a
    constraint (code 19, a deferred foreign key): COMMIT did not happen,
    and modernc and mattn both send ROLLBACK after a COMMIT that failed.
    An I/O error can come after the commit point: unknown.
- The error's message and the driver's error in it are unchanged: the
  driver's error is wrapped in a type whose `Is` matches the sentinel.
- `TxRetrySerializable` never runs fn again after an error that matches,
  though it be retryable before COMMIT (MySQL's 1205). A rollback at
  COMMIT that is retryable (a deadlock, SQLITE_BUSY) is retried.
- A savepoint's RELEASE that fails rolls back to the savepoint (0066's
  rule), so it is not a COMMIT and does not match.
- A distinct type, `*CommitError`, was rejected: a caller wants one
  question answered, whether the transaction may have committed, and a
  sentinel answers it alongside `ErrTimeout` and the context's error, which
  the same error can also match.

## Consequences

A caller's own retry asks `errors.Is(err, velox.ErrCommitUnknown)` before
anything else and stops there. The outcome is still unknown to velox: the
caller reconciles, against an idempotency key or by reading the rows.

## Evidence

- `internal/apitest/commit_unknown_test.go`: `TestCommitUnknown` (a driver
  error, a bad connection, PostgreSQL's 40003 and MySQL's 1205 and 1180 at
  COMMIT match; a serialization failure, a deferred constraint (23503,
  23P01), pgx's `ErrTxCommitRollback`, a MySQL deadlock, a failed BEGIN and
  fn's error do not; message and driver error kept),
  `TestCommitUnknownWhenCanceledDuringCommit`,
  `TestCommitUnknownIsNotRetried`, `TestCommitUnknownRetryableIsNotRetried`
  (1205 at COMMIT runs fn once, 1213 four times).
- `internal/dberr`: `TestRolledBack` (each driver's error),
  `TestRolledBackSQLite` (modernc's real SQLITE_BUSY and deferred foreign
  key at COMMIT; the row is not kept), `TestPgxTypesMirrored` (pgx's text).
- `internal/integration/commit_unknown_test.go`:
  `TestCommitRolledBackIsNotUnknown`, on PostgreSQL through pgx and
  database/sql: a statement error fn swallows, then COMMIT, gives
  `pgx.ErrTxCommitRollback` and not `ErrCommitUnknown`; a deferred unique
  constraint gives a `*ConstraintError`.
