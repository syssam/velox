# Migrating from Ent to Velox

This guide is for teams running Ent in production who want to migrate to Velox. It maps Ent concepts and syntax to their Velox equivalents and flags the places where Velox deliberately diverges.

---

## Why migrate?

Velox targets teams hitting Ent's scale limits:

- **Code generation speed** — Velox uses Jennifer (programmatic AST) instead of `text/template`. On a 50-entity schema: ~3.2× faster generation, ~2.1× less memory.
- **Privacy as explicit field** — Ent wires privacy as `Hooks[0]`. Velox stores it as a typed `policy` field evaluated before hooks. Execution order is unambiguous.
- **Shared interceptor pointer** — Ent clones `[]Interceptor` on every query clone. Velox uses `*entity.InterceptorStore`; registrations are immediately visible to all existing queries.
- **Safer mutations** — Ent's `u.Update()` uses `u.config` (the entity's embedded driver, which may be a committed `*txDriver`). Velox does not generate `Update()`/`Delete()` on entities; callers use `client.User.UpdateOne(u)` which always uses the live client driver.

---

## Module setup

**Ent:**
```go
import "entgo.io/ent/entc"

entc.Generate("./ent/schema", &gen.Config{
    Target:  "./ent",
    Package: "example.com/myapp/ent",
})
```

**Velox:**
```go
import (
    "github.com/syssam/velox/compiler"
    "github.com/syssam/velox/compiler/gen"
)

cfg, _ := gen.NewConfig(
    gen.WithTarget("./ent"),
    gen.WithPackage("example.com/myapp/ent"),
)
compiler.Generate("./ent/schema", cfg)
```

---

## Schema definition

Most field and edge declarations map directly. Key differences are noted.

### Fields

| Ent | Velox | Notes |
|-----|-------|-------|
| `field.String("name")` | `field.String("name")` | Identical |
| `field.Int("age").Optional()` | `field.Int("age").Optional()` | Identical |
| `field.String("bio").Optional().Nillable()` | `field.String("bio").Nullable()` | `Nullable()` is shorthand for `Optional().Nillable()` |
| `field.Enum("status").Values("a","b")` | `field.Enum("status").Values("a","b")` | Must add `.Default("a")` — Velox requires explicit default for enums |
| `field.UUID("id", uuid.UUID{}).Default(uuid.New)` | `field.UUID("id", uuid.UUID{}).Default(uuid.New)` | Identical |

> **Gotcha:** `Optional()` does **not** add a DB DEFAULT in Velox. Always pair it with `.Default(v)` or `sqlschema.Default("now()")`. Enums and custom types without a default cause a codegen-time error.

### Edges

| Ent | Velox | Notes |
|-----|-------|-------|
| `edge.To("posts", Post.Type)` | `edge.To("posts", Post.Type)` | Identical |
| `edge.From("owner", User.Type).Ref("posts")` | `edge.From("owner", User.Type).Ref("posts")` | Identical |
| `edge.To("cars", Car.Type).Unique()` | `edge.To("cars", Car.Type).Unique()` | Identical |

### Indexes

```go
// Ent and Velox — identical
func (User) Indexes() []ent.Index {
    return []ent.Index{
        index.Fields("email").Unique(),
        index.Fields("first_name", "last_name"),
    }
}
```

---

## Hooks

Hooks are registered identically at the root client level. The **concurrency contract** is the same as Ent: all `Use()` calls must complete before concurrent queries begin (startup-time only).

```go
// Ent
client.Use(func(next ent.Mutator) ent.Mutator {
    return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
        // pre-mutation logic
        v, err := next.Mutate(ctx, m)
        // post-mutation logic
        return v, err
    })
})

// Velox — identical API
client.Use(func(next velox.Mutator) velox.Mutator {
    return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
        v, err := next.Mutate(ctx, m)
        return v, err
    })
})
```

---

## Interceptors

```go
// Ent
client.Intercept(intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
    // traverse logic
    return nil
}))

// Velox — identical API
client.Intercept(velox.InterceptFunc(func(ctx context.Context, q velox.Querier) (velox.Value, error) {
    return q.Query(ctx)
}))
```

**Key difference:** In Ent, interceptors registered after a query is created are NOT visible to that query (slice was already copied). In Velox, registrations are always visible because all queries share the same `*InterceptorStore` pointer.

---

## Privacy

This is the most significant structural difference.

**Ent** wires privacy as `Hooks[0]` in the generated `runtime.go`:
```go
// Ent runtime.go (generated)
task.Hooks[0] = func(next ent.Mutator) ent.Mutator { ... }
```

**Velox** emits a `RuntimePolicy` variable set by `schema.Policy()` in `init()`, then stores it as a typed `policy` field on each entity client — evaluated explicitly before hooks run.

The **schema authoring API is identical**:

```go
// Works the same in both Ent and Velox
func (User) Policy() ent.Policy {
    return privacy.Policy{
        Mutation: privacy.MutationPolicy{
            rule.DenyIfNoViewer(),
            privacy.AlwaysAllowRule(),
        },
        Query: privacy.QueryPolicy{
            privacy.AlwaysAllowRule(),
        },
    }
}
```

**What changes:** `FilterFunc` policies on mutations now work correctly in Velox. In Ent, `FilterFunc` on a mutation would silently return `Deny` if the mutation did not implement `Filterable`. In Velox, `Filterable` is always emitted on mutations when privacy is enabled.

---

## Transactions

```go
// Ent
tx, err := client.Tx(ctx)
u, err := tx.User.Create().SetName("Alice").Save(ctx)
tx.Commit()
// u.config.Driver is now a committed *txDriver — u.Update() will fail

// Velox — same Tx API, but Unwrap() is required before returning
// a tx-created entity to code that will read edges after commit
tx, err := client.Tx(ctx)
u, err := tx.User.Create().SetName("Alice").Save(ctx)
tx.Commit()
u = u.Unwrap() // detaches from txDriver — safe to pass to callers
```

> `Unwrap()` panics on non-transactional entities. Only call it on entities returned from within a `Tx` block.

---

## No `u.Update()` / `u.Delete()`

Ent generates `Update()` and `Delete()` convenience methods on entity structs. Velox does not.

```go
// Ent
u.Update().SetName("Bob").Save(ctx)

// Velox — use the client directly
client.User.UpdateOne(u).SetName("Bob").Save(ctx)

// Inside a transaction
tx.User.UpdateOne(u).SetName("Bob").Save(ctx)
```

This is intentional: `client.User.UpdateOne(u)` always uses `c.config` (the live client driver), so it is immune to the stale-`*txDriver` bug that Ent's `u.Update()` is susceptible to after a committed transaction.

---

## GraphQL (entgql → contrib/graphql)

| Ent (entgql) | Velox (contrib/graphql) | Notes |
|---|---|---|
| `entgql.RelayConnection()` | `graphql.RelayConnection()` | Identical semantics |
| `entgql.QueryField()` | `graphql.QueryField()` | Identical |
| `entgql.Mutations(entgql.MutationCreate())` | `graphql.Mutations(graphql.MutationCreate())` | Identical |
| `entgql.Skip(entgql.SkipAll)` | `graphql.Skip(graphql.SkipAll)` | Identical |
| `entgql.WhereInputs(true)` on extension | `graphql.WithWhereInputs(true)` | Same — but Velox uses a whitelist model by default; fields must opt-in with `graphql.WhereInput()` annotation |
| `entgql.OrderField("NAME")` | `graphql.OrderField("NAME")` | Identical |

**WhereInput opt-in (Velox only):**
```go
// Fields are NOT filterable by default in Velox.
// Opt-in explicitly:
field.String("email").Annotations(graphql.WhereInput())
edge.To("posts", Post.Type).Annotations(graphql.WhereInput())

// Or bulk on the entity:
graphql.WhereInputFields("email", "status")
```

---

## Feature flags

| Ent feature | Velox equivalent |
|---|---|
| `gen.FeaturePrivacy` | `gen.FeaturePrivacy` |
| `gen.FeatureIntercept` | `gen.FeatureIntercept` |
| `gen.FeatureSnapshot` | `gen.FeatureSnapshot` |
| `gen.FeatureSchemaConfig` | `gen.FeatureSchemaConfig` |
| `gen.FeatureNamedEdges` | `gen.FeatureNamedEdges` (short: `"namedges"`) |
| `gen.FeatureGlobalID` | `gen.FeatureGlobalID` |
| `gen.FeatureVersionedMigration` | `gen.FeatureVersionedMigration` |

---

## SQLite driver name

If you were using `github.com/mattn/go-sqlite3` with Ent:

```go
// Ent with go-sqlite3 (CGO)
sql.Open("sqlite3", "file:test.db?_fk=1")

// Velox uses modernc.org/sqlite (pure Go, no CGO)
sql.Open("sqlite", "file:test.db?_pragma=foreign_keys(1)")
```

The driver name changes from `"sqlite3"` to `"sqlite"` and the DSN foreign-key pragma syntax changes. No other query API changes.

---

## Step-by-step migration checklist

1. Replace `entgo.io/ent` with `github.com/syssam/velox` in `go.mod`
2. Replace `entgql` import with `github.com/syssam/velox/contrib/graphql`
3. Update `generate.go` to use `compiler.Generate` (see Module setup above)
4. Add explicit `.Default()` to all `Optional()` enum and custom-type fields
5. Replace `Nullable()` calls — they are now `Optional().Nillable()` or the shorthand `Nullable()` (already compatible)
6. Replace `sqlite3` driver name and DSN pragma if using SQLite
7. Add `graphql.WhereInput()` annotations to fields you want filterable (Velox whitelist model)
8. Replace `u.Update()` / `u.Delete()` with `client.User.UpdateOne(u)` / `client.User.DeleteOne(u)`
9. Add `u.Unwrap()` before returning tx-created entities across a commit boundary
10. Run `go generate ./...` and fix any codegen errors
11. Run `go test -race ./...`
