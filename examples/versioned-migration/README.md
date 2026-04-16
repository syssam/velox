# versioned-migration

A minimal "versioned migration" pattern — `.sql` files with timestamp prefixes, applied in order, recorded in a `schema_migrations` table so re-runs are idempotent. This is the style used by Rails, golang-migrate, goose, and Atlas.

## Run

```bash
go run generate.go
go test ./...
```

## What it shows

- **File layout:** `migrations/YYYYMMDDHHMMSS_name.sql` — one file per change, lexicographic order = apply order
- **`go:embed`:** migration SQL lives inside the binary at compile time; no runtime filesystem dependency
- **`schema_migrations` table:** records which versions have been applied so `Up(ctx)` is safe to run every boot
- **Transactional per-migration:** each migration runs in its own transaction, including the `INSERT INTO schema_migrations` — a crash mid-migration leaves no partial state

## Typical workflow

1. Add a new `.sql` file: `migrations/20260315000000_add_orders.sql`
2. Rebuild the binary — the new file is embedded automatically
3. On boot, call `Runner{DB: db}.Up(ctx)` — only the new migration runs

```go
import versionedmigration "example.com/versioned-migration"

runner := &versionedmigration.Runner{DB: db}
if err := runner.Up(ctx); err != nil {
    log.Fatal(err)
}
```

## When to use this vs. velox's auto-migrate

| | `Schema.Create()` (auto) | Versioned migrations |
|---|---|---|
| Dev / prototype | ✅ just works | Overkill |
| Staging / prod | ⚠️ no rollback, no audit | ✅ every change recorded |
| Complex DML changes (backfills, type conversions) | ❌ | ✅ write exact SQL |
| Multi-environment (dev → staging → prod) | Each env regenerates schema | ✅ identical migrations everywhere |

`Schema.Create()` compares the current DB to the generated schema and applies DDL — fine for dev. For production you want **replayable, auditable, reviewable** migration files, which is this pattern.

## Integration with velox

This example shows the migration runner as **standalone infrastructure** — it doesn't use velox codegen internals. Your velox-generated client uses the same DB connection after migrations have been applied. Typical boot sequence:

```go
db, _ := sql.Open("sqlite", dsn)
(&versionedmigration.Runner{DB: db}).Up(ctx)         // 1. apply migrations
client := velox.NewClient(velox.Driver(sql.OpenDB(db)))  // 2. wire velox client
```

## Production alternatives

For real systems, prefer a battle-tested library:

- **[Atlas](https://atlasgo.io/)** — schema-as-code, understands your velox schema, generates migrations automatically
- **[golang-migrate](https://github.com/golang-migrate/migrate)** — similar file-based pattern, more drivers, up/down support
- **[goose](https://github.com/pressly/goose)** — Go-native, supports both SQL and Go migrations
