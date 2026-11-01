# tenant

`gen.Project` belongs to an organization. `field.Int64("org_id").Tenant()` in
its schema makes every statement on it scoped to the tenant the context names,
and fail when the context names none.

```sh
cd examples
go run ./tenant
```

```go
ctx = velox.WithTenant(ctx, acme)      // acme is an int64, the tenant column's type; WithTenant(ctx, 1) would be converted to it

db.Insert(&p).Exec(ctx)                // fills p.OrgID
db.Query[gen.Project]().Count(ctx)     // ... WHERE "org_id" = ?
db.Update[gen.Project]()...Exec(ctx)   // changes only this tenant's rows

db.Insert(&p).OnConflict(P.OrgID, P.Name).Update(P.Status).Exec(ctx) // a tenant-safe upsert

report := velox.WithAllTenants(ctx, "weekly status report") // unscoped, with a reason
```

It fails closed:

| What | Error |
|---|---|
| no `WithTenant` or `WithAllTenants` (or `WithAllTenants` with no reason) | `velox.ErrNoTenant` |
| `WithTenant(ctx, 1)`: an `int`, not the column's `int64` | `velox.ErrTenantMismatch` |
| inserting a row whose `OrgID` names another tenant | `velox.ErrTenantMismatch` |
| `Set` of the tenant column to another tenant | `velox.ErrTenantImmutable` |
| an upsert that updates, whose `OnConflict` lacks the tenant column | `velox.ErrTenantUpsert` |
| setting a foreign key to a tenant-scoped row that is not this tenant's, or is not there | `velox.ErrTenantRef` |

The last costs one `SELECT` per foreign key set, on the write's connection, and
`WithAllTenants` skips it. It says what is true when it runs: a target deleted
after it is the database foreign key's to refuse. A composite foreign key,
`(org_id, project_id)` to `(org_id, id)`, is the stronger guarantee, as it holds
for every writer of the table; `velox-gen` does not write one for you.

The output is in `testdata/output.golden`.
