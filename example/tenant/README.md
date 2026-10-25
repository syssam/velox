# tenant

`gen.Project` belongs to an organization. `field.Int64("org_id").Tenant()` in
its schema makes every statement on it scoped to the tenant the context names,
and fail when the context names none.

```sh
cd example
go run ./tenant
```

```go
ctx = velox.WithTenant(ctx, acme)      // acme is an int64: the tenant column's exact Go type

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

The output is in `testdata/output.golden`.
