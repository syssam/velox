# tour

One walkthrough that touches everything once: a scope, a policy, a hook, a
mixin, relations, reads, writes, guards, transactions, raw SQL and deadlines,
on invoices for two organizations. The other examples take one topic each.

```sh
cd examples
go run ./tour
```

## Layout

- `veloxschema/` declares the entities in the ent-style DSL. `invoice.go` declares
  `Invoice`, which lists the `mixin.Time` mixin (`created_at` and
  `updated_at`), the `InOrg` mixin (`inorg.go`: an `Immutable` `org_id`, and the
  policy and hook that confine every statement to one organization), the
  `softdelete` mixin (`deleted_at`), a unique number per organization among the
  invoices that are not deleted, a `Lines` relation and a `status` enum. `line.go` declares `Line`.
  `invoice_behaviour.go` declares `Invoice`'s `Policy()` and `Hooks()`, and
  `line_policy.go` `Line`'s `Policy()`. They name columns by their schema
  names (`velox.FieldEQ("status", s)`, `r.Value("number")`) and do not import `veloxgen`.
- `veloxgen/` is written entirely by `go tool velox-gen -out ./tour/veloxgen ./tour/veloxschema`.
  Each entity's descriptor lists the mixins' `Policy` and `Hooks`, then the
  entity's own.
- `role/` holds the billing role a request acts with.
- `migrations/` holds the tables, made by `go tool velox-gen migrate diff
  -dialect sqlite -dir ./tour/migrations ./tour/veloxschema`; `main.go` applies it
  with `migrate.Up` to a fresh SQLite file and runs the walkthrough.

## What it shows

1. A context carrying the organization (`veloxschema.OrgKey.With`) and a role for the
   policy; `Insert` and `InsertAll` fill the `org_id` column from it, and the
   insert hook refuses an invalid invoice.
2. Reads: `Where`, `OrderBy`, `Limit` and `All`; `One`; `Count`; `Iter`;
   eager loading with `With`; `Has`.
3. `SQL(ctx)`, which prints the statement a query would send, with the
   organization condition and the policy's condition for that context.
4. Writes: `Update` with `Set` and `Where`, `Save` of named columns, and
   `Delete` with `Where`; an invoice's delete is soft, `softdelete.Skip` shows it,
   and an update under `Skip` restores it.
5. Guards: a query with no `Limit` fails with `ErrUnbounded`, a delete with no
   `Where` with `ErrNoWhere`.
6. Policy and organizations: a viewer's update fails with `ErrDenied`, a viewer
   does not see drafts, another organization's rows are invisible, and a context
   with no organization fails with `scope.ErrNoScope`.
7. A transaction with `db.Tx`, and a nested `Tx` (a savepoint) that recovers
   from a unique-constraint violation.
8. A report across organizations on `db.WithoutPolicy(reason)`: the
   paid invoices summed by the database with `velox.Sum`, and its per-organization
   totals with `velox.SumBy`, a `GROUP BY`.
9. Deadlines set at `Open` with `velox.WithDeadlines`, and a transaction that
   runs past its `velox.TxTimeout`.

The output is in `testdata/output.golden`.
