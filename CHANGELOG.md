# Changelog

All notable changes to Velox will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
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
- Comprehensive CLAUDE.md with project guidelines
- ARCHITECTURE.md with detailed system design
- Package-level documentation (doc.go files)
- Usage examples in documentation

[Unreleased]: https://github.com/syssam/velox/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/syssam/velox/releases/tag/v0.1.0
