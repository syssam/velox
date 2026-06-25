# Changelog

All notable changes to Velox will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed
- Driver no longer writes failing SQL statements and their arguments to the process-wide default logger on query errors — a leftover debug `log.Printf` in `dialect/sql/driver.go` leaked query args (often PII) to stderr and spammed logs on normal `context.Canceled`/`DeadlineExceeded`. The error is still returned to the caller (wrapped via `%w`); opt-in query logging remains available via `LogDriver`/`DebugDriver`. Pinned by `dialect/sql/driver_logging_test.go`; context-cancellation propagation across reads/writes/tx pinned by `tests/integration/e2e_context_cancel_test.go`
- Versioned-migration output was silently lost: `FeatureVersionedMigration` and the core migration generator both wrote `migrate/migrate.go` concurrently, racing on the final rename — the feature's types (`Migration`, `MigrationDir`, `LocalDir`) now live in their own `migrate/versioned.go`, and `TestOptionalFeatureSpecs_UniqueOutputs` pins that no two writers ever share an output path
- `internal/globalid.go` was rewritten on every generation when Snapshot+GlobalID were enabled (`ResolveIncrementStartsConflict` wrote unconditionally even with no conflict markers)

### Added
- Observability guide (`docs/observability.md`): OpenTelemetry tracing + metrics via `otelsql` + `sql.OpenDB` (the Ent-aligned `database/sql`-layer approach), the built-in `StatsDriver`/`LogDriver`/`DebugDriver`, and interceptor-based ORM-level spans. The documented otelsql wiring is verified end-to-end against a real `otelsql` release by the new isolated `contrib/otelvelox` module (CI-gated via the `contrib-modules` job), and the `database/sql` instrumentation seam velox routes through is pinned by `dialect/sql/observability_test.go`
- Public-API stability guard (`apiguard_test.go`): golden snapshots of the exported surface of the 8 consumer-facing packages (`velox`, `privacy`, `schema/{field,edge,index,mixin}`, `dialect/sql`, `runtime`) — funcs, methods, exported struct fields, interface methods, and generic type-parameter constraints — failing the build on any change (regenerate with `-update-api`). It is the blocking, every-push (including direct pushes to `main`) complement to the advisory PR-only `apidiff` job; see `COMPATIBILITY.md` § Enforcement
- Write-if-changed for all generated artifacts: a no-op regeneration rewrites zero files (preserving mtimes for make rules, file watchers, and editor indexers); a one-field schema change rewrites only the files whose bytes differ — measured 1 of 145 files in the integration prototype. Pinned by `TestGen_NoopRegen_PreservesMtimes`
- `docs/troubleshooting.md` section on generation/build caching, including the make-stamp pattern for skipping generation when the schema is unchanged
- ROADMAP.md — stage-promotion criteria, the v1.0.0 checklist, and deliberate non-goals
- Multi-dialect e2e coverage for predicate-scoped bulk UPDATE, clear-to-NULL (`ClearXxx`), NULL-aware aggregates (`MIN`/`MAX` skip NULLs, `SUM` over the empty set scans as `nil`), `ORDER BY` over nullable columns, and cursor pagination ordered by a nullable column (`tests/integration/e2e_multidialect_null_test.go`)
- testschema `User.nickname` (`Optional().Nillable()`) — the prototype's NULL-path guard; previously the entire clear-to-NULL chain had zero e2e coverage
- Documented + pinned Ent-parity limitation: cursor pagination ordered by a nullable column dead-ends when a page boundary lands on a NULL value (`docs/architecture-overview.md` §4.8, `docs/troubleshooting.md`)
- Comprehensive CLI integration tests for `cmd/velox`
- DataLoader utilities tests for `contrib/dataloader`
- CHANGELOG.md for tracking version history
- Improved test coverage across all packages
- CONTRIBUTING.md with development setup and contribution guidelines
- GitHub Actions CI/CD pipeline (test, lint, build)
- Massive test coverage for `compiler/gen/sql/` (14 new test files)
- Shared test infrastructure in `compiler/gen/sql/testutil_test.go`
- `version` command in CLI

### Fixed
- Linter warnings in multiple packages
- Unused parameter warnings in test files
- Shadow variable declarations in various functions
- File formatting issues detected by golangci-lint
- Missing `DeprecatedReason` assignment in JSON field builder
- Inaccurate gRPC references in README.md and velox.yaml (gRPC not implemented)
- Incorrect CLI commands in README.md (`velox init`, target flags)

### Changed
- Raised golangci-lint run timeout from 5m to 25m — a full-repo run exceeds 5m and the truncated run misleadingly printed "0 issues" before failing
- README documentation table now links the architecture overview (generated-code walkthrough) and roadmap; `docs/architecture-overview.md` headline numbers updated to the measured 10–25× incremental-rebuild figures
- Updated golangci-lint configuration compatibility
- Improved error handling with explicit error ignoring in debug paths
- Replaced `log.Printf` with `log/slog` across all core packages
- Added section comments to complex functions in `compiler/gen/graph.go`
- Refactored `cmd/velox/main.go` for testability (extracted `run()` function)
- Replaced `github.com/mattn/go-sqlite3` (CGO) with `modernc.org/sqlite` (pure Go)
- Changed `dialect.SQLite` constant from `"sqlite3"` to `"sqlite"` to match modernc.org/sqlite driver name
- Updated SQLite DSN format: `_fk=1` → `_pragma=foreign_keys(1)` for modernc.org/sqlite compatibility

### Removed
- gRPC references from configuration and documentation (not implemented)

## [0.1.0] - Initial Release

### Added
- **Core ORM Framework**
  - Type-safe query builders with compile-time checking
  - Fluent schema definition API
  - Support for PostgreSQL, MySQL, and SQLite dialects

- **Schema Definition (`schema/`)**
  - Field builders: String, Int, Float, Bool, Time, UUID, Enum, JSON, Bytes
  - Edge builders for relationships: To (O2M), From (O2O), Through (M2M)
  - Index builders with composite and unique support
  - Mixin support for reusable schema components

- **Code Generation (`compiler/`)**
  - Jennifer-based code generation for type safety
  - Parallel generation with configurable workers
  - Streaming writes for memory efficiency
  - Auto-tracked imports (no goimports needed)

- **SQL Dialect (`dialect/`)**
  - Query builders: SELECT, INSERT, UPDATE, DELETE
  - Transaction support with proper rollback handling
  - Connection pooling via standard database/sql
  - JSON operations support (sqljson)
  - Graph traversal for eager loading (sqlgraph)

- **Privacy Layer (`privacy/`)**
  - ORM-level authorization policies
  - Built-in rules: DenyIfNoViewer, HasRole, IsOwner, TenantRule
  - Rule combinators: And, Or, Not, Chain
  - Viewer context management

- **GraphQL Extension (`contrib/graphql/`)**
  - Optional GraphQL schema generation
  - Relay-style cursor pagination
  - WhereInput filtering support
  - Mutation input generation
  - gqlgen compatibility

- **DataLoader Utilities (`contrib/dataloader/`)**
  - Generic batch loading helpers
  - OrderByKeys for result ordering
  - GroupByKey for one-to-many relationships
  - Cache priming utilities
  - Context-based loader injection

- **Features System**
  - Privacy policies (FeaturePrivacy)
  - Query interceptors (FeatureIntercept)
  - EntQL query language (FeatureEntQL)
  - Global ID for Relay (FeatureGlobalID)
  - Versioned migrations (FeatureVersionedMigration)
  - Upsert support (FeatureUpsert)
  - Row-level locking (FeatureLock)

### Documentation
- `docs/architecture.md` with detailed system design
- `docs/reference.md` with schema and API conventions
- Package-level documentation (doc.go files)
- Usage examples in documentation

[Unreleased]: https://github.com/syssam/velox/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/syssam/velox/releases/tag/v0.1.0
