# Velox

[![CI](https://github.com/syssam/velox/actions/workflows/ci.yml/badge.svg)](https://github.com/syssam/velox/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/syssam/velox)](https://goreportcard.com/report/github.com/syssam/velox)
[![GoDoc](https://godoc.org/github.com/syssam/velox?status.svg)](https://godoc.org/github.com/syssam/velox)
[![codecov](https://codecov.io/gh/syssam/velox/graph/badge.svg)](https://codecov.io/gh/syssam/velox)

A type-safe Go ORM framework with integrated code generation for GraphQL services.

> **Note:** This is a personal project. If you're looking for a production-ready Go ORM with code generation, I highly recommend **[Ent](https://entgo.io/)** — it's mature, well-documented, and backed by a strong community. Velox was built to address specific performance bottlenecks I encountered with Ent's template-based code generation in large schemas. Velox uses [Jennifer](https://github.com/dave/jennifer) for programmatic code generation instead of `text/template`, which provides streaming writes, auto-managed imports, and parallel generation — resulting in significantly faster build times for large projects.

## Why Velox?

Velox is **API-compatible with Ent** — same schema definition patterns, same generated query builder API, same hooks/interceptors/privacy model. The key difference is the code generation engine:

| | Ent | Velox |
|---|---|---|
| Code generation | `text/template` | [Jennifer](https://github.com/dave/jennifer) (programmatic) |
| Import management | `goimports` post-process | Auto-tracked at generation time |
| File writes | Buffer then write | Streaming to disk |
| Parallel generation | Sequential | `errgroup` + semaphore |
| Generated predicates | Verbose functions per field | Generic predicates (~97% less code) |

If you have a small-to-medium schema, **use Ent**. Velox only makes sense if you're hitting code generation performance limits with 50+ entity types.

## Features

- **Type-Safe Query Builders** — Fluent API with compile-time type checking
- **Jennifer Code Generation** — Programmatic codegen with streaming writes and parallel execution
- **Multi-Database Support** — PostgreSQL, MySQL, SQLite (pure Go, no CGO)
- **Relay-Style Pagination** — Cursor-based and offset pagination for GraphQL
- **Privacy Layer** — ORM-level authorization policies with composable rules
- **Hooks & Interceptors** — Middleware-style mutation hooks and query interceptors
- **Eager Loading** — Efficient relationship loading with query options
- **Generic Predicates** — Compact, type-safe predicates (~97% less generated code)

## Installation

```bash
go get github.com/syssam/velox
```

## Quick Start

### 1. Define Schema

Create entity schemas in `schema/`:

```go
// schema/user.go
package schema

import (
    "github.com/syssam/velox"
    "github.com/syssam/velox/contrib/mixin"
    "github.com/syssam/velox/schema/edge"
    "github.com/syssam/velox/schema/field"
    "github.com/syssam/velox/schema/index"
)

type User struct{ velox.Schema }

func (User) Mixin() []velox.Mixin {
    return []velox.Mixin{
        mixin.Time{}, // Adds created_at and updated_at
    }
}

func (User) Fields() []velox.Field {
    return []velox.Field{
        field.String("name").NotEmpty().MaxLen(100),
        field.String("email").Unique().NotEmpty(),
        field.Int("age").Optional().Positive(),
        field.Enum("role").Values("admin", "user", "guest").Default("user"),
    }
}

func (User) Edges() []velox.Edge {
    return []velox.Edge{
        edge.To("posts", Post{}),                                        // One-to-Many
        edge.To("profile", Profile{}).Unique(),                           // One-to-One
        edge.To("groups", Group{}).Through("memberships", Membership{}),  // Many-to-Many
    }
}

func (User) Indexes() []velox.Index {
    return []velox.Index{
        index.Fields("email").Unique(),
        index.Fields("role", "created_at"),
    }
}
```

### 2. Generate Code

Create a `generate.go` file:

```go
//go:build ignore

package main

import (
    "log/slog"
    "os"

    "github.com/syssam/velox/compiler"
    "github.com/syssam/velox/compiler/gen"
)

func main() {
    cfg, err := gen.NewConfig(
        gen.WithTarget("./velox"),
        // Optional features
        gen.WithFeatures(gen.FeaturePrivacy, gen.FeatureIntercept),
    )
    if err != nil {
        slog.Error("creating config", "error", err)
        os.Exit(1)
    }

    if err := compiler.Generate("./schema", cfg); err != nil {
        slog.Error("running velox codegen", "error", err)
        os.Exit(1)
    }
}
```

Run it:

```bash
go run generate.go
```

Or use the CLI:

```bash
velox generate ./schema --target ./velox
```

### 3. Use Generated Code

```go
package main

import (
    "context"
    "log"

    _ "github.com/lib/pq"
    "yourproject/velox"
    "yourproject/velox/user"
)

func main() {
    // Open a database connection
    client, err := velox.Open("postgres", "postgres://localhost/mydb?sslmode=disable")
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    // Run auto-migration
    ctx := context.Background()
    if err := client.Schema.Create(ctx); err != nil {
        log.Fatal(err)
    }

    // Create
    u, err := client.User.Create().
        SetName("Alice").
        SetEmail("alice@example.com").
        SetRole("user").
        Save(ctx)
    if err != nil {
        log.Fatal(err)
    }

    // Query with filtering and eager loading
    users, err := client.User.Query().
        Where(user.RoleField.EQ("admin")).
        WithPosts().
        Limit(10).
        All(ctx)
    if err != nil {
        log.Fatal(err)
    }
    _ = users

    // Update
    u, err = client.User.UpdateOneID(u.ID).
        SetName("Alice Smith").
        Save(ctx)
    if err != nil {
        log.Fatal(err)
    }

    // Delete
    if err := client.User.DeleteOneID(u.ID).Exec(ctx); err != nil {
        log.Fatal(err)
    }
}
```

## Configuration

All configuration is done through Go code using functional options:

```go
cfg, err := gen.NewConfig(
    gen.WithTarget("./velox"),                // Output directory (default: parent of schema path)
    gen.WithIDType("int64"),                  // Default ID type
    gen.WithFeatures(                         // Enable optional features
        gen.FeaturePrivacy,
        gen.FeatureIntercept,
        gen.FeatureUpsert,
        gen.FeatureNamedEdges,
        gen.FeatureVersionedMigration,
    ),
)
```

## Field Types

```go
field.String("name")                        // VARCHAR/TEXT
field.Text("bio")                           // TEXT (unlimited)
field.Int("count")                          // INTEGER
field.Int64("big_count")                    // BIGINT
field.Float64("price")                      // DOUBLE PRECISION
field.Bool("active")                        // BOOLEAN
field.Time("created_at")                    // TIMESTAMP
field.Enum("status").Values("a", "b")       // VARCHAR with validation
field.JSON("metadata")                      // JSONB/JSON
field.UUID("external_id", uuid.UUID{})      // UUID
field.Bytes("data")                         // BYTEA/BLOB
```

## Field Modifiers

```go
field.String("email").
    Unique().              // UNIQUE constraint
    NotEmpty().            // Validation: must not be empty
    MaxLen(255).           // VARCHAR(255)
    Optional().            // Not required in create input
    Nillable().            // Database NULL, Go *string
    Default("value").      // Default value
    Immutable()            // Cannot update after create
```

## Relationships

```go
// One-to-Many (default)
edge.To("posts", Post{})             // User has many Posts
edge.From("author", User{}).Unique() // Post belongs to one User

// One-to-One
edge.To("profile", Profile{}).Unique()

// Many-to-Many (with join table)
edge.To("groups", Group{}).Through("memberships", Membership{})
```

## Predicates

Velox generates compact, type-safe predicates using generics. All fields use the `Field` suffix:

```go
// Entity package-level predicate variables (all use Field suffix)
user.NameField.EQ("Alice")              // name = 'Alice'
user.NameField.Contains("ali")          // name LIKE '%ali%'
user.NameField.HasPrefix("A")           // name LIKE 'A%'
user.AgeField.GT(18)                    // age > 18
user.AgeField.In(18, 21, 25)            // age IN (18, 21, 25)
user.EmailField.IsNil()                 // email IS NULL
user.RoleField.EQ("admin")              // enum fields

// Edge predicates (functions, no Field suffix)
user.HasPosts()                          // EXISTS (SELECT 1 FROM posts ...)
user.HasPostsWith(post.TitleField.EQ("Hello"))

// Combinators
user.And(user.AgeField.GT(18), user.RoleField.EQ("active"))
user.Or(user.RoleField.EQ("admin"), user.RoleField.EQ("moderator"))
user.Not(user.RoleField.EQ("banned"))
```

## Eager Loading

```go
// Load all edges
client.User.Query().
    WithPosts().
    WithProfile().
    All(ctx)

// Load with filtering
client.User.Query().
    WithPosts(func(q *velox.PostQuery) {
        q.Where(post.PublishedField.EQ(true)).
          Limit(5)
    }).
    All(ctx)
```

## Hooks & Interceptors

Hooks use a **middleware pattern** wrapping the mutation chain:

```go
// Mutation hook - logs all mutations
func LoggingHook() velox.Hook {
    return func(next velox.Mutator) velox.Mutator {
        return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
            slog.Info("mutation", "op", m.Op(), "type", m.Type())
            v, err := next.Mutate(ctx, m)
            if err != nil {
                slog.Error("mutation failed", "op", m.Op(), "error", err)
            }
            return v, err
        })
    }
}

// Register globally or per-entity
client.Use(LoggingHook())
client.User.Use(LoggingHook())
```

Query interceptors filter or modify all queries (using the generated `intercept` package):

```go
// import "yourproject/velox/intercept"

// Interceptor - limit all queries to prevent unbounded results
client.Intercept(intercept.TraverseFunc(func(ctx context.Context, q intercept.Query) error {
    q.Limit(1000)
    return nil
}))
```

## Transactions

```go
// Using WithTx helper
err := velox.WithTx(ctx, client, func(tx *velox.Tx) error {
    user, err := tx.User.Create().
        SetEmail("user@example.com").
        SetName("Alice").
        Save(ctx)
    if err != nil {
        return err // Rollback
    }

    _, err = tx.Profile.Create().
        SetUserID(user.ID).
        Save(ctx)
    return err // Commit on nil, rollback on error
})

// Manual transaction control
tx, err := client.Tx(ctx)
// ... use tx.User, tx.Post, etc.
tx.Commit()  // or tx.Rollback()
```

## Error Handling

```go
import "github.com/syssam/velox"

_, err := client.User.Create().
    SetEmail("dup@example.com").
    Save(ctx)
if err != nil {
    if velox.IsNotFound(err) {
        // Entity not found
    }
    if velox.IsConstraintError(err) {
        // Unique constraint, foreign key, etc.
    }
    if velox.IsValidationError(err) {
        // Field validation failed
    }
    if velox.IsNotSingular(err) {
        // Expected exactly one result
    }
}
```

## Privacy Layer

ORM-level authorization policies evaluated before database access:

```go
import (
    "github.com/syssam/velox/privacy"
    "github.com/syssam/velox/schema/policy"
)

func (User) Policy() velox.Policy {
    return policy.Policy(
        policy.Mutation(
            privacy.DenyIfNoViewer(),
            privacy.HasRole("admin"),
            privacy.IsOwner("user_id"),
            privacy.AlwaysDenyRule(),
        ),
        policy.Query(
            privacy.AlwaysAllowQueryRule(),
        ),
    )
}
```

## Mixins

Built-in mixins for common patterns:

```go
import "github.com/syssam/velox/contrib/mixin"

mixin.ID{}         // int64 auto-increment primary key
mixin.Time{}       // created_at, updated_at timestamps
mixin.SoftDelete{} // deleted_at for soft deletes
mixin.TenantID{}   // tenant_id for multi-tenancy
```

## Database Support

| Feature | PostgreSQL | MySQL | SQLite |
|---------|------------|-------|--------|
| CRUD | Yes | Yes | Yes |
| Transactions | Yes | Yes | Yes |
| RETURNING | Yes | No | No |
| UUID type | Yes | No | No |
| JSONB | Yes | JSON | JSON |
| Upsert | Yes | Yes | Yes |

SQLite uses [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) (pure Go, no CGO required).

## GraphQL Integration

Optional extension for generating GraphQL schemas and resolvers (works with [gqlgen](https://gqlgen.com/)):

```go
import "github.com/syssam/velox/contrib/graphql"

ex, err := graphql.NewExtension(
    graphql.WithConfigPath("./gqlgen.yml"),
    graphql.WithSchemaPath("./velox/schema.graphql"),
)

cfg, err := gen.NewConfig(gen.WithTarget("./velox"))

compiler.Generate("./schema", cfg, compiler.Extensions(ex))
```

Generated GraphQL includes types, inputs, connections (Relay), filtering (WhereInput), and mutations (opt-in).

## Acknowledgements

Velox is heavily inspired by [Ent](https://entgo.io/). The schema definition API, hooks/interceptors pattern, privacy layer, and generated query builder design all follow Ent's excellent patterns. If Ent works for your project, use it.

## License

[MIT](LICENSE)
