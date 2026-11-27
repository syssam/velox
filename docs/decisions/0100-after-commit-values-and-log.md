# 100. AfterCommit, Values and IDs, and WithLog

Status: accepted, 2026-11-21. The section "Values and IDs" is superseded by
[0158](0158-values-and-ids-are-query-methods.md).

## Context

nextapp, the veloxe application being ported, defers events until commit
with veloxe's `tx.OnCommit` (`auditlog/status_events.go`). It reads one
column, mostly the key, with `IDs(ctx)` and `Strings(ctx)` at more than 75
call sites. It debugs with `Debug()`. The gap analysis
(`docs/history/superpowers/specs/2026-11-21-veloxe-gap-analysis.md` §2.1 items 7 to
9) asked for each. velox had none: `TxAround` wraps the whole `Tx` call, but
nothing queued work on one transaction from code inside it.

## Decision

### AfterCommit

`velox.AfterCommit(tx *DB, fn func(context.Context)) error` is a
function (0184 adds ctx first and resolves the DB under the context, as
statements do since 0171). `fn` runs once the outermost transaction has committed.

- The functions run in the order queued. They run after COMMIT succeeded,
  the `TxAround` hooks returned and the connection went back to the pool,
  and before `Tx` returns. On SQLite's one-connection pool, `fn` can
  query `db`.
  A `TxAround` hook that returns an error of its own after a successful
  commit does not undo the commit. The functions still run, and `Tx`
  returns the hook's error. A hook that panics after the commit does not
  skip them either. A deferred recover in `txAround` runs them, then
  panics again with the hook's value; a panic of theirs is dropped for
  the hook's, which came first. `txAround` keeps its state in one struct,
  so the queue costs no allocation and the deferred recover none either.
  `TestTxAroundAllocationBudget` failed at 10 allocations against 9 while
  the queue was a separate variable.
- `fn` gets the context given to `Tx`, through `context.WithoutCancel`:
  its values without its cancellation or deadline. That context has no
  transaction mark.
- A rollback runs none, whether from `fn`'s error, its panic, a context
  that ended or a COMMIT that failed. A retried attempt runs in a frame of
  its own, so its queue goes with it.
- A nested `Tx` that rolls back drops the functions queued while it ran.
  The savepoint keeps a mark into the root frame's list, as it does for a
  Recorder's kept rows (0094), and truncates the list to it on every path
  that undoes the savepoint. A function queued through the outer `tx`
  while the savepoint ran goes too. Statements on the outer `tx` then run
  inside the savepoint and roll back with it, so this is the same rule.
- A panicking function does not stop the others. Once all have run, `Tx`
  panics again with the first panic's value; the transaction committed.
- It returns an error, and queues nothing, in these cases:
  - a nil `fn`;
  - a DB in no transaction, so the call fails instead of running `fn` at
    once;
  - a transaction that has ended, or whose end has taken the queue;
  - a nested Tx's DB once that Tx has returned, released or rolled back.
    Each handle knows its own frame (`DB.txSelf`), whose `ended` the
    savepoint sets on return, so a stale handle cannot queue onto the
    root that is still running;
  - a UseTx DB (0066) and its savepoints. The caller commits that
    transaction, and velox never sees it end.

The plan asked for `velox.AfterCommit(ctx, fn)`, which finds the transaction
from the context's mark. velox takes the `*DB` instead:

- In velox the `*DB` is the transaction, as `Tx` documents. Statements
  run on the DB they are called on. The context mark only catches misuse
  (`ErrOutsideTx`), and `WithoutTx` removes it on purpose. A function
  keyed on the context would queue on whatever transaction a context
  happened to carry.
- With the DB, `fn` cannot reach a transaction the caller did not hold.
  Code that has a DB in no transaction gets an error, not a silent run.
  In a hook inside `db.Tx`, `velox.AfterCommit(m.DB(), fn)` reaches the
  transaction. Outside one, `Mutation.DB` is the database's, even for a
  write that velox runs in a transaction of its own inside the hooks, such
  as a recorded write. So the call returns an error there.
- It is veloxe's shape, `tx.OnCommit`, without the `Committer` chain.

A mutex on the root frame guards the queue, the closed flag and the
handoff. The commit takes the queue and closes it (`takeAfter`). A
rollback drops it and closes it (`closeAfter`). A savepoint's mark and
truncation take the lock too. So a call from another goroutine that races
the end of the transaction either queues a function that runs, or returns
an error; it is never lost. The godoc still asks for calls from the
transaction's goroutine, as for its statements.

It is a function, not a method of `DB`, as `UseTx` and `Pool` are. Every
method of `DB` is compiled into each package of entities through the
export data, even when unused and not generic. Measured with the method
added back, `entityDescUse` took 771492 bytes against 771266 for the
function: 226 bytes per package for nothing. The whole of this ADR took
`entityDescUse` from 770366 to 771266 bytes, `entityDescHooks` from
855484 to 856366 and `entityDescMixin` from 900776 to 901666. The rest of
that growth is in the engine types an entity's package reaches.

### Values and IDs

- `velox.Values(ctx, q, I.Number.Key()) ([]V, error)`. The column is a
  `GroupKey[E, V]`, the type a handle's `Key()` returns for `CountBy`, so
  `V` is inferred: the value type, or `*V` for a nullable column.
- `velox.Values(ctx, q, I.Number)` cannot infer `V`. Every handle method
  that mentions `V` is a lazy generic method, and a generic method
  satisfies no interface. A union of the handle types has no core type
  that holds `V`. An ordinary method on each handle costs about 20 KiB a
  handle (`TestGenericTypesCompileSmall`). `Key()` is the precedent the
  grouped aggregates already set. A JSON column has no `Key`, so it cannot
  be read: its stored bytes are not its value.
- `velox.IDs[K](ctx, q) ([]K, error)`. Nothing in `E`'s type says its
  key's type: it may be `int64` or a UUID. So the caller names `K`, and `E`
  is inferred from `q`. Before anything is sent, velox checks that the
  descriptor's address of the key field is a `*K`. That is a type
  assertion, with no reflection in the generic body. A composite key is
  refused. The alternative, `IDs(ctx, q, U.ID.Key())`, would only repeat
  `Values`.
- Both are functions (0022). They read as `All` reads: Where, After,
  OrderBy, Limit and Offset, the Read policy, the scope, the interceptors,
  `WithFilter`, the Limit-or-AllowUnbounded rule and the row cap. They set
  the prepared statement's `Columns` to the one column and run it through
  `DB.Run`, as `Count` and the aggregates read. They do not go through
  `Select` and `Rows`: a projection refuses `With` and `After`, which
  Values keeps (With is ignored, After applies), and would scan through
  `RawRow` by position. Columns, Omit and With are ignored. The result is
  nil when no row matches. A join, a projection and an
  encoded column are refused before anything is sent. The generic bodies
  are a scan loop. The checks are the engine's `ColumnRead` and
  `KeyColumn`.

### WithLog

- `velox.WithLog(l *slog.Logger, opts ...LogOption) Option` appends an
  Around hook. It writes one Debug record per statement after it ran, with
  `sql`, `args` (only with `velox.LogArgs()`), `duration` (without Iter's
  loop body), `rows` (affected for a write, read for a read) and `err`.
  When `l` does not enable Debug, the hook calls next and does nothing
  else. Without `WithLog`, velox adds no hook.
- A `Statement`'s `Args` is a flat list, with nothing tying an argument to
  a column. So the renderer records that itself. The writer has a `track`
  mode that notes, for each argument, which column it is written for:
  compared, assigned, inserted, an owner's key, a JSON path's column. It
  also notes the limits velox computes. The current entity is the
  writer's `in`, so a `HasWith` subquery on another entity is attributed
  to that entity's column. The log hook writes the statement's arguments
  again in that mode (`ShownArgs`) and compares them with `st.Args`.
- It fails safe. An argument of a Sensitive column prints as
  `"<sensitive>"`. These print as `"<hidden>"`:
  - an argument tied to no column: an `Expr`'s, a `Calc`'s or a
    `SetExpr`'s bound value;
  - every argument of raw SQL;
  - every argument of a statement whose arguments, written again, differ
    in number or value from `st.Args`, as when an outer Around hook
    changed them.

  An argument is printed only when velox has tied it to a column that is
  not Sensitive, or computed it. The arguments are masked before the
  hook calls next, as a hook given after it may change them.
- A driver's error message can echo a value. MySQL's 1062 says
  "Duplicate entry 'x' for key …", and PostgreSQL's 22P02 says
  "invalid input syntax for type uuid: \"x\"". So `err` is the error's
  text only under `LogArgs` and when every argument was shown. Otherwise
  it is redacted (`RedactedError`):
  - for a constraint violation (a `*ConstraintError` in the chain, or a
    driver error `dberr.Constraint` reads), the entity, the kind, the
    constraint's name and the driver's code;
  - for one of velox's own errors whose text is fixed (`safeError`):
    `ErrNotFound`, `ErrNotSingular`, `ErrStale`, `ErrUnbounded`,
    `ErrNoWhere`, `ErrLimitTooLarge`, `ErrTooManyRows`, `ErrImmutable`,
    `ErrVersionSet`, `ErrPolicyUpsert`, `ErrOutsideTx`, `ErrTxControl`,
    `ErrDryRun`, `ErrDenied` (a `PolicyError`, not its `Err`),
    `ErrTimeout` and `ErrLocked`, matched with `dberr.Is` through any
    wrapping, the entity and each matched error's own text, never the
    wrapper's; an `EdgeError`'s entity and edge, not its keys; a
    `ValidationError`'s entity and field, then "validation failed",
    never its `Err`. `ErrOutOfRange` and `ErrNoShard` are not in the set:
    their wrappers name a value;
  - for any other error, the Go types of its chain, the driver's code
    when `dberr.DriverCode` has one, and whether a context's end or
    velox's deadline caused it.

  Each ends with "the message is hidden". A statement with no argument
  under `LogArgs` counts as all shown.
- Tracking costs one predictable branch per argument when it is off. The
  second write happens only under `LogArgs`, for a logger enabled at
  Debug.

## Consequences

- `internal/apitest/aftercommit_test.go` covers order and timing, the
  context, the rollbacks, the savepoints, the refusals, the panics and a
  hook. `internal/engine/aftercommit_internal_test.go` covers a retried
  attempt, the TxAround order, a TxAround hook that fails or panics after
  the commit, and a call from another goroutine that races the end.
  `internal/integration/aftercommit_test.go` covers a failing statement's
  savepoint on PostgreSQL, MySQL and SQLite.
- `internal/apitest/values_test.go` covers Values and IDs under a policy, a
  filter and `WithoutPolicy`, a nullable column, the refusals and real
  SQLite rows.
- `internal/apitest/log_test.go` checks that no Sensitive value is printed
  in an update, an insert, a subquery on another entity, an Expr or raw
  SQL, on every dialect, nor when a hook changed the arguments. It also
  checks that a MySQL 1062 and a PostgreSQL 22P02 that echo a value are
  redacted. `internal/integration/keys_test.go` reads `IDs` of a UUID key
  and a string key.
- veloxe's `Strings(ctx)`, `Ints(ctx)` and the like map to `Values` with
  the handle's `Key()`. `IDs(ctx)` maps to `velox.IDs[int64](ctx, q)`.
  `Debug()` maps to `WithLog(slog.Default())` with the logger at Debug.
