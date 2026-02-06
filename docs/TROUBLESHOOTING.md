# Velox Troubleshooting Guide

This guide covers common issues and their solutions when working with Velox.

## Table of Contents

1. [Code Generation Issues](#code-generation-issues)
2. [Schema Definition Issues](#schema-definition-issues)
3. [Query and Mutation Issues](#query-and-mutation-issues)
4. [Privacy and Authorization Issues](#privacy-and-authorization-issues)
5. [GraphQL Issues](#graphql-issues)
6. [Database Issues](#database-issues)
7. [Performance Issues](#performance-issues)

---

## Code Generation Issues

### "undefined: velox" after running generate.go

**Symptoms**: Import errors for generated package.

**Causes**:
1. Code generation failed silently
2. Wrong package path configuration

**Solutions**:

```go
// Check generate.go configuration
cfg, err := gen.NewConfig(
    gen.WithTarget("./velox"),                    // Output directory
    gen.WithPackage("your/module/path/velox"),    // Must match go.mod
)
```

Verify the package path matches your go.mod:
```bash
# Check your module name
head -1 go.mod
# Should match the package path prefix
```

### "cannot find package" for schema types

**Symptoms**: Compiler can't find schema package.

**Solutions**:

1. Ensure schema files are in a valid Go package:
   ```
   project/
   ├── schema/
   │   ├── user.go
   │   └── post.go  # Must have "package schema"
   └── generate.go
   ```

2. Run `go mod tidy` to update dependencies.

### Generated code has compilation errors

**Symptoms**: Generated files don't compile.

**Solutions**:

1. Ensure all edge types exist:
   ```go
   // If Post.go has edge to User, User.go must exist
   edge.To("author", User.Type)
   ```

2. Check for circular dependencies in edges

3. Regenerate with verbose output:
   ```bash
   go run generate.go 2>&1 | tee generate.log
   ```

---

## Schema Definition Issues

### "field name cannot be empty"

**Cause**: Empty string passed to field builder.

**Solution**:
```go
// Wrong
field.String("")

// Correct
field.String("name")
```

### "schema name conflicts with Go keyword"

**Cause**: Using reserved Go keyword as schema name.

**Solution**:
```go
// Wrong - "type" is a Go keyword
type Type struct { velox.Schema }

// Correct
type EntityType struct { velox.Schema }
```

Reserved names: `type`, `func`, `map`, `chan`, `interface`, etc.

### "unique field cannot have default value"

**Cause**: Unique fields shouldn't have defaults as they must be unique.

**Solution**:
```go
// Wrong
field.String("email").Unique().Default("default@example.com")

// Correct - no default for unique fields
field.String("email").Unique()
```

### "sensitive field cannot have struct tags"

**Cause**: Sensitive fields (like passwords) shouldn't be serialized.

**Solution**:
```go
// Wrong
field.String("password").Sensitive().StructTag(`json:"password"`)

// Correct - sensitive fields auto-exclude from serialization
field.String("password").Sensitive()
```

### "id field cannot be optional"

**Cause**: Primary key fields must always have a value.

**Solution**:
```go
// Wrong
field.Int64("id").Optional()

// Correct
field.Int64("id")
```

---

## Query and Mutation Issues

### "entity not found"

**Cause**: Query returned no results when one was expected.

**Solutions**:

```go
// Use Query().First() for optional single result
user, err := client.User.Query().
    Where(user.ID(123)).
    First(ctx)
if velox.IsNotFound(err) {
    // Handle not found case
}

// Use Query().Only() when exactly one result is expected
user, err := client.User.Query().
    Where(user.ID(123)).
    Only(ctx)
```

### "entity not singular"

**Cause**: `Only()` returned more than one result.

**Solution**:
```go
// Check your WHERE clause is specific enough
users, err := client.User.Query().
    Where(user.EmailEQ("test@example.com")).
    All(ctx)
// Then verify uniqueness
```

### Edge not loaded error

**Cause**: Accessing edge that wasn't eager-loaded.

**Solutions**:

```go
// Option 1: Eager load with WithXxx
user, err := client.User.Query().
    Where(user.ID(123)).
    WithPosts().  // Eager load posts
    Only(ctx)
// Now user.Edges.Posts is populated

// Option 2: Query edge separately
posts, err := user.QueryPosts().All(ctx)

// Option 3: Check if loaded
posts, err := user.Edges.PostsOrErr()
if err != nil {
    // Edge not loaded, query it
    posts, err = user.QueryPosts().All(ctx)
}
```

### Transaction not committed

**Cause**: Forgot to commit transaction.

**Solution**:
```go
tx, err := client.Tx(ctx)
if err != nil {
    return err
}
// Use defer to ensure cleanup
defer func() {
    if v := recover(); v != nil {
        tx.Rollback()
        panic(v)
    }
}()

// Do work...

// IMPORTANT: Commit the transaction
if err := tx.Commit(); err != nil {
    return err
}
```

---

## Privacy and Authorization Issues

### "privacy denied query on X"

**Cause**: Privacy policy denied the query.

**Solutions**:

1. Check if viewer context is set:
   ```go
   ctx := privacy.WithViewer(ctx, &privacy.SimpleViewer{
       UserID: "user-123",
       Roles:  []string{"user"},
   })
   ```

2. Review your privacy rules:
   ```go
   func (User) Policy() velox.Policy {
       return policy.Policy{
           Query: policy.QueryPolicy{
               privacy.AlwaysAllowQueryRule(), // Debug: allow all
           },
       }
   }
   ```

3. Check rule ordering (first match wins):
   ```go
   policy.Query(
       privacy.HasRole("admin"),        // Checked first
       privacy.TenantRule("tenant_id"), // Then this
       privacy.AlwaysDenyRule(),        // Deny by default
   )
   ```

### Privacy rules not being evaluated

**Cause**: Privacy feature not enabled.

**Solution**:
```go
cfg, _ := gen.NewConfig(
    gen.WithFeatures(gen.FeaturePrivacy),
)
```

---

## GraphQL Issues

### "field X not found in type Y"

**Cause**: Field skipped in GraphQL schema.

**Solution**:
```go
// Check for Skip annotations
field.String("internal_field").
    Annotations(graphql.Skip(graphql.SkipAll)) // This hides from GraphQL
```

### Mutation returns "unauthorized"

**Cause**: Privacy policy or middleware blocking mutation.

**Solutions**:

1. Check privacy policies for the entity
2. Check authentication middleware
3. Enable debug logging:
   ```go
   client = client.Debug()
   ```

### Connection/pagination not working

**Cause**: Relay connection not enabled.

**Solution**:
```go
func (User) Annotations() []velox.Annotation {
    return []velox.Annotation{
        graphql.RelayConnection(), // Enable connections
        graphql.QueryField(),      // Enable query field
    }
}
```

---

## Database Issues

### "constraint violation"

**Cause**: Database constraint violated (unique, foreign key, check).

**Solutions**:

```go
if velox.IsConstraintError(err) {
    var constraintErr *velox.ConstraintError
    if errors.As(err, &constraintErr) {
        switch {
        case strings.Contains(constraintErr.Error(), "unique"):
            // Handle duplicate value
        case strings.Contains(constraintErr.Error(), "foreign_key"):
            // Handle missing reference
        }
    }
}
```

### "cannot start a transaction within a transaction"

**Cause**: Trying to create nested transaction.

**Solution**:
```go
// Check if already in transaction
func doWork(ctx context.Context, client *velox.Client) error {
    if _, ok := client.Tx(); ok {
        // Already in transaction, use existing client
        return doActualWork(ctx, client)
    }
    // Start new transaction
    tx, err := client.Tx(ctx)
    // ...
}
```

### Connection timeout

**Cause**: Database connection issues.

**Solutions**:

```go
// Configure connection pool
db.SetMaxOpenConns(25)
db.SetMaxIdleConns(10)
db.SetConnMaxLifetime(5 * time.Minute)

// Add connection timeout
ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
defer cancel()
```

---

## Performance Issues

### Slow queries

**Solutions**:

1. Enable query logging:
   ```go
   client = client.Debug()
   ```

2. Add missing indexes:
   ```go
   func (User) Indexes() []velox.Index {
       return []velox.Index{
           index.Fields("email"),              // Single field
           index.Fields("status", "created"),  // Composite
       }
   }
   ```

3. Use projections:
   ```go
   // Only select needed columns
   client.User.Query().
       Select(user.FieldID, user.FieldName).
       All(ctx)
   ```

### N+1 query problem

**Cause**: Loading relationships in a loop.

**Solution**:
```go
// Wrong - N+1 queries
users, _ := client.User.Query().All(ctx)
for _, u := range users {
    posts, _ := u.QueryPosts().All(ctx) // N queries
}

// Correct - eager loading
users, _ := client.User.Query().
    WithPosts().  // Single query for all posts
    All(ctx)
```

### Memory issues with large result sets

**Solutions**:

1. Use pagination:
   ```go
   client.User.Query().
       Limit(100).
       Offset(page * 100).
       All(ctx)
   ```

2. Use cursor-based pagination for better performance:
   ```go
   client.User.Query().
       Where(user.IDGT(lastID)).
       Limit(100).
       All(ctx)
   ```

---

## Getting Help

If you can't find a solution here:

1. **Check the documentation**: [CLAUDE.md](/CLAUDE.md) has detailed API docs
2. **Search existing issues**: [GitHub Issues](https://github.com/syssam/velox/issues)
3. **Open a new issue** with:
   - Velox version (`go list -m github.com/syssam/velox`)
   - Go version (`go version`)
   - Database type and version
   - Complete error message
   - Minimal reproduction code
   - Schema definition (if relevant)
