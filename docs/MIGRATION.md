# Velox Migration Guide

This guide helps you migrate from other ORMs or between Velox versions.

## Table of Contents

1. [Migrating from Ent](#migrating-from-ent)
2. [Migrating from GORM](#migrating-from-gorm)
3. [Database Migrations](#database-migrations)
4. [Schema Evolution](#schema-evolution)
5. [Breaking Changes](#breaking-changes)

---

## Migrating from Ent

Velox is designed to be API-compatible with Ent in many ways. Here's how to migrate:

### Schema Definition

**Ent:**
```go
package schema

import (
    "entgo.io/ent"
    "entgo.io/ent/schema/field"
)

type User struct {
    ent.Schema
}

func (User) Fields() []ent.Field {
    return []ent.Field{
        field.String("name"),
        field.String("email").Unique(),
    }
}
```

**Velox:**
```go
package schema

import (
    "github.com/syssam/velox"
    "github.com/syssam/velox/schema/field"
)

type User struct {
    velox.Schema
}

func (User) Fields() []velox.Field {
    return []velox.Field{
        field.String("name"),
        field.String("email").Unique(),
    }
}
```

### Key Differences

| Feature | Ent | Velox |
|---------|-----|-------|
| Base Schema | `ent.Schema` | `velox.Schema` |
| Import Path | `entgo.io/ent` | `github.com/syssam/velox` |
| GraphQL | Separate extension | Integrated `contrib/graphql` |
| Code Generation | Templates | Jennifer (type-safe) |

### Migration Steps

1. **Update imports:**
   ```bash
   # Replace ent imports with velox
   find ./schema -name "*.go" -exec sed -i '' 's/entgo.io\/ent/github.com\/syssam\/velox/g' {} \;
   ```

2. **Update generate.go:**
   ```go
   //go:build ignore

   package main

   import (
       "log"
       "github.com/syssam/velox/compiler"
       "github.com/syssam/velox/compiler/gen"
   )

   func main() {
       cfg, err := gen.NewConfig(
           gen.WithTarget("./velox"),
           gen.WithPackage("your/package/velox"),
       )
       if err != nil {
           log.Fatalf("creating config: %v", err)
       }
       if err := compiler.Generate("./schema", cfg); err != nil {
           log.Fatalf("running velox codegen: %v", err)
       }
   }
   ```

3. **Run code generation:**
   ```bash
   go run generate.go
   ```

4. **Update client usage:**
   ```go
   // Before (Ent)
   client, err := ent.Open("postgres", dsn)

   // After (Velox)
   client, err := velox.Open("postgres", dsn)
   ```

---

## Migrating from GORM

### Model Definition

**GORM:**
```go
type User struct {
    gorm.Model
    Name  string
    Email string `gorm:"uniqueIndex"`
}
```

**Velox:**
```go
// schema/user.go
type User struct {
    velox.Schema
}

func (User) Mixin() []velox.Mixin {
    return []velox.Mixin{
        mixin.Time{},  // Adds created_at, updated_at
    }
}

func (User) Fields() []velox.Field {
    return []velox.Field{
        field.String("name"),
        field.String("email").Unique(),
    }
}
```

### Query Patterns

**GORM:**
```go
var users []User
db.Where("age > ?", 18).Find(&users)
```

**Velox:**
```go
users, err := client.User.Query().
    Where(user.AgeGT(18)).
    All(ctx)
```

### Key Benefits of Velox over GORM

1. **Type Safety**: Compile-time checked queries
2. **Code Generation**: No runtime reflection
3. **Privacy Layer**: Built-in authorization
4. **GraphQL Integration**: First-class support

---

## Database Migrations

### Automatic Migrations

Velox can automatically migrate your database schema:

```go
if err := client.Schema.Create(ctx); err != nil {
    log.Fatalf("failed creating schema: %v", err)
}
```

### Migration Options

```go
import "github.com/syssam/velox/dialect/sql/schema"

// Create tables only (no modifications)
client.Schema.Create(ctx, schema.WithCreateOnly())

// Drop columns that are no longer in schema
client.Schema.Create(ctx, schema.WithDropColumn(true))

// Drop indexes that are no longer in schema
client.Schema.Create(ctx, schema.WithDropIndex(true))

// Run with foreign key constraints disabled
client.Schema.Create(ctx, schema.WithForeignKeys(false))
```

### Versioned Migrations

For production, use versioned migrations:

```go
// Enable versioned migration feature
cfg, _ := gen.NewConfig(
    gen.WithFeatures(gen.FeatureVersionedMigration),
)

// Generate migration files
atlas migrate diff migration_name \
  --dir "file://migrations" \
  --to "ent://schema" \
  --dev-url "sqlite://file?mode=memory"
```

### Dry Run

Check what SQL would be executed:

```go
changes, err := client.Schema.Diff(ctx)
if err != nil {
    log.Fatalf("failed getting diff: %v", err)
}
for _, change := range changes {
    fmt.Println(change)
}
```

---

## Schema Evolution

### Adding Fields

1. Add the field to your schema:
   ```go
   func (User) Fields() []velox.Field {
       return []velox.Field{
           field.String("name"),
           field.String("email").Unique(),
           field.Int64("age").Optional(), // New field
       }
   }
   ```

2. Regenerate code:
   ```bash
   go run generate.go
   ```

3. Run migration:
   ```go
   client.Schema.Create(ctx)
   ```

### Removing Fields

**Warning**: Removing fields will cause data loss.

1. Mark field as deprecated (optional):
   ```go
   field.String("old_field").
       Annotations(graphql.Skip(graphql.SkipAll))
   ```

2. Remove from schema
3. Regenerate and migrate with `WithDropColumn`:
   ```go
   client.Schema.Create(ctx, schema.WithDropColumn(true))
   ```

### Renaming Fields

1. Add new field
2. Migrate data:
   ```sql
   UPDATE users SET new_name = old_name;
   ```
3. Remove old field

### Changing Field Types

**Warning**: May cause data loss or require data transformation.

1. Create new field with new type
2. Migrate data with transformation
3. Remove old field

---

## Breaking Changes

### Version 1.x to 2.x

(Currently single version - section for future reference)

### Deprecation Notices

- `Config()` method on schema is deprecated. Use `Annotations()` instead.
- `Nullable()` on fields is deprecated. Use `Nillable()` instead.

---

## Troubleshooting Migration Issues

### Common Errors

#### "duplicate key violates unique constraint"

**Cause**: Trying to add a unique index on a column with duplicate values.

**Solution**:
```sql
-- Find duplicates
SELECT email, COUNT(*) FROM users GROUP BY email HAVING COUNT(*) > 1;

-- Remove or merge duplicates before adding unique constraint
```

#### "column cannot be cast to type"

**Cause**: Changing column type to incompatible type.

**Solution**: Create new column, transform data, then drop old column.

#### "cannot drop column: constraint depends on it"

**Cause**: Foreign key or index depends on the column.

**Solution**: Drop dependent constraints first, then drop column.

### Getting Help

1. Check the [Troubleshooting Guide](./TROUBLESHOOTING.md)
2. Search [GitHub Issues](https://github.com/syssam/velox/issues)
3. Open a new issue with:
   - Velox version
   - Go version
   - Database type and version
   - Schema definition
   - Error message
   - Steps to reproduce
