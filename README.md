# velox

velox is an ORM for Go. You describe your tables as Go structs, in the same
schema style as ent. velox-gen turns them into plain entity structs and typed
column handles, and a small generic runtime runs your queries. velox also
plans and applies its own migrations, and a plugin generates a GraphQL API.

```go
over30, err := db.Query[gen.User]().
	Where(gen.Users.Age.GT(30)).
	OrderBy(gen.Users.Age.Desc()).
	Limit(10).
	All(ctx)
```

Coming from ent? Most schemas port by changing imports.
[docs/coming-from-ent.md](docs/coming-from-ent.md) lists what differs:
`Optional` stays NOT NULL, the implicit ID is int64, bool and number Defaults,
foreign keys, many-to-many links and GraphQL nulls.

## Not released yet

<!-- Remove this section when v1.0.0 is tagged. -->

v1.0.0 will be the first release from this repository, and until then the API
can still change. Today `go get github.com/syssam/velox` and pkg.go.dev reach an
older, different project that held this path. To try this code, check out this
repository, and in the quick start run
`go mod edit -replace github.com/syssam/velox=/path/to/checkout` straight after
`go mod init`.

## Why velox

- **Small generated code, fast builds.** The query builders are generic
  types in the runtime, so generated code is mostly metadata and grows little
  with each entity ([ADR 0055](docs/decisions/0055-lazy-unsafe-scope.md)).
  In one comparison run at 300 entities, velox generated 49,550 lines where
  ent generated 1,141,879, and a cold build used under a third of ent's CPU
  time ([the 2026-10-29 report](docs/history/reviews/2026-10-29-perf-comparison.md)).
- **Mistakes caught early.** Column handles are typed, so comparing a number
  column with a string does not compile. velox-gen also checks the column
  names that hooks and policies use.
- **Guard rails on by default.** A read without `Limit` fails with
  `ErrUnbounded`, unless it says `Unbounded()`. An update or delete without
  `Where` fails with `ErrNoWhere`, unless it says `All()`. Both are refused
  before anything reaches the database.
- **Policies that fail closed.** Read, write and insert policies live in the
  schema. A policy that returns no condition fails the statement rather than
  matching every row, a scoped entity read without its tenant fails with
  `scope.ErrNoScope`, and lifting policies takes a DB made by
  `db.WithoutPolicy(reason)` ([ADR 0054](docs/decisions/0054-fail-closed-runtime.md)).
- **Migrations included, or Atlas if you prefer.** velox-gen diffs the schema
  into SQL files and refuses a destructive change unless you allow it with
  `-allow`. For Atlas, it prints the schema's `CREATE` statements instead
  ([ADR 0006](docs/decisions/0006-own-migrate-and-atlas.md)).
- **GraphQL from the same schema.** The velox-graphql plugin generates
  gqlgen types, Relay connections, where and order inputs and mutation
  inputs, with the same policies applied.
- **Also in the box.** Transactions with savepoints and retries,
  optimistic locking, row locks, deadlines, read replicas and shards, and
  OpenTelemetry tracing in `contrib/veloxotel`.

## Quick start

This goes from an empty directory to a query that runs on SQLite. It needs Go
1.27.

1. Make a module, and add velox, its generator and a SQLite driver:

   ```sh
   mkdir hello && cd hello
   go mod init example.com/hello
   go get github.com/syssam/velox
   go get -tool github.com/syssam/velox/cmd/velox-gen
   go get modernc.org/sqlite
   ```

2. Write the schema, a struct with its fields, in `schema/user.go`:

   ```go
   // schema/user.go

   package schema

   import (
   	"github.com/syssam/velox/schema"
   	"github.com/syssam/velox/schema/field"
   )

   type User struct{ schema.Schema }

   func (User) Fields() []schema.Field {
   	return []schema.Field{
   		field.String("name"),
   		field.String("email").Unique(),
   		field.Int64("age"),
   	}
   }
   ```

3. Generate the `gen` package, with the struct `gen.User` and its column handles
   `gen.Users`:

   ```sh
   go tool velox-gen ./schema
   ```

4. Write the first migration into `./migrations`, and apply it to `app.db`:

   ```sh
   go tool velox-gen migrate diff -dialect sqlite
   go tool velox-gen migrate apply -db app.db
   ```

5. Use it, in `main.go`. The handles are typed, so a wrong column name or
   value type is a compile error. A read needs `Limit` (or `Unbounded()`),
   and an update or delete needs `Where` (or `All()`); without one it fails
   before reaching the database:

   ```go
   // main.go

   package main

   import (
   	"context"
   	"fmt"
   	"log"

   	"example.com/hello/gen"
   	"github.com/syssam/velox"
   	_ "modernc.org/sqlite"
   )

   func main() {
   	ctx := context.Background()

   	db, err := velox.Connect("sqlite", "app.db")
   	if err != nil {
   		log.Fatal(err)
   	}
   	defer db.Close()

   	U := gen.Users

   	ada := gen.User{Name: "Ada", Email: "ada@example.com", Age: 36}
   	if err := db.Insert(&ada).Exec(ctx); err != nil { // sets ada.ID
   		log.Fatal(err)
   	}

   	over30, err := db.Query[gen.User]().
   		Where(U.Age.GT(30)).
   		OrderBy(U.Age.Desc()).
   		Limit(10).
   		All(ctx)
   	if err != nil {
   		log.Fatal(err)
   	}
   	for _, u := range over30 {
   		fmt.Println(u.Name, u.Age)
   	}

   	updated, err := db.Update[gen.User]().
   		Set(U.Age.Add(1)).
   		Where(U.Email.EQ("ada@example.com")).
   		Exec(ctx)
   	if err != nil {
   		log.Fatal(err)
   	}

   	deleted, err := db.Delete[gen.User]().
   		Where(U.Email.EQ("ada@example.com")).
   		Exec(ctx)
   	if err != nil {
   		log.Fatal(err)
   	}
   	fmt.Println("updated", updated, "deleted", deleted)
   }
   ```

6. Run it:

   ```sh
   go run .
   ```

   ```text
   Ada 36
   updated 1 deleted 1
   ```

A test runs these steps exactly as written (`TestReadmeQuickStart`), so they
stay correct. To manage the pool yourself, open a `*sql.DB` and pass it to
`velox.Open`; closing it is then up to you. On PostgreSQL, `veloxpgx.Open`
takes a pgx pool and is faster. [docs/guide.md](docs/guide.md) picks up from
here.

## What velox doesn't do

Some things are left out on purpose, and raw SQL covers them when you need
them.

- Joins are for to-one relations only (`Join`, `LeftJoin`); a to-many
  relation is loaded with `With` or tested with `Has`. There is no
  `DISTINCT`, `UNION` or `HAVING`. Subqueries are the `EXISTS` of `Has`,
  `HasWith` and `Of`, and a column's `Select` tested with `InSelect`; one
  with an aggregate, a CTE or a window function is raw SQL. `GROUP BY` is one key (`CountBy`, `SumBy` and
  the like).
- `Raw` scans only into entities. For a report, range over
  `db.Rows(ctx, reason, sql, args...)` and `Scan` each row: it runs on the
  connection the DB uses, a transaction's included. `velox.Pool(db)`, the
  `*sql.DB` under a DB, is nil on a transaction's DB and on a DB from
  `OpenConn`, `OpenShards` or `veloxpgx`. Inside a `Tx` or a loop over `Iter`
  on a pool of one connection, as `Connect` makes on SQLite, SQL on the pool
  hangs: they hold that connection, and velox does not see the pool.
- No down migrations. To undo one, change the schema back and diff forward.
- No composite primary keys on entities: an entity's key is one column. The one
  composite key velox makes is the join table of a many-to-many edge. For a
  natural key, keep the `id` and add `index.Fields("org_id", "name").Unique()`.
- Go 1.27 or newer only: the API is built on generic methods.
- `Query` can't sit behind an interface, because a generic method doesn't satisfy
  one. So there is nothing to mock: tests use a real SQLite database.

## Modules

- `github.com/syssam/velox`: the runtime, with `schema`, `migrate`, `plugin`,
  `veloxtype` and `cmd/velox-gen`.
- `github.com/syssam/velox/contrib/graphql`: the GraphQL generator, which
  writes the types, connections, where and order inputs and, as entgql does,
  the mutation inputs with their `Create` and `Update`, and `veloxgql`.
- `github.com/syssam/velox/contrib/veloxotel`: OpenTelemetry tracing and
  metrics.

## Requirements

- Go 1.27.
- PostgreSQL 14, MySQL 8.4 or SQLite 3.35, or newer. MariaDB is not supported.

## Examples

`examples/` is a module of runnable examples.
[examples/start](examples/start/README.md) is the first: create, read, update and
delete on one entity. [examples/README.md](examples/README.md) lists the others.

```sh
cd examples
go run ./start
```

## Migrations

There are two paths, and a directory is managed by one of them.

- velox's planner. `go tool velox-gen migrate diff` writes the next migration
  from the schema package; the first diff of a directory needs `-dialect`, one of
  `postgres`, `mysql` or `sqlite`. `go tool velox-gen migrate apply -db <dsn>`
  applies the pending ones, or reads the DSN from `$VELOX_DB`. On SQLite the DSN
  is a file path or a `file:` URI.
- Atlas. `go tool velox-gen migrate sql -dialect postgres ./schema` prints the
  schema package's `CREATE` statements, which Atlas reads through its
  `external_schema`.

See [examples/README.md](examples/README.md#migrations) for both, and for
adopting an existing database.

## Learn more

- [docs/guide.md](docs/guide.md): a tour of velox by task, from schema to
  GraphQL. Read this after the quick start.
- [docs/coming-from-ent.md](docs/coming-from-ent.md): what differs from ent.
- [docs/stability.md](docs/stability.md): what may change before 1.0, and
  what 1.0 will promise.
- [examples/](examples/README.md): runnable programs, one topic each, and a
  whole service.
- [docs/decisions/](docs/decisions/README.md): the design decisions, and why
  velox works the way it does.
- [docs/migrations-operations.md](docs/migrations-operations.md): what to do
  when `migrate apply` refuses.
- Each module's API is on pkg.go.dev, with runnable examples:
  [velox](https://pkg.go.dev/github.com/syssam/velox),
  [contrib/graphql](https://pkg.go.dev/github.com/syssam/velox/contrib/graphql),
  [contrib/veloxotel](https://pkg.go.dev/github.com/syssam/velox/contrib/veloxotel).
- [CHANGELOG.md](CHANGELOG.md) lists the changes.
- [CONTRIBUTING.md](CONTRIBUTING.md) explains how to work on velox.
- [SECURITY.md](SECURITY.md) explains how to report a vulnerability.

## License

velox is licensed under the [Apache License 2.0](LICENSE).
