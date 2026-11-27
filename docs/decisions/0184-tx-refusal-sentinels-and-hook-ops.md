# 184. Sentinels for "no transaction", AsConstraintError, hooks need an op

Status: accepted, 2026-11-27.

## Context

nextapp's migration report (a large multi-tenant application on velox)
found four places where code had to read a message or guess.

- `AfterCommit`, `BeforeCommit` and `AfterRollback` refused with bare
  `errors.New`. A caller could not tell a DB in no transaction, whose work is
  committed already, from a `UseTx` one, whose commit is the caller's. An
  event publisher that sends at once when it cannot queue is right for the
  first and wrong for the second (and for the pool DB under a transaction's
  context, which the queue functions refuse though its statements join).
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
- The queue functions take the context first (below).
- `velox.InTx(db)`, a function as `Pool` is, reports `DB.InTx`.
- `On` and `Before` panic with no op, at the call that builds the hook, as
  `WithHooks` panics on a nil one.

## Consequences

A program that matched the old messages still works. A schema or option that
passed no op now panics when its hook list is built, which is the first
`velox-gen` or test run; the one test that pinned the old behaviour is
replaced by one that pins the panic.

`ErrNoTx` says the DB given is not a running transaction's. It does not say
the work is committed: a rolled-back transaction's DB gets it, and so does a
savepoint's DB once it returned while the transaction runs.

### AfterCommit, BeforeCommit and AfterRollback take ctx first

`velox.AfterCommit(ctx, db, fn)` (and the other two) queue on
`db.In(ctx)`, the handle a statement on `db` under `ctx` runs on (0171): the
transaction `db` began when `ctx` is the one its `fn` got, the savepoint when
it is a savepoint's, and `db` itself otherwise. So the pool DB queues under
the transaction's context, which nextapp worked around with `TxFrom`. A
publisher that sends at once on `ErrNoTx` is then right for a DB and context
it still holds: no transaction of this DB runs under that context. A handle
kept past its transaction is the caller's bug, and also gets `ErrNoTx`.

0100 chose the `*DB` alone because a function keyed on the context "would
queue on whatever transaction a context happened to carry", and the mark was
then only a misuse check. Since 0171 statements do join by the mark, and
`In` joins only for the DB that began the transaction, in its mode: another
DB, or the same DB under another context, still gets `ErrNoTx` (`In` returns
it as it is). `WithoutTx` still lifts the join. Resolving through `In`, not
`TxFrom`, keeps the savepoint: `TxFrom` returns the root's handle, which
would make an `AfterRollback` miss its savepoint's rollback.

The signature changes, so every caller adds `ctx`. nextapp builds against
main, so this merges only after its callers have moved.

Not done: `HooksInTx`; a hook that must write atomically with
the mutation runs it in `db.Tx`.
