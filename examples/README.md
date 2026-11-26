# velox examples

Small runnable programs, one topic each but for tracker, a whole service, on SQLite files in a temporary directory (routing uses one per database).
Each directory is a `main` package with its own `veloxschema/` and `veloxgen/` packages,
a README, and a golden test of its output (tracker's and graphql's tests check the
responses of their servers instead).

| Example | What it shows | Run |
|---|---|---|
| [start](start) | Full CRUD on one entity: `Insert`, `InsertAll`, `Where`/`OrderBy`/`Limit`/`All`, `One` and `ErrNotFound`, `Count`, `Iter`, `Update`…`Set`…`Where`, `Save`, `Delete`…`Where`, and the `ErrUnbounded`/`ErrNoWhere` guards. **Start here.** | `go run ./start` |
| [relations](relations) | One-to-many, belongs-to and many-to-many, declared with `edge.To` and `edge.From`; linking through the join entity; `With` eager loading, nested and filtered; `Has`/`HasWith`; keyset pagination with `After` | `go run ./relations` |
| [tenant](tenant) | The `scope` package: a context value confines reads, writes and inserts of an entity to an organization; `scope.ErrNoScope`, `scope.ErrMismatch`; `db.WithoutPolicy` with a reason; a tenant-safe upsert | `go run ./tenant` |
| [policy](policy) | `velox.Policy` Read/Write/Insert by the user in the context; `ErrDenied` and `*PolicyError`; a child visible when its parent is, through `Has`; `WithoutPolicy`; `SQL(ctx)` | `go run ./policy` |
| [hooks](hooks) | The `Hooks()` of an entity and of a mixin: an insert hook deriving a field, an update hook that reads and sets values through a `Mutation`, a delete hook narrowing with `Where`, `Before` and `On`, `Save` writing back; a run-time hook (`velox.WithHooks`) that writes an audit row on the write's DB (`m.DB()`; inside `db.Tx` it commits or rolls back with the write), and a dry run that refuses that write | `go run ./hooks` |
| [tx](tx) | `db.Tx` commit and rollback, a nested `Tx` savepoint, `ConstraintError`, `TxRetryRolledBack`, `TxTimeout` and `WithDeadlines` | `go run ./tx` |
| [routing](routing) | `OpenShards` with `WithShardFor` choosing a shard by organization, a read replica, `WithSession` pinning a request's reads to the primary after its write, `WithPrimaryReads`, `WithShard`, `ErrNoShard`, and `Statement.Route`. SQLite files, one per database | `go run ./routing` |
| [otel](otel) | Tracing and metrics with `contrib/veloxotel` on an in-memory exporter: the span tree of a request and its transaction, fingerprints, call sites, the SQL comment sent, and the metric points | `go run ./otel` |
| [graphql](graphql) | A gqlgen server on the schema velox-graphql generates: IDs that are the rows' keys, keyset-paged connections with `orderBy` and `where`, edges (a many-to-many among them), a renamed type, organization scoping through all of them, and a sensitive field left out. Its test sends real GraphQL over HTTP and checks the responses and the SQL | `go run ./graphql` |
| [tracker](tracker) | A whole service: an issue tracker's JSON API with organizations, logins, policies, relations, transactions, keyset pages and migrations, on SQLite or PostgreSQL | `go run ./tracker` |
| [tour](tour) | One walkthrough that touches everything once | `go run ./tour` |

Run from this directory:

```sh
cd examples
go run ./start
```

## How the examples are built

- Entities are declared in `veloxschema/` with the ent-style DSL: a struct that
  embeds `velox.Schema`, with `Fields`, `Edges`, `Indexes` and `Mixin`
  methods. As in ent, a schema also declares its entity's `Policy()` and
  `Hooks()`, and a mixin its own. They name columns and edges by their
  schema names (`velox.FieldEQ("owner", u)`, `velox.HasEdge("doc")`,
  `m.Fill("org_id", org)`), so the schema never imports `veloxgen`.
- `veloxgen/` is written entirely by velox-gen from the schema; never edit it. It
  holds every entity. Each entity's descriptor lists the `Policy` and
  `Hooks` methods of its mixins and of its schema, so `main.go` needs no
  extra import. A hook that needs a `veloxgen` type, such as the hooks example's
  audit row, is a run-time hook, added with `velox.WithHooks[veloxgen.Task]`.
  Context helpers the policies read (the request's user, role or
  principal) are small packages of their own, such as `policy/auth`.
- Each example's tables come from its `migrations/` directory, made by
  `go tool velox-gen migrate diff -dialect sqlite` and applied with
  `migrate.Up` to a fresh SQLite file; CI checks the directories with
  `migrate diff -check`, which also applies the files to a temporary SQLite
  file and fails unless they make `schema.json`. tracker's PostgreSQL
  directory needs an empty PostgreSQL database for that, `-scratch-db
  <dsn>`, or `-replay=false` to compare the schema package alone. A real
  application plans its migrations from the schemas the same way, as below.
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
  the tool with `go get -tool github.com/syssam/velox/cmd/velox-gen`, then
  run `atlas` from the module root with this `atlas.hcl`:

  ```hcl
  data "external_schema" "velox" {
    program = ["go", "tool", "velox-gen", "migrate", "sql", "-dialect", "postgres", "./veloxschema"]
  }

  env "velox" {
    src = data.external_schema.velox.url
    dev = "docker://postgres/18/dev?search_path=public"
    migration { dir = "file://migrations" }
  }
  ```

  `atlas migrate diff --env velox` writes a migration, and
  `atlas migrate apply --env velox -u <url>` applies it. For a schema that
  keeps tables in PostgreSQL schemas (`sqlschema.SchemaTable`), use
  `docker://postgres/18/dev`, which is database-scoped. For MySQL use
  `docker://mysql/8.4/dev` with `-dialect mysql`, and for SQLite
  `sqlite://dev?mode=memory` with `-dialect sqlite`.

### Adopting an existing database

A database made before the directory (by ent, by another tool or by hand) joins
velox's planner in five steps:

1. Run `go tool velox-gen migrate baseline -dialect postgres -db <dsn>`
   against one environment. It writes a new `./migrations` whose
   `0001_baseline.sql` builds the schema package's tables as that database
   holds them. Read its warnings: an extension to add to `0001_baseline.sql`
   by hand, a sequence's start or increment it does not keep, a PostgreSQL
   domain type, which it does not create. MySQL's AUTO_INCREMENT counter is
   not read, and gets no warning: a database built from the file starts IDs
   at 1.
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
5. Run `go tool velox-gen migrate diff` as usual. The diff holds only what
   velox spells differently from ent (`TestMigrateBaselineEnt`):
   - an index renamed to carry its table's prefix, which ent leaves off;
   - a foreign-key constraint renamed when its name is longer than the
     dialect allows, since velox caps it with FNV-1 (ADR 0090);
   - a string column's default set to its zero (ADR 0090);
   - on PostgreSQL, a serial key becoming an identity.

   It adds no index of a foreign key: velox declares none, as ent does.

## Regenerate

velox-gen is a tool dependency of this module (the `tool` line in `go.mod`).
After changing a schema, run from `examples/`, for the example `<x>`:

```sh
go tool velox-gen -out ./<x>/veloxgen ./<x>/veloxschema
```

`go tool velox-gen -check -out ./<x>/veloxgen ./<x>/veloxschema`
writes nothing and exits 3 when a generated file is out of date, and
`go tool velox-gen verify -out ./<x>/veloxgen ./<x>/veloxschema` builds every entity's
schema and reports its mistakes. CI runs both for every example, with
`go vet`, `gofmt`, golangci-lint and the tests.

The graphql example also runs the velox-graphql plugin, and gqlgen after it
(see [its README](graphql/README.md)):

```sh
go tool velox-gen -out ./graphql/veloxgen -plugin velox-graphql ./graphql/veloxschema
go -C graphql tool gqlgen generate
```

CI checks both: velox-gen with `-check`, and gqlgen by running it and failing
when the checked-in `graphql/graph` differs.

This directory is its own Go module, so the velox module does not depend on
anything the examples use. In a module of your own, add the tool with
`go get -tool github.com/syssam/velox/cmd/velox-gen` (and, for GraphQL,
`github.com/syssam/velox/contrib/graphql/cmd/velox-graphql` and
`github.com/99designs/gqlgen`). This module instead points
`github.com/syssam/velox` (which holds the generator), `contrib/veloxotel` and
`contrib/graphql` at the directories beside it with `replace` lines, so it
always builds against the code there. Only the otel example uses the
OpenTelemetry SDK, and only the graphql example gqlgen.
