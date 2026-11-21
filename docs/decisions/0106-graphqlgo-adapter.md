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
  with graphql-go's own options, as nextapp's executor does: `With(db)` is
  a `graphql.ExecutorOption`, `Scalars()` a `graphql.SchemaOption`, and
  `ErrorPresenter(log)` a `graphql.ErrorPresenter`; there is nothing else. It takes no
  `veloxgql.Option`, and the core holds no adapter state: the first
  design's `veloxgql.AdapterOption` and `Config.Adapter`, and a
  `graphqlgo.Option` with `Use`, were rejected. gqlgenx keeps
  `NewExtension(db, ...veloxgql.Option)`, gqlgen's idiom.
- graphql-go is pinned at `v0.0.0-20261029022150-3e0050be7462`, nextapp's
  pin. The newer 5d906e7 is not fetchable (`go list -m` reports an unknown
  revision), and nothing here needs it: every API used is at 3e0050b.
- `With` given twice serves with the later one's state: graphql-go runs
  the first interceptor given outermost, so the later one installs inside
  it.
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
- There is no transaction per operation. A mutation runs in a velox
  session, in no transaction, and a resolver that needs one opens it
  (`db.Tx`, `velox.UseTx`), as nextapp's do (its `WithTx`). An earlier
  `MutationTx` (with `TxOptions`, `TxFilter`, `ErrorLog`, first ordered by
  a package registry keyed by a weak pointer to the executor, then by an
  ordering rule) was removed: nothing called it, and its field interceptor,
  which serialised a transaction's fields, sent every field of every
  operation down graphql-go's type-erased path. An application that wants
  one writes an operation interceptor of about 20 lines around
  `velox.UseTx`. gqlgenx keeps its transaction per mutation
  (`veloxgql.WithTxOptions`, `veloxgql.WithTxFilter`), which gqlgen's
  extension runs without a field interceptor of graphql-go's.
- No limits are configurable under graphqlgo, and velox's cost is not
  applied. ent and gqlgen leave a request's limits to the server, and
  graphql-go has its own (`WithMaxDepth`, `WithMaxComplexity`,
  `WithQueryCost`, `WithMaxResponseBytes`, `WithOperationTimeout`). velox
  keeps only the bounds its own rules need, as a read needs a bound
  (`ErrUnbounded`): generated code reads `veloxgql.DefaultLimits()`,
  MaxPage 100, MaxList 1000, MaxOrderTerms and the filter limits, a
  backstop against unbounded reads and where-input abuse, not
  configuration. An application that wants a smaller page refuses a larger
  `first`/`last` in its own interceptor, as nextapp's PaginationInterceptor
  does. `LimitsFromContext` reports `DefaultLimits()`, and its godoc says
  velox does not enforce the request-wide fields under graphqlgo. The
  earlier `Limits(l)`, `PageLimits(l)` and `Cost()` were removed; gqlgenx
  keeps `veloxgql.WithLimits`, as gqlgen has no limits of its own beyond
  complexity.
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
  logged with the stack, is not logged again, wrapped or not; graphql-go
  does not export its type, so it is known by its name,
  `*graphql.panicError`, anywhere in the error's chain
  (`TestPresenterPanic` fails if that changes).

## Consequences

- The parity suite (`graphqlgo/parity_test.go`) serves one hand-bound
  schema through gqlgenx and graphqlgo: a nested connection with a
  connection, a to-one and a list edge, cursor paging, a where filter, a
  Node lookup, aliases, a velox input error and a mutation outside a
  transaction (gqlgenx's under a `WithTxFilter` that leaves it out). The
  JSON is equal, errors' locations aside, and so are the statements, in
  number and SQL: 6, 1, 3, 5, 4 and 0 per query, under `-race`. The entities are
  hand-written, not internal/testmodel's.
- graphql-go refuses a malformed literal cursor when it validates the
  document (GRAPHQL_VALIDATION_FAILED, no data); gqlgen refuses it when it
  decodes the argument. That is the engines', not the adapters'.
- A waiter unparks itself, where graphql-go's protocol prefers the waker
  to: between the load's end and the waiter's next run the operation may
  look idle, and a loader may dispatch early. That costs a batch, never a
  result.

## Evidence

- `contrib/graphql/veloxgql/graphqlgo`: `TestParity`,
  `TestParityMutation`, `TestConcurrentRequests` (four workers, five rounds,
  6 statements each), `TestNoWith` (ErrNoSelectionSource and ErrNoDB, no
  statement), `TestWith` (nil DB, given twice, a mutation in no
  transaction), `TestWaitParks`, `TestLimitsFromContext`,
  `TestSubscription`, `TestErrorPresenter`, `TestPresenterPanic`,
  `TestScalars`, `TestPublicAPI`.
- `contrib/graphql/deps_test.go`: `TestGraphqlgoImportsNoGqlgen`.
- `contrib/graphql/veloxgql/internal/request`: `TestCostError`.
