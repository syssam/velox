# 132. A filter reads through the statement's DB

- Status: accepted, 2026-11-23. Amends
  [0098](0098-hooks-and-filters-for-every-entity.md). Amended by
  [0148](0148-explicit-aggregates-filter-entity-policy-db.md): the entity
  is a `FilterEntity`, not its name.

## Context

The port of nextapp's row scoping onto velox
(`velox2-nextapp-crud`: `internal/rowscope`, `salesorder/scope.go`,
`access/filter_in_tx_test.go`) makes a `WithFilter` filter that reads the
database: a sales order's scope is the caller's organization subtree and
teammates, which the filter looks up before it returns `FieldIn`. A
`Filter` was given `(ctx, entity, op)` and no DB. The filter read on the DB
it captured at `Connect`. Inside `db.Tx` the statement's context carries the
transaction's mark, so that read failed with `ErrOutsideTx`; under
`WithoutTx(ctx)` it needed a second connection, which SQLite's pool of one
does not have, and was refused. nextapp's lookups swallow the error and
narrow, so a statement saw 3 orders outside a transaction and 2 inside.

## Decision

- `Filter` takes the DB as a parameter:
  `func(ctx context.Context, db *DB, entity string, op Op) (Predicate, error)`.
  `db` is the DB the statement runs on: the transaction's inside `Tx` (a
  savepoint's inside a nested one), and the `WithoutPolicy` or `Record` DB
  the statement was made on. It is never nil. A filter that does not read
  names it `_`.
- Why a parameter, not an accessor on the context (`FilterDB(ctx)`, the
  first form of this record): a hook reaches its DB through `m.DB()` on
  its `Mutation`, so a filter reaches its DB through the argument it is
  given, one rule for both. A context accessor returns nil under a context
  no filter was given, so every caller checks or trusts; a parameter
  cannot be nil, and the compiler checks that a filter that reads has the
  DB in scope. The cost is that `Filter`'s type changed, which is free
  before 1.0.
- The engine calls each filter with its own context type, `filterCtx`: the
  statement's context, the filter (its place in the shared config) and the
  frame of the filter whose read made the statement; the DB is passed
  beside it. One allocation per call, made only on a DB with filters; a
  filter runs at most once per statement for each entity and op (0098), so
  a DB without filters pays nothing. The root wraps the filter once, at
  `WithFilter`, to hand it the root `*DB`.
- Recursion: a statement made under a call of filter f, directly or
  through another filter's reads, is not narrowed by f. The scope walks the
  frames of the statement's context and skips each filter it finds there,
  in a row condition and in the join-entity check of a many-to-many
  relation. So a filter that reads its own entity ends, and two filters
  that read each other's entities end, at a depth no greater than the
  number of filters. The other filters, the interceptors and the policies
  apply to the filter's reads as to any statement. This is the guard
  nextapp wrote by hand (`withScopeResolution`), built in. ent and veloxe
  have none: an interceptor's own query runs every interceptor, and the
  application marks its context.
- Skipping all filters for a filter's reads was rejected: a soft-delete
  filter would then let a row scope see deleted organizations, where
  nextapp's soft delete, an interceptor of its own, applied. Refusing a
  re-entry with an error was rejected: a filter that guards itself with a
  context value would be refused before its guard ran. A depth limit was
  rejected as arbitrary.
- Caching: velox keeps nothing between statements, as before. A filter's
  reads run once per statement for each entity and op it is asked about.
  A filter that should read once per request keeps its answer in the
  request's context. The render cache is unchanged: a filter's condition
  is a predicate like any other, its values bound.
- SQLite's pool of one: the scope runs before the statement takes the
  connection, so a filter's read outside a transaction does not wait for
  it. The check that refuses a statement waiting for the connection its
  own `Tx` or `Iter` holds sees through `filterCtx`: a filter's read on db
  under `WithoutTx(ctx)`, or under the context `Tx` was given, is refused
  as it was without the frame, not left waiting.

## Consequences

A row scope that reads gets the same condition inside a transaction and
outside. A filter's reads are statements like any other, sent before the
statement they were made for. `Interceptor` and `Policy` functions
get no DB; their conditions are the schema's, and a read there would need
the same rule.

## Evidence

- `internal/apitest/filterdb_test.go`: `TestFilterReadsThroughTheStatementsDB`
  (the bed's shape on SQLite's pool of one: one note inside and outside a
  Tx through the `db` parameter; a captured DB gives `ErrOutsideTx`, and
  `WithoutTx` is refused, not left waiting, which hung before
  `unfiltered`), `TestFilterGetsTheStatementsDB`, `TestGuideFilterExample`
  (`internal/apitest/filter_guide_test.go`, the guide's example),
  `TestFilterReadsWithoutItself` (two filters reading each other's entity,
  through a many-to-many `Has`).
