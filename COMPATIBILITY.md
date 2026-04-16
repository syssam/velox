# Compatibility

## API Stability

Velox follows [Go module versioning](https://go.dev/doc/modules/version-numbers):

- **Exported APIs** in non-internal packages are stable within a major version.
  Breaking changes require a major version bump.
- **Generated code patterns** may change between minor versions.
  Regenerate after upgrading: `go generate ./...`
- **`internal/` packages** are not part of the public API and may change at any time.

## Database Support

| Database | Version | Driver | Status |
|----------|---------|--------|--------|
| PostgreSQL | 12+ | `lib/pq` | Primary, fully tested |
| MySQL | 8.0+ | `go-sql-driver/mysql` | Fully tested |
| SQLite | 3.35+ | `modernc.org/sqlite` | Fully tested (pure Go, no CGO) |

## Go Version

Velox requires the Go version specified in `go.mod`. We support the two most
recent Go releases, matching the [Go release policy](https://go.dev/doc/devel/release#policy).

## Deprecation Policy

Deprecated APIs are marked with `// Deprecated:` comments and will be removed
in the next major version. Migration paths are documented in the deprecation
comment.

## Reporting Issues

If you encounter a compatibility issue, please file it at
https://github.com/syssam/velox/issues with the `compatibility` label.
