# velox2 examples

Small runnable programs, one topic each, on SQLite files in a temporary directory (routing uses one per database).
Each directory is a `main` package with its own `schema/` and `gen/` packages,
a README, and a golden test of its output (graphql's test checks the
responses of its server instead).

| Example | What it shows | Run |
|---|---|---|
| [start](start) | Full CRUD on one entity: `Insert`, `InsertAll`, `Where`/`OrderBy`/`Limit`/`All`, `One` and `ErrNotFound`, `Count`, `Iter`, `Update`…`Set`…`Where`, `Save`, `Delete`…`Where`, and the `ErrUnbounded`/`ErrNoWhere` guards. **Start here.** | `go run ./start` |
| [relations](relations) | One-to-many and belongs-to, declared with `edge.To` and `edge.From`; `With` eager loading, nested and filtered; `Has`/`HasWhere`; keyset pagination with `After` | `go run ./relations` |
| [tenant](tenant) | `WithTenant` scoping of reads, writes and inserts; `ErrNoTenant`, `ErrTenantMismatch`, `ErrTenantImmutable`; `WithAllTenants` with a reason; a tenant-safe upsert | `go run ./tenant` |
| [policy](policy) | `Policy[E]` Read/Write/Insert by the user in the context; `ErrDenied` and `*PolicyError`; a child visible when its parent is, through `Has`; `WithoutPolicy`; `SQL(ctx)` | `go run ./policy` |
| [hooks](hooks) | The typed `Hooks()` of an entity: an insert hook deriving a field, an update hook that reads and sets values through a `Mutation` and writes an audit row on the write's DB (`m.DB()`; inside `db.Tx` it commits or rolls back with the write), a delete hook narrowing with `Where`, `On`, a generic hook any entity can list, `Save` writing back, a dry run that refuses the hook's audit write | `go run ./hooks` |
| [tx](tx) | `db.Tx` commit and rollback, a nested `Tx` savepoint, `ConstraintError`, `TxRetrySerializable`, `TxTimeout` and `WithDeadlines` | `go run ./tx` |
| [routing](routing) | `OpenShards` with `WithShardFor` choosing a shard by tenant, a read replica, `WithSession` pinning a request's reads to the primary after its write, `WithPrimaryReads`, `WithShard`, `ErrNoShard`, and `Statement.Route`. SQLite files, one per database | `go run ./routing` |
| [otel](otel) | Tracing and metrics with `contrib/veloxotel` on an in-memory exporter: the span tree of a request and its transaction, fingerprints, call sites, the SQL comment sent, and the metric points | `go run ./otel` |
| [graphql](graphql) | A gqlgen server on the schema velox-graphql generates: `node`/`nodes` by global ID, keyset-paged connections with `orderBy` and `where`, edges (a many-to-many among them), a renamed type, tenant scoping through all of them, and a sensitive field left out. Its test sends real GraphQL over HTTP and checks the responses and the SQL | `go run ./graphql` |
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
- Each example's tables come from its `migrations/` directory, made by
  `go tool velox-gen migrate diff -dialect sqlite` and applied with
  `migrate.Up` to a fresh SQLite file; CI checks the directories with
  `migrate diff -check`. A real application plans its migrations from the
  schemas the same way, as below.
- The output is deterministic. `go test ./...` compares each example's output
  with its `testdata/output.golden`; after an intended change,
  `go test ./start -update` rewrites that example's file.

## Migrations

There are two paths, and a migration directory is managed by one of them:
velox's own `migrate` and Atlas are alternatives for one directory.

- **velox's planner.** `go tool velox-gen migrate diff -dialect postgres`
  writes the next migration into `./migrations` from the schema package, and
  `go tool velox-gen migrate apply -db <dsn>` applies the pending ones.
- **Atlas.** `go tool velox-gen migrate sql` prints the schema package's
  `CREATE` statements, which Atlas reads through its `external_schema`. Add
  the tool with `go get -tool github.com/syssam/velox2/cmd/velox-gen`, then
  run `atlas` from the module root with this `atlas.hcl`:

  ```hcl
  data "external_schema" "velox" {
    program = ["go", "tool", "velox-gen", "migrate", "sql", "-dialect", "postgres", "./schema"]
  }

  env "velox" {
    src = data.external_schema.velox.url
    dev = "docker://postgres/16/dev?search_path=public"
    migration { dir = "file://migrations" }
  }
  ```

  `atlas migrate diff --env velox` writes a migration, and
  `atlas migrate apply --env velox -u <url>` applies it. For a schema that
  keeps tables in PostgreSQL schemas (`schema.TableIn`), use
  `docker://postgres/16/dev`, which is database-scoped. For MySQL use
  `docker://mysql/8.4/dev` with `-dialect mysql`, and for SQLite
  `sqlite://dev?mode=memory` with `-dialect sqlite`.

### Adopting an existing database

A database made before the directory (by ent, by old velox or by hand) joins
velox's planner in five steps:

1. Run `go tool velox-gen migrate baseline -dialect postgres -db <dsn>`
   against one environment. It writes a new `./migrations` whose
   `0001_baseline.sql` builds the schema package's tables as that database
   holds them. Read its warnings: an extension to add to `0001_baseline.sql`
   by hand, a sequence's start or increment it does not keep, a PostgreSQL
   domain type, which it does not create.
2. `0001_baseline.sql` may be edited, followed by
   `go tool velox-gen migrate hash`, only before any environment runs
   `apply -baseline`. After that, an edit is a checksum error on every
   baselined database.
3. Commit the directory, and run
   `go tool velox-gen migrate apply -baseline 1 -db <dsn>` once on each
   existing environment. It records the file as applied without running it,
   and refuses a history that is not empty and a database with no tables
   besides the history.
4. Run `go tool velox-gen migrate drift -db <dsn>` on each environment,
   since baseline read only one.
5. Run `go tool velox-gen migrate diff` as usual. An ent database's names
   come back as renames, and its varchar enums as `alter-type` changes,
   which are destructive and need `-allow` (as
   `-allow alter-type=users.role`).

## Regenerate

velox-gen is a tool dependency of this module (the `tool` line in `go.mod`).
After changing a schema, run from `example/`, for the example `<x>`:

```sh
go tool velox-gen -out ./<x>/gen ./<x>/schema
```

`go tool velox-gen -check -out ./<x>/gen ./<x>/schema`
writes nothing and exits 3 when a generated file is out of date, and
`go tool velox-gen verify -out ./<x>/gen ./<x>/schema` builds every entity's
schema and reports its mistakes. CI runs both for every example, with
`go vet`, `gofmt`, golangci-lint and the tests.

The graphql example also runs the velox-graphql plugin, and gqlgen after it
(see [its README](graphql/README.md)):

```sh
go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema
go -C graphql tool gqlgen generate
```

CI checks both: velox-gen with `-check`, and gqlgen by running it and failing
when the checked-in `graphql/graph` differs.

This directory is its own Go module, so the velox2 module does not depend on
anything the examples use. In a module of your own, add the tool with
`go get -tool github.com/syssam/velox2/cmd/velox-gen` (and, for GraphQL,
`github.com/syssam/velox2/contrib/graphql/cmd/velox-graphql` and
`github.com/99designs/gqlgen`). This module instead points
`github.com/syssam/velox2` (which holds the generator), `contrib/veloxotel` and
`contrib/graphql` at the directories beside it with `replace` lines, so it
always builds against the code there. Only the otel example uses the
OpenTelemetry SDK, and only the graphql example gqlgen.
