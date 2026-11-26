# 182. TxCancelOnEnd makes a late statement fail

Status: accepted, 2026-11-26.

## Context

Under a context fn got, a statement on the DB that began the transaction joins
it (0171). Once the transaction has ended, its mark counts as none, and a
statement a goroutine sends then under fn's ctx runs on its own, in
autocommit. That is deliberate and tested: a retry may use the first
attempt's ctx, a ctx derived from fn's may carry values into later work, and
velox cannot tell a write that escaped its transaction from an independent
one. An application that treats the escape as a bug had no way to make it
fail, short of wrapping every Tx in a function that cancels a child context
(nextapp has about 1690 calls and no wrapper).

## Decision

`velox.TxCancelOnEnd()` is a `TxOption`, meant for `WithTxDefaults`. With it,
fn's ctx is a child of the context the attempt runs on, canceled with cause
`ErrTxEnded` when the attempt ends, committed, rolled back or panicked. The
context of a savepoint's fn is canceled when that nested Tx returns. A
savepoint takes no options: it follows the root's choice.

A late statement fails in database/sql with `context.Canceled` before
anything is sent; `context.Cause(ctx)` is `ErrTxEnded`.

The commit, the rollback and the AfterCommit functions run on the context Tx
was given, not on the child, so they are unaffected. Each retry makes a new
child. The default is unchanged.

## Consequences

One allocation per transaction (the cancel context), and one per savepoint,
only with the option. Work that is meant to outlive the transaction must not
use fn's ctx, and takes `context.WithoutCancel` of it or the caller's.

Rejected: failing every statement under an ended mark by default, which would
break the cases above. Rejected: a new error for the statement: it would
not match `context.Canceled`, which is what code that already handles a
canceled request expects.
