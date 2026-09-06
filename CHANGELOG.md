# Changelog

All notable changes to Velox will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Performance
- Generated files are no longer parsed and re-printed after Jennifer renders them. `gen.FormatGoBytes` now regroups the import block textually (stdlib above third-party, each sorted by path — exactly what goimports produced) instead of calling `x/tools/imports`, which parsed and printed every file twice on top of Jennifer's own gofmt pass. Output is byte-identical (every golden in `compiler/gen/sql` and `contrib/graphql` is unchanged); an import block the textual pass does not recognize falls back to the parser-backed format-only pass. Render phase on the 328-entity stress fixture: 1.32s → 1.05s, CPU samples 22s → 17.6s
- The schema loader's work directory (`.velox/`, next to where generation runs) now persists between runs and ignores itself via its own `.gitignore`. On an unchanged schema the loader asks the go tool whether the cached binary is current (`go build -n`, ~0.14s, prints no link step when it is) and runs it as is — no relink, and no macOS first-exec code-signature validation (0.4–0.7s) because the file is never rewritten. When a build is needed it goes to a per-process temp file (concurrent loads in one directory cannot race on it) and replaces the cached binary only if the bytes differ. Measured: integration prototype `go run generate.go` 1.5s → 0.94s, 100-entity fixture 1.15s → 0.6s (unchanged schema). Stale loaders for the same schema package are pruned by an anchored match that leaves nested and `_`-suffixed sibling packages alone; the directory is safe to delete at any time. Review note: the first version of this change built to a fixed temp name on every run, so its "link skip" never actually happened and two concurrent loads raced on the temp file; both were caught in review and fixed before release
- The schema loader binary is named after a hash of its source instead of a per-run timestamp, and linked with `-ldflags=-s -w`. The Go build cache keys a command-line-arguments package on the file name, so the timestamp made every run a full compile+link (~0.8s). Unchanged-schema reruns (CI, `--check`, regen scripts) now rebuild from cache in ~0.2s; after a schema edit only the stripped link runs (~0.6s). Measured on the 328-entity fixture

- Generation raises GOGC to 200 for the whole `Graph.Gen` window — core generator and extensions such as contrib/graphql, which renders after the core returns — and restores it when the outermost generation returns (nested or concurrent generations share one window via a depth counter); an explicit `GOGC` in the environment is left alone. An execution trace (`go tool trace`) of a 328-entity generation showed the 16 parallel render workers serializing on the garbage collector's mark phase (`gcMarkDone`/`gcStart`), not on any lock. Measured on the same binary, alternating runs: render phase 2.3s → 1.6s (about 30% faster); peak RSS 129 MB → 268 MB at 328 entities, small next to the multi-GB compile that follows. Pinned by `compiler/gen/gc_test.go::TestRelaxGC`

### Added
- `graphql.InterfaceField(name)` on edges: GraphQL interface fields, ported from entgql (ent/contrib #638). Several edges on one type sharing the name become a single polymorphic field typed as the GraphQL interface their targets share (declared with `graphql.Implements`); a single edge with the name is a rename that keeps the edge's own field. When every implementor of an interface shares a renamed field, velox generates the `interface` definition from the fields all implementors expose with the same type, a Go interface with marker methods in `entity/gql_interfaces.go` that gqlgen binds through `@goModel`, the entity resolver methods (with a foreign-key fast path for `__typename`/`id`-only selections over nullable owner-side keys), and field-collection metadata so selecting the field eager-loads every contributing edge (`runtime.CollectMeta.InterfaceFields`, `runtime.EdgeMeta.OwnFK`). A to-many group is exposed as `<Interface>Connection` and resolved by the application. Documented in `docs/reference.md`; exercised end to end through gqlgen in `examples/fullgql` (`TestInterfaceField_EndToEnd`). The view-backed global interface connection from the same upstream change is not included
- Package-level set-operation constructors in `dialect/sql`: `Union`, `UnionAll`, `Except`, `ExceptAll`, `Intersect`, `IntersectAll` return a `Querier` that wraps every branch in parentheses, so per-branch `ORDER BY` / `LIMIT` / `OFFSET` are legal on MySQL and Postgres (and the SQL standard); the dialect is inferred from the first selector. On SQLite, which rejects parenthesized compound-select branches, each branch is rendered as a derived table (`SELECT * FROM (<branch>)`), which keeps its clauses — the same rows come back on every dialect, where ent #4505 silently drops the clauses on SQLite. `ExceptAll`/`IntersectAll` record a builder error on SQLite like the chaining methods do. `WithBuilder.As` accepts any `Querier`, so `With("all").As(UnionAll(a, b))` composes. Ported from ent #4503/#4504/#4505/#4506. Pinned by `dialect/sql/builder_test.go::TestSetOpFuncs` and, live on SQLite/Postgres/MySQL, by `tests/integration/e2e_multidialect_test.go::TestMultiDialect_SetOpFuncs` (Postgres placeholders keep counting across branches)

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

### Changed
- Generated-file formatting no longer resolves imports. `gen.FormatGoBytes` runs `x/tools/imports` in `FormatOnly` mode: Jennifer already tracks every import, so the resolving pass was pure overhead — and it spawned one `go env` subprocess per generated file (a fresh `ProcessEnv` per call). Measured on this laptop: integration-prototype regeneration 2.4s → 1.9s, the 100-entity stress fixture 5.8s → 2.7s, `go test -race ./cmd/velox` 90s → 31s. The pass still groups stdlib imports apart from third-party ones. Pinned by `compiler/gen/format_test.go`. Side effect worth knowing: the old resolving pass silently deleted imports it could not resolve under the golden-test mock, so four goldens (`client.go`, `tx.go`, `hook.go`, `privacy.go`) were missing the `client/<entity>` imports their bodies referenced; they now match the real generated layout
- Generated `ForUpdate`/`ForShare` decide whether to drop `DISTINCT` via the new `dialect.CapLockWithDistinct` capability instead of comparing the dialect name to `dialect.Postgres`. Behaviour is unchanged (Postgres drops DISTINCT, MySQL keeps it, SQLite ignores the lock) but a new dialect now only declares its flags. Pinned by `compiler/gen/sql/wiring_test.go::TestLockingUsesCapabilityFlags` and, behaviourally, by `tests/integration/e2e_multidialect_test.go::TestMultiDialect_LockWithDistinct` — the first end-to-end coverage of the generated lock builders
- `genQueryPkg` (`compiler/gen/sql/query_pkg.go`) is split into a `queryGen` struct with one method per emitted section, spread over `query_pkg.go`, `query_pkg_terminals.go` and `query_pkg_select.go`. It was a single 1,542-line function; the largest section is now 157 lines. Byte-identical output (goldens unchanged); still one generator for the one output file
- `runtime.CollectFields` takes the entity's `*CollectMeta` (columns, edges and `CollectedFor`) and is the single field-collection entry point; the interim `CollectFieldsMeta` and the two-map wrapper that silently dropped `CollectedFor` are gone. Regenerate after upgrading; the generated `CollectFields` methods call the new signature. API snapshot updated
- `scripts/regen.sh` classifies handwritten versus generated files in one awk pass over the first three lines of each file instead of forking two processes per file (the per-file loop cost about three minutes per run on macOS); gofmt and goimports now run only over the ~1,600 handwritten files. It also rebuilds every module after formatting, so a formatting pass that corrupts generated output fails the script instead of leaving broken modules behind
- The benchmark stress fixtures (`benchmarks/fixtures/stress-{100,200,328}`) are their own Go modules. Their gitignored generated output used to land inside the root module once generated locally, inflating `go list ./...` from 72 to 1,352 packages and pushing a full-tree `golangci-lint run` past its 25-minute timeout

- Generated `XxxSelect` holds its query as a named field (`UserQuery *UserQuery`) instead of embedding it, and forwards the 13 terminals the `entity.XxxSelector` interface exposes (`All`, `First`, `Only`, `Count`, `Exist`, `IDs`, `FirstID`, `OnlyID` and their X variants) explicitly. Embedding made the compiler emit a promoted-method wrapper for every query method per entity. Measured on the 328-entity stress fixture: functions in the query package 83,387 → 50,915 (−39%), object file 298 MB → 199 MB (−33%). Compile-time effect not yet measured on a quiet machine. The public API is unchanged: `Select(...)` returns the interface, and every method it lists is still there. Code that held a concrete `*XxxSelect` and called a promoted query method such as `Where` must call it on `s.XxxQuery` instead. Same cost class as gqlgen issue #4297, in its linear form. Pinned by `compiler/gen/sql/wiring_test.go::TestSelectForwardsInsteadOfEmbedding`

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

### Fixed
- Concurrent migrations no longer race on shared table state. The generated `migrate.Tables` is a package-level slice, so every client in a process shares it, and `Create` rewrites foreign-key symbols and primary-key markers on those values while the planning phase reads them back. The engine's own mutex is per-instance and each client builds its own, so two clients calling `Schema.Create` at the same time — parallel tests, or an application provisioning several databases — raced. Migration now serializes on the shared state; it is a startup-time, DDL-bound operation, so the lost concurrency costs nothing. Found by the race detector on the full integration suite and pinned by `tests/integration/e2e_concurrent_migrate_test.go::TestConcurrentMigrate_SharedTables` (reproduces the race on every run without the guard)
- Typed-JSON scalar generation in contrib/graphql is gated on the field's Go type being a NAMED type (an identifier, optionally package-qualified, optionally behind one `*`). The previous guard was a deny-list of spellings, and every unnamed shape it did not list — `field.JSON("x", struct{ X int }{})`, `[3]int{}`, `&[]string{}` — reached the scalar generator and produced `func Unmarshalstruct { X int }(...)`, failing the whole run at the format step. Such fields now fall back to the generic JSON scalar like slices and maps already did. Pinned by `contrib/graphql/schema_types_test.go`
- Tests that run the schema loader (`compiler`, `cmd/velox`) no longer leave a `.velox/` cache dir with a 25 MB binary in the package directory, and the loader source is written with goimports-style import grouping so lint gates that walk dot-directories stay quiet
- The `velox` CLI (`cmd/velox`: generate, check, validate, init, watch) is now actually in the repository. The bare `velox` pattern in `.gitignore`, meant for the built binary and relied on to hide generated `velox/` output directories, also matched the CLI's source directory, so `cmd/velox` had never been committed: it was absent from origin, from every clone, and from CI, while the README documented `velox generate`. Re-included with a `!/cmd/velox/` negation; its 63 tests now run in CI
- Generated imports of `client/<entity>` packages carry an explicit alias (`userclient "…/client/user"`). The package is named `<entity>client` while its path ends in `<entity>`; the generator registered the name without an alias and relied on the old resolving format pass to add one. Once that pass became format-only, the unaliased import was valid Go but fragile: `scripts/regen.sh`'s goimports step, run from the repo root, could not resolve sub-module paths (`velox.test/parity/...`, `example.com/...`), treated the import as unused and deleted it — every example module and the parity module were left uncompilable after a regen while the script reported success, because it built before formatting. The script now skips generator output in its goimports pass and rebuilds every module after formatting. Pinned by `compiler/gen/sql/wiring_test.go::TestClientPackageImportsAreAliased`
- `graphql.CollectedFor` now does something. The annotation was parsed, documented and tested at the constructor level, but the field collector never consulted it: a resolver field such as `fullName` annotated on `first_name`/`last_name` still hit the "unknown field → SELECT *" fallback. The generated `CollectMeta` now carries a `CollectedFor` map (a column annotated for several names appears under each; a hidden `Skip(SkipType)` column is still collected, which is the PII use case) and the collector projects exactly those columns. Runtime API: `CollectMeta.CollectedFor`, `CollectFieldsMeta`, and `SetFieldCollector` now takes a `FieldCollector` that receives the whole `*CollectMeta` — the old `CollectFields(ctx, q, fields, edges, ...)` still works as a wrapper. Pinned by `contrib/graphql/collect_test.go::TestCollectFields_CollectedFor`, `collect_gen_test.go::TestGenEntityCollection_EmitsCollectedFor` and `runtime/collect_test.go::TestCollectFieldsMeta`. Ports the behaviour of ent/contrib#633 (multiple ent fields per GraphQL field)
- `Transactioner.InterceptResponse` no longer panics when the context carries no gqlgen operation (custom transports, tests, non-gqlgen callers); it passes the request through. Ports ent/contrib#630. Pinned by `TestTransactioner_InterceptResponse_NoOperationContext`
- `scripts/regen.sh` no longer runs gofmt/goimports over `testdata/`. Golden files pin generator output byte-for-byte and their import paths (`github.com/test/project/...`) do not resolve, so goimports stripped the imports it could not find and silently corrupted the pins — the same masking that hid the missing-import goldens above
- Driver no longer writes failing SQL statements and their arguments to the process-wide default logger on query errors — a leftover debug `log.Printf` in `dialect/sql/driver.go` leaked query args (often PII) to stderr and spammed logs on normal `context.Canceled`/`DeadlineExceeded`. The error is still returned to the caller (wrapped via `%w`); opt-in query logging remains available via `LogDriver`/`DebugDriver`. Pinned by `dialect/sql/driver_logging_test.go`; context-cancellation propagation across reads/writes/tx pinned by `tests/integration/e2e_context_cancel_test.go`
- Versioned-migration output was silently lost: `FeatureVersionedMigration` and the core migration generator both wrote `migrate/migrate.go` concurrently, racing on the final rename — the feature's types (`Migration`, `MigrationDir`, `LocalDir`) now live in their own `migrate/versioned.go`, and `TestOptionalFeatureSpecs_UniqueOutputs` pins that no two writers ever share an output path
- `internal/globalid.go` was rewritten on every generation when Snapshot+GlobalID were enabled (`ResolveIncrementStartsConflict` wrote unconditionally even with no conflict markers)

- Linter warnings in multiple packages
- Unused parameter warnings in test files
- Shadow variable declarations in various functions
- File formatting issues detected by golangci-lint
- Missing `DeprecatedReason` assignment in JSON field builder
- Inaccurate gRPC references in README.md and velox.yaml (gRPC not implemented)
- Incorrect CLI commands in README.md (`velox init`, target flags)

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
