# velox2 example

A runnable walkthrough of velox2 on an in-memory SQLite database. It is its
own Go module, so the velox2 module does not depend on anything the example
uses.

## Layout

- `billing/` is an entity package, as the team that owns invoices would
  write it. `invoice.go` declares `Invoice`, which embeds the `Timestamps`
  mixin, is scoped to a tenant (`OrgID`), has a unique number per tenant, a
  `Lines` relation, a policy and a `BeforeInsert` hook. `line.go` declares
  `Line`. The `*_velox.go` files beside them are written by velox-gen.
- `main.go` creates the tables with plain DDL (the migration planner is a
  later velox2 sub-project) and runs the walkthrough.
- `main_test.go` runs the walkthrough and compares its output with
  `testdata/output.golden`.

## What it shows

1. A context carrying the tenant (`velox.WithTenant`) and a role for the
   policy; `Insert` and `InsertAll` fill the tenant column from it, and the
   `BeforeInsert` hook refuses an invalid invoice.
2. Reads: `Where`, `OrderBy`, `Limit` and `All`; `One`; `Count`; `Iter`;
   eager loading with `With`; `Has`.
3. `SQL(ctx)`, which prints the statement a query would send, with the
   tenant condition and the policy's condition for that context.
4. Writes: `Update` with `Set` and `Where`, `Save` of named columns, and
   `Delete` with `Where`.
5. Guards: a query with no `Limit` fails with `ErrUnbounded`, a delete with no
   `Where` with `ErrNoWhere`.
6. Policy and tenants: a viewer's update fails with `ErrDenied`, a viewer does
   not see drafts, another tenant's rows are invisible, and a context with no
   tenant fails with `ErrNoTenant`.
7. A transaction with `db.Tx`, and a nested `Tx` (a savepoint) that recovers
   from a unique-constraint violation.
8. A report across tenants with `velox.AllTenants` and a reason.
9. Deadlines set at `Open` with `velox.WithDeadlines`, and a transaction that
   runs past its `velox.Timeout`.

## Run it

```sh
cd example
go run .
```

The output is deterministic. `go test ./...` compares it with
`testdata/output.golden`; after an intended change,
`go test -run TestWalkthrough -update .` rewrites the file.

## Regenerate

velox-gen is a tool dependency of this module (the `tool` line in `go.mod`).
After changing a struct in `billing/`, run from `example/`:

```sh
go tool velox-gen ./...
```

`go tool velox-gen -check ./...` writes nothing and fails when a generated file
is out of date; CI runs it, with `go vet`, `gofmt` and the tests.

In a module of your own, add the tool with
`go get -tool github.com/syssam/velox2/cmd/velox-gen`. This module instead
points `github.com/syssam/velox2` and its generator at the parent directory
with `replace` lines, so it always builds against the code beside it.
