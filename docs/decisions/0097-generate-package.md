# 97. Package generate runs velox-gen's generate from Go

Status: accepted, 2026-11-21.

## Context

velox-gen's generate ran only as a command. ent and veloxe applications
generate from a Go program instead: a `generate.go` behind `//go:build
ignore` calls `compiler.Generate("./schema", cfg)` with
`gen.NewConfig(gen.WithTarget(...), gen.WithFeatures(...), gen.WithHooks(...))`.
The user asked for the same entry point.

An application that moves from veloxe also brings codegen hooks. Of the
four in the largest such schema, three have velox answers already: index
names are table-prefixed by default (0090), where inputs are opt-in with
`where=whitelist` (0087) and a field's operators are its `WhereOps`. The
fourth, a hook that sorts every entity's fields (the tenant key first, the
timestamps and actor fields last), has none.

A generic hook over the model, or templates, would give back what 0034 took
away: generated code that only the application's program can reproduce, so
that `velox-gen -check` and `verify` no longer prove the package current.

## Decision

- **Package `github.com/syssam/velox/generate`** has
  `Run(ctx, dir, Config) (Result, error)`. Config's fields are velox-gen's
  flags: `Schema` (the package argument), `Out` (`-out`), `Package`
  (`-package`), `Plugins` (`-plugin`), `Check` (`-check`), with the same
  defaults. `Env` is the go commands' environment, which replaces veloxe's
  `BuildFlags` (`GOFLAGS=-p=4`). `Stdout` and `Stderr` get what the command
  prints. The name is not `gen`, so it clashes neither with the generated
  package nor with ent habit.
- **velox-gen is a flag parser over Run.** Its generate and `-check` call
  `generate.Run`, so the command and a program write the same files by
  construction. Its output, its exit codes and its tests are unchanged. Run
  returns `ErrStale` for a stale check, which the command turns into its
  rerun hint and exit 3, and `ErrInterrupted` for a signal during the write,
  exit 130. The pipeline lives in `internal/genrun`, whose loader and writer
  verify, describe and migrate use too.
- **A plugin may run in the process.** `Plugin.Run` is
  `func(context.Context, *plugin.Request) (*plugin.Response, error)`, the
  protocol's own types. It is given the request decoded from the JSON a tool
  would read, so it sees exactly what a tool sees and cannot change what the
  next plugin reads. Its response is checked as a tool's (warnings, Error,
  paths), but for Protocol: it is built with this velox. A
  `plugin.GenerateFunc` was not chosen: Run returns the whole response, and
  an error of its own when it cannot answer.
- **`contrib/graphql/graphqlgen.Plugin(opts)`** is velox-graphql in the
  process. It answers through `plugin.Run`, as the tool does, and writes the
  tool's files byte for byte; a test generates examples/graphql's package
  with it and compares. The package is not named `generate`, which a
  generate.go would have to rename on import beside velox's; `graphqlgen` is
  veloxe's name for its GraphQL generator.
- **`Config.FieldOrder`** is the one model rewrite. It is called per entity
  between building the graph and lowering it, with the declared fields'
  names (mixins' included) in declaration order, and must return a
  permutation of them; a name left out, added or repeated fails the run,
  naming the schema. The ID stays first and is not given. The foreign keys
  velox adds for edges stay last and are not given, as ent's `Fields` holds
  neither. A field declared for an edge is a declared field and is given.
- **No hooks, templates, features or extensions.** Features are always on
  (privacy, interceptors, locking, upsert, raw SQL, defaults, migrations).
  Templates and graph hooks are not offered, for 0034's reason: the
  generated package is entirely velox-gen's, reproducible from the schema.

## Consequences

- A Config that only a program can express, FieldOrder or a plugin with
  Run, is checked by running the program with `Check` set. `velox-gen
  -check` and `verify` do not know FieldOrder; `verify -plugin` compares the
  plugins' Go files and would report a reordered file stale.
- FieldOrder shapes the generated code and the plugins' model. velox-gen
  migrate reads the schema, so new columns are planned in declaration order.
- An error about `Package` names the flag, `-package`, as the command's
  does: the message is shared.
- cmd/velox-graphql is one line over `internal/gqlplugin`, which holds its
  answer and its tests; its coverage floor is 0.0.
