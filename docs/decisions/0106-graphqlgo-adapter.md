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
  (`TestGraphqlgoImportsNoGqlgen`). Its API: `With`, `WithMutationTx`,
  `WithCost`, `Limits`, `Scalars`, `ErrorPresenter`.
- graphql-go is pinned at `v0.0.0-20261029022150-3e0050be7462`, nextapp's
  pin. The newer 5d906e7 is not fetchable (`go list -m` reports an unknown
  revision), and nothing here needs it: every API used is at 3e0050b.
- `With(db, opts...)` returns executor options: an operation interceptor,
  which installs the state (DB, limits, source) and a new table of rows; a
  subscription interceptor, which installs the state for the source's
  context. graphql-go runs the operation interceptors for each event of a
  subscription, so each event has its own table. A mutation without a
  transaction runs in a velox session.
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
  `OperationInterceptor` (now `InterceptOperation(ctx, oc, next)`, with
  `oc.Operation` gqlparser's `*ast.OperationDefinition`, which
  `veloxgql.WithTxFilter` takes), `SubscriptionInterceptor`,
  `FieldInterceptor` and `WaveCoordinator`. A `Field` accessor gets no
  `FieldContext`, and generated code reaches gqlgraph only through methods
  that take a context, which gqlc binds with `Resolve`.
- Engine-only options are `veloxgql.Option`s, so they are given with the
  core's: `graphqlgo.With(db, veloxgql.WithLimits(l),
  graphqlgo.WithMutationTx())`. The core gains `veloxgql.AdapterOption(v)`,
  which appends `v` to the new `Config.Adapter`; an adapter refuses a value
  it does not know (`With` panics, gqlgenx's `Validate` fails).
- `WithMutationTx` (Q5, off by default) runs a mutation in `db.Tx` unless
  `WithTxFilter` leaves it out, rolls back on any error, nulls the data,
  and adds a commit's error presented by `ErrorPresenter(nil)`. A field
  interceptor holds a per-operation mutex around each field's resolver
  (graphql-go calls interceptors around the resolver alone, so it cannot
  nest); a field that waits for it is parked in the wave coordinator, so an
  application's loader still flushes. `TxRetrySerializable` is refused.
- `WithCost` (Q6, off by default) runs velox's cost
  (`request.CostError`, shared with gqlgenx) against the executor's schema,
  which an executor option records; the options then serve one executor,
  and a second panics. A subscription over the cost does not open. budget.go
  is not ported.
- `Limits(l)` maps `MaxDepth`, `MaxTokens`, `MaxErrors`,
  `MaxResponseBytes` and `Timeout` onto graphql-go's options. `With` does
  not add them: an application configures graphql-go's limits itself.
- `Scalars()` binds `Time` (`graphql.Time`), `UUID`, `Bytes` and `Uint64`
  to `time.Time`, `uuid.UUID`, `[]byte` and `uint64`, as veloxgql's types
  read and write them. graphql-go refuses a binding to a scalar the schema
  lacks, so the schema must declare the four, as velox's vocabulary does.
- `ErrorPresenter(log)` shows a `*graphql.Error` or `*gqlerror.Error` that
  is not wrapped as graphql-go's default presenter does (graphql-go's own
  errors, a timeout among them), and anything else as `veloxgql.Present`
  does, logging internal errors.

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
- Under `WithMutationTx` every field of every operation takes graphql-go's
  type-erased path, as any field interceptor makes it.
- A waiter unparks itself, where graphql-go's protocol prefers the waker
  to: between the load's end and the waiter's next run the operation may
  look idle, and a loader may dispatch early. That costs a batch, never a
  result.

## Evidence

- `contrib/graphql/veloxgql/graphqlgo`: `TestParity`,
  `TestParityMutation`, `TestConcurrentRequests` (four workers, five rounds,
  6 statements each), `TestNoWith` (ErrNoSelectionSource and ErrNoDB, no
  statement), `TestWithPanics`, `TestMutationPlain`, `TestMutationSerial`,
  `TestWaitParks`, `TestCost`, `TestLimits`, `TestSubscription`,
  `TestErrorPresenter`, `TestScalars`, `TestPublicAPI`.
- `contrib/graphql/deps_test.go`: `TestGraphqlgoImportsNoGqlgen`.
- `contrib/graphql/veloxgql/internal/request`: `TestCostError`;
  `veloxgql`: `TestConfig` (AdapterOption); `gqlgenx`:
  `TestForeignAdapterOption`.
