# 175. GraphQL's setup calls are removed

Status: accepted, 2026-11-25. Supersedes the setup parts of 0105, 0121 and
0173 ("The setup calls stay"). Builds on 0171 (a query carries its DB) and
0173 (each adapter registers a reader in `init`).

## Context

Since 0173 a server pages, projects and batches with nothing installed.
`gqlgenx.Setup`, `gqlgenx.NewExtension`, `graphqlgo.With` and
`veloxgql.Service` stayed, because each still gave something. Two ways to
build a server is one too many, and entgql and veloxe have only the
no-setup one. The user's decision: remove them, and keep only the no-setup
version.

What they gave, and where each piece goes, must not vanish silently. The
rule for each: optional, standalone, named for one feature, never needed to
page, batch or read, in ent's shape where ent has one.

## Decision

Removed, with no replacement under the same name: `gqlgenx.Setup`,
`gqlgenx.Server`, `gqlgenx.NewExtension`, `gqlgenx.Extension` and its
`NewContext`, `gqlgenx.Option` with `WithLimits`, `WithTxOptions`,
`WithTxFilter` and `WithBatchScope`; `graphqlgo.With`; `veloxgql.Service`,
`ServiceWithLimits`, `DB`, `DBFromContext`, `LimitsFromContext`, `ErrNoDB`
and `ErrNoSelectionSource`. In `gqlcore`, the context state that installed
a DB, limits and a source (`Install`, `InstallDB`, `WithDB`, `DB`,
`LimitsOf`, `Source`, `WithTable`, `TableFromContext`, `Table.Scope`):
nothing else read it. `FieldOf` asks only the registered readers. An edge
of a row no statement made fails with `velox.ErrNoSource` alone.

Where each piece went:

1. **The gqlgen mutation transaction** is `gqlgenx.Transactioner{DB: db,
   Options: ...}`, entgql's name and shape (`entgql.Transactioner{TxOpener:
   client}`), a value used as `srv.Use(gqlgenx.Transactioner{DB: db})`. It
   keeps what made the extension's transaction safe: a mutation's field
   resolvers run one at a time under a per-operation lock, and its
   resolvers get the transaction's context, so a statement on `r.DB` under
   that context joins it (0171). It rolls back on any error, on a panic,
   and when `ResponseLimit` stopped the response, in either `Use` order.
   It refuses `TxRetrySerializable`, as gqlgen makes a response once.
   `WithTxFilter`, which left some mutations out, is dropped: entgql has no
   such filter, and a server that wants a mutation outside a transaction
   omits `Transactioner` and opens transactions in resolvers.

   Neither `Transactioner` nor `ResponseLimit` is a gqlgen
   `FieldInterceptor`: gqlgen's chain of field interceptors makes a closure
   for each field it resolves, for each interceptor, so splitting the one
   extension into two cost a query about 10 allocations at 3 nodes, and
   more with every field. Each wraps the operation's
   `OperationContext.ResolverMiddleware` in its `InterceptOperation`
   instead: `Transactioner` for a mutation only, so a query pays nothing
   for its lock, and `ResponseLimit` for every operation, with no closure
   per field.
2. **Hardening.** velox keeps what gqlgen cannot do, each its own piece:
   - `gqlgenx.CostLimit(db)`: the connection-aware cost (`internal/cost`),
     with `MaxCost` and `MaxIntrospectionCost`. It replaces gqlgen's
     `extension.FixedComplexityLimit`, which counts fields, not rows.
   - `gqlgenx.RequestLimit(db)`: the pre-parse bounds of the document and
     its variables (0027): `MaxBodyBytes` of the query and variables,
     `MaxTokens`, `MaxFields`, `MaxFragments`, `MaxDepth`, `MaxNameBytes`,
     `MaxLiteralBytes`, `MaxVariableNodes`. gqlgen has only a token limit,
     and its validation is quadratic before that.
   - `gqlgenx.ResponseLimit(db)`: `MaxResponseBytes`, `MaxErrors`,
     `Timeout`, which a cost cannot see.
   - `gqlgenx.MaxBodyBytes(n)`: HTTP middleware bounding the body and each
     websocket message, as `Setup`'s handler did.
   - `gqlgenx.ErrorPresenter` stays as it was. It now also shows gqlgen's
     answer to `__schema` with introspection off as "introspection is
     disabled" (GRAPHQL_VALIDATION_FAILED), which the extension did by
     refusing the operation.
   - `gqlgenx.QuietValidation(db)`, for `srv.SetValidationRulesFn` with
     `srv.SetDisableSuggestion(true)`: gqlgen's flag quiets the field rule
     alone, and QuietValidation quiets the argument, type, value and
     scalar-leaf rules too, holds validation to `MaxErrors` errors, and
     runs gqlparser's rules as one rule, which spares about 70
     allocations per uncached request (Setup's rules, kept).
   Dropped, as gqlgen does it: the parser's token limit
   (`srv.SetParserTokenLimit`, or `RequestLimit`). No bundle recreates
   `Setup`: the guide's "Hardening a public server" lists each knob, and
   examples/graphql composes them.
3. **Page limits** are the DB's. `veloxgql.WithLimits(l)` is a
   `velox.Option`, and `veloxgql.LimitsOf(db)` reads it. A connection reads
   the limits of its query's DB, an edge those of the DB that read its row,
   `ResolveNodes` those of its DB, and the three request pieces those of
   the DB they are given, so the cost multiplies by the page the reads
   allow. The query already carries its DB (0171), so no context wrapper
   is needed, and a DB is not a global. velox's root had no way for
   another module to attach a setting to a DB, so it gains two small,
   general names: `velox.WithValue(key, value)` and `velox.Value(db, key)`,
   read as `context.Value` is, kept by every DB derived from the DB
   (transactions, `WithoutPolicy`, `Record`, a row's source). `Value` is a
   function, as `Pool` is: a method of DB is in the export data of every
   entity package, and the values are an `any` in the config for the same
   reason (TestGenericTypesCompileSmall). And
   `untyped.QueryDB(q)`, the DB of a TableQuery, as `untyped.SourceDB` is a
   row's. A where input's `Pred` holds to the filter limits of the
   connection's DB, which `Paginate` passes to it; called directly, outside
   a connection, it holds to `DefaultLimits()`.
4. **graphql-go's mutation session.** The fork has
   `graphql.WithOperationInterceptor` and `OperationInterceptorFunc`, so
   the session is the application's interceptor, in the guide and in
   graphqlgo's package documentation: on a mutation,
   `ctx = velox.WithSession(ctx)`. No velox option. nextapp writes its own.
   Under gqlgen, `Transactioner` runs the mutation on the primary, so it
   reads its own writes; a server without it uses `srv.AroundOperations`
   with the same two lines.
5. **Other per-request state.** `WithBatchScope` is dropped: a batch's key
   already holds the row scope, the DB, a list edge's `MaxList` and the SQL
   each caller would send after the Read policy, the interceptors and the
   Rewrite hooks, and only a context value that an Around hook alone reads
   escapes it. The extension's schema-aware selection source and its
   per-response batch table go with it: the reader's per-root table and its
   schema-less selection (0173) sent the same statements. `Extension.
   NewContext` is plain `ctx`. `veloxgql.DB(ctx)` is `r.DB`.

Resolvers now write through `r.DB` under the Transactioner's context, not
through the transaction's handle (0171), which showed two engine gaps,
fixed with this change:

- An entity a joining DB inserts, saves or reads under the transaction's
  context takes the transaction's Source (`DB.SourceIn`), as one the
  transaction's own handle makes does. Before, it took the DB's own
  Source; when the transaction was begun WithoutPolicy, that Source's DB
  (the one without the reason) is not the beginner, so the row's edge
  read failed with ErrOutsideTx (`createVolume { shelf }`).
- A write on `db.In(ctx)` (Write, UpdateOne and the others) built its
  statement on the caller's DB, so an Around hook saw `InTx` false;
  the pipeline now sets `InTx` from the DB that runs it.

0173's fail-closed reasoning still holds: no part of reading depended on
the removed calls, the reader is linked by what a server binds, and
velox-graphql's gqlgen.yml check stays.

The check reads gqlgen.yml only, and only where velox-graphql finds it. A
server that links no adapter (a graphql-go server without
`graphqlgo.Scalars`, a gqlgen.yml elsewhere) would read each edge a row
at a time with nothing to say so. So `FieldOf` logs one slog warning per
process when no reader is registered (amended 2026-11-25, after review).

Generated code keeps its shape: a where input's `Pred` doc comment names
the DB's limits. `veloxgql.GeneratedCodeIsVersion31` stays.

## Measured

On 2026-11-25, darwin/arm64 (Apple M3 Max), go1.27.4, gqlgen v0.17.86;
base 65d23efa against this change, test binaries run in turn, six runs
each, benchstat. "setup" is `Setup` at base and the application's
composition of the opt-in pieces after (`Transactioner`, `RequestLimit`,
`CostLimit`, `ResponseLimit`, `ErrorPresenter`, `SetDisableSuggestion`,
`QuietValidation`, `MaxBodyBytes`).

examples/graphql `BenchmarkNestedRequest`, 5 statements in every case:

| | base | after | |
| --- | --- | --- | --- |
| setup n=10 | 4.455 ms, 32.58k allocs | 4.368 ms, 30.94k allocs | ~ time, -5.0% allocs |
| setup n=50 | 17.70 ms, 160.7k allocs | 17.30 ms, 152.4k allocs | -2.2% time, -5.2% allocs |
| nosetup n=10 | 4.323 ms, 30.89k allocs | 4.311 ms, 30.89k allocs | ~ |
| nosetup n=50 | 17.42 ms, 152.4k allocs | 17.24 ms, 152.4k allocs | ~ |

veloxgql/graphqlgo `BenchmarkNested`, 5 statements in every case, each
case run alone (run in one binary, the cases' order moved the times by up
to 11% while their allocations stayed):

| | base | after | |
| --- | --- | --- | --- |
| graphqlgo/nosetup | 8.484 ms, 39.19k allocs | 8.575 ms, 39.18k allocs | ~ |
| gqlgen/nosetup | 17.32 ms, 83.38k allocs | 17.22 ms, 83.38k allocs | ~ |
| gqlgen setup / hardened | 16.44 ms ±15%, 87.55k allocs | 18.16 ms ±8%, 83.53k allocs | ~ time (p=0.18), -4.6% allocs |

examples/graphql `TestMutationBudget`, `updateLabel`: 2 statements and
446 allocations at base under `Setup`; 444 after under the composition.
Before the field hooks moved to
the operation's middleware and the validation ran as one rule, the
composition made 517.

## Rejected

- A `veloxgql.WithLimits` context wrapper: the caller must remember it,
  as `Service`.
- A global default of the limits: velox has no global flags.
- Limits in the schema's annotations, generated into each type: static,
  a shape change, and the cost extension would need each type's.
- A `graphqlgo.MutationSession()` option: the fork's interceptor does it in
  a few lines of the application's.
- One `gqlgenx.Hardening` bundle: it would be `Setup` again.
