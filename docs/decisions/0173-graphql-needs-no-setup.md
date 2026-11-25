# 173. GraphQL needs no setup

Status: accepted, 2026-11-25. Amends 0105 and 0121: a read with no
adapter state no longer fails with `ErrNoSelectionSource`. Builds on 0171
(an edge reads on its row's DB) and 0172. Its "The setup calls stay"
part is superseded by 0175: `gqlgenx.Setup`, `NewExtension`,
`graphqlgo.With` and `veloxgql.Service` are removed, and what they gave is
re-homed in opt-in pieces.

## Context

After 0171 a resolver holds its DB, and an edge reads on the DB of its
row. A server still had to call `gqlgenx.Setup` (or `NewExtension`), or
give graphql-go `graphqlgo.With`, and a service had to call
`veloxgql.Service`. Without them `Paginate` failed with
`ErrNoSelectionSource` and `ErrNoDB`. ent needs none of it: an entgql
server is `handler.NewDefaultServer(schema)` on a resolver that holds the
client, and a service's `Paginate` runs anywhere. nextapp calls
`veloxgql.Service` at two entry points only to be let through.

The setup gave five things: the DB, the limits, the selection source (the
field a resolver resolves and its selection), the per-request batch table,
and, under gqlgen, the mutation transaction, the request's bounds and the
error presenter. After 0171 the DB is not needed by generated code. The
limits default when absent. The selection source and the batch table are
what fail-closed (0105) guarded: a server that forgot the setup read
every column and batched nothing, N+1, with no error.

## Decision

- Each adapter registers a reader of its engine's context with
  `gqlcore.Register` in its `init`, the package's only start-up code
  (`TestNothingRunsAtStart` allows that one call). A context whose state
  has no source of its own asks the registered readers, which answer only
  for their engine's context. A reader's `In` says so without allocating,
  so a batch wait finds its reader for nothing.
- gqlgen's reader finds the field from `graphql.GetFieldContext` and keeps
  a batch table for each root field, keyed by a weak pointer to the root
  `FieldContext` in a `sync.Map`, dropped by `runtime.AddCleanup` when
  the root is collected. gqlgen has no per-operation store to hold it,
  and a subscription event's fields have a new root, so each event starts
  new batches, as the extension's per-response table does.
- graphql-go's reader keeps its table in the operation's
  `OperationContext.GetOrSet`, one per operation and per subscription
  event, which the fork gives already: no fork change.
- Outside any engine (a service's list method, a job, a test) there is no
  field and no reader answers: `Paginate` reads every column and each
  edge reads alone, as under `veloxgql.Service`, with `DefaultLimits()`.
- The adapter is linked by what every server already binds: gqlgen.yml's
  `ID`, `Time` and velox's other scalars are gqlgenx's models, and a
  graphql-go schema takes `graphqlgo.Scalars`. A server that binds none
  imports the adapter blank. velox-graphql's files do not import it:
  that linked gqlgen and net/http into every program that only queries,
  measured below.
- velox-graphql, for gqlgen, refuses a gqlgen configuration that binds
  nothing from gqlgenx, naming the fix: bind `ID` to `gqlgenx.Int64ID`, or
  import gqlgenx blank in the server and give the option `gqlgenx=blank`,
  which skips the check. The configuration is the first `.gqlgen.yml`,
  `gqlgen.yml` or `gqlgen.yaml` (gqlgen's names) in the generated
  package's directory or one above it, up to the module's root; the
  module is the one velox-graphql runs in, and the package's directory is
  its import path below it. A comment does not count. With no
  configuration, a package outside the module, or `engine=graphql-go`,
  nothing is checked: a graphql-go schema cannot do without
  `graphqlgo.Scalars` or `ScalarsOnly`.
- The setup calls stay, unchanged, and none is deprecated, since each
  still does something no-setup does not:
  - `gqlgenx.Setup` and `NewExtension`: the request's size, depth, cost,
    response and time bounds, a mutation in one transaction (as entgql's
    `Transactioner`, opt-in), `ErrorPresenter`, `WithBatchScope`, and the
    DB for `veloxgql.DB(ctx)`. Its own schema-aware source and
    per-response table keep running under it.
  - `graphqlgo.With`: the DB for `veloxgql.DB(ctx)`, and a mutation's
    reads pinned to the primary of a routed DB (`velox.WithSession`).
  - `veloxgql.Service` and `ServiceWithLimits`: the DB for
    `veloxgql.DB(ctx)` and the limits; reads under it are as without it.
- Without the extension a mutation runs in no transaction, on either
  engine; that was already so under graphql-go. A resolver that needs one
  opens it (`db.Tx`, `velox.UseTx`). No silent change for a server that
  keeps `Setup`.
- `veloxgql.ErrNoSelectionSource` is no longer returned. It stays
  exported so that code that tests for it compiles.
- A generated connection's `Conn` field is exported, and
  `gqlgraph.ConnectionFromRows(rows, total, info)` makes a connection of
  rows given with no DB and no query, for a test's fake, as ent's
  connection struct is built by hand. One generic function in gqlgraph,
  nothing per entity. `veloxgql.GeneratedCodeIsVersion31`.

## How no-setup avoids N+1

0105 failed closed because a missing setup could not be seen. Now nothing
is missing: the reader comes with the adapter, and a server cannot serve
velox's types through gqlgen or graphql-go without binding its scalars,
which links it, and velox-graphql refuses a gqlgen configuration that
binds none of gqlgenx's unless the application says it imports it blank. Inside a field the reader gives the same field, the same
selection and a batch table, so an edge's first caller loads it for every
row of its batch, as before. Outside any engine there is no sibling to
batch with, and reading every column is the only answer, which 0121
already gave under `Service`. The one way to an unbatched read inside a
server is a third engine, which velox-graphql does not generate for, or a
gqlgen configuration velox-graphql cannot find (outside the generated
package's module, or under a name gqlgen is given with `--config`).

## Measured

Apple M3 Max, `-count 6`, alternating base (02d05ebb) and branch.

examples/graphql `BenchmarkNestedRequest` (gqlgen, the example's nested
query), base setup → branch setup, and branch with no setup:

| | statements | time | allocs | B/op |
| --- | --- | --- | --- | --- |
| n=10 setup, base | 5 | 4.35 ms | 32.59k | 2.23 MiB |
| n=10 setup | 5 | 4.35 ms | 32.59k | 2.23 MiB |
| n=10 no setup | 5 | 4.22 ms | 30.89k | 2.19 MiB |
| n=50 setup, base | 5 | 16.96 ms | 160.7k | 10.71 MiB |
| n=50 setup | 5 | 17.09 ms | 160.7k | 10.71 MiB |
| n=50 no setup | 5 | 16.90 ms | 152.4k | 10.53 MiB |

veloxgql/graphqlgo `BenchmarkNested` (50 authors, 10 posts each, their
author and tags), one schema on both engines:

| | statements | time | allocs | B/op |
| --- | --- | --- | --- | --- |
| graphql-go setup, base | 5 | 8.31 ms | 39.12k | 3.92 MiB |
| graphql-go setup | 5 | 8.28 ms | 39.11k | 3.91 MiB |
| graphql-go no setup | 5 | 8.08 ms | 38.95k | 3.86 MiB |
| gqlgen setup, base | 5 | 15.76 ms | 87.53k | 6.01 MiB |
| gqlgen setup | 5 | 15.78 ms | 87.53k | 6.01 MiB |
| gqlgen no setup | 5 | 14.71 ms | 83.33k | 5.90 MiB |

No setup sends the same statements and is as fast or faster: the
extension's own per-request work is gone. The setup path is unchanged.
A first build found a batch wait asking the reader for the field, +2.2%
allocations on graphql-go's setup path; `Reader.In` removed it.

Compile cost, `scripts/scale-check.sh -n 500 -s 100 -graphql` (-p=2),
base and branch alternating:

| | cold | edit + rebuild | service binary | link peak |
| --- | --- | --- | --- | --- |
| base, two runs | 7.52x, 7.37x | 6.95x, 6.77x | 7.1 MB | 299–300 MB |
| as built | 7.40x | 6.79x | 7.1 MB | 300 MB |
| with the blank import | 8.19x | 7.11x | 9.1 MB | 348 MB |

The blank import put gqlgen and net/http into each of the 100 services,
over the 8.7 MB binary threshold; without it the cost is base's.

## Rejected

- Eager loading of edges in `Paginate` from the selection: the same
  statement count, a far larger change, and edges stored by name on
  entities.
- A blank import of the adapter in velox-graphql's files, so that no
  server could leave it out: it linked gqlgen and net/http into every
  service that imports the generated package (the measured cost above).
- Deprecating the setup calls: each still does something.
