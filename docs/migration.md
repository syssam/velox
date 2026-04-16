# Velox Migration Guide

This guide helps you migrate from other ORMs or between Velox versions.

## Table of Contents

1. [Migrating from Ent](#migrating-from-ent)
2. [Migrating from GORM](#migrating-from-gorm)
3. [Database Migrations](#database-migrations)
4. [Schema Evolution](#schema-evolution)
5. [Breaking Changes](#breaking-changes)
6. [Zero-Downtime Migrations](#zero-downtime-migrations)
7. [Rollback Patterns](#rollback-patterns)
8. [Data Migrations](#data-migrations)

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

### SP-2: Cross-Package State Unification (2026-04)

SP-2 changed how velox propagates client-level interceptors, hooks, and
privacy through the codegen output. The user-facing API is unchanged
(`client.X.Intercept(...)`, `client.X.Use(...)`, schema `Policy()` all
work the same), but the propagation semantics changed in two ways that
matter at the edges:

#### `client.X.Intercept(...)` is now visible to already-constructed queries

**Before SP-2:** `client.User.Intercept(myInter)` only affected queries
constructed AFTER the call. Queries constructed before the call would
not see `myInter` because the interceptor list was copied into each
query at construction time.

**After SP-2:** `client.User.Intercept(myInter)` is immediately visible
to ALL queries — including queries constructed before the call. This
matches Ent's semantics. The change is structurally enforced: every
generated `*EntityQuery` now holds a `*entity.InterceptorStore` shared
pointer instead of a `[]Interceptor` slice copy.

**Migration:** No code changes required for the common case (most
users register interceptors before constructing queries). If your code
relied on the old "construct then intercept" ordering for any reason,
the behavior is now different.

#### `SetInters(...)` method removed from generated `*EntityQuery`

The `SetInters([]Interceptor)` method on generated query types no
longer exists. Interceptors propagate via the shared
`*entity.InterceptorStore` pointer set automatically at query
construction. If you were calling `SetInters` directly (which
shouldn't be possible from outside the generator), use
`SetInterStore(*entity.InterceptorStore)` — but you almost certainly
don't need to.

#### Privacy `Policy()` evaluation moved into the hook + interceptor chain

Privacy `EvalQuery` / `EvalMutation` calls no longer appear in
generated query / mutation chain code. Privacy rules are compiled into
the per-package `Hooks[0]` and `Interceptors[0]` slots at codegen-init
time, mirroring Ent's `runtime/runtime.go` pattern. End-user code is
unaffected — your schema's `Policy()` still works exactly the same
way; only the wiring mechanism changed.

### Deprecation Notices

- `Config()` method on schema is deprecated. Use `Annotations()` instead.
- `Nullable()` on fields has been removed. Use `Nillable()` instead.

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

1. Check the [Troubleshooting Guide](./troubleshooting.md)
2. Search [GitHub Issues](https://github.com/syssam/velox/issues)
3. Open a new issue with:
   - Velox version
   - Go version
   - Database type and version
   - Schema definition
   - Error message
   - Steps to reproduce

---

## Zero-Downtime Migrations

Applying schema changes without downtime requires careful sequencing. The core principle is: make the database change backward-compatible first, deploy the new application code, then make the database change non-backward-compatible as a follow-up.

### Adding a Required Column

Adding a NOT NULL column with no default breaks reads from running application instances that do not yet include the new field. The safe three-step process is:

**Step 1 — Add the column as nullable:**

```go
// schema/user.go
field.String("phone").
    Optional().
    Nillable() // NULL in DB, *string in Go
```

Migrate and deploy. All running instances can write rows without the new column.

**Step 2 — Backfill existing rows:**

```go
// Run this as a one-off job or data migration script
func backfillPhone(ctx context.Context, client *velox.Client) error {
    users, err := client.User.Query().
        Where(user.PhoneIsNil()).
        All(ctx)
    if err != nil {
        return err
    }
    for _, u := range users {
        if err := client.User.UpdateOne(u).
            SetPhone("").
            Exec(ctx); err != nil {
            return err
        }
    }
    return nil
}
```

For large tables, batch the backfill to avoid locking the table or exhausting memory:

```go
func backfillPhoneBatched(ctx context.Context, client *velox.Client) error {
    const batchSize = 500
    for {
        ids, err := client.User.Query().
            Where(user.PhoneIsNil()).
            Limit(batchSize).
            IDs(ctx)
        if err != nil {
            return err
        }
        if len(ids) == 0 {
            return nil // done
        }
        if err := client.User.Update().
            Where(user.IDIn(ids...)).
            SetPhone("").
            Exec(ctx); err != nil {
            return err
        }
    }
}
```

**Step 3 — Make the column NOT NULL:**

```go
// schema/user.go — after all rows are backfilled
field.String("phone").
    Default("") // NOT NULL with default
```

Regenerate and run the migration. This ALTER TABLE is safe because no NULL values remain.

### Adding a Unique Index

Adding a unique index to a column with duplicate values fails. Identify and resolve duplicates before applying the migration:

```sql
-- Find duplicate values (PostgreSQL / MySQL)
SELECT email, COUNT(*) AS n
FROM users
GROUP BY email
HAVING COUNT(*) > 1;

-- Deduplicate: keep the row with the lowest ID
DELETE FROM users
WHERE id NOT IN (
    SELECT MIN(id) FROM users GROUP BY email
);
```

Then run the Velox migration. With Atlas versioned migrations, this SQL can be embedded directly in a migration file.

### Renaming a Column

Databases do not have a native RENAME COLUMN in all versions (MySQL pre-8.0, for example). The safe sequence is:

1. Add the new column (`new_name`) as nullable
2. Write a hook or trigger that copies `old_name` to `new_name` on every write
3. Backfill: `UPDATE table SET new_name = old_name WHERE new_name IS NULL`
4. Deploy application code that reads from `new_name`
5. Drop `old_name` after verifying no reads remain

---

## Rollback Patterns

### Manual Rollback SQL

Always generate the inverse SQL before applying a migration. Save it alongside your migration files:

```sql
-- migration/0015_add_users_phone.up.sql
ALTER TABLE users ADD COLUMN phone VARCHAR(20);

-- migration/0015_add_users_phone.down.sql
ALTER TABLE users DROP COLUMN phone;
```

Test the rollback script in a staging environment before applying the forward migration to production.

### Atlas Rollback

If you use Atlas versioned migrations, Atlas stores migration state and can generate rollback plans.

Check the current migration status:

```bash
atlas migrate status \
    --dir "file://migrations" \
    --url "postgres://user:pass@localhost/mydb"
```

Apply a specific version:

```bash
atlas migrate apply \
    --dir "file://migrations" \
    --url "postgres://user:pass@localhost/mydb" \
    --version 14   # roll forward/backward to version 14
```

Generate a new migration from the current schema diff:

```bash
atlas migrate diff add_users_phone \
    --dir "file://migrations" \
    --to "ent://schema" \
    --dev-url "sqlite://file?mode=memory"
```

### Application-Level Rollback

For changes that cannot be undone by a simple SQL DROP (e.g., data transformations), keep both schema versions deployable simultaneously for at least one deploy cycle. Feature flags or config values can toggle which code path is active.

---

## Data Migrations

Data migrations transform the content of existing rows during a schema change. Unlike schema migrations (DDL), they are DML operations (UPDATE, INSERT, DELETE) and must be treated as application-level operations, not schema-level ones.

### Running Data Migrations from Go

Write data migrations as standalone Go programs or as part of your application startup sequence:

```go
//go:build ignore

// cmd/migrate/main.go
// Run with: go run ./cmd/migrate

package main

import (
    "context"
    "log/slog"
    "os"

    "yourapp/velox"
    "yourapp/velox/user"
)

func main() {
    ctx := context.Background()
    client, err := velox.Open("postgres", os.Getenv("DATABASE_URL"))
    if err != nil {
        slog.Error("open db", "error", err)
        os.Exit(1)
    }
    defer client.Close()

    if err := normalizeEmails(ctx, client); err != nil {
        slog.Error("migration failed", "error", err)
        os.Exit(1)
    }
    slog.Info("migration completed")
}

func normalizeEmails(ctx context.Context, client *velox.Client) error {
    const batchSize = 1000
    var cursor int
    for {
        users, err := client.User.Query().
            Where(user.IDGT(cursor)).
            Order(user.ByID()).
            Limit(batchSize).
            All(ctx)
        if err != nil {
            return err
        }
        if len(users) == 0 {
            return nil
        }
        for _, u := range users {
            normalized := strings.ToLower(strings.TrimSpace(u.Email))
            if normalized != u.Email {
                if err := client.User.UpdateOne(u).
                    SetEmail(normalized).
                    Exec(ctx); err != nil {
                    return fmt.Errorf("updating user %d: %w", u.ID, err)
                }
            }
        }
        cursor = users[len(users)-1].ID
    }
}
```

### Wrapping Data Migrations in Transactions

For small tables where atomicity matters, wrap the migration in a transaction:

```go
func migrateInTx(ctx context.Context, client *velox.Client) error {
    tx, err := client.Tx(ctx)
    if err != nil {
        return err
    }
    defer func() {
        if err != nil {
            _ = tx.Rollback()
        }
    }()

    // Perform updates using tx.User, tx.Post, etc.
    if err = backfillStatuses(ctx, tx); err != nil {
        return err
    }

    return tx.Commit()
}
```

Transactions are not appropriate for large backfills — they hold locks for the entire duration.

### Idempotent Migrations

Write data migrations so they can be safely run multiple times. Filter only rows that need updating:

```go
// Only update rows where the new field is still empty
client.User.Update().
    Where(
        user.StatusEQ(""),    // not yet migrated
        user.ActiveEQ(true),  // only active users
    ).
    SetStatus("active").
    Exec(ctx)
```

This ensures that re-running the migration after a partial failure does not overwrite data written by the new application code.

### Tracking Migration State

For complex multi-step data migrations, track progress explicitly:

```go
// schema/migration_state.go — optional tracking table
type MigrationState struct {
    velox.Schema
}

func (MigrationState) Fields() []velox.Field {
    return []velox.Field{
        field.String("name").Unique(),
        field.Time("applied_at").Default(time.Now).Immutable(),
    }
}
```

```go
func runIfNotApplied(ctx context.Context, client *velox.Client, name string, fn func() error) error {
    exists, err := client.MigrationState.Query().
        Where(migrationstate.NameEQ(name)).
        Exist(ctx)
    if err != nil {
        return err
    }
    if exists {
        slog.Info("migration already applied, skipping", "name", name)
        return nil
    }
    if err := fn(); err != nil {
        return err
    }
    return client.MigrationState.Create().SetName(name).Exec(ctx)
}
```
