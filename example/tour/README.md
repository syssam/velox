# tour

One walkthrough that touches everything once: tenants, a policy, a hook, a
mixin, relations, reads, writes, guards, transactions, raw SQL and deadlines,
on invoices for two organizations. The other examples take one topic each.

```sh
cd example
go run ./tour
```

## Layout

- `schema/` declares the entities in the ent-style DSL. `invoice.go` declares
  `Invoice`, which lists the `mixin.Time` mixin (`created_at` and
  `updated_at`), is scoped to a tenant (`OrgID`), has a unique number per
  tenant, a `Lines` relation and a `status` enum. `line.go` declares `Line`.
- `gen/` is written by `go tool velox-gen -out ./tour/gen ./tour/schema`,
  except the files you write by hand: `invoice.go` has `Invoice`'s `Policy()`
  and its `Hooks()`, `line.go` has `Line`'s `Policy()`, and `role.go` has the
  billing role. velox-gen binds the `Policy` and `Hooks` methods into the
  entities' metadata.
- `main.go` creates the tables with plain DDL (the migration planner is a
  later velox2 sub-project) and runs the walkthrough.

## What it shows

1. A context carrying the tenant (`velox.WithTenant`) and a role for the
   policy; `Insert` and `InsertAll` fill the tenant column from it, and the
   insert hook refuses an invalid invoice.
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
8. A report across tenants with `velox.AllTenants` and a reason, and its
   totals summed by the database with raw SQL (`db.Raw`), which the
   builder cannot express (a `GROUP BY`) and which also takes a reason.
9. Deadlines set at `Open` with `velox.WithDeadlines`, and a transaction that
   runs past its `velox.TxTimeout`.

The output is in `testdata/output.golden`.
