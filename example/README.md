# velox2 examples

Small runnable programs, one topic each, on an in-memory SQLite database.
Each directory is a `main` package with its own entity package (`model/`, or
`billing/` in the tour), a README, and a golden test of its output.

| Example | What it shows | Run |
|---|---|---|
| [start](start) | Full CRUD on one entity: `Insert`, `InsertAll`, `Where`/`OrderBy`/`Limit`/`All`, `One` and `ErrNotFound`, `Count`, `Iter`, `Update`…`Set`…`Where`, `Save`, `Delete`…`Where`, and the `ErrUnbounded`/`ErrNoWhere` guards. **Start here.** | `go run ./start` |
| [relations](relations) | One-to-many and belongs-to, by convention and declared; `With` eager loading, nested and filtered; `Has`/`HasWhere`; keyset pagination with `After` | `go run ./relations` |
| [tenant](tenant) | `WithTenant` scoping of reads, writes and inserts; `ErrNoTenant`, `ErrTenantMismatch`, `ErrTenantImmutable`; `AllTenants` with a reason; a tenant-safe upsert | `go run ./tenant` |
| [policy](policy) | `Policy[E]` Read/Write/Insert by the user in the context; `ErrDenied` and `*PolicyError`; a child visible when its parent is, through `Has`; `SkipPolicy`; `SQL(ctx)` | `go run ./policy` |
| [hooks](hooks) | `BeforeInsert` (and a mixin's), `BeforeUpdate` through a `Mutation`, `BeforeDelete` narrowing, `Save` writing back, `IsDryRun` | `go run ./hooks` |
| [tx](tx) | `db.Tx` commit and rollback, a nested `Tx` savepoint, `ConstraintError`, `RetrySerializable`, `Timeout` and `WithDeadlines` | `go run ./tx` |
| [tour](tour) | One walkthrough that touches everything once | `go run ./tour` |

Run from this directory:

```sh
cd example
go run ./start
```

## How the examples are built

- Entities are plain Go structs with a `Schema(s *velox.Schema)` method. The
  `*_velox.go` files beside them, which hold the typed column handles
  (`model.Users`, …), are written by velox-gen; never edit them.
- Tables are created with plain DDL. Planning migrations from the structs is
  a later velox2 sub-project.
- The output is deterministic. `go test ./...` compares each example's output
  with its `testdata/output.golden`; after an intended change,
  `go test ./start -update` rewrites that example's file.

## Regenerate

velox-gen is a tool dependency of this module (the `tool` line in `go.mod`).
After changing an entity struct, run from `example/`:

```sh
go tool velox-gen ./...
```

`go tool velox-gen -check ./...` writes nothing and fails when a generated file
is out of date, and `go tool velox-gen verify ./...` builds every entity's
schema and reports its mistakes. CI runs both, with `go vet`, `gofmt`,
golangci-lint and the tests.

This directory is its own Go module, so the velox2 module does not depend on
anything the examples use. In a module of your own, add the tool with
`go get -tool github.com/syssam/velox2/cmd/velox-gen`. This module instead
points `github.com/syssam/velox2` and its generator at the parent directory
with `replace` lines, so it always builds against the code beside it.
