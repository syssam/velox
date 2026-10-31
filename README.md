# velox2

velox2 is an ORM for Go. A schema is a Go struct in an ent-style DSL. velox-gen
generates typed queries from it, and velox2 plans and applies its own
migrations. It is before 1.0: the API can still change.

## Modules

- `github.com/syssam/velox2`: the runtime, with `schema`, `migrate`, `plugin`,
  `dbtype` and `cmd/velox-gen`.
- `github.com/syssam/velox2/contrib/graphql`: the GraphQL generator and
  `veloxgql`.
- `github.com/syssam/velox2/contrib/veloxotel`: OpenTelemetry tracing and
  metrics.

`example/` is a module of runnable examples.

## Requirements

- Go 1.27.
- PostgreSQL 14, MySQL 8.4 or SQLite 3.35, or newer. MariaDB is not supported.

## Install

In your module:

```sh
go get github.com/syssam/velox2
go get -tool github.com/syssam/velox2/cmd/velox-gen
go tool velox-gen ./schema
```

The last command reads the schema package `./schema` and writes the `gen`
package.

## A first look

A schema is a struct with its fields:

```go
package schema

import (
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/field"
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

`go tool velox-gen ./schema` writes the struct `gen.User` and its column handles
`gen.Users`. A wrong column or value type does not compile:

```go
db := velox.Open(velox.SQLite, sqlDB)
U := gen.Users

ada := gen.User{Name: "Ada", Email: "ada@example.com", Age: 36}
if err := db.Insert(&ada).Exec(ctx); err != nil { // sets ada.ID
	return err
}

over30, err := db.Query[gen.User]().
	Where(U.Age.GT(30)).
	OrderBy(U.Age.Desc()).
	Limit(10).
	All(ctx)
if err != nil {
	return err
}

n, err := db.Update[gen.User]().
	Set(U.Age.Add(1)).
	Where(U.Email.EQ("ada@example.com")).
	Exec(ctx)
```

## Start

[example/start](example/start/README.md) is the first example: create, read,
update and delete on one entity. [example/README.md](example/README.md) lists
the others.

```sh
cd example
go run ./start
```

## Migrations

There are two paths, and a directory is managed by one of them.

- velox's planner. `go tool velox-gen migrate diff -dialect postgres` writes
  the next migration from the schema package; the first diff of a directory
  needs `-dialect`. `go tool velox-gen migrate apply` applies the pending
  ones.
- Atlas. `go tool velox-gen migrate sql -dialect postgres ./schema` prints the
  schema package's `CREATE` statements, which Atlas reads through its
  `external_schema`.

See [example/README.md](example/README.md#migrations) for both, and for
adopting an existing database.

## Documentation

- Each module's API is on pkg.go.dev, with runnable examples:
  [velox2](https://pkg.go.dev/github.com/syssam/velox2),
  [contrib/graphql](https://pkg.go.dev/github.com/syssam/velox2/contrib/graphql),
  [contrib/veloxotel](https://pkg.go.dev/github.com/syssam/velox2/contrib/veloxotel).
- [docs/decisions/](docs/decisions/) records the design decisions.
- [CHANGELOG.md](CHANGELOG.md) lists the changes.
- [CONTRIBUTING.md](CONTRIBUTING.md) explains how to work on velox2.
- [SECURITY.md](SECURITY.md) explains how to report a vulnerability.

## License

velox2 is licensed under the [Apache License 2.0](LICENSE).
