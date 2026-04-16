# privacy

ORM-level authorization for Velox.

## Design

Core types are importable **without** running codegen first. This avoids
circular dependencies when schema files import privacy rules.

## Rules

- `DenyIfNoViewer()` -- Require authenticated viewer
- `HasRole(role)` / `HasAnyRole(roles...)` -- Role-based access
- `IsOwnerOnCreate(field)` -- Owner check on create operations
- `TenantRule(field)` -- Multi-tenant isolation
- `AlwaysAllowRule()` / `AlwaysDenyRule()` -- Catch-all rules

## Combinators

- `And(rules...)` -- All must Allow
- `Or(rules...)` -- Any must Allow
- `Not(rule)` -- Invert Allow/Deny

## FilterFunc

For row-level filtering without type-safe predicates:

```go
privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
    f.WhereP(func(s *sql.Selector) {
        s.Where(sql.EQ(s.C("tenant_id"), tenantID))
    })
    return privacy.Skip
})
```
