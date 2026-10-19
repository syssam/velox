# velox2 examples

Small runnable programs, one topic each, on an in-memory SQLite database (routing uses SQLite files, one per database).
Each directory is a `main` package with its own `schema/` and `gen/` packages,
a README, and a golden test of its output.

| Example | What it shows | Run |
|---|---|---|
| [start](start) | Full CRUD on one entity: `Insert`, `InsertAll`, `Where`/`OrderBy`/`Limit`/`All`, `One` and `ErrNotFound`, `Count`, `Iter`, `Update`…`Set`…`Where`, `Save`, `Delete`…`Where`, and the `ErrUnbounded`/`ErrNoWhere` guards. **Start here.** | `go run ./start` |
| [relations](relations) | One-to-many and belongs-to, declared with `edge.To` and `edge.From`; `With` eager loading, nested and filtered; `Has`/`HasWhere`; keyset pagination with `After` | `go run ./relations` |
| [tenant](tenant) | `WithTenant` scoping of reads, writes and inserts; `ErrNoTenant`, `ErrTenantMismatch`, `ErrTenantImmutable`; `AllTenants` with a reason; a tenant-safe upsert | `go run ./tenant` |
| [policy](policy) | `Policy[E]` Read/Write/Insert by the user in the context; `ErrDenied` and `*PolicyError`; a child visible when its parent is, through `Has`; `SkipPolicy`; `SQL(ctx)` | `go run ./policy` |
| [hooks](hooks) | The typed `Hooks()` of an entity: an insert hook deriving a field, an update hook that reads and sets values through a `Mutation` and writes an audit row on the write's DB (`m.DB()`; inside `db.Tx` it commits or rolls back with the write), a delete hook narrowing with `Where`, `On`, a generic hook any entity can list, `Save` writing back, `IsDryRun` | `go run ./hooks` |
| [tx](tx) | `db.Tx` commit and rollback, a nested `Tx` savepoint, `ConstraintError`, `RetrySerializable`, `Timeout` and `WithDeadlines` | `go run ./tx` |
| [routing](routing) | `OpenShards` with `WithShardFor` choosing a shard by tenant, a read replica, `WithSession` pinning a request's reads to the primary after its write, `Primary`, `OnShard`, `ErrNoShard`, and `Statement.Route`. SQLite files, one per database | `go run ./routing` |
| [otel](otel) | Tracing and metrics with `contrib/otel` on an in-memory exporter: the span tree of a request and its transaction, fingerprints, call sites, the SQL comment sent, and the metric points | `go run ./otel` |
| [tour](tour) | One walkthrough that touches everything once | `go run ./tour` |

Run from this directory:

```sh
cd example
go run ./start
```

## How the examples are built

- Entities are declared in `schema/` with the ent-style DSL: a struct that
  embeds `schema.Schema`, with `Fields`, `Edges`, `Indexes` and `Mixin`
  methods. A schema holds structure only.
- `gen/` is written by velox-gen from the schema, except the files you write
  by hand: the `Policy()` and `Hooks()` methods of an entity, typed with its
  generated handles (`gen.Users`, …), and any helpers they use. velox-gen
  binds those methods into the entity's metadata. Never edit the
  `*_velox.go` files.
- Tables are created with plain DDL. Planning migrations from the schemas is
  a later velox2 sub-project.
- The output is deterministic. `go test ./...` compares each example's output
  with its `testdata/output.golden`; after an intended change,
  `go test ./start -update` rewrites that example's file.

## Regenerate

velox-gen is a tool dependency of this module (the `tool` line in `go.mod`).
After changing a schema, run from `example/`, for the example `<x>`:

```sh
go tool velox-gen -out ./<x>/gen ./<x>/schema
```

`go tool velox-gen -check -out ./<x>/gen ./<x>/schema`
writes nothing and fails when a generated file is out of date, and
`go tool velox-gen verify -out ./<x>/gen ./<x>/schema` builds every entity's
schema and reports its mistakes. CI runs both for every example, with
`go vet`, `gofmt`, golangci-lint and the tests.

This directory is its own Go module, so the velox2 module does not depend on
anything the examples use. In a module of your own, add the tool with
`go get -tool github.com/syssam/velox2/cmd/velox-gen`. This module instead
points `github.com/syssam/velox2`, its generator and `contrib/otel` at the
directories beside it with `replace` lines, so it always builds against the
code there. Only the otel example uses the OpenTelemetry SDK.
