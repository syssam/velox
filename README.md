# Velox

[![CI](https://github.com/syssam/velox/actions/workflows/ci.yml/badge.svg)](https://github.com/syssam/velox/actions/workflows/ci.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/syssam/velox)](https://goreportcard.com/report/github.com/syssam/velox)
[![GoDoc](https://godoc.org/github.com/syssam/velox?status.svg)](https://godoc.org/github.com/syssam/velox)
[![codecov](https://codecov.io/gh/syssam/velox/graph/badge.svg)](https://codecov.io/gh/syssam/velox)

A type-safe Go ORM framework with integrated code generation for GraphQL services.

> ⚠️ **Pre-release software.** The API is still evolving and breaking changes may land without a deprecation cycle until v1.0.0. Feature stability is tracked per feature — see the [Feature Stability](#feature-stability) table below before depending on any Experimental/Alpha feature in production.

## Table of Contents

- [Why Velox?](#why-velox)
- [Features](#features)
  - [Feature Stability](#feature-stability)
- [Installation](#installation)
- [Quick Start](#quick-start)
- [Configuration](#configuration)
- [Field Types](#field-types)
- [Field Modifiers](#field-modifiers)
- [Relationships](#relationships)
- [Predicates](#predicates)
- [Eager Loading](#eager-loading)
- [Hooks & Interceptors](#hooks--interceptors)
- [Transactions](#transactions)
- [Error Handling](#error-handling)
- [Privacy Layer](#privacy-layer)
- [Mixins](#mixins)
- [Database Support](#database-support)
- [GraphQL Integration](#graphql-integration)
- [Documentation](#documentation)
- [Acknowledgements](#acknowledgements)
- [License](#license)

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

### Benchmark Results (50 entities, Apple M3 Max)

Benchmarked with identical 50-entity schemas including GraphQL (Relay connections, WhereInput, mutations). Pre-compiled generator binaries, warmup discarded, 5 measured runs. See [docs/benchmarks.md](docs/benchmarks.md) for full methodology.

| Metric | Ent | Velox | Delta |
|--------|-----|-------|-------|
| **Code generation time** | 6.32s | 2.00s | 3.2x faster |
| **Generation peak memory** | 1.86 GB | 0.89 GB | 2.1x less |
| **Total generated lines** | 335,365 | 230,280 | 31% fewer |
| **Largest single file** | 56,050 lines | 9,444 lines | 5.9x smaller |
| **Files > 1,000 lines** | 47 | 0 | No monoliths |
| **Cold compile time** | 12.4s | 13.5s | Ent 9% faster |
| **Cold compile memory** | 3.31 GB | 1.54 GB | 2.2x less |

Velox generates faster with less memory, but cold compilation takes slightly longer due to more packages. Incremental builds are identical (~0.2s). Generated files are dramatically smaller — Ent's `mutation.go` alone is 56K lines vs Velox's largest file at 9.4K lines.

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
- **Whitelist Filtering** — WhereInput fields opt-in by default (industry standard)

### Feature Stability

Opt-in features live behind flags in `gen.Config.Features`. Stages follow Ent's convention: **Experimental** (in development, expect breaks) → **Alpha** (API may change) → **Beta** (documented, no breaks expected) → **Stable** (production-ready on velox infra).

| Feature flag | Stage | Purpose |
|---|---|---|
| `sql/schemaconfig` | Stable | Alternate schema names per entity (multi-database tables) |
| `sql/entpredicates` | Stable | Ent-compatible standalone predicate functions |
| `graphql/whereinputall` | Stable | Expose all fields in WhereInput by default (Ent-compatible) |
| `validator` | Stable | ORM-level validators (NotEmpty, MaxLen, Range, …) run pre-save |
| `sql/multischema` | Beta | Auto-enabled by storage driver for multi-schema annotations |
| `privacy` | Alpha | Policy-based authorization (requires `intercept`) |
| `intercept` | Alpha | Query interceptor helper package |
| `namedges` | Alpha | Eager-load edges with dynamic names |
| `sql/lock` | Alpha | Row-level locking (`FOR UPDATE`/`FOR SHARE`) |
| `sql/upsert` | Alpha | `ON CONFLICT` / `ON DUPLICATE KEY` for INSERT |
| `sql/modifier` | Alpha | Custom query modifiers |
| `sql/autodefault` | Alpha | Auto-emit DB `DEFAULT` for all NOT NULL fields |
| `entql` | Experimental | Runtime generic filtering language |
| `bidiedges` | Experimental | Two-way references on O2M/O2O eager-load |
| `schema/snapshot` | Experimental | Schema snapshot for merge-conflict resolution |
| `sql/execquery` | Experimental | Expose driver `ExecContext`/`QueryContext` |
| `sql/versioned-migration` | Experimental | Atlas versioned migration files |
| `sql/globalid` | Experimental | Unique global IDs across all node types |

All non-Stable flags are off by default. Enable per-project via `gen.Config{Features: []gen.Feature{gen.FeaturePrivacy, ...}}`. Missing `Requires:` dependencies are auto-enabled with a warning at codegen time.

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
    "github.com/syssam/velox/schema/mixin"
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
        edge.To("posts", Post.Type),                                              // One-to-Many
        edge.To("profile", Profile.Type).Unique(),                                // One-to-One
        edge.To("groups", Group.Type).Through("memberships", Membership.Type),    // Many-to-Many
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

    // For PostgreSQL: go get github.com/lib/pq
    _ "modernc.org/sqlite"
    "yourproject/velox"
    "yourproject/velox/user"
)

func main() {
    // Open a database connection
    client, err := velox.Open("sqlite", "file:velox?mode=memory&_pragma=foreign_keys(1)")
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
edge.To("posts", Post.Type)             // User has many Posts
edge.From("author", User.Type).Unique() // Post belongs to one User

// One-to-One
edge.To("profile", Profile.Type).Unique()

// Many-to-Many (with join table)
edge.To("groups", Group.Type).Through("memberships", Membership.Type)
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
import "github.com/syssam/velox/privacy"

func (User) Policy() velox.Policy {
    return privacy.Policy{
        Mutation: privacy.MutationPolicy{
            privacy.DenyIfNoViewer(),
            privacy.HasRole("admin"),
            privacy.IsOwner("user_id"),
            privacy.AlwaysDenyRule(),
        },
        Query: privacy.QueryPolicy{
            privacy.AlwaysAllowRule(),
        },
    }
}
```

## Mixins

Built-in mixins for common patterns:

```go
import "github.com/syssam/velox/schema/mixin"

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

### WhereInput Filtering (Whitelist Model)

By default, fields are **NOT filterable** in WhereInput — you must explicitly opt-in. This follows industry best practices (Pothos, Nexus, Hasura, Shopify, GitHub).

```go
func (User) Fields() []velox.Field {
    return []velox.Field{
        field.String("email").Annotations(graphql.WhereInput()),                  // filterable, smart defaults
        field.String("role").Annotations(graphql.WhereOps(graphql.OpsEquality)),  // filterable, explicit ops
        field.String("internal_notes"),                                           // NOT filterable
    }
}

func (User) Edges() []velox.Edge {
    return []velox.Edge{
        edge.To("posts", Post.Type).Annotations(graphql.WhereInput()),  // HasPosts, HasPostsWith
        edge.To("audit_logs", AuditLog.Type),                           // NOT filterable
    }
}

// Or use entity-level bulk opt-in:
func (User) Annotations() []velox.Annotation {
    return []velox.Annotation{
        graphql.WhereInputFields("email", "role", "created_at"),
        graphql.WhereInputEdges("posts"),
    }
}
```

Enable `FeatureWhereInputAll` to restore Ent-compatible behavior (all fields filterable by default):

```go
cfg, err := gen.NewConfig(
    gen.WithFeatures(gen.FeatureWhereInputAll),
)
```

### Custom Resolvers

Use `Resolvers()` to add custom resolver fields to an entity type. gqlgen generates resolver stubs that you implement. For forcing existing fields to use resolvers, configure `forceResolver` in gqlgen.yml instead.

```go
func (Invoice) Annotations() []velox.Annotation {
    return []velox.Annotation{
        graphql.Resolvers(
            graphql.Map("glAccount", "PublicGlAccount!"),
            graphql.Map("approver", "PublicUser"),
            graphql.Map("priceListItem(priceListId: ID!)", "PriceListItem!"),
        ),
    }
}
```

Generated SDL:

```graphql
type Invoice implements Node {
  id: ID!
  glAccount: PublicGlAccount! @goField(forceResolver: true)
  approver: PublicUser @goField(forceResolver: true)
  priceListItem(priceListId: ID!): PriceListItem! @goField(forceResolver: true)
}
```

- `Map(fieldName, returnType)` — adds a new field with `@goField(forceResolver: true)`. The `returnType` is the full GraphQL type including nullability (`"Type!"` for non-null, `"Type"` for nullable). The `fieldName` can include inline arguments in SDL syntax. If the field name matches an edge, the auto-generated edge resolver is suppressed.
- `.WithComment(text)` — adds a GraphQL description (`"""..."""`) above the field.

Field-level and edge-level comments from schema definitions (`.Comment("...")`) are also emitted as GraphQL descriptions automatically.

### Omittable (PATCH Mutations)

Use `Omittable()` to distinguish "field not sent" from "field sent as null" in update mutations. This enables PATCH semantics using gqlgen's `graphql.Omittable[T]`.

```go
func (Invoice) Fields() []velox.Field {
    return []velox.Field{
        field.String("memo").Optional().Nillable().
            Annotations(graphql.Omittable()),
    }
}
```

Generated Go struct uses `graphql.Omittable[*string]` instead of `*string`, and the `Mutate()` method uses `IsSet()`/`Value()`:

```go
type UpdateInvoiceInput struct {
    Memo graphql.Omittable[*string]  // not *string
}

// In Mutate():
if i.Memo.IsSet() {
    v := i.Memo.Value()
    if v != nil {
        m.SetMemo(*v)    // user sent a value
    } else {
        m.ClearMemo()    // user sent null
    }
}
// if !IsSet() → user didn't send the field, do nothing
```

To enable Omittable for **all** nullable fields globally, set `nullable_input_omittable: true` in your `gqlgen.yml`. Velox auto-detects this and generates matching Go structs.

## Documentation

| Document | Description |
|----------|-------------|
| [Getting Started](docs/getting-started.md) | Step-by-step tutorial from zero to working app |
| [Architecture](docs/architecture.md) | System design, pipeline stages, generated code structure |
| [Privacy & Authorization](docs/privacy.md) | Policy-based access control, row-level filtering, multi-tenancy |
| [Hooks & Interceptors](docs/hooks-and-interceptors.md) | Mutation middleware and query interceptors |
| [Reference](docs/reference.md) | Schema annotations, field types, validation, generated code patterns |
| [DataLoader](docs/dataloader.md) | Batch loading helpers for GraphQL N+1 |
| [Migration](docs/migration.md) | Database migration strategies and Atlas integration |
| [Benchmarks](docs/benchmarks.md) | Performance comparison methodology and results |
| [Ent Comparison](docs/ent-comparison.md) | API, architecture, and feature comparison with Ent |
| [Compatibility](COMPATIBILITY.md) | API stability contract and database support matrix |
| [Troubleshooting](docs/troubleshooting.md) | Common issues and solutions |
| [Contributing](CONTRIBUTING.md) | Development setup, testing, and PR process |

## Examples

Runnable tutorials under [`examples/`](examples/). Each has a README and an `e2e_test.go` or `main.go`.

| Example | What it shows |
|---------|---------------|
| [basic](examples/basic/) | Minimal 4-entity ORM: fields, edges, CRUD, bulk, tx |
| [fullgql](examples/fullgql/) | Full GraphQL + gqlgen showcase: 10 entities, hooks, privacy rules, Relay pagination |
| [realworld](examples/realworld/) | Production-style app pattern with Unwrap() tx-boundary contract |
| [tree](examples/tree/) | Self-referencing edge — `Category(parent/children)` hierarchy |
| [edge-schema](examples/edge-schema/) | M2M with intermediate entity — `User ⇆ Group via Membership(role, joined_at)` |
| [json-field](examples/json-field/) | JSON columns in three shapes: typed struct, untyped map, slice |
| [versioned-migration](examples/versioned-migration/) | Embedded `.sql` migration files + idempotent runner |
| [globalid](examples/globalid/) | Opaque cross-type IDs for GraphQL Relay `Node` interface |

## Acknowledgements

Velox is heavily inspired by [Ent](https://entgo.io/). The schema definition API, hooks/interceptors pattern, privacy layer, and generated query builder design all follow Ent's excellent patterns. If Ent works for your project, use it.

## License

[MIT](LICENSE)
