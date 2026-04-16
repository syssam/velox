# Getting Started with Velox

This guide walks you through creating a simple application with Velox ORM.

## Prerequisites

- Go 1.26+ (see `go.mod` for exact version)
- A database: PostgreSQL, MySQL, or SQLite (no setup needed)

## Project Setup

```bash
mkdir myapp && cd myapp
go mod init myapp
go get github.com/syssam/velox
```

## Step 1: Define Your Schema

Create a `schema/` directory with Go files defining your entities:

```bash
mkdir schema
```

**schema/user.go:**

```go
package schema

import (
    "github.com/syssam/velox"
    "github.com/syssam/velox/schema/edge"
    "github.com/syssam/velox/schema/field"
    "github.com/syssam/velox/schema/mixin"
)

type User struct{ velox.Schema }

func (User) Mixin() []velox.Mixin {
    return []velox.Mixin{mixin.Time{}}
}

func (User) Fields() []velox.Field {
    return []velox.Field{
        field.String("name").NotEmpty(),
        field.String("email").Unique(),
    }
}

func (User) Edges() []velox.Edge {
    return []velox.Edge{
        edge.To("posts", Post.Type),
    }
}
```

**schema/post.go:**

```go
package schema

import (
    "github.com/syssam/velox"
    "github.com/syssam/velox/schema/edge"
    "github.com/syssam/velox/schema/field"
    "github.com/syssam/velox/schema/mixin"
)

type Post struct{ velox.Schema }

func (Post) Mixin() []velox.Mixin {
    return []velox.Mixin{mixin.Time{}}
}

func (Post) Fields() []velox.Field {
    return []velox.Field{
        field.String("title").NotEmpty(),
        field.Text("body"),
        field.Bool("published").Default(false),
    }
}

func (Post) Edges() []velox.Edge {
    return []velox.Edge{
        edge.From("author", User.Type).
            Ref("posts").
            Unique(),
    }
}
```

## Step 2: Generate Code

Create `generate.go` in your project root:

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

This generates all CRUD builders, predicates, and runtime code in `./velox/`.

## Step 3: Use the Generated Code

**main.go:**

```go
package main

import (
    "context"
    "fmt"
    "log"

    _ "modernc.org/sqlite"

    "myapp/velox"
    "myapp/velox/user"
    "myapp/velox/post"
)

func main() {
    // SQLite in-memory (no setup required)
    client, err := velox.Open("sqlite", "file:app?mode=memory&_pragma=foreign_keys(1)")
    if err != nil {
        log.Fatal(err)
    }
    defer client.Close()

    ctx := context.Background()

    // Auto-migrate the schema
    if err := client.Schema.Create(ctx); err != nil {
        log.Fatal(err)
    }

    // Create a user
    alice, err := client.User.Create().
        SetName("Alice").
        SetEmail("alice@example.com").
        Save(ctx)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Created user: %s (ID: %d)\n", alice.Name, alice.ID)

    // Create posts for the user
    _, err = client.Post.Create().
        SetTitle("Hello World").
        SetBody("My first post").
        SetAuthorID(alice.ID).
        Save(ctx)
    if err != nil {
        log.Fatal(err)
    }

    // Query with eager loading
    users, err := client.User.Query().
        Where(user.NameField.EQ("Alice")).
        WithPosts().
        All(ctx)
    if err != nil {
        log.Fatal(err)
    }
    for _, u := range users {
        fmt.Printf("User %s has %d posts\n", u.Name, len(u.Edges.Posts))
    }

    // Query posts by predicate
    published, err := client.Post.Query().
        Where(post.PublishedField.EQ(true)).
        Count(ctx)
    if err != nil {
        log.Fatal(err)
    }
    fmt.Printf("Published posts: %d\n", published)
}
```

Install the SQLite driver and run:

```bash
go get modernc.org/sqlite
go run main.go
```

## Step 4: Add GraphQL (Optional)

Install the GraphQL extension:

```bash
go get github.com/syssam/velox/contrib/graphql
go get github.com/99designs/gqlgen
```

Update `generate.go`:

```go
//go:build ignore

package main

import (
    "log/slog"
    "os"

    "github.com/syssam/velox/compiler"
    "github.com/syssam/velox/compiler/gen"
    "github.com/syssam/velox/contrib/graphql"
)

func main() {
    ex, err := graphql.NewExtension(
        graphql.WithConfigPath("./gqlgen.yml"),
        graphql.WithSchemaPath("./velox/schema.graphql"),
    )
    if err != nil {
        slog.Error("creating graphql extension", "error", err)
        os.Exit(1)
    }

    cfg, err := gen.NewConfig(gen.WithTarget("./velox"))
    if err != nil {
        slog.Error("creating config", "error", err)
        os.Exit(1)
    }

    if err := compiler.Generate("./schema", cfg, compiler.Extensions(ex)); err != nil {
        slog.Error("running velox codegen", "error", err)
        os.Exit(1)
    }
}
```

Add GraphQL annotations to your schema:

```go
func (User) Annotations() []velox.Annotation {
    return []velox.Annotation{
        graphql.RelayConnection(),
        graphql.QueryField(),
        graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
        graphql.WhereInputFields("name", "email"),
    }
}
```

## Next Steps

- [Privacy & Authorization](privacy.md) -- Role-based access, multi-tenancy, row-level filtering
- [Hooks & Interceptors](hooks-and-interceptors.md) -- Mutation middleware and query interceptors
- [DataLoader](dataloader.md) -- Batch loading for GraphQL N+1
- [Migration](migration.md) -- Database migration strategies
- [Troubleshooting](troubleshooting.md) -- Common issues and solutions
- [Benchmarks](benchmarks.md) -- Performance comparison with Ent

## Database Drivers

| Database | Driver | DSN Example |
|----------|--------|-------------|
| PostgreSQL | `github.com/lib/pq` | `host=localhost dbname=app sslmode=disable` |
| MySQL | `github.com/go-sql-driver/mysql` | `root:pass@tcp(localhost:3306)/app?parseTime=true` |
| SQLite | `modernc.org/sqlite` | `file:app.db?_pragma=foreign_keys(1)` |
