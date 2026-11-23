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
- A deferred constraint checked at COMMIT (a `*ConstraintError`, 0053) and
  a serialization failure or deadlock at COMMIT (the errors
  `TxRetrySerializable` retries) are rollbacks and do not match.
- The error's message and the driver's error in it are unchanged: the
  driver's error is wrapped in a type whose `Is` matches the sentinel.
- `TxRetrySerializable` already never retried such an error, as none is a
  serialization failure; a test now holds that.
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
  error and a bad connection at COMMIT match; a serialization failure, a
  deferred constraint, a failed BEGIN and fn's error do not; message and
  driver error kept), `TestCommitUnknownWhenCanceledDuringCommit`,
  `TestCommitUnknownIsNotRetried`.
