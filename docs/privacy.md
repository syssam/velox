# Privacy & Authorization

Velox provides an ORM-level authorization layer that evaluates policies before queries and mutations reach the database. Policies are composable, type-safe, and work with any authentication system.

## Quick Start

Define a privacy policy on your schema:

```go
package schema

import (
    "github.com/syssam/velox"
    "github.com/syssam/velox/privacy"
)

type Todo struct{ velox.Schema }

func (Todo) Policy() velox.Policy {
    return privacy.Policy{
        Mutation: privacy.MutationPolicy{
            privacy.DenyIfNoViewer(),
        },
        Query: privacy.QueryPolicy{
            privacy.AlwaysAllowRule(),
        },
    }
}
```

Enable the privacy feature in code generation:

```go
cfg, err := gen.NewConfig(
    gen.WithTarget("./velox"),
    gen.WithFeatures(gen.FeaturePrivacy),
)
```

## Setting the Viewer

All privacy rules access the viewer from context:

```go
viewer := &privacy.SimpleViewer{UserID: "user-123", UserRoles: []string{"admin"}}
ctx := privacy.WithViewer(context.Background(), viewer)

// All queries/mutations on this context are evaluated against policies
todos, err := client.Todo.Query().All(ctx)
```

For custom user types, implement the `privacy.Viewer` interface:

```go
type AuthUser struct {
    UserID    string
    UserRoles []string
    Tenant    string
}

func (u *AuthUser) ID() string        { return u.UserID }
func (u *AuthUser) Roles() []string   { return u.UserRoles }
func (u *AuthUser) TenantID() string  { return u.Tenant }
```

## Common Patterns

### Role-Based Access Control

```go
func (Todo) Policy() velox.Policy {
    return privacy.Policy{
        Mutation: privacy.MutationPolicy{
            privacy.DenyIfNoViewer(),
            privacy.HasAnyRole("admin", "editor"),
        },
        Query: privacy.QueryPolicy{
            privacy.DenyIfNoViewer(),
            privacy.HasRole("viewer"),
        },
    }
}
```

### Owner-Based Access

```go
func (Todo) Policy() velox.Policy {
    return privacy.Policy{
        Mutation: privacy.MutationPolicy{
            privacy.DenyIfNoViewer(),
            // Allow admins to mutate anything
            privacy.HasRole("admin"),
            // Otherwise, only the owner can mutate
            privacy.IsOwner("owner_id"),
        },
    }
}
```

**Important:** `IsOwner` only works reliably for create operations (comparing the field value being set). For updates/deletes, use `FilterFunc` to add a WHERE clause instead.

### Multi-Tenant Isolation

```go
func (Todo) Policy() velox.Policy {
    return velox.Policy{
        Query: privacy.QueryPolicy{
            privacy.DenyIfNoViewer(),
            privacy.TenantQueryRule("tenant_id"),
        },
        Mutation: velox.MutationPolicy{
            privacy.DenyIfNoViewer(),
            privacy.TenantRule("tenant_id"),
        },
    }
}
```

The tenant viewer must implement `privacy.TenantIDer`:

```go
func (u *AuthUser) TenantID() string { return u.Tenant }
```

### Row-Level Filtering with FilterFunc

For dynamic WHERE clauses that filter results based on the viewer:

```go
func (Todo) Policy() velox.Policy {
    return velox.Policy{
        Query: privacy.QueryPolicy{
            privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
                viewer := privacy.ViewerFromContext(ctx)
                if viewer == nil {
                    return privacy.Deny
                }
                // Only return rows belonging to the viewer's tenant
                type ColumnChecker interface {
                    HasColumn(string) bool
                }
                cc, ok := f.(ColumnChecker)
                if !ok || !cc.HasColumn("tenant_id") {
                    return privacy.Skip
                }
                f.WhereP(func(s *sql.Selector) {
                    tid := viewer.(privacy.TenantIDer).TenantID()
                    s.Where(sql.EQ(s.C("tenant_id"), tid))
                })
                return privacy.Skip
            }),
        },
    }
}
```

### Combining Rules

```go
// Must be admin AND in the same tenant
privacy.And(
    privacy.HasRole("admin"),
    privacy.TenantRule("tenant_id"),
)

// Admin OR owner can mutate
privacy.Or(
    privacy.HasRole("admin"),
    privacy.IsOwner("created_by"),
)

// Anyone except guests
privacy.Not(privacy.HasRole("guest"))
```

## Decision Model

Rules return one of three decisions:

| Decision | Meaning |
|----------|---------|
| `privacy.Allow` | Operation permitted. Stops evaluation. |
| `privacy.Deny` | Operation rejected. Stops evaluation. |
| `privacy.Skip` | No opinion. Continue to next rule. |

If all rules return `Skip`, the operation is **allowed** (permissive default).

## HTTP Middleware Example

```go
func AuthMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        token := r.Header.Get("Authorization")
        user, err := validateToken(token)
        if err != nil {
            http.Error(w, "unauthorized", http.StatusUnauthorized)
            return
        }
        ctx := privacy.WithViewer(r.Context(), user)
        next.ServeHTTP(w, r.WithContext(ctx))
    })
}
```

## Next Steps

- [Getting Started](getting-started.md) -- Basic Velox setup
- [Hooks & Interceptors](hooks-and-interceptors.md) -- Mutation middleware
- [Migration](migration.md) -- Database migration strategies
