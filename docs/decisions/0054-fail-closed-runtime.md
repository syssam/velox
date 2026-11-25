# 54. The runtime fails closed: a DB's bypass, a batch by its SQL, a hold by its context

Status: accepted, 2026-11-08. Amends 0028; revises the bypass in 0029, 0030
and 0038.

## Context

A senior review of velox named three places where the runtime trusted the
application to do something it could not check.

- `velox.WithoutPolicy(ctx, reason)` put the bypass in a context. A context
  is passed down every call: to a hook, to a library, to a goroutine, to an
  outbound request's code. Each callee that reached a DB was unscoped, and
  none of them chose it.
- The GraphQL batcher read one edge for many callers in one statement. It
  kept callers apart by velox's row scope and by `WithBatchScope`. A Read
  policy that read a context value of its own (the viewer, a role) and did
  not record it with `WithScopeValue` let the first caller's rows answer the
  others: it failed open.
- On a pool of one connection, velox refused a statement that would wait
  for its own Iter or Tx. It told the statement apart by its goroutine ID,
  parsed from `runtime.Stack`, which Go does not expose on purpose. It cost
  29 µs at 60 frames, and missed a goroutine the body started.

## Decision

**The bypass is a DB's.** `db.WithoutPolicy(reason)` returns a DB whose
statements, transactions and write hooks (`Mutation.DB`) skip the entities'
policies. The context form is gone. The DB shares the pool and options of
the one it came from; Close on it closes nothing. Interceptors still apply,
as before. `RowScope.PolicyBypass` is gone; `Statement.PolicyBypass` still
shows the reason to hooks. A bypass is visible where it is made, and reaches
only the code given that DB.

**A batch is keyed by its SQL.** The batch key of a GraphQL edge holds the
SQL and arguments of a read of the edge's target under each caller's
context, a dry run after the Read policy, the interceptors and the Rewrite
hooks. Callers whose policies see different rows never share a read,
whatever context values those read. A caller whose read fails reads alone.
It costs about 1.4 µs and 3 allocations a caller (SQLite, Apple M3 Max), the
rendering being cached. `WithBatchScope` remains for Around hooks, which a
dry run does not run. `WithScopeValue` is no longer needed for the batcher.

**A hold is by its context.** A pool's one connection is held by the
context its Iter or Tx was given. A statement under that context, or under
`WithoutTx` of fn's, is refused from any goroutine. Any other context waits,
as database/sql's does. If the loop or fn waits for it, it ends at its
deadline, and its error says the connection was held. `goid` is gone.
A statement or BEGIN that has waited 5 seconds for a connection its DB's
Iter or Tx holds logs one slog warning, which names the holder, so a wait
without a deadline does not hang silently. It is a warning, not a refusal:
another goroutine's wait under a derived context is legitimate, and
velox cannot tell the two apart (amended 2026-11-25).

## Consequences

- A test suite that ran every statement under a bypassing context opens its
  DB with `.WithoutPolicy(reason)` instead, and tests of a policy use the DB
  without it.
- The batcher batches less when callers' reads differ: that is the point.
- A concurrent statement under the Iter's or Tx's own context is refused,
  where it used to wait. A statement in the loop under an unrelated context
  waits until its deadline, where it used to be refused at once.
