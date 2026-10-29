# velox2

velox2 is an ORM for Go. A schema is a Go struct in an ent-style DSL. velox-gen
generates typed queries from it, and velox2 plans and applies its own
migrations. It is before 1.0: the API can still change.

## Modules

- `github.com/syssam/velox2`: the runtime, with `schema`, `migrate`, `plugin`
  and `cmd/velox-gen`.
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

- velox's planner. `go tool velox-gen migrate diff` writes the next migration
  from the schema package. `go tool velox-gen migrate apply` applies the
  pending ones.
- Atlas. `go tool velox-gen migrate sql` prints the schema package's `CREATE`
  statements, which Atlas reads through its `external_schema`.

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

<!-- LICENSE: TBD -->

The licence is not chosen yet.
