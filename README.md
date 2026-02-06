# Velox

A type-safe Go ORM framework with integrated code generation for GraphQL services.

## Features

- **Type-Safe Query Builders** - Fluent API with compile-time type checking
- **Code Generation** - Generate ORM models, GraphQL schemas, and migrations
- **Multi-Database Support** - PostgreSQL, MySQL, SQLite
- **Relay-Style Pagination** - Cursor-based and offset pagination
- **Soft Delete** - Built-in soft delete support via mixins
- **Hooks & Interceptors** - BeforeCreate, AfterCreate, BeforeUpdate, etc.
- **Eager Loading** - Efficient relationship loading with query options
- **Retry Logic** - Automatic retry with exponential backoff for transient errors
- **SQL Error Parsing** - Structured error handling across database dialects

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
    "github.com/syssam/velox/schema/field"
    "github.com/syssam/velox/schema/edge"
    "github.com/syssam/velox/schema/mixin"
)

type User struct{ velox.Schema }

func (User) Mixins() []velox.Mixin {
    return []velox.Mixin{
        mixin.ID{},
        mixin.Time{},
    }
}

func (User) Fields() []velox.Field {
    return []velox.Field{
        field.String("email").Unique().NotEmpty(),
        field.String("name").MaxLen(100),
        field.Enum("status").Values("active", "inactive").Default("active"),
    }
}

func (User) Edges() []velox.Edge {
    return []velox.Edge{
        edge.To("posts", Post.Type),          // O2M (default)
        edge.To("profile", Profile.Type).Unique(), // O2O
    }
}
```

### 2. Register Schemas

```go
// schema/entities.go
package schema

import "github.com/syssam/velox"

func Schemas() []velox.Schema {
    return []velox.Schema{
        User{},
        Post{},
        Profile{},
    }
}
```

### 3. Generate Code

```bash
go run ./cmd/velox generate
```

### 4. Use Generated Code

```go
package main

import (
    "context"
    "database/sql"
    "log"

    _ "github.com/lib/pq"
    "yourproject/generated/orm"
)

func main() {
    db, err := sql.Open("postgres", "postgres://localhost/mydb?sslmode=disable")
    if err != nil {
        log.Fatal(err)
    }

    client := orm.NewClient(db)
    ctx := context.Background()

    // Create
    user, err := client.User.Create().
        SetEmail("user@example.com").
        SetName("John Doe").
        Save(ctx)

    // Query with filters
    users, err := client.User.Query().
        Where(
            orm.UserStatusEQ("active"),
            orm.UserEmailContains("@example.com"),
        ).
        WithPosts(func(q *orm.PostQuery) {
            q.Where(orm.PostPublishedEQ(true))
        }).
        OrderBy(orm.UserCreatedAtDesc()).
        Limit(10).
        All(ctx)

    // Update
    user, err = client.User.UpdateOneID(user.ID).
        SetName("Jane Doe").
        Save(ctx)

    // Delete
    err = client.User.DeleteOneID(user.ID).Exec(ctx)
}
```

## Configuration

`velox.yaml`:

```yaml
schema:
  path: ./schema

database:
  dialect: postgres  # postgres, mysql, sqlite

output:
  orm:
    path: ./generated/orm
    package: orm
  graphql:
    path: ./generated/graphql
    package: graphql
  migrations:
    path: ./migrations

features:
  softDelete: true
  timestamps: true
  hooks: true

graphql:
  relay: true
  mutations: true
  filters: true
  ordering: true
```

## Field Types

```go
field.Int64("id")                   // BIGINT (use field.PrimaryKey annotation)
field.String("name")                // VARCHAR/TEXT
field.Text("bio")                   // TEXT (unlimited)
field.Int64("count")                // BIGINT
field.Float64("price")              // DOUBLE PRECISION
field.Bool("active")                // BOOLEAN
field.Time("created_at")            // TIMESTAMP
field.Enum("status").Values(...)    // VARCHAR with validation
field.JSON("metadata")              // JSONB/JSON
field.UUID("external_id", uuid.UUID{})  // UUID
field.Bytes("data")                 // BYTEA/BLOB
```

## Field Modifiers

```go
field.String("email").
    Unique().           // UNIQUE constraint
    Index().            // Create index
    NotEmpty().         // Validation
    MaxLen(255).        // VARCHAR(255)
    Optional().         // NULL allowed
    Default("value").   // Default value
    Immutable().        // Cannot update after create
    Order()             // Include in ORDER BY options
```

## Relationships

```go
// One-to-Many (O2M is the default)
edge.To("posts", Post.Type)           // User has many Posts
edge.From("author", User.Type)        // Post belongs to User

// One-to-One (use .Unique())
edge.To("profile", Profile.Type).Unique()

// Many-to-Many (use .Through())
edge.To("tags", Tag.Type).Through(PostTag.Type)
```

## Querying

```go
// Predicates
orm.UserEmailEQ("test@example.com")
orm.UserEmailNEQ("test@example.com")
orm.UserEmailIn("a@b.com", "c@d.com")
orm.UserEmailContains("@example")
orm.UserEmailHasPrefix("admin")
orm.UserEmailHasSuffix(".com")
orm.UserAgeGT(18)
orm.UserAgeLTE(65)
orm.UserAgeBetween(18, 65)
orm.UserDeletedAtIsNil()

// Combining predicates
orm.And(orm.UserAgeGT(18), orm.UserStatusEQ("active"))
orm.Or(orm.UserRoleEQ("admin"), orm.UserRoleEQ("moderator"))
orm.Not(orm.UserStatusEQ("banned"))

// Eager loading
client.User.Query().
    WithPosts().
    WithProfile().
    All(ctx)

// Eager loading with options
client.User.Query().
    WithPosts(func(q *orm.PostQuery) {
        q.Where(orm.PostPublishedEQ(true)).
          OrderBy(orm.PostCreatedAtDesc()).
          Limit(5)
    }).
    All(ctx)
```

## Pagination

### Cursor-based (Relay)

```go
users, err := client.User.Query().
    Where(orm.UserStatusEQ("active")).
    Limit(10).
    All(ctx)
```

### Offset-based

```go
// Simple offset pagination using Limit and Offset
page := 2
perPage := 20

users, err := client.User.Query().
    Offset((page - 1) * perPage).
    Limit(perPage).
    All(ctx)
```

## Hooks

```go
// In your entity schema
func (User) Hooks() []velox.Hook {
    return []velox.Hook{
        velox.BeforeCreate(func(ctx context.Context, u *User) error {
            u.CreatedAt = time.Now()
            return nil
        }),
        velox.AfterCreate(func(ctx context.Context, u *User) error {
            log.Printf("User created: %d", u.ID)
            return nil
        }),
    }
}
```

## Transactions

```go
err := client.WithTx(ctx, func(tx *orm.Tx) error {
    user, err := tx.User.Create().
        SetEmail("user@example.com").
        Save(ctx)
    if err != nil {
        return err // Rollback
    }

    _, err = tx.Profile.Create().
        SetUserID(user.ID).
        Save(ctx)
    return err // Commit on nil, rollback on error
})
```

## Client Options

```go
client := orm.NewClient(db,
    orm.WithLogger(slog.Default()),
    orm.WithQueryLogging(true),
    orm.WithSlowQueryLogging(100*time.Millisecond),
    orm.WithQueryTimeout(5*time.Second),
    orm.WithMaxOpenConns(25),
    orm.WithMaxIdleConns(5),
    orm.WithConnMaxLifetime(time.Hour),
)
```

## Error Handling

```go
import "github.com/syssam/velox"

user := &orm.User{Email: "dup@test.com"}
_, err := client.User.Create(user).Save(ctx)
if err != nil {
    if velox.IsNotFound(err) {
        // Handle not found
    }
    if velox.IsConstraintError(err) {
        // Handle constraint violation (unique, foreign key, etc.)
    }
}
```

## Mixins

Built-in mixins for common patterns:

```go
mixin.ID{}         // int64 auto-increment primary key
mixin.Time{}       // created_at, updated_at timestamps
mixin.SoftDelete{} // deleted_at for soft deletes
mixin.TenantID{}   // tenant_id for multi-tenancy
```

## CLI Commands

```bash
velox generate ./schema                       # Generate code from schema
velox generate ./schema --target ./velox       # Specify output directory
velox generate ./schema --package mymodule/ent # Specify output package
velox help                                     # Show help
velox version                                  # Show version
```

## Database Support

| Feature | PostgreSQL | MySQL | SQLite |
|---------|------------|-------|--------|
| CRUD | ✅ | ✅ | ✅ |
| Transactions | ✅ | ✅ | ✅ |
| RETURNING | ✅ | ❌ | ❌ |
| UUID type | ✅ | ❌ | ❌ |
| JSONB | ✅ | JSON | JSON |
| Soft Delete | ✅ | ✅ | ✅ |

## License

MIT
