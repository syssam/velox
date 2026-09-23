# Changelog

All notable changes to Velox will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed
- **BREAKING:** Schema validators (`NotEmpty`, `MaxLen`, `Range`, …) and enum validation are always generated; `FeatureValidator` is a deprecated no-op. Invalid values that were previously accepted now return a `ValidationError`
- Back-references on eager-loaded edges are only set with `FeatureBidiEdgeRefs` (Ent parity); fixes `json.Marshal` cycles on eager-loaded results
- `FeatureLock` is a deprecated no-op; `ForUpdate`/`ForShare` are always generated
- **BREAKING:** Schema- and mixin-level `Interceptors()` is a codegen error — it was assigned at init and never read. Register interceptors on the client instead
- **BREAKING:** `graphql.MapsTo`, `graphql.Mapping` and `graphql.Unbind` are rejected at codegen time instead of being silently ignored
- **BREAKING:** `runtime.CollectFields` takes the entity's `*CollectMeta`; regenerate after upgrading
- Generated `XxxSelect` holds its query as a named field instead of embedding `*XxxQuery` (−39% functions in the query package at 328 entities); code calling a promoted query method on a concrete `*XxxSelect` must go through `s.XxxQuery`
- `privacy.TenantQueryRule` is deprecated — it only checks presence and never filtered; use `TenantFilterRule`
- Generated `ForUpdate`/`ForShare` decide whether to drop `DISTINCT` via `dialect.CapLockWithDistinct` instead of a dialect-name comparison
- Generated `client/<entity>` imports carry an explicit alias, so goimports cannot delete them
- Faster code generation: textual import regrouping instead of re-parsing every file, a persistent `.velox/` loader cache that skips the relink on an unchanged schema, and GOGC=200 during generation
- Go 1.25 is the documented and CI-tested minimum, matching `go.mod`

### Added
- `graphql.InterfaceField(name)`: GraphQL interface fields over edges, with the interface, Go markers and resolvers generated (ported from ent/contrib #638, without the view-backed global connection)
- Package-level `sql.Union`, `UnionAll`, `Except`, `ExceptAll`, `Intersect`, `IntersectAll` with parenthesized branches; SQLite renders branches as derived tables so every dialect returns the same rows
- `privacy.TenantFilterRule(column)`: appends `WHERE <column> = <viewer tenant>` to reads, bulk UPDATE and bulk DELETE
- Generated `AGENTS.md` in the output directory describing the generated layout and what the API does not do
- `docs/observability.md` and the `contrib/otelvelox` module: OpenTelemetry tracing and metrics via `otelsql` + `sql.OpenDB`
- `examples/multitenant`: runnable multi-tenant isolation reference
- Public-API guard (`apiguard_test.go`) that fails the build on any change to the exported surface of the 8 consumer-facing packages

### Fixed
- Field validators now run on UPDATE, not only on CREATE — `UpdateOneID(id).SetTitle("")` used to write past `NotEmpty()` (a622611)
- `UpdateOne.Select(...)` no longer discards `SetXxx` values for columns outside the selection (c11f35c)
- Merging schema-level `Hooks()` into a builder's hooks could overwrite a hook in the shared hook store, silently dropping it for every later mutation (4f42d8e)
- `IDValidator` on a custom ID field is now called; it was generated and never run
- Fields with a custom `GoType` compile again now that validators are always generated: a GoType enum's validator no longer references a leaf enum type and an `IsValid()` that do not exist, a GoType scalar's validator is declared at the basic type and called as `Validator(string(v))` (Ent parity), and `String()` converts a string-kind GoType. Enum validators are generated functions (Ent parity) instead of init-assigned variables, so they can never be nil
- A `field.Bytes` validator was asserted as `func(any) error` at init; it is now `func([]byte) error`
- `sqlgraph.IsUniqueConstraintError` and friends no longer miss a SQLite constraint error wrapped by an application error that has its own `Code() int` (an HTTP status, say); the SQLite match now requires the `modernc.org/sqlite` error type
- `sql.WithVar` inside a transaction no longer leaks session variables to the pooled connection
- `sql.WithVar` on Postgres no longer leaks a setting whose dotted name contains an SQL keyword (`app.user`) to the pooled connection: the reset is now `set_config(name, NULL, false)` with the name bound as a parameter, where `RESET app.user` failed to parse
- `privacy.Not` no longer converts a fail-closed denial into `Allow`
- Privacy trace recording is safe for concurrent use
- `errors.Is(err, velox.ErrTxStarted)` now matches the error a generated client returns from a nested `Tx`/`BeginTx`; the generated `ErrTxStarted` aliases `velox.ErrTxStarted`
- `field.Sensitive()` now hides the value from JSON, `String()` and the GraphQL output type (it stays settable through mutation inputs)
- `Noder`/`Noders` no longer fail at random in schemas that mix ID types
- `graphql.QueryField` honors its name, description and directives
- `graphql.CollectedFor` now reaches the field collector instead of falling back to `SELECT *`
- `graphql.Type()` renaming an entity no longer generates uncompilable Go
- GraphQL SDL is always validated at codegen time; descriptions containing `"""` are escaped
- Typed-JSON GraphQL scalars are generated only for named Go types; unnamed struct/array JSON fields fall back to the generic scalar instead of breaking generation
- `Transactioner.InterceptResponse` no longer panics without a gqlgen operation context
- Concurrent `Schema.Create` calls no longer race on the shared migration tables
- The SQL driver no longer logs failing statements and their arguments to the default logger
- The loader's staleness check recognizes a link step on Windows
- The `velox` CLI (`cmd/velox`) is now in the repository; a `.gitignore` pattern had excluded it since the first commit

### Removed
- `contrib/graphql.FieldCollection`, an exported type nothing referenced
- **BREAKING (dead API):** `runtime.EdgeQuery`, `runtime.NewEdgeQuery` and the generated `query.NewXxxQueryFromEdge` constructors — nothing constructed an `EdgeQuery`
- **BREAKING (dead API):** `runtime.QueryBase`, `NewQueryBase`, `QueryAllSC`, `QueryCount`, `QueryExist`, `QueryIDsOnly`, `QueryFirstIDOnly`, `QueryOnlyIDOnly`, `ScanOnly`, `ScanMapRows`, `ScanConfig`
- **BREAKING (dead API):** `runtime.RegisterTypeInfo`, `FindRegisteredType`, `RegisteredTypeInfo`, `RegisterEntityClient`, `NewEntityClient`, `EntityClientFunc`, and the `TypeInfo`/`Client` fields of `runtime.EntityRegistration` — written at init, never read
- **BREAKING (dead API):** `velox.Cache`, `velox.CacheKey`
- **BREAKING (dead API):** `velox.QueryError`, `MutationError`, `PrivacyError`, `RollbackError`, `AggregateError` and their helpers, `NewNotFoundErrorWithID`, `NewNotSingularErrorWithCount`, `(*NotFoundError).ID`, `(*NotSingularError).Count` — never constructed
- **BREAKING (dead API):** `runtime.ErrTxStarted` — use `velox.ErrTxStarted`
- **BREAKING (dead API):** 11 unread `dialect.Cap*` flags; `CapForUpdate`, `CapForShare`, `CapLockWithDistinct` remain

## [0.2.1] - 2026-06-25

### Fixed
- Upsert `OnConflict().SetXxx()`/`Update()` no longer renders an empty `DO UPDATE SET` (syntax error near `RETURNING`)
- `UpdateOne` no longer returns a false `NotFound` for a no-op update on MySQL, which reports changed rows rather than matched rows

## [0.2.0] - 2026-06-15

Regenerate after upgrading: generated import paths moved.

### Changed
- **BREAKING:** Generated layout split into a leaf `{entity}/` package and a heavy `client/{entity}/` package; enums moved to the leaf package, and the GraphQL WhereInput package was renamed `gqlfilter` → `filter`
- **BREAKING:** The generated root-package constant `Sum` (module go.sum) is renamed `Checksum`; it collided with the aggregate `Sum` function
- GraphQL edge connections with `where` autobind to the entity method (`where *filter.XxxWhereInput`); user-written resolver stubs and `@goField(forceResolver: true)` are no longer needed
- `WithXxxPredicate` pagination options are folded into a typed `WithXxxFilter` (Ent parity)
- `privacy` no longer requires `intercept`; the two features are independent
- `ForUpdate`/`ForShare` are part of the generated querier interfaces; on SQLite they are a no-op
- Generated composite literals are `gofmt -s` canonical
- A regeneration that changes nothing rewrites no files; a schema change rewrites only the files whose bytes differ

### Added
- Multi-column cursor pagination for `graphql.MultiOrder()` entities, including as edge-connection targets
- `graphql.UnionMember(...)`: Go marker methods for gqlgen unions
- Query `Modifier` support for aggregate and projection queries
- After `Create`/`CreateBulk`, M2O owner edges carry an ID-only stub so `Edges.XxxOrErr()` works without a query
- Query factories register themselves; no manual registration import needed
- `ROADMAP.md`, `docs/architecture-overview.md`, and a troubleshooting section on generation/build caching

### Fixed
- `UpdateOneID(id).Where(...)` honors the chained predicate and returns `NotFound` when no row matches
- `sqlschema.OnDelete(...)` rendered invalid Go; it is now honored on either edge of a pair, and FK constraint names use the assoc edge, as in Ent
- JSON array append (`AppendXxx`) failed on Postgres (raw `?` placeholder) and mishandled JSON `null` columns on every dialect
- Backward (`before`) cursor pagination returned rows from the wrong side of the cursor, for both single- and multi-field orders; `TotalCount` is stable across pages
- Pagination degrades to an ID-only cursor when the order field is the ID
- `Skip(SkipType)` no longer suppresses the entity's WhereInput
- `where` on edge connections was silently dropped
- Partial-index `WHERE` clauses were lost in migrations
- `BuildSelectorFrom` qualifies selected columns, avoiding ambiguous-column errors in joins
- Versioned-migration output was silently lost to a concurrent write of `migrate/migrate.go`
- `internal/globalid.go` was rewritten on every generation with Snapshot + GlobalID enabled
- Go reserved keywords are accepted as field and edge names; SQL metacharacters in names are rejected at schema load

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

[Unreleased]: https://github.com/syssam/velox/compare/v0.2.1...HEAD
[0.2.1]: https://github.com/syssam/velox/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/syssam/velox/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/syssam/velox/releases/tag/v0.1.0
