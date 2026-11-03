# tour

One walkthrough that touches everything once: a scope, a policy, a hook, a
mixin, relations, reads, writes, guards, transactions, raw SQL and deadlines,
on invoices for two organizations. The other examples take one topic each.

```sh
cd examples
go run ./tour
```

## Layout

- `schema/` declares the entities in the ent-style DSL. `invoice.go` declares
  `Invoice`, which lists the `mixin.Time` mixin (`created_at` and
  `updated_at`), has an `OrgID` that is `Immutable` and `Scoped`, a unique number per
  organization, a `Lines` relation and a `status` enum. `line.go` declares `Line`.
  `invoice_behaviour.go` declares `Invoice`'s `Policy()` and `Hooks()`, and
  `line_policy.go` `Line`'s `Policy()`, typed with the generated entities.
- `gen/` is written entirely by `go tool velox-gen -out ./tour/gen ./tour/schema`.
  The `OrgKey` key and the scope that confines every statement to one
  organization (the `scope` package) are generated, from the `Scoped` columns of
  the schema, into `scope_velox.go`. The schema's `Policy` and `Hooks` are typed
  with `gen/entity`, and `gen`, which `main.go` imports, binds them around the
  scope.
- `role/` holds the billing role a request acts with.
- `migrations/` holds the tables, made by `go tool velox-gen migrate diff
  -dialect sqlite -dir ./tour/migrations ./tour/schema`; `main.go` applies it
  with `migrate.Up` to a fresh SQLite file and runs the walkthrough.

## What it shows

1. A context carrying the organization (`gen.OrgKey.With`) and a role for the
   policy; `Insert` and `InsertAll` fill the `org_id` column from it, and the
   insert hook refuses an invalid invoice.
2. Reads: `Where`, `OrderBy`, `Limit` and `All`; `One`; `Count`; `Iter`;
   eager loading with `With`; `Has`.
3. `SQL(ctx)`, which prints the statement a query would send, with the
   organization condition and the policy's condition for that context.
4. Writes: `Update` with `Set` and `Where`, `Save` of named columns, and
   `Delete` with `Where`.
5. Guards: a query with no `Limit` fails with `ErrUnbounded`, a delete with no
   `Where` with `ErrNoWhere`.
6. Policy and organizations: a viewer's update fails with `ErrDenied`, a viewer
   does not see drafts, another organization's rows are invisible, and a context
   with no organization fails with `scope.ErrNoScope`.
7. A transaction with `db.Tx`, and a nested `Tx` (a savepoint) that recovers
   from a unique-constraint violation.
8. A report across organizations with `velox.WithoutPolicy` and a reason: the
   paid invoices summed by the database with `velox.Sum`, and its per-organization
   totals with `velox.SumBy`, a `GROUP BY`.
9. Deadlines set at `Open` with `velox.WithDeadlines`, and a transaction that
   runs past its `velox.TxTimeout`.

The output is in `testdata/output.golden`.
