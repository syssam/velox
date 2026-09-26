# Changelog

All notable changes to Velox will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

Regenerate after upgrading. Breaking changes are marked **BREAKING** below.

### Fixed
- `WithXxx()` panicked (`interface {} is *int, not int`) when any row's optional foreign key was NULL — e.g. `WithParent()` on any tree with a root
- A to-one edge set on create read back as a stub with only its ID filled in: `createPost { author { name } }` answered with an empty name. Create now leaves the edge unloaded, and the resolver queries it
- `SetXxxID` on a unique edge called twice (or overridden by a hook) kept a random one of the two IDs; the last call now wins
- A query-level traversal (`client.User.Query().QueryPosts()`) skipped the source's privacy policy and interceptors, so posts of denied or filtered users were returned
- A query-level traversal returned a target once per source reaching it — the author of two posts twice, `Only()` failed with NotSingular. Traversals now select targets with a semi-join (`WHERE id IN (…)`), so each is returned once with no `DISTINCT`, and ordering by an edge count (`ByPostsCount`) works on PostgreSQL and MySQL
- `Select()` and `GroupBy()` wrote field names into the SQL unchecked, so an expression passed as a field ran as SQL. Unknown fields now return a `ValidationError`
- Upsert `UpdateNewValues()` overwrote immutable fields (`created_at`) and a caller-supplied ID on conflict; both are now kept. An upsert on a UUID ID returns the stored row's ID, not the one generated for the row that was not inserted
- `OnConflict(...).DoNothing()` on a duplicate no longer writes the skipped row's edges, and returns the zero ID for a caller-generated key (as it already did for auto-increment keys)
- `AppendXxx` called twice on one builder kept only the second value
- A bulk create evaluated the mutation policy before applying defaults, so a rule saw an unset field where single-row `Save` saw its default
- `client.Mutate` with a hand-built `OpDeleteOne` mutation deleted every row in the table; it now deletes the row its ID names, errors without an ID, and returns NotFound when the row is absent
- `Count()` ignored `Limit`/`Offset`: `Offset(n).Count()` failed with "no rows in result set" and `Limit(n).Count()` counted every row. It now counts the rows the query returns (Ent has the same bug)
- A bulk create with `OnConflict(... DoNothing())` that skipped rows assigned the returned IDs by position, so rows got other rows' IDs and their edges were linked to the wrong rows, with no error (Ent has the same bug). Rows with edges now fail before anything is linked, with a hint (`sql.ResolveWithIgnore()`, or one-at-a-time creates on MySQL, which reports no per-row IDs); without edges, no row reports another row's ID
- Upsert `UpdateNewValues()` with `FeatureAutoDefault` reset fields the caller did not set: their zero value, filled only to satisfy NOT NULL on insert, was copied onto the conflicting row. Those columns are now left as stored
- MySQL upserts now match PostgreSQL and SQLite. `OnConflict(...).DoNothing()` on a duplicate skips the row's edges and reports the zero ID; MySQL's `LAST_INSERT_ID(id)` resolution named the existing row, and the edges were written to it. After a conflict on a non-numeric key (UUID), the ID is reported as zero (MySQL cannot return it) instead of the key generated for the row that was not inserted, and a create that also sets edges fails instead of linking them to that key. New `sql.ConflictDoesNothing(opts...)`
- GraphQL cursor pagination ordered by a NULL-able column reaches every row. A page boundary on a NULL value produced `col > NULL`, so the next page was empty with `hasNextPage` true (entgql does the same). Cursor predicates are NULL-aware and place NULLs where the dialect sorts them (new `dialect.CapNullsFirst`)
- GraphQL cursor pagination ordered by a time column on SQLite returned an empty or repeated page when the times were not in the server's local zone: the cursor decoded the time in the local zone, and SQLite compares times as text. Cursors now keep the value's offset and zone name; cursors issued before still decode
- GraphQL cursor pagination skipped rows after a page boundary whose order value was its type's zero value (`0`, `""`, `false`). The cursor's msgpack `omitempty` dropped the value, and the NULL-aware predicate then asked for `col IS NULL`, so the next page was empty or truncated on every dialect. Cursors issued before this fix with a zero order value still decode as NULL
- **Security:** a many-to-many edge predicate (`HasTags()`, `HasTagsWith(...)`) ran past a denying target policy: the join subquery dropped the policy's error, so reads returned rows and `Update()`/`Delete()` wrote them. `UpdateOne` with an edge predicate evaluated the target's policy without the request context, so it neither filtered nor denied. Both now behave like the other edge shapes and bulk writes
- `GroupBy(...).Aggregate(...)`, `Aggregate(...)` and `Order(...)` on an unknown column returned zeros or every column with a nil error; the builder's error is now returned
- An M2M edge declared `Through()` an edge schema now fills the join entity's defaults (e.g. `joined_at` = `time.Now`); `AddXxxIDs` failed with a NOT NULL error before
- Mutating an edge schema's generated edge (`AddMembershipIDs`, `ClearMemberships`) built SQL against the wrong table ("no such column")
- Edges bound to a field (`.Field("owner_id")`) now share the field's mutation state: `OwnerIDs()`, `AddedEdges()`, `ClearedEdges()` and `EdgeCleared()` reflect `SetOwnerID`/`ClearOwnerID`, and `ClearOwner()` / `ClearEdge("owner")` clear the column instead of doing nothing
- With `FeatureAutoDefault`, an unset `Optional()` field is no longer validated: its zero value made `Optional().Positive()` reject every create that omitted the field. Hooks now see such a field as unset; the zero is filled after validation

### Added
- `sql.QueryErr(q)` renders a statement and returns the error its builder recorded while being built or rendered. runtime and sqlgraph render through it (a guard test rejects a bare `Query()` there), so an unknown column, a failed subquery or a denied edge-predicate policy stops the statement instead of running it

### Security
- Edge predicates (`HasXxx()`, `HasXxxWith(...)`, and GraphQL `hasXxx` / `hasXxxWith` filters) now apply the target entity's privacy policy inside their subquery (new `runtime.ApplyEntityPolicy`). They read the target table unscoped, so a caller could learn whether rows it may not read exist; a policy that denies now fails the query, including bulk `Update().Where(...)` / `Delete().Where(...)`. Ent has the same gap
- Errors recorded inside a subquery (`EXISTS (…)`, `IN (…)`, `UNION` branches, `ExprFunc`) were dropped when the outer statement was rendered, so a failed subquery ran as if it had succeeded. They now reach the outer statement's `Err()`, and sqlgraph checks it after rendering on every read and write path
- The row `OldXxx` / `OldField` loads in a hook is read through the entity's query privacy policy, as Ent's loader is: a mutation policy that filters does not stop hooks from running, and they could be handed a row the viewer may not read. Interceptors are still not applied to that read (deliberate; Ent applies them)
- Selectors built for `All`, `Select`, `GroupBy` and eager loads now carry the request context, which edge predicates read (for the schema config under `FeatureSchemaConfig`, and for the target's policy)

### Changed
- **BREAKING:** A mutation policy is also evaluated after the hooks, before the SQL runs, on the mutation the hooks produced. A hook that set a value a rule rejects used to write it: the only check ran before the hooks. The pre-hook check stays, so a denied request still never reaches a hook. The second check runs only when hooks are registered — without them the mutation cannot change, so a write without hooks evaluates its rules once, as before. With hooks, rules run twice per write and must not have side effects; a `FilterFunc` appends its predicate twice, which is harmless
- **BREAKING:** The generic `ClearField(name)` accepts only Nillable fields, matching the typed `ClearXxx`. An Optional, non-Nillable field (NOT NULL in velox) was accepted, listed in `ClearedFields()`, and never cleared
- **BREAKING:** `AppendXxx` is generated only for JSON fields of slice type. On a struct or map field it stored a JSON array the entity could no longer decode, so every later read of the row failed
- **BREAKING:** `OldXxx` / `OldField` called after the UPDATE ran return an error wrapping the new `runtime.ErrOldValueAfterMutation` instead of silently returning the new value (Ent parity). Read old values in the hook before calling `next.Mutate`; a value read there stays available afterwards
- **BREAKING:** GraphQL `Noder` / `Noders` return an error wrapping the new `runtime.ErrAmbiguousNodeID` when an id matches rows in more than one entity type, instead of whichever resolver the registry map happened to try first (a different type from call to call). Relay IDs must be globally unique — enable `FeatureGlobalID`. Resolution goes through the new `runtime.ResolveNode`, which probes every type (one query each)
- **BREAKING:** A unique edge that was eager-loaded but has no target (NULL key) is now marked loaded, and `XxxOrErr()` returns a NotFound error for it (Ent parity). It used to report "not loaded", and GraphQL re-queried each such row. GraphQL resolvers still return `null`

## [0.3.0] - 2026-09-24

Regenerate after upgrading. Breaking changes are marked **BREAKING** below.

### Changed
- **BREAKING:** With the GraphQL extension, `CollectFields(ctx, satisfies ...string) (XxxQuerier, error)` is part of the generated `entity.XxxQuerier` interface, so list resolvers call `client.Xxx.Query().CollectFields(ctx)` without asserting `*query.XxxQuery`. The concrete method now returns `entity.XxxQuerier` (like the other chainers) instead of `*query.XxxQuery`
- Generated `Paginate` skips its `COUNT` query unless the GraphQL selection reads `totalCount` (Ent parity; `pageInfo` comes from the limit+1 row). Outside a GraphQL operation it keeps counting. New `gqlrelay.TotalCountSelected(ctx)`. Regenerate after upgrading
- **BREAKING:** `runtime.RegisterEntityPolicy` takes the address of the entity's policy variable (`*velox.Policy`) and `runtime.EntityPolicy` reads it at lookup. Regenerate after upgrading
- **BREAKING:** Schema validators (`NotEmpty`, `MaxLen`, `Range`, …) and enum validation are always generated; `FeatureValidator` is a deprecated no-op. Invalid values that were previously accepted now return a `ValidationError`
- Back-references on eager-loaded edges are only set with `FeatureBidiEdgeRefs` (Ent parity); fixes `json.Marshal` cycles on eager-loaded results
- `FeatureLock` is a deprecated no-op; `ForUpdate`/`ForShare` are always generated
- `FeatureModifier` is a deprecated no-op; `Modify` is always generated
- **BREAKING:** Schema- and mixin-level `Interceptors()` is a codegen error — it was assigned at init and never read. Register interceptors on the client instead
- **BREAKING:** `graphql.MapsTo`, `graphql.Mapping` and `graphql.Unbind` are rejected at codegen time instead of being silently ignored
- **BREAKING:** GraphQL field collection runs in applications. Generated `Paginate` collects from the connection's `edges.node` selection (Ent parity) and generated `CollectFields` calls `gqlrelay.CollectFields` directly; the runtime registry (`runtime.SetFieldCollector`, `runtime.FieldCollector`, `runtime.CollectFields`) and `graphql.RegisterFieldCollector` are removed. Regenerate after upgrading. Custom resolver fields without `graphql.CollectedFor` keep `SELECT *`
- **BREAKING:** `runtime.FieldCollectable.WithEdgeLoad` returns the edge query, and generated `WithEdgeLoad` honors its `LoadOption`s (they were discarded). `runtime.Limit` on a to-many edge limits rows **per parent** with a window function; `LoadConfig.Offset` is removed
- Generated `XxxSelect` holds its query as a named field instead of embedding `*XxxQuery` (−39% functions in the query package at 328 entities); code calling a promoted query method on a concrete `*XxxSelect` must go through `s.XxxQuery`
- `privacy.TenantQueryRule` is deprecated — it only checks presence and never filtered; use `TenantFilterRule`
- Generated `ForUpdate`/`ForShare` decide whether to drop `DISTINCT` via `dialect.CapLockWithDistinct` instead of a dialect-name comparison
- Generated `client/<entity>` imports carry an explicit alias, so goimports cannot delete them
- Faster code generation: textual import regrouping instead of re-parsing every file, a persistent `.velox/` loader cache that skips the relink on an unchanged schema, and GOGC=200 during generation
- Go 1.25 is the documented and CI-tested minimum, matching `go.mod`
- Generated many-to-many eager loaders are one `runtime.M2MLoad` call instead of a per-edge copy of the scan loop (join, pivot scan, dedup, per-parent limit, policy, interceptors, nested eager loads, config injection); the per-parent limit of every to-many loader is planned by `runtime.PlanPerParentLimit`. `examples/fullgql`'s query package shrinks from 11,936 to 11,251 lines with no change in compile time. Regenerate after upgrading

### Added
- Version-aware dialect capabilities: `dialect.CapWindowFunctions` (static on PostgreSQL and SQLite, granted on MySQL 8.0+ / MariaDB 10.2+ only), `dialect.VersionCapabilities(dialect, version)`, the `dialect.CapabilityProber` interface and `dialect.DriverCapabilities(ctx, drv)`, which looks through `DebugDriver` and transactional drivers. `sql.Driver.ServerCapabilities` runs `SELECT VERSION()` once per MySQL driver and caches the answer; other dialects never query
- `sql.Selector.LimitPerPartition(partition, n)`: keeps n rows per partition with `ROW_NUMBER() OVER (PARTITION BY …)`, ranked by the selector's order; `runtime.NewLoadConfig`
- `docs/dataloader.md` § Field Collection: what the collector projects and eager-loads, and when a connection falls back to per-row pagination
- Dead-API guard (`deadapi_test.go`): fails when a `gen.Feature` is consulted by no generator, a `graphql.Annotation` field is read by nothing (an accessor counts only if it has a caller), a runtime registry is written but never read, or an exported `runtime` identifier is unreachable from generated code and every other package; see CONTRIBUTING.md § Dead-API Guard
- `graphql.InterfaceField(name)`: GraphQL interface fields over edges, with the interface, Go markers and resolvers generated (ported from ent/contrib #638, without the view-backed global connection)
- Package-level `sql.Union`, `UnionAll`, `Except`, `ExceptAll`, `Intersect`, `IntersectAll` with parenthesized branches; SQLite renders branches as derived tables so every dialect returns the same rows
- `privacy.TenantFilterRule(column)`: appends `WHERE <column> = <viewer tenant>` to reads, bulk UPDATE and bulk DELETE
- Generated `AGENTS.md` in the output directory describing the generated layout and what the API does not do
- `docs/observability.md` and the `contrib/otelvelox` module: OpenTelemetry tracing and metrics via `otelsql` + `sql.OpenDB`
- `examples/multitenant`: runnable multi-tenant isolation reference
- Public-API guard (`apiguard_test.go`) that fails the build on any change to the exported surface of the 8 consumer-facing packages

### Fixed
- A many-to-many eager load with a per-parent limit and an order (`WithEdgeLoad("tags", runtime.OrderBy(...), runtime.Limit(n))`) keeps each parent's rows in that order. A target shared by several parents was assigned in the order it was first read, and the window returns rows ranked across all parents, so a parent's targets could come back out of order When a target interceptor reorders or replaces the loaded targets, the eager load follows the interceptor's order, as the direct edge query and Ent do
- The MySQL server-version probe never holds its lock across the query, and a caller inside a transaction probes on its own connection instead of waiting for an in-flight pool probe — on a pool fully held by transactions, that wait was a deadlock
- **BREAKING:** a per-parent eager-load limit (`runtime.Limit` through `WithEdgeLoad`) combined with `Limit`/`Offset` on the same edge query now returns an error. The window path applied that Limit after ranking and the MySQL 5.7 fallback before it, so the two returned different rows
- The MySQL server-version probe behind per-parent eager-load limits runs on the caller's open transaction when there is one; probing through a second pooled connection hung forever on a pool of one held by the transaction (`dialect.CapabilityProberVia`, `(*sql.Driver).ServerCapabilitiesVia`)
- Per-parent eager-load limits (`runtime.Limit` through `WithEdgeLoad`, and the GraphQL collector's limit for nested `first:` connections) work on MySQL 5.7 and MariaDB < 10.2. They rendered `ROW_NUMBER() OVER`, which those servers reject with `Error 1064`; the loader now asks the driver (`dialect.DriverCapabilities`) and, without window functions, reads the edge in its ranking order and keeps each parent's first n rows in memory — the same rows as the window path
- GraphQL field collection: an interface field (`graphql.InterfaceField`) and a direct selection of the same edge share one eager-loaded query, and its projection now covers both. It was narrowed to the direct selection, so the interface resolver returned the other fields as zero values (`principal { ... on Workspace { description } }` next to `workspace { name }` gave `null`); such an edge is also no longer limited per parent
- Re-running a query (or a clone of it) with eager-loaded edges loads the same edges: loaders narrowed the stored child query in place, stacking a second `IN` over parent keys (dropping parents that appeared between runs) and a second per-parent limit
- Edges eager-loaded under a many-to-many edge (`WithTags(func(q){ q.WithPosts() })`, `WithEdgeLoad("tags", runtime.WithEdge("posts"))`) are loaded; the M2M loader never ran the target query's loaders
- A projected query that eager-loads a to-one edge bound to a declared foreign-key field (`.Field("owner_id")`) keeps that column, so the edge is no longer silently nil (Ent parity)
- `LT`/`LTE`/`GT`/`GTE` predicates and SQLite's `ESCAPE` clause wrote into the predicate instead of the builder, so a `Clone()`d selector rendered `WHERE "a"$1` (same bug in Ent)
- `Selector.OrderExprFunc` / `DialectBuilder.Expr` keep the arguments their callback binds; they rendered it to a bare string, dropping every argument (same bug in Ent). `WindowBuilder.Query` no longer appends to itself on every call
- `LimitPerPartition` moves an existing `LIMIT`/`OFFSET` to the outer query, where it caps rows across partitions; on the ranked inner query it cut rows before ranking
- Eager loads and edge queries read the target entity's `RuntimePolicy` variable when they run, as its client does; they read a copy taken at init, so replacing the variable did not reach them
- `graphql.Mutations(graphql.MutationCreate().Description(...))` puts the description on the generated `Create…Input` (likewise for update); it was stored in `Annotation.MutationInputs` and read by nothing
- A connection edge method served from an eager-loaded edge (`WithXxx()`) now returns what the database path returns. The fast path ignored `orderBy`, returned `last` pages in reverse, and reported the page length as `totalCount`. It now runs only without `orderBy`, sorts the loaded rows by ID and counts the whole edge; edges whose target has a string ID always query, since their order depends on the column collation
- Field validators now run on UPDATE, not only on CREATE — `UpdateOneID(id).SetTitle("")` used to write past `NotEmpty()` (a622611)
- `UpdateOne.Select(...)` no longer discards `SetXxx` values for columns outside the selection (c11f35c)
- Merging schema-level `Hooks()` into a builder's hooks could overwrite a hook in the shared hook store, silently dropping it for every later mutation (4f42d8e)
- `IDValidator` on a custom ID field is now called; it was generated and never run
- Fields with a custom `GoType` compile again now that validators are always generated: a GoType enum's validator no longer references a leaf enum type and an `IsValid()` that do not exist, a GoType scalar's validator is declared at the basic type and called as `Validator(string(v))` (Ent parity), and `String()` converts a string-kind GoType. Enum validators are generated functions (Ent parity) instead of init-assigned variables, so they can never be nil
- A `field.Bytes` validator was asserted as `func(any) error` at init; it is now `func([]byte) error`
- `sqlgraph.IsUniqueConstraintError` and friends no longer miss a SQLite constraint error wrapped by an application error that has its own `Code() int` (an HTTP status, say); the SQLite match now requires the `modernc.org/sqlite` error type
- `sql.WithVar` inside a transaction no longer leaks session variables to the pooled connection
- `sql.WithVar` no longer reports a failed `set_config`/`SET @var` twice: the variable's reset was queued before the set and re-ran during cleanup
- `sql.WithVar` on Postgres no longer leaks a setting whose dotted name contains an SQL keyword (`app.user`) to the pooled connection: the reset is now `set_config(name, NULL, false)` with the name bound as a parameter, where `RESET app.user` failed to parse
- `privacy.Not` no longer converts a fail-closed denial into `Allow`
- `docs/privacy.md` § Combining Rules: `Not(HasRole("guest"))` was described as "anyone except guests", but it returns `Skip`, not `Allow`, for non-guests; the section now explains the three-valued combinators and replaces an `And(HasRole, TenantRule)` example that did not compile
- Privacy trace recording is safe for concurrent use
- `errors.Is(err, velox.ErrTxStarted)` now matches the error a generated client returns from a nested `Tx`/`BeginTx`; the generated `ErrTxStarted` aliases `velox.ErrTxStarted`
- `field.Sensitive()` now hides the value from JSON, `String()` and the GraphQL output type (it stays settable through mutation inputs)
- `Noder`/`Noders` no longer fail at random in schemas that mix ID types
- `graphql.QueryField` honors its name, description and directives
- GraphQL field collection was inactive in applications: the collector was registered only in the codegen process and no generated resolver called it, so projection, `graphql.CollectedFor` and eager loading of nested edges never happened and nested edges resolved per row (N+1). A `users → todos → owner` query now costs 4 queries for any number of users (32 before at 6 users); nested `first: n` connections are limited per parent in one query
- Eager-loaded edge queries (`WithXxx`, `WithNamedXxx`, `WithEdgeLoad`) and query-level `QueryXxx` traversals now carry the target entity's privacy policy; they ran without it, so an eager load returned rows the target's `Policy()` hid from `entity.QueryXxx()`
- The generated GraphQL collection metadata listed an O2M edge's foreign key and an M2M edge's join-table columns as columns of the parent table
- `graphql.Type()` renaming an entity no longer generates uncompilable Go
- GraphQL SDL is always validated at codegen time; descriptions containing `"""` are escaped
- Typed-JSON GraphQL scalars are generated only for named Go types; unnamed struct/array JSON fields fall back to the generic scalar instead of breaking generation
- `Transactioner.InterceptResponse` no longer panics without a gqlgen operation context
- Concurrent `Schema.Create` calls no longer race on the shared migration tables
- The SQL driver no longer logs failing statements and their arguments to the default logger
- The loader's staleness check recognizes a link step on Windows
- The `velox` CLI (`cmd/velox`) is now in the repository; a `.gitignore` pattern had excluded it since the first commit
- `benchmarks/run.sh scale` runs again: the `stress-{100,200,328}` fixture modules lagged the root module's `golang.org/x/*` versions, so `go run generate.go` stopped with `updates to go.mod needed`

### Removed
- `contrib/graphql.FieldCollection`, an exported type nothing referenced
- The generated `loadTotal` field on every query (declared, cloned and ranged over, never populated) and the `withFKs` field on queries of entities without foreign-key columns (could never be set). The dead-API guard's new rule (e) fails on a generated struct field written only by `clone()`
- **BREAKING (dead API):** `runtime.EdgeQuery`, `runtime.NewEdgeQuery` and the generated `query.NewXxxQueryFromEdge` constructors — nothing constructed an `EdgeQuery`
- **BREAKING (dead API):** `runtime.QueryBase`, `NewQueryBase`, `QueryAllSC`, `QueryCount`, `QueryExist`, `QueryIDsOnly`, `QueryFirstIDOnly`, `QueryOnlyIDOnly`, `ScanOnly`, `ScanMapRows`, `ScanConfig`
- **BREAKING (dead API):** `runtime.RegisterTypeInfo`, `FindRegisteredType`, `RegisteredTypeInfo`, `RegisterEntityClient`, `NewEntityClient`, `EntityClientFunc`, and the `TypeInfo`/`Client` fields of `runtime.EntityRegistration` — written at init, never read
- **BREAKING (dead API):** `velox.Cache`, `velox.CacheKey`
- **BREAKING (dead API):** `velox.QueryError`, `MutationError`, `PrivacyError`, `RollbackError`, `AggregateError` and their helpers, `NewNotFoundErrorWithID`, `NewNotSingularErrorWithCount`, `(*NotFoundError).ID`, `(*NotSingularError).Count` — never constructed
- **BREAKING (dead API):** `runtime.ErrTxStarted` — use `velox.ErrTxStarted`
- **BREAKING (dead API):** `runtime.ErrNotFound`, `ErrNotSingular`, `NewValidationError`, `InterceptFunc`, `TraverseFunc` — aliases nothing used; use the `velox` root package's
- **BREAKING (dead API):** `runtime.WithDriverContext`/`DriverFromContext` (nothing read the driver back out of the context), `RegisteredTypeNames`, `EdgeLoad`, and the `Where`/`Offset` load options
- **BREAKING (dead API):** 11 unread `dialect.Cap*` flags; `CapForUpdate`, `CapForShare`, `CapLockWithDistinct` remain

### Performance
- `Count()` (and the `totalCount` of `Paginate`) renders `COUNT(*)` instead of `COUNT(<id>)` when the selector has no JOIN, no DISTINCT and no selected columns — the id is a NOT NULL primary key there, so the result is identical. On SQLite a 10k-row unfiltered count drops from ~900µs to ~16µs (`BenchmarkCountNodes_SQLite`); Postgres runs `count(*)` ~25% faster server-side. Counts over a JOIN (M2M/M2O traversals, order-by-neighbor terms, a predicate or modifier that joins) keep `COUNT(<id>)`. Ent emits `COUNT(<id>)` everywhere; this is a deliberate deviation
- `gqlrelay.PageLoaded` (the eager-loaded edge-connection fast path) copies the edge once at its exact size and skips the sort when it is already in ID order: 1 allocation per call instead of 8–11 (`BenchmarkPageLoaded`)

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

[Unreleased]: https://github.com/syssam/velox/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/syssam/velox/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/syssam/velox/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/syssam/velox/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/syssam/velox/releases/tag/v0.1.0
