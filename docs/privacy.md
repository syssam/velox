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
    return privacy.Policy{
        Query: privacy.QueryPolicy{
            privacy.DenyIfNoViewer(),
            privacy.TenantFilterRule("tenant_id"),
        },
        Mutation: privacy.MutationPolicy{
            privacy.DenyIfNoViewer(),
            privacy.TenantFilterRule("tenant_id"), // scopes UPDATE/DELETE
            privacy.TenantRule("tenant_id"),       // rejects a mismatched tenant on CREATE
        },
    }
}
```

`TenantFilterRule` is the rule that isolates: it appends
`WHERE tenant_id = <viewer tenant>` to the statement, and it is a
`QueryMutationRule`, so the same call covers reads, bulk `UPDATE` and
bulk `DELETE`. It denies when the viewer or tenant is missing, so a
tenant-scoped read without a tenant fails instead of returning every
tenant's rows.

`TenantRule` complements it on `CREATE` by rejecting a row whose
`tenant_id` does not match the viewer. `TenantQueryRule` is a
presence guard only — it appends no predicate and is deprecated in
favour of `TenantFilterRule`.

**Stamp the tenant column from a hook, do not accept it from the caller.**
`TenantRule` skips when the field is not set, and a policy whose rules all
skip allows the operation — so a `CREATE` that simply omits `tenant_id`
passes every rule and lands a row with a zero tenant, invisible to every
tenant afterwards. Set the column from the viewer instead:

```go
func (Todo) Hooks() []velox.Hook {
    return []velox.Hook{
        func(next velox.Mutator) velox.Mutator {
            return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
                if m.Op().Is(velox.OpCreate) {
                    viewer, ok := privacy.ViewerFromContext(ctx).(privacy.TenantIDer)
                    if !ok {
                        return nil, errors.New("todo: create requires a tenant viewer")
                    }
                    if err := m.SetField("tenant_id", viewer.TenantID()); err != nil {
                        return nil, err
                    }
                }
                return next.Mutate(ctx, m)
            })
        },
    }
}
```

The hook makes the column unspoofable (it overwrites whatever the caller
supplied) and unomittable; `TenantRule` then remains as a defence in
depth rather than the only check.

If the tenant column is not a string, implement `privacy.TenantIDValuer`
on the viewer so the predicate binds with the column's own type;
Postgres rejects `integer = text`.

**Two reads, one mechanism.** A row filter cannot tell a read whose rows
are returned to the caller from a read that guards an invariant — a
dependency `Exist()` before a delete, a uniqueness probe, a lock
acquisition. Narrowing the second kind does not leak data, it corrupts
it: the guard answers "no dependents" for rows outside the tenant and the
delete proceeds. Only the call site knows which kind it is.

Run invariant checks through a client that does not carry the tenant
policy, or under a viewer whose role bypasses it. Do not rely on
remembering a per-call-site opt-out: an opt-out that is forgotten fails
in the destructive direction, and nothing detects it afterwards.

**Edge predicates are scoped by the target's policy.** `HasTodos()` and
`HasTodosWith(...)` compile to a subquery, and velox evaluates the Todo
policy for it: a `FilterFunc` narrows the subquery to the rows the viewer may
read, so filtering users through the edge (`users(where: {hasTodosWith: …})`)
cannot reveal whether out-of-scope todos exist; a rule that denies fails the
whole query — or the bulk `Update().Where(...)` / `Delete().Where(...)` — with
that error. Policies that filter through edges to each other (Todo through
its owner, User through its todos) are reported as a cycle rather than
evaluated forever. Interceptors do not reach edge subqueries; keep row-level
rules in `Policy()`.

The tenant viewer must implement `privacy.TenantIDer`:

```go
func (u *AuthUser) TenantID() string { return u.Tenant }
```

#### Database-enforced isolation with `sql.WithVar`

To back the ORM policy with Postgres row-level security (or to hand a value
to MySQL triggers and views), put the tenant in a session variable with
`sql.WithVar` from `github.com/syssam/velox/dialect/sql`. velox sets it
before every statement run with that context:

```go
ctx = sql.WithVar(ctx, "app.tenant_id", tenantID)
todos, err := client.Todo.Query().All(ctx)
```

Outside a transaction the variable is set on the connection reserved for
the statement and reset before the connection returns to the pool. Inside a
transaction the two databases differ:

| | Postgres | MySQL |
|---|---|---|
| Mechanism | `set_config(name, value, true)` (transaction-local) | `SET @name = ?`, reset after the statement |
| Later statements in the same transaction | still see the value, even without `WithVar` on their context | do not see it |
| After `COMMIT`/`ROLLBACK` | gone | gone |

Neither leaks to another pooled connection. Because the in-transaction
behavior differs, pass the `WithVar` context to **every** statement that
depends on the variable — do not rely on an earlier statement in the
transaction having set it. On Postgres, write the RLS policy so that an
unset variable matches nothing: `current_setting('app.tenant_id', true)`
returns NULL, or an empty string once the variable has been reset on that
connection, and neither should equal a real tenant id.

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

`And`, `Or` and `Not` take and return `QueryMutationRule`s, so they combine
rules such as `HasRole`, `HasAnyRole`, `DenyIfNoViewer` and
`TenantFilterRule`; mutation-only rules (`TenantRule`, `IsOwner`) go in a
`MutationPolicy` list directly. The combinators work on the three decisions
described under [Decision Model](#decision-model), not on booleans:

- `And(a, b)` returns `Allow` only if every rule returns `Allow`; otherwise
  it returns the first decision that is not `Allow` — often `Skip`.
- `Or(a, b)` returns `Allow` if any rule returns `Allow`; otherwise the last
  rule's decision.
- `Not(r)` turns `Allow` into `Deny` and the bare `Deny` into `Allow`. `Skip`
  stays `Skip`, and a denial that carries a reason (`Denyf`, a missing
  viewer) stays a denial.

```go
// Must hold both roles.
privacy.And(
    privacy.HasRole("admin"),
    privacy.HasRole("billing"),
)

// Either role is enough.
privacy.Or(
    privacy.HasRole("admin"),
    privacy.HasRole("editor"),
)

// Denies guests. It does NOT allow anyone: HasRole returns Skip for a
// viewer without the role, Not keeps that Skip, and evaluation moves on.
privacy.Not(privacy.HasRole("guest"))
```

Because a non-guest gets `Skip` from `Not(HasRole("guest"))`, what happens to
them is decided by the rules after it — or, if every rule skips, by the
permissive default. To express "anyone signed in except guests", end the
list explicitly:

```go
Query: privacy.QueryPolicy{
    privacy.DenyIfNoViewer(),              // no viewer: Deny
    privacy.Not(privacy.HasRole("guest")), // guest: Deny; anyone else: Skip
    privacy.AlwaysAllowRule(),             // everyone left: Allow
},
```

## Decision Model

Rules return one of three decisions:

| Decision | Meaning |
|----------|---------|
| `privacy.Allow` | Operation permitted. Stops evaluation. |
| `privacy.Deny` | Operation rejected. Stops evaluation. |
| `privacy.Skip` | No opinion. Continue to next rule. |

If all rules return `Skip`, the operation is **allowed** (permissive default).

## Policies on Edges and Eager Loads

Reading through an edge evaluates the **target** entity's query policy, on
every path: `post.QueryAuthor()`, `client.Post.Query().QueryAuthor()`,
`WithAuthor()`, `WithNamedTags(...)`, a many-to-many `WithTags()`, edges
nested under any of those, and the eager loads GraphQL field collection
schedules. A `FilterFunc` rule on the target narrows the loaded rows.

A `Deny` from the target's policy on an eager load fails the **whole parent
query**: `client.Post.Query().WithAuthor().All(ctx)` returns the policy
error, not posts with a nil author. This matches Ent. If a caller may read
the parent but not the edge, do not eager-load the edge for that caller, or
make the target's rule filter (`FilterFunc`) instead of deny.

Every one of these paths reads the target's policy from the same variable
its own client does — `<entity>.RuntimePolicy` in the generated entity
package, set once at init — and reads it when the query runs. Replacing that
variable (as some tests do to install a stricter policy) therefore reaches
eager loads and edge queries too; it is a package global, so tests that
replace it must not run in parallel with anything that reads it.

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
