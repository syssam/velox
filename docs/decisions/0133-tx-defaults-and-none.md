# 133. Transaction defaults, None, and no IDs method

- Status: accepted, 2026-11-23.

## Context

The port of nextapp's approval workflow onto velox found three small gaps.
nextapp's `WithTx` retries a transaction 3 times, bounds it to 30 seconds
and turns a panic into an error, for every call. velox's `Tx` takes
`TxRetrySerializable`, `TxTimeout`, `TxIsolation` and `TxReadOnly` per
call, so a port repeats them at each of its transactions. A row filter
that denies a caller every row wrote `velox.Not(velox.Allow())`, rendered
`NOT (1 = 1)`, for ent's `sql.False()`. And veloxe's `Query().IDs(ctx)` is
`velox.IDs[int64](ctx, q)`.

## Decision

- `velox.WithTxDefaults(opts ...TxOption) Option` gives every outermost
  `Tx` on the DB those options before the call's own, which come after and
  override them: `TxRetrySerializable(0)` runs a call once under a default
  of 3, `TxTimeout(d)` replaces a default timeout, and `TxTimeout(0)` lifts
  it, leaving `WithDeadlines`' `Tx` and `MaxTx`. That makes `TxTimeout(d)`
  with `d <= 0` "no bound of its own", where it was "ignored"; with no
  default the two are the same. Several `WithTxDefaults` add up in order. A
  zero `TxOption` panics at the option, not at the first `Tx`.
- The defaults reach the transactions velox begins itself for a write that
  needs one, such as a chunked insert, and the DBs derived from the DB
  (`WithoutPolicy`, `Record`), which share its config. A nested `Tx`, a
  savepoint, applies none and still refuses options: its fn runs again
  only when the outermost transaction retries. `UseTx` applies none: the
  transaction is the caller's. `TxRetries(opts...)` reads a call's options,
  not the DB's.
- gqlgenx's mutation transaction ends its options with
  `TxRetrySerializable(0)`: gqlgen writes the response once, so a retry
  in the defaults must not reach it, as `Validate` already refuses one in
  `WithTxOptions`.
- No panic recovery option. A velox `Tx` rolls back and panics again, the
  Go convention and database/sql's; an application that wants an error
  recovers in fn.
- `velox.None() Predicate` matches no row: `1 = 0` on every dialect, an Or
  of nothing, which the renderer and the guard's `alwaysFalse` already
  knew. It sits beside `Allow()` for policies, interceptors and filters.
  Typed code writes `Or[E]()`, as it writes `And[E]()` for every row.
- No `Query.IDs` method. A generic method on `Query[E]` costs every package
  that names `Query` its declaration, about 1 KB
  (`internal/repocheck/compilecost_test.go`: `Paginate` and `Rows` together
  2.1 KB), for an entity's package that already reaches
  `velox.IDs[K](ctx, q)`. A non-generic `IDs(ctx) ([]int64, error)` would be
  wrong for a UUID or text key. The function stays, and
  `docs/coming-from-veloxe.md` maps the call.

## Consequences

A port sets nextapp's retry and timeout once, at `Connect`. Every
transaction's fn on such a DB must be safe to run again, velox's own
writes' hooks included. Generated code does not change.

## Evidence

- `internal/apitest/txdefaults_test.go`: a default retry and its override,
  a savepoint that fails under a default retry (each fn runs twice), a
  default timeout, its override and `TxTimeout(0)`, a default isolation in
  velox's own transaction, the zero option.
- `contrib/graphql/veloxgql/gqlgenx/config_internal_test.go`:
  `TestRunOptionsEndWithoutRetry`.
- `internal/apitest/none_test.go`: `1 = 0` on PostgreSQL, MySQL and SQLite;
  a read, an update and a delete under `None` on SQLite.
