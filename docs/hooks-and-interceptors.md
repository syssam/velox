# Hooks and Interceptors

Velox provides two middleware mechanisms for adding cross-cutting logic to your application:

- **Hooks** run around mutations (create, update, delete). Use them for validation, auditing, cascading side effects, and enforcing invariants on writes.
- **Interceptors** run around queries. Use them for default filters, query logging, caching, and tenant isolation on reads.

Both are registered programmatically after the client is opened and do not require schema changes or code regeneration.

---

## Hooks

### What They Are

A hook is a function with the signature `func(velox.Mutator) velox.Mutator`. It receives the next mutator in the chain and returns a new mutator that wraps it. This is the standard middleware/decorator pattern applied to mutations.

The `velox.Mutator` interface has a single method:

```go
type Mutator interface {
    Mutate(context.Context, Mutation) (Value, error)
}
```

### Writing a Hook

Use `velox.MutateFunc` to adapt a plain function into a `Mutator`:

```go
func LoggingHook() velox.Hook {
    return func(next velox.Mutator) velox.Mutator {
        return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
            start := time.Now()
            slog.Info("mutation starting", "type", m.Type(), "op", m.Op().String())

            v, err := next.Mutate(ctx, m)

            if err != nil {
                slog.Error("mutation failed",
                    "type", m.Type(),
                    "op", m.Op().String(),
                    "duration", time.Since(start),
                    "error", err,
                )
            } else {
                slog.Info("mutation completed",
                    "type", m.Type(),
                    "op", m.Op().String(),
                    "duration", time.Since(start),
                )
            }
            return v, err
        })
    }
}
```

The key structure is always:

1. Receive `next velox.Mutator`
2. Return a `velox.MutateFunc` that does pre-work, calls `next.Mutate(ctx, m)`, then does post-work
3. Return the value and error from `next.Mutate` (or an error of your own)

### Mutation Lifecycle

When `client.User.Create().SetName("Alice").Save(ctx)` is called, Velox builds a `UserMutation` and passes it through the hook chain. The sequence for a chain of hooks `[A, B, C]` is:

```
A.pre → B.pre → C.pre → actual DB write → C.post → B.post → A.post
```

The outermost hook (A) wraps all others. Hooks registered first run outermost.

### The Mutation Interface

Inside a hook, the `velox.Mutation` interface provides access to what is being changed:

```go
m.Op()              // velox.OpCreate | OpUpdate | OpUpdateOne | OpDelete | OpDeleteOne
m.Type()            // "User", "Order", etc.
m.Fields()          // []string of changed field names
m.Field("email")    // (Value, bool) — current value being set
m.OldField(ctx, "email") // (Value, error) — previous DB value (UpdateOne only)
m.SetField("status", "active") // override a field value
m.FieldCleared("deleted_at")   // bool — was this nullable field cleared?
```

---

## Registration

### Global Hooks — client.Use()

`client.Use()` registers hooks that run on mutations of every entity:

```go
client.Use(
    LoggingHook(),
    TimestampHook(),
    ValidationHook(),
)
```

### Per-Entity Hooks — client.Entity.Use()

Register a hook on a single entity client to limit its scope:

```go
// Only fires on User mutations
client.User.Use(func(next velox.Mutator) velox.Mutator {
    return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
        slog.Info("user mutation", "op", m.Op().String())
        return next.Mutate(ctx, m)
    })
})

// Only fires on Order mutations
client.Order.Use(func(next velox.Mutator) velox.Mutator {
    return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
        if m.Op() == velox.OpCreate {
            slog.Info("new order created")
        }
        return next.Mutate(ctx, m)
    })
})
```

### Schema-Level Hooks — Hooks()

Hooks can also be declared directly on the schema. These are fixed at code-generation time and run before any hooks registered via `Use()`. Schema hooks are appropriate for invariants that are always required regardless of how the client is constructed.

```go
func (User) Hooks() []velox.Hook {
    return []velox.Hook{
        // Always enforce email validity on User mutations
        hook.If(ValidateEmailHook(), hook.HasFields("email")),
    }
}
```

Mixin hooks work the same way. Mixin hooks run before schema hooks, which run before dynamically registered hooks.

---

## Execution Order

The full execution order for a mutation is:

1. Mixin hooks (in the order mixins are listed in `Mixin()`)
2. Schema hooks (in the order returned by `Hooks()`)
3. Global hooks registered via `client.Use()` (in registration order)
4. Per-entity hooks registered via `client.Entity.Use()` (in registration order)
5. The actual database write

Within each group, the first registered hook is the outermost wrapper.

---

## Conditional Hooks

The generated `hook` package (in your generated `velox/hook/`) provides helpers for conditional execution.

### hook.On — filter by operation

```go
import "yourapp/velox/hook"

// Only run on create operations
client.Use(hook.On(AuditHook(), velox.OpCreate))

// Run on both create and delete
client.Use(hook.On(NotifyHook(), velox.OpCreate|velox.OpDelete))
```

### hook.Unless — exclude by operation

```go
// Run on every operation except bulk update
client.Use(hook.Unless(LoggingHook(), velox.OpUpdate))
```

### hook.If — arbitrary condition

```go
// Only run when the "email" field is being changed
client.User.Use(hook.If(ValidateEmailHook(), hook.HasFields("email")))

// Run only on creates that set both "name" and "email"
client.User.Use(hook.If(
    WelcomeEmailHook(),
    hook.And(hook.HasOp(velox.OpCreate), hook.HasFields("name", "email")),
))
```

### Available Conditions

| Condition | Description |
|-----------|-------------|
| `hook.HasOp(op)` | True when the mutation matches the given op |
| `hook.HasFields(fields...)` | True when all listed fields are being set |
| `hook.HasAddedFields(fields...)` | True when all listed numeric fields are being incremented |
| `hook.HasClearedFields(fields...)` | True when all listed nullable fields are being cleared |
| `hook.HasEdge(edges...)` | True when edges are being added or removed |
| `hook.HasClearedEdge(edges...)` | True when edges are being cleared |
| `hook.And(conds...)` | All conditions must be true |
| `hook.Or(conds...)` | At least one condition must be true |
| `hook.Not(cond)` | Inverts a condition |

---

## Common Patterns

### Validation

Validate field values before the mutation reaches the database. Return an error to abort the mutation.

```go
func ValidateEmailHook() velox.Hook {
    return func(next velox.Mutator) velox.Mutator {
        return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
            if m.Type() == "User" {
                if v, exists := m.Field("email"); exists {
                    if email, ok := v.(string); ok && !strings.Contains(email, "@") {
                        return nil, fmt.Errorf("invalid email: %q", email)
                    }
                }
            }
            return next.Mutate(ctx, m)
        })
    }
}
```

### Timestamps

Automatically set timestamp fields. Prefer `mixin.Time{}` for standard cases — use a hook only when you need conditional logic.

```go
func TimestampHook() velox.Hook {
    return func(next velox.Mutator) velox.Mutator {
        return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
            now := time.Now()
            switch m.Op() {
            case velox.OpCreate:
                if setter, ok := m.(interface{ SetCreatedAt(time.Time) }); ok {
                    setter.SetCreatedAt(now)
                }
                if setter, ok := m.(interface{ SetUpdatedAt(time.Time) }); ok {
                    setter.SetUpdatedAt(now)
                }
            case velox.OpUpdate, velox.OpUpdateOne:
                if setter, ok := m.(interface{ SetUpdatedAt(time.Time) }); ok {
                    setter.SetUpdatedAt(now)
                }
            }
            return next.Mutate(ctx, m)
        })
    }
}
```

### Audit Trail

`mixin.AuditHook` in `schema/mixin` sets `created_by` and `updated_by` from a context-extracted actor. Use it with `mixin.Audit{}` fields:

```go
// schema/user.go
func (User) Mixin() []velox.Mixin {
    return []velox.Mixin{
        mixin.ID{},
        mixin.Audit{}, // adds created_at, created_by, updated_at, updated_by
    }
}
```

```go
// Wire up after opening the client
client.Use(mixin.AuditHook(func(ctx context.Context) string {
    // Extract the actor from your auth context
    if viewer, ok := ctx.Value(viewerKey{}).(*Viewer); ok {
        return viewer.UserID
    }
    return ""
}))
```

`AuditHook` skips the mutation silently if the actor is empty, so unauthenticated operations (e.g., migrations, background jobs) are not blocked.

### Soft Delete

Intercept delete operations and convert them to updates that set `deleted_at`. Requires `mixin.SoftDelete{}` on the entity.

```go
func SoftDeleteHook[T interface {
    SetDeletedAt(time.Time)
}, M interface {
    velox.Mutation
    SetDeletedAt(time.Time)
}]() velox.Hook {
    return hook.On(
        func(next velox.Mutator) velox.Mutator {
            return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
                if typed, ok := m.(M); ok {
                    typed.SetDeletedAt(time.Now())
                    // Redirect the delete to an update
                    // (entity-specific implementation required)
                }
                return next.Mutate(ctx, m)
            })
        },
        velox.OpDeleteOne|velox.OpDelete,
    )
}
```

In practice, soft delete typically requires entity-specific hooks because update builders are typed. The `intercept.TraverseFunc` pattern (see Interceptors below) is used to exclude soft-deleted records from queries.

### Logging

A simple structured logging hook using `log/slog`:

```go
func LoggingHook() velox.Hook {
    return func(next velox.Mutator) velox.Mutator {
        return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
            start := time.Now()
            v, err := next.Mutate(ctx, m)
            slog.Info("mutation",
                "type", m.Type(),
                "op", m.Op().String(),
                "duration_ms", time.Since(start).Milliseconds(),
                "error", err,
            )
            return v, err
        })
    }
}
```

---

## Interceptors

### What They Are

An interceptor is a function that wraps query execution. It implements the `velox.Interceptor` interface:

```go
type Interceptor interface {
    Intercept(Querier) Querier
}
```

There are two kinds:

- **`intercept.Func`** (from your generated `velox/intercept/` package) — runs at query execution time, after all traversals are complete. Good for logging and caching.
- **`intercept.TraverseFunc`** — runs at each traversal step as the query builder walks through edges. This fires during `.QueryPosts()`, `.QueryOwner()`, etc., not just at the final `.All(ctx)`. Use this for default filters like tenant isolation or soft-delete exclusion.

The distinction matters for eager loading: a `TraverseFunc` on the `Post` type will also filter posts when they are loaded via `client.User.Query().WithPosts().All(ctx)`.

### InterceptFunc

`velox.InterceptFunc` is the low-level adapter when you need full control over the `Querier` pipeline:

```go
interceptor := velox.InterceptFunc(func(next velox.Querier) velox.Querier {
    return velox.QuerierFunc(func(ctx context.Context, q velox.Query) (velox.Value, error) {
        start := time.Now()
        v, err := next.Query(ctx, q)
        slog.Info("query completed", "duration_ms", time.Since(start).Milliseconds())
        return v, err
    })
})
```

### Registration

Use `client.Intercept()` for global interceptors and `client.Entity.Intercept()` for per-entity:

```go
// Global — applies to all entity queries
client.Intercept(
    LimitInterceptor(1000),
    LoggingInterceptor(),
)

// Per-entity — applies only to Product queries
client.Product.Intercept(intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
    q.WhereP(func(s *sql.Selector) {
        s.Where(sql.EQ("active", true))
    })
    return nil
}))
```

---

## Common Interceptor Patterns

### Tenant Filtering

Add a WHERE clause to every query in a multi-tenant system:

```go
func TenantInterceptor(tenantID string) velox.Interceptor {
    return intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
        q.WhereP(func(s *sql.Selector) {
            s.Where(sql.EQ("tenant_id", tenantID))
        })
        return nil
    })
}

// Wire up per request
client.Intercept(TenantInterceptor(viewer.TenantID))
```

Because this uses `TraverseFunc`, it also filters edge loads. If a user belongs to tenant A, querying their posts via `.WithPosts()` will still only return posts from tenant A.

### Query Limiting

Prevent runaway queries by enforcing a maximum result count:

```go
func LimitInterceptor(maxLimit int) velox.Interceptor {
    return intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
        q.Limit(maxLimit)
        return nil
    })
}

client.Intercept(LimitInterceptor(1000))
```

### Soft Delete Exclusion

Pair with the `mixin.SoftDelete{}` mixin to automatically exclude deleted records:

```go
func SoftDeleteInterceptor() velox.Interceptor {
    return intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
        q.WhereP(func(s *sql.Selector) {
            s.Where(sql.IsNull("deleted_at"))
        })
        return nil
    })
}
```

Register this globally if most entities use soft delete, or per-entity if only some do.

### Query Logging

Log the query type and operation at execution time (not traversal time):

```go
func LoggingInterceptor() velox.Interceptor {
    return intercept.Func(func(ctx context.Context, q intercept.Query) error {
        qc := velox.QueryFromContext(ctx)
        if qc != nil {
            slog.Info("query", "type", q.Type(), "op", qc.Op)
        } else {
            slog.Info("query", "type", q.Type())
        }
        return nil
    })
}
```

`velox.QueryFromContext(ctx)` returns a `*velox.QueryContext` with `Op`, `Type`, `Limit`, `Offset`, and `Fields` for the current query execution.

---

## Testing Hooks

Hooks can be tested in isolation by constructing a minimal harness — no real database required.

### Unit Testing a Hook

```go
func TestValidateEmailHook(t *testing.T) {
    hook := ValidateEmailHook()

    // Build a mutator that records whether it was called
    called := false
    next := velox.MutateFunc(func(_ context.Context, _ velox.Mutation) (velox.Value, error) {
        called = true
        return nil, nil
    })

    wrapped := hook(next)

    // Construct a fake mutation — use go-sqlmock or a stub
    m := &stubMutation{typ: "User", fields: map[string]velox.Value{"email": "not-an-email"}}

    _, err := wrapped.Mutate(context.Background(), m)
    if err == nil {
        t.Fatal("expected validation error")
    }
    if called {
        t.Fatal("next should not have been called on invalid input")
    }
}
```

### Stub Mutation

For unit tests, implement the `velox.Mutation` interface minimally:

```go
type stubMutation struct {
    typ    string
    op     velox.Op
    fields map[string]velox.Value
}

func (m *stubMutation) Op() velox.Op     { return m.op }
func (m *stubMutation) Type() string     { return m.typ }
func (m *stubMutation) Fields() []string {
    keys := make([]string, 0, len(m.fields))
    for k := range m.fields {
        keys = append(keys, k)
    }
    return keys
}
func (m *stubMutation) Field(name string) (velox.Value, bool) {
    v, ok := m.fields[name]
    return v, ok
}
func (m *stubMutation) SetField(name string, v velox.Value) error {
    m.fields[name] = v
    return nil
}
// ... implement remaining methods as no-ops
```

### Integration Testing with go-sqlmock

For hooks that read `OldField` or perform queries, use `go-sqlmock` with a real generated client against a mock driver. See `compiler/gen/sql/` tests for examples of this pattern.

---

## Common Mistakes

### Infinite Loops

A hook that calls `client.User.Create()` inside a User create hook will recurse infinitely. To avoid this, pass a skip signal via context:

```go
type skipHooksKey struct{}

func withSkipHooks(ctx context.Context) context.Context {
    return context.WithValue(ctx, skipHooksKey{}, true)
}

func shouldSkipHooks(ctx context.Context) bool {
    v, _ := ctx.Value(skipHooksKey{}).(bool)
    return v
}

func MyHook() velox.Hook {
    return func(next velox.Mutator) velox.Mutator {
        return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
            if shouldSkipHooks(ctx) {
                return next.Mutate(ctx, m)
            }
            // ... do work, call client with withSkipHooks(ctx)
            return next.Mutate(ctx, m)
        })
    }
}
```

### Forgetting to Call next.Mutate

Every hook must call `next.Mutate(ctx, m)` (or return an error). Omitting it silently prevents the mutation from reaching the database. The pattern is always: do pre-work, call next, do post-work.

### Swallowing Errors

Return errors from `next.Mutate` to the caller unless you have an explicit reason to suppress them. Silently discarding errors hides bugs and makes debugging difficult.

```go
// Bad: error discarded
v, _ := next.Mutate(ctx, m)
return v, nil

// Good: error propagated
return next.Mutate(ctx, m)
```

### Context Leaks

Do not store context values from a request-scoped hook in package-level variables. Each mutation call receives its own context — use it for the lifetime of that mutation only.

### Ordering Issues

Hooks registered via `client.Use()` wrap in order: the first call to `Use` produces the outermost hook. This means logging hooks (which should see everything) belong first, and validation hooks (which should block early) also belong early. An audit hook that depends on a field being set by a previous hook must be registered after that hook:

```go
// Correct: TimestampHook sets updated_at, then AuditHook records it
client.Use(TimestampHook(), mixin.AuditHook(actorFromCtx))

// Wrong: AuditHook runs before TimestampHook has set the fields
client.Use(mixin.AuditHook(actorFromCtx), TimestampHook())
```

### Schema Hooks vs. Dynamic Hooks

Schema hooks (declared in `Hooks()`) cannot be removed at runtime. Do not put development-only or request-scoped logic in schema hooks. Use `client.Use()` for anything that varies by environment or request.
