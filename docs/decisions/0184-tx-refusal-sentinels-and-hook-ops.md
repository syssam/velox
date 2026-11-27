# 184. Sentinels for "no transaction", AsConstraintError, hooks need an op

Status: accepted, 2026-11-27.

## Context

nextapp's migration report (a large multi-tenant application on velox)
found four places where code had to read a message or guess.

- `AfterCommit`, `BeforeCommit` and `AfterRollback` refused with bare
  `errors.New`. A caller could not tell a DB in no transaction, whose work is
  committed already, from a `UseTx` one, whose commit is the caller's. An
  event publisher that sends at once when it cannot queue is right for the
  first and wrong for the second.
- `IsConstraintError` recognizes a driver's raw error (raw SQL, `Pool`), but
  `errors.As` finds a `*ConstraintError` only where velox made it, so callers
  matched index names in strings.
- `Before(f)` and `On(h)` with no op ran on no write, as documented. An audit
  hook that forgot `OpInsert` did nothing, silently.
- No public test of "is this DB a transaction's", short of `TxFrom`, which
  asks a different question.

## Decision

- `ErrNoTx` (no running transaction) and `ErrAdoptedTx` (a `UseTx` DB) are
  matched by `errors.Is` on every refusal of the three queue functions;
  `ErrNoTx` also on the locking read that runs outside `Tx`. The messages are
  unchanged: the errors are of a type whose text is the message and whose `Is`
  names the sentinel, so no message is repeated or reworded.
- `velox.AsConstraintError(err)` returns the `*ConstraintError` of errors.As,
  else the one read from a driver's error. `IsConstraintError` is its `ok`.
- `velox.InTx(db)`, a function as `Pool` is, reports `DB.InTx`.
- `On` and `Before` panic with no op, at the call that builds the hook, as
  `WithHooks` panics on a nil one.

## Consequences

A program that matched the old messages still works. A schema or option that
passed no op now panics when its hook list is built, which is the first
`velox-gen` or test run; the one test that pinned the old behaviour is
replaced by one that pins the panic.

Not done: a context-aware `AfterCommit(ctx, db, fn)`. A statement on the pool
DB joins the context's transaction (0171) and the queue functions do not, but
the one-line `TxFrom(ctx, db)` serves, and a changed signature breaks every
caller. Now that the refusal says `ErrNoTx`, the harm of the gap (a wrong
guess) is gone. Not done: `HooksInTx`; a hook that must write atomically with
the mutation runs it in `db.Tx`.
