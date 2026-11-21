# 104. The veloxgql core and its adapters

Status: accepted, 2026-11-22. Amended by 0106: the options, their
config and `Cost` moved from the core to `gqlgenx` (`gqlgenx.WithLimits`,
`gqlgenx.WithTxOptions`, `gqlgenx.WithTxFilter`, `gqlgenx.WithBatchScope`,
`gqlgenx.Cost`), and the core imports no gqlparser.

## Context

`veloxgql` was gqlgen's runtime. Its extension, setup, presenter and
marshal functions returned gqlgen's types, and `gqlgraph` read the field of
a resolver through `graphql.GetFieldContext`. `go list -deps
./veloxgql/gqlgraph` held gqlgen, its error codes, gqlparser's validator
and `net/http`. A server on github.com/syssam/graphql-go (phase B) would
compile all of gqlgen to run velox-graphql's output.

## Decision

- `veloxgql` is the engine-free core: `Cursor`, `PageInfo`,
  `OrderDirection`, the scalar types, `Limits`, the errors and codes,
  `DB`/`DBFromContext`, `Batch`, `Cost`, `Present`, the options and
  `GeneratedCodeIsVersion27`. `gqlgraph` and `internal/gqlcore` are core
  too. None imports gqlgen, graphql-go or `net/http`; the core imports
  gqlparser's `ast`, which both engines parse with.
- Package `veloxgql/gqlgenx` is gqlgen's adapter: `NewExtension`,
  `Extension`, `Setup`, `Server`, `ErrorPresenter`, `OnCanceled`,
  `PresenterOption`, the response budget, the function marshalers
  (`MarshalTime`…, `MarshalInt64ID`) and gqlgen's selection source. It
  imports no graphql-go. A second adapter, `graphqlgo`, follows (0106).
- The options configure a `veloxgql.Config` (`Limits`, `TxOptions`,
  `TxFilter`, `BatchScope`), which `veloxgql.NewConfig(opts...)` builds
  and every adapter reads: `gqlgenx.NewExtension(db,
  veloxgql.WithLimits(l))`. An option of one engine is its adapter's.
- `veloxgql.Present(err)` is the code and message a client may see. It
  is the core of `gqlgenx.ErrorPresenter`, which adds gqlgen's integer
  overflow error (BAD_USER_INPUT, gqlgen's text) and the logging.
- The document and variable walks (`MaxTokens`, `MaxFields`,
  `MaxVariableNodes` and the rest) live in `veloxgql/internal/request`,
  not in the core's exported API. Only gqlgenx runs them today; graphql-go
  has limits of its own (Q6). `request.CheckLimits(who, l)` names the
  adapter in its error.
- `Cursor` gains `MarshalGQL(w)` and `UnmarshalGQL(v)`, which graphql-go
  needs; gqlgen keeps calling the context pair, which it prefers.
  `gqlcore.Error` gains `GraphQLExtensions()`, graphql-go's
  `ExtensionsProvider`. The scalar types keep `MarshalGQL`/`UnmarshalGQL`;
  the compile-time checks that they are gqlgen's marshalers moved to
  gqlgenx.
- A nil `*gqlgraph.Connection`'s `Page` is the empty page and its
  `TotalCount` is 0, so the zero connection is the empty one.
- `deps_test.go` enforces the rules with `go list -deps`:
  `TestCoreImportsNoEngine` (core, gqlgraph, gqlcore, cost, request),
  `TestGqlgenxImportsNoGraphqlGo`, `TestGeneratorImportsNoEngine`.

## The gqlgen gate

Generated velox-graphql output is unchanged: it names only core symbols
(`veloxgql.Cursor`, `veloxgql.PageInfo`, `gqlgraph`), so
`GeneratedCodeIsVersion27` stays. gqlgen binds a model's function
marshalers before its type, so `gqlgen.yml` names
`veloxgql/gqlgenx.Int64ID`, `.Float32`, `.Uint64`, `.Uint`, `.Time`,
`.UUID` and `.Bytes`; `JSON`, `Cursor`, `OrderDirection` and `PageInfo`
stay on `veloxgql`. examples/graphql's `graph/generated.go` changes in its
imports and in `veloxgql.MarshalTime`/`MarshalInt64ID` (and their
Unmarshal pairs) becoming `gqlgenx.`, nothing else.

## Consequences

- An application imports `gqlgenx` for the server and `veloxgql` for the
  options, the errors and the limits: before 1.0 nothing is kept for
  compatibility, so the old names are gone.
- `veloxgql.Config.TxFilter` takes gqlparser's `*ast.OperationDefinition`;
  graphqlgo must give one.
- The core's `go list -deps` no longer holds gqlgen or `net/http`.
