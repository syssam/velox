# Compatibility

## API Stability

Velox follows [Go module versioning](https://go.dev/doc/modules/version-numbers):

- **Exported APIs** in non-internal packages are stable within a major version.
  Breaking changes require a major version bump.
- **Generated code patterns** may change between minor versions.
  Regenerate after upgrading: `go generate ./...`
- **`internal/` packages** are not part of the public API and may change at any time.

### Enforcement

The exported surface of the consumer-facing packages — the root `velox` package,
`privacy`, `schema/field`, `schema/edge`, `schema/index`, `schema/mixin`,
`dialect/sql`, and `runtime` — is protected by two complementary layers (same
package set):

- **`apiguard_test.go` (blocking):** golden snapshots in `testdata/apiguard/`,
  checked by `go test ./...` on **every push, including direct pushes to `main`**.
  Any change to an exported function, method, type, struct field, or interface
  fails the build — so an accidental break can't ship silently.
- **the `apidiff` CI job (advisory):** a semantic Go-compatibility diff against the
  pull-request base, surfaced as a reviewer warning. PR-only.

When you change the public API **intentionally**, regenerate the snapshot and call
the change out in `CHANGELOG.md`:

```bash
go test . -run TestPublicAPIGuard -update-api
```

A failing `TestPublicAPIGuard` with no intended API change means you introduced an
unintended break — revert it rather than updating the golden.

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
