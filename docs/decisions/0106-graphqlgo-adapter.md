# 106. The graphqlgo adapter

Status: accepted, 2026-11-22.

## Context

The core (0104) and the selection source (0105) let a second engine run
velox-graphql's generated code. nextapp serves GraphQL with
github.com/syssam/graphql-go, bound by its generator gqlc, and configures
graphql-go's own limits, cost, timeouts and error presenter. It installed
no selection source for veloxe, so projection and batching never ran.

## Decision

- Package `veloxgql/graphqlgo` is graphql-go's adapter. It imports
  graphql-go and the core, and no gqlgen and no `net/http`
  (`TestGraphqlgoImportsNoGqlgen`).
- Its API is graphql-go's vocabulary, so that an application composes it
  with graphql-go's own options, as nextapp's executor does: `With(db)`,
  `MutationTx(opts...)`, `PageLimits(l)` and `Cost()` are
  `graphql.ExecutorOption`s, `Scalars()` a `graphql.SchemaOption`, and
  `ErrorPresenter(log)` a `graphql.ErrorPresenter`. It takes no
  `veloxgql.Option`, and the core holds no adapter state: the first
  design's `veloxgql.AdapterOption` and `Config.Adapter`, and a
  `graphqlgo.Option` with `Use`, were rejected. gqlgenx keeps
  `NewExtension(db, ...veloxgql.Option)`, gqlgen's idiom.
- graphql-go is pinned at `v0.0.0-20261029022150-3e0050be7462`, nextapp's
  pin. The newer 5d906e7 is not fetchable (`go list -m` reports an unknown
  revision), and nothing here needs it: every API used is at 3e0050b.
- The options work in any order. Each looks up its executor's adapter in a
  registry keyed by a weak pointer to the executor, which the first one
  makes and whose interceptors it registers; the entry is dropped when the
  executor is collected (`runtime.AddCleanup`), and the adapter holds no
  reference to the executor. graphql-go applies options in order and keeps
  its interceptors unexported, so there is nowhere else to meet. Giving
  `With`, `PageLimits` or `MutationTx` twice to one executor panics.
- `With(db)` gives an operation interceptor, which installs the state (DB,
  limits, source) and a new table of rows, and a subscription interceptor,
  which installs the state for the source's context. graphql-go runs the
  operation interceptors for each event of a subscription, so each event
  has its own table. A mutation without a transaction runs in a velox
  session.
- The source reads `graphql.FieldFrom(ctx)`: `Object` is
  `fc.Object.Name`, `Name` `fc.Field.Name`, `Type` the field type's name.
  `Key` is the parent type, the name and `fc.Selection()`, a struct of one
  pointer to the plan's selection set, which graphql-go makes once per
  field of a plan: every row of a list shares it, and two aliases have two.
  A leaf has no Key and no Sel. `On(typ)` is
  `Selection.ForType(typ).Fields()`, with graphql-go's fragments, abstract
  ones too, and `@skip`/`@include` folded, so `ok` is always true.
  `Withheld` is not consulted (Q9). `Wait` is the operation's
  `Waves().Park()` and `Unpark`.
- The spec's APIs are graphql-go's at 3e0050b: `FieldFrom`,
  `FieldContext{Field, Object}`, `FieldContext.Selection()`,
  `Selection.ForType`, `Fields` (an `iter.Seq`), `Sub`,
  `OperationInterceptor` (`InterceptOperation(ctx, oc, next)`, with
  `oc.Operation` gqlparser's `*ast.OperationDefinition`),
  `SubscriptionInterceptor`, `FieldInterceptor` and `WaveCoordinator`. A
  `Field` accessor gets no `FieldContext`, and generated code reaches
  gqlgraph only through methods that take a context, which gqlc binds with
  `Resolve`.
- `MutationTx(TxOptions(...), TxFilter(f), ErrorLog(l))` (Q5, off by
  default) runs a mutation in `db.Tx` unless the filter leaves it out,
  rolls back on any error and nulls the data. A begin or commit error is
  added, presented as `ErrorPresenter` does and logged to `ErrorLog`'s
  logger: graphql-go gives an interceptor no access to the executor's
  presenter. A field interceptor holds a per-operation mutex around each
  field's resolver (graphql-go calls interceptors around the resolver
  alone, so it cannot nest); a field that waits for it is parked in the
  wave coordinator, so an application's loader still flushes.
  `TxRetrySerializable` is refused. Without `With`, a mutation fails with
  `veloxgql.ErrNoDB`, naming `graphqlgo.With`, before any resolver.
- `PageLimits(l)` sets what gqlgraph reads from the state (`MaxPage`,
  `MaxList`, `MaxOrderTerms`, the filter limits) and the cost
  `Cost` refuses; it is an option and not an argument of `With`, so that
  `With(db)` stays one argument and the defaults need nothing. The limits
  of a whole request are graphql-go's own options (Q6): the first design's
  `Limits(l)`, which mapped velox's onto them, is gone.
- `Cost()` (Q6, off by default) runs velox's cost (`request.CostError`,
  shared with gqlgenx) against the executor's schema. It is not
  `graphql.WithQueryCost`: that takes one `QueryCost` per executor, which
  would replace the application's own, and it charges a list by
  `DefaultListSize`, not velox's `MaxList` per parent row and introspection
  apart. budget.go is not ported.
- `WithBatchScope` has no graphqlgo option: graphql-go gives each operation
  its own table, the row scope is already in every batch's key, and no
  caller needs more. It stays in the core for gqlgenx.
- `Scalars()` binds `Time` (`graphql.Time`), `UUID`, `Bytes` and `Uint64`
  to `time.Time`, `uuid.UUID`, `[]byte` and `uint64`, as veloxgql's types
  read and write them. graphql-go refuses a binding to a scalar the schema
  lacks, so the schema must declare the four, as velox's vocabulary does.
- `ErrorPresenter(log)` shows a `*graphql.Error` or `*gqlerror.Error` that
  is not wrapped as graphql-go's default presenter does (graphql-go's own
  errors, a timeout among them), and anything else as `veloxgql.Present`
  does, logging internal errors. A panic graphql-go recovered, which it has
  logged with the stack, is not logged again; graphql-go does not export
  its type, so it is known by its name, `*graphql.panicError`
  (`TestPresenterPanic` fails if that changes).

## Consequences

- The parity suite (`graphqlgo/parity_test.go`) serves one hand-bound
  schema through gqlgenx and graphqlgo: a nested connection with a
  connection, a to-one and a list edge, cursor paging, a where filter, a
  Node lookup, aliases, a velox input error and a mutation (commit and
  rollback). The JSON is equal, errors' locations aside, and so are the
  statements, in number and SQL: 6, 1, 3, 5, 4 and 0 per query, 4 and 1 in
  the mutation's transaction, under `-race`. The entities are
  hand-written, not internal/testmodel's.
- graphql-go refuses a malformed literal cursor when it validates the
  document (GRAPHQL_VALIDATION_FAILED, no data); gqlgen refuses it when it
  decodes the argument. That is the engines', not the adapters'.
- Under `MutationTx` every field of every operation takes graphql-go's
  type-erased path, as any field interceptor makes it.
- A waiter unparks itself, where graphql-go's protocol prefers the waker
  to: between the load's end and the waiter's next run the operation may
  look idle, and a loader may dispatch early. That costs a batch, never a
  result.

## Evidence

- `contrib/graphql/veloxgql/graphqlgo`: `TestParity`,
  `TestParityMutation`, `TestConcurrentRequests` (four workers, five rounds,
  6 statements each), `TestNoWith` (ErrNoSelectionSource and ErrNoDB, no
  statement), `TestOptionPanics`, `TestOptionsAnyOrder` (graphqlgo's and
  graphql-go's options mixed, With first and last), `TestMutationTxNoWith`,
  `TestMutationTxErrorLog`, `TestMutationPlain`, `TestMutationSerial` (a
  mutation's fields one at a time, a query's concurrently),
  `TestWaitParks`, `TestCost`, `TestSubscription`, `TestErrorPresenter`,
  `TestPresenterPanic`, `TestScalars`, `TestAdapterDropped`,
  `TestPublicAPI`.
- `contrib/graphql/deps_test.go`: `TestGraphqlgoImportsNoGqlgen`.
- `contrib/graphql/veloxgql/internal/request`: `TestCostError`.
