# Core 1 — follow-ups

Findings from the reviews of Core 1 and of the plans after it that were deliberately left for later. Items already fixed are not listed; the schema DSL plans (1 to 4) closed the old struct-first path's items, which are gone from this file.

## Still open from before core plan 3

- Resolved in API plan 5 (`ESCAPE '!'` on every dialect; `Like`'s doc says `!` escapes; `TestLikeIgnoresServerSettings` runs MySQL with `NO_BACKSLASH_ESCAPES` and PostgreSQL with `standard_conforming_strings` off). The LIKE escape depended on server settings: MySQL's `ESCAPE '\\'` assumes `NO_BACKSLASH_ESCAPES` is off, and PostgreSQL's `ESCAPE '\'` assumes `standard_conforming_strings` is on. A non-backslash escape character (e.g. `!`) removes both dependencies.
- Resolved in API plan 5 (`TestRender` holds all three dialects for every case, and `TestNewClausesOnEveryDialect`). Golden SQL for MySQL and SQLite on the read-path render cases that still assert PostgreSQL only (writes cover all three since core plan 3), an OR-between-siblings case, and the NEQ/GTE/LT/LTE operators.

## Left by core plan 4 (relations)

- A rewrite hook that points a statement at another table (tenancy, sharding) does not redirect the tables inside `EXISTS` subqueries; relation loads do go through the hooks.
- Resolved in API plan 5 (`One` checks its row count before it loads relations; its results do not change). `One` loaded relations for the two rows it reads before returning `ErrNotSingular`.
- A has-one relation with several matching rows loads the first in the relation's order.
- On MySQL, an `UPDATE` or `DELETE` whose `EXISTS` reads the table being changed fails with error 1093 (documented on `Has`).
- A `time.Time` cursor made in memory, finer than the column's precision, can skip or repeat a row with `After` (documented: `x` must be a row a query returned).

## Left by core plan 5 (scale guards)

- `bench/scale`'s time thresholds are 4x the largest of three runs on an M3 Max (sizes and memory 1.5x, re-baselined on 2026-10-19). Recalibrate them from the first GitHub run: set each to a margin over the figures that run reports.
- The object-size guard's large rows (`entityDescUse` and similar) carry a fixed per-package cost, so a 2x threshold on them tolerates a bigger absolute growth than on the small rows.
- The spec's "98 lines per entity" (the generated code's size, §2) was not re-measured for the schema DSL's generated `Desc` literals.
- `TestRewriteCanReplaceTheEntity` can only catch a regression when the allocator reuses an address, and the bypass that renders uncached when a hook replaced the entity has no test pinning that the cache stays flat. The hook-replaced-entity cache check was not mutation-tested.
- `TestCacheKeepsShapesApart` does not pin limit/offset presence, order or nesting as separate shapes (`FuzzRender` compares the cached path with the uncached one, which covers them only for the inputs it generates).
- Resolved in API plan 5 (`Sum`, `Avg`, `Min` and `Max` take a `NumberCol`; a `NullNumberCol` goes through `Raw` until one is asked for). `Sum` and `Avg` on `NumberCol` awaited aggregates (sub-project 2 or later).

## Sub-project 2 (guardrails and policy)

- Resolved in policy plan 1: hook misuse hardening. `WithRewrite(nil)` and `WithAround(nil)` panic when called (in `Open`'s argument list); a rewrite hook cannot set `st.Entity` to nil or swap it for an entity that is not a copy of the original (so a replaced `EntityInfo` whose columns differ from the scan plan is refused); a zero `Predicate{}` is refused.
- Resolved in policy plan 1: the hook-error convention. A `Rewrite` hook's error is returned as `velox: <Entity>: rewrite: …`; an `Around` hook's error is returned unchanged, because it normally wraps `next`'s error, which already carries the prefix (policy design §6). Sentinels are bare phrases wrapped with `%w`, so the prefix is never doubled.
- Resolved in policy plan 1: the one-level copy. A predicate holds its own copy of `In`'s values, so neither the caller nor a hook can change a built query's arguments through the slice (`TestInCopiesItsValues`).
- Resolved in policy plan 1: `Statement.Count`'s contract is documented (velox clears `Columns`, `OrderBy`, `Limit` and `Offset`), and an empty `Columns` list is refused instead of rendering `SELECT  FROM`.

## Sub-project 3 (observability)

- Resolved in observability: `Around`'s doc says that `Iter`'s loop body runs inside the hooks, so a hook must not recover a panic and return normally.

## CI

- The GitHub workflow has not run on GitHub yet; the first push is its first run.
- Workflow hardening: it runs twice for a branch with a pull request (`on: [push, pull_request]`), has no `permissions: contents: read`, does not take the Go version from `go.mod`, and its gofmt step does not list the files.
- `postgres:16` and `mysql:8.4` are floating tags; pin digests if CI and local runs drift. `ci-local.sh` does not check up front that `docker` exists.

## Small

- `snake` works on ASCII only; its doc comment does not say so. `EqualFold`'s `LOWER` is ASCII-only on SQLite.
- `Like` takes `string` while `HasPrefix`, `HasSuffix` and `Contains` take `V`.
- `pred` has no default case (an unknown op renders nothing and fails at the database); a zero `Dialect` renders SQLite-style.
- A package-level helper named `flag` in the root package invites a collision.
- `Count` calls `r.Err()` while rows are still open after the first row; `export_test`'s `assign` panics on a wrongly typed fake value.

## Left by core plan 2 (generator)

- `velox-gen verify`'s temporary directory survives SIGINT/SIGKILL: there is no signal handling and no sweep of stale `.velox-verify-*` directories. Applications should ignore `.velox-verify-*/` in git (said in velox-gen's package comment).
- velox-gen writes and deletes its files one at a time; an error part-way leaves the files already changed (it prints each path as it goes).
- Naming edge cases of `internal/gen/names.go`: the plural of names ending in an uppercase S or X ("DNSs"), a digit before y, and lowerCamel of "IDs" ("iDs").
- verify's messages start `velox-gen verify: `, and a compile failure of its program is not explained beyond the compiler's output.

## Left by core plan 3 (writes)

- Detached rollbacks have no timeout, and RetrySerializable retries immediately, with no backoff and no context check between attempts.
- A rollback or savepoint error after the statement ran: sqlExecutor.exec returns an error when RowsAffected fails although the statement ran; begin on a transaction executor would dereference nil (unreachable: nested Tx uses savepoints).
- Error classification: MySQL 1364 (NOT NULL without default, strict mode) and MariaDB's 4025 are not classified; a typed-nil driver error would panic; SQLite's NOT NULL names "table.column" while PostgreSQL and MySQL name the column.
- Static slice or map Defaults are shared by every inserted entity (pointer defaults are copied).
- Resolved in policy plan 1: an UPDATE or DELETE with neither Where nor All returns `ErrNoWhere`.
- InsertAll batches of many distinct sizes each add a render-cache shape.
- Test gaps: value semantics of UpdateQuery/DeleteQuery extended twice; Save on a composite key; a named numeric type (type Cents int64) under the Default rule; a uint64 key read back through RETURNING; a look-alike driver error type from another package; cache keys that differ only in Conflict/Update/Returning/Set columns; schema-qualified INSERT and UPDATE golden SQL; that ErrTxDone/ErrTxClosed from rollback is not joined.
- CI tests MySQL 8.4 only, while the spec supports 8.0.19 and later.

## Left by policy plan 1

- A statement-level timeout set by velox replaces the original error: the caller sees `ErrTimeout`, not the driver's error. The transaction-level timeout joins both.
- `SAVEPOINT`, `RELEASE` and `ROLLBACK TO` run on the caller's context, so the transaction's deadline does not bound them.
- A `Rewrite` hook that returns a velox-prefixed error reads `velox: X: rewrite: velox: Y: …`: the prefix doubles.
- The zero Predicate is recognized by the literal op `0` in guard.go's `hasZero`, which the guard and db.go's `checkColumns` use; there is no named constant.
- In `checkColumns`' walk, the "the entity has N columns" branch for an EXISTS's owner column is now effectively unreachable: `HasWhere` resolves the link, and a hook can neither build an opExists node nor replace the Entity with one of other columns.
- Test gaps: no test of `MaxTx` capping a larger `Timeout`, or of `MaxTx` alone; no test of `MaxRows` across nested `With` loads; no test of a `MaxRows` that a `Rewrite` hook sets.

## Left by policy plan 2

- PostgreSQL with a schema-qualified table in `DO UPDATE … WHERE` is verified only on SQLite.
- Errors inside nested `EXISTS`: `existsError` names the statement's entity and the innermost target only, dropping the middle of the path.
- `Mutation`'s `Set` clones the assignments for every replacement; `ConflictClause`'s unexported policy flag lives on an exported type.
- `scoper.node`'s walk is O(nodes) per statement.
- Test gaps: `Op.String`'s "unknown"; the Review Focus 1 test of policy 2's Task 2 runs its base query on a shared DB without per-run argument checks; the count case with only an `EXISTS` does not assert its SQL; policy tests use package-level counters; the order of a Delete's predicates is not asserted; the hidden-row check through `run` (with hooks); the post-hook guard backstop; `hooks_internal_test`'s text-compare case and its `Errs` case assert only `err != nil`; `TestOverWaitsForAPassedDeadline` lacks block and early-cancel cases; the split `InsertAll` `SQL(ctx)` test does not compare arguments; `FuzzScope`'s input-tree check compares only up to the shorter length and its "copy" is a rebuild; `ExistsOn` makes an unchecked type assertion; the CI fuzz regex is looser than `^FuzzScope$`.
- The itest seed has no cross-org parent, so `With(Parent)`'s tenant condition on the load is not exercised against a row it must hide.
- The upsert's update branch is asserted on the real databases only through `done`.
- An `InsertAll` runs in a transaction when velox predicts, before the Rewrite hooks, that its upsert will carry a policy condition. A hook that makes it conditioned (adds `Conflict`, clears `DoNothing`, fills `Update`) defeats the prediction: a hidden existing row then returns the `*PolicyError` after the visible rows are written. No row crosses tenants. Backstop: refuse a policy-conditioned statement with more than one row and `!InTx` in `scopeInsert`.
- A Rewrite hook that drops an insert's RETURNING skips the hidden-row check: the upsert then reports less than it should, but writes nothing across tenants.
- A Read policy's own parameters are not counted when `With` loads are chunked under the parameter limit; a chunk that goes over fails closed with the parameter-limit error.
- Doc nits: `run`'s doc and `Exec`'s doc exceed the line width; `node`'s doc wrap; in `TestUpsertPolicyAndCache`, the comment "A policy that denies denies the upsert, as an update" is misleading: the Insert policy denies first.

## Left by observability

API rough spots found while building `./example`:
- `WithTenant` needs the tenant column's exact Go type: on an `int64` column, `WithTenant(ctx, 1)` (an untyped constant, so an `int`) fails at run time with `ErrTenantMismatch`; nothing catches it at compile time.
- `InsertAll` reads no generated keys back, so code that inserts parents and then their children must read the parents' IDs again.
- Resolved in API plan 5 but for `GROUP BY` (`Sum`, `Avg`, `Min` and `Max`; the tour sums the paid invoices with `velox.Sum`). `GROUP BY` stays `Raw`, as the tour's per-tenant totals show. There was no `Sum`, `Avg` or `GROUP BY`: the example totalled revenue per organization in Go, over `Iter`.
- Resolved in API plan 1: a policy says `velox.Allow[E]()` for no restriction, and a zero `Pred[E]{}` is an error naming the entity. A policy that only denies still returns a `Pred` beside its error, which is ignored.
- The core package imports pgx, so an application that uses only SQLite or MySQL still links pgx.
- On SQLite, `ConstraintError.Constraint` holds the columns (`invoices.org_id, invoices.number`), not the constraint's name.
- Resolved in API plan 1: `Tx` passes its context to `fn`, a statement on another `*DB` under it fails with `ErrOutsideTx`, and `TxInfo.Context` is gone.

API rough spots found while splitting `./example` into one directory per topic:
- A policy function gets no `*DB`, so an inherited policy cannot check on insert that the parent is visible: `example/policy`'s `Comment` inherits `Doc`'s `Read` through `Has`, which covers reads only.
- A `Write` policy filters rather than denies, so a forbidden update returns 0 rows, not an error.
- Resolved in API plan 5 (`Mutation.Value` is removed; a column handle's `ValueIn(m)` returns the handle's value type, `*V` for a nullable one). `Mutation.Value` returned `any`, so an update hook type-asserted the value of a column it named through a typed handle.
- `RetrySerializable` cannot be shown retrying on single-connection SQLite: `example/tx`'s retry runs once.

Left by the observability plan:
- §6's 300 ns is read from benchmarks and recorded, not asserted.
- [x] Resolved in API plan 4 (`error.type` is the driver's code where it would be `_OTHER`: `40001`, `40P01`, a MySQL error number). A serialization failure or deadlock that `RetrySerializable` retries has `error.type` `_OTHER`; there is no value of its own.
- A statement refused before execution (guard, scope, policy, hooks) makes no statement span and no metric point; it shows only as the error of a transaction it ends.
- Inside a transaction, a statement span's parent is always `velox.tx`, even when the application started a span of its own inside `fn` and passed its context (a spec choice).
- A statement with no application frame within 64 frames (an `Iter` driven through `iter.Pull`, say) has a zero `Caller`.
- The `TxInfo` allocated per transaction when hooks are configured has no budget of its own; `TestTxAllocationBudget` pins only that a DB with nothing configured makes none.
- `contrib/veloxotel`'s `go.mod` requires velox2 at a placeholder version, with a `replace` to the repository root, until velox tags releases.
- Test gaps left by the task reviews:
  - fingerprints: no test asserts that the render cache's byte bound counts the fingerprint's bytes;
  - call sites: no concurrent test of the program-counter cache at the cache's bound;
  - results: no test of `Affected` on a failed `INSERT … RETURNING`; the no-pairs `WithTags` test's identity check is vacuous;
  - comments: no concurrent-eviction test; the cache's byte accounting is not checked exactly;
  - transactions: no test of `RetrySerializable` exhausted with a `TxAround` hook.
- Doc and cost nits:
  - `render` stays in non-test code only for the tests.
  - `contrib/veloxotel`'s `record` allocates its attribute set even under a no-op `MeterProvider`; an instrument-enabled check would skip it.

## Left by schema plan 1

- Resolved in migrate plans 1 and 2: MySQL refuses `Index.Where` at plan time, and SQLite keeps it (plan 1); `Drift` compares each dialect's printed predicate with the snapshot's through the predicate normaliser, SQLite's included, and `TestPredicatesPostgres` pins it against a real PostgreSQL (plan 2, Tasks 5–8). `Index.Where` was PostgreSQL-only, and nothing refused it on other dialects.
- [x] Resolved in migrate plan 1 for the symbols (`Tables` names foreign keys by `edge.Symbol` and `edge.Symbols`, checked by rule 51); `edge.Immutable` stays as it is. `edge.Symbol` and `edge.Immutable` are carried into the Graph's `Edge` (`symbols`, `immutable`); the migration planner is the first to use the symbols. Rule 34 refuses an edge whose immutability differs from its bound field's, as ent does.
- Irregular plurals need `schema.Table`.
- `singular()` is the simple inverse of plural (`children` gives `children_id`, where ent says `child_id`). A self-M2M edge named after its own type (edge `nodes` on `Node`) gives a join table with two `node_id` columns; rule 35 refuses it and asks for `edge.Columns`.
- Rule 31 is stricter than ent: `edge.Column` with a bound field whose column differs is refused (ent lets the field adopt the edge's column).
- `desc.GoTypeOf` does not collect imports from generic type arguments (`pkg.Box[time.Time]`) or from unnamed func, struct and interface types; the generator must not rely on `Imports` for those.
- Rule 12 accepts a bound foreign key whose `GoType` differs from the ID's for int and string kinds; generated code may need conversions.
- `velox-gen` finds only the types that embed `schema.Schema` directly. A type that embeds another struct which embeds `schema.Schema` is not found, and is not loaded.
- velox2 names a default index `<table>_<cols>` (`users_org_id_name`), where ent uses `<lower(type)>_<cols>` (`user_org_id_name`). Against a database ent created, the migration planner will see each such index as a rename.
- velox2's default ID is `int64`, and ent's is `int`. A ported edge field declared `field.Int("x_id")` must become `field.Int64("x_id")`, or the schema must declare its ID with `field.Int("id")`; rule 12's error says so.

## Left by schema plan 2

- A join entity (`EdgeThrough`) can have neither a tenant column nor a `Read` policy: the relation is refused when it resolves, since `Has` would not apply them and loading would. Its `Insert` and `Write` policies are allowed. Join rows are therefore not tenant-scoped, and velox does not check a link's two endpoints: an application that writes links checks both, using the join entity's `Policy()` (`Insert`, and `Write` for updates and deletes) or a hook. Loading applies the target's tenant and `Read` policy only.
- `InsertAll` reads no generated keys back, so after `next` the rows of an `InsertAll` that a hook's `Rows()` returns have no generated IDs.
- A hook can change the rows of an insert, but not how many there are: `Rows()` returns the rows, not the slice that holds them.
- `ValidationError` names the Go field, while `ErrImmutable` names the column.

## Left by schema plan 3

- Resolved in API plan 1 (2026-10-20): `Optional` no longer means a pointer. Nullability is old velox's (API cleanup spec §3.1): `Optional` is not required on create and stays NOT NULL with a value field; `Nillable` is the pointer and the NULL-able column; an `Optional` JSON column is NULL-able. A ported ent schema's `Optional()` fields need no change, except that an `Optional` enum, bytes or time field needs a `Default` or `Nillable`.
- Join entity naming: each many-to-many join table becomes an entity named `pascal(singular(table))` (`user_groups` gives `UserGroup`), which can collide with a schema of that name; the generator refuses the collision, and the only remedy is a different join table name (`edge.Table`).
- The test model is generated from `internal/testmodel/schema`. A field's Go name comes from `pascal` of its name, so `SKU` is declared `field.String("SKU").StorageKey("sku")`: the name `sku` would give `Sku`. The named types `Status`, `Cents` and `Score` live in `internal/testmodel/kind` because the schema package names them in `GoType` and the generated package imports them; `testmodel` re-exports them as aliases.

## Left by schema plan 4

- The link peak grows with N, though a service's binary does not: the linker reads the whole `gen` package's object file. It was 185 MB at N=100 and 279 MB at N=500 (about 0.2 MB per entity). This is inherent to the choice of one generated package (core spec, A.6, note of 2026-10-19); splitting `gen` by domain is out of scope until after 1.0.
- A relation's `Rel` handle is checked at every use, not at compile time: a hand-edited `Rel[User, Comment]` over a `Many[Post]` field compiles, and fails with a `*SchemaError` each time a statement uses it, whichever handle used the relation first (and in `velox-gen verify`, which calls `CheckRelation` for every generated handle).

## Left by routing

- Read-only transactions on replicas: `db.Tx(ctx, fn, ReadOnly())` runs on the primary, like every transaction.
- `TxInfo` has no shard, so `contrib/veloxotel`'s transaction spans carry no `velox.route.shard`; their statements' spans do.
- The session pin covers every shard and every DB the context is used with, not one shard: it errs toward the primary.
- A savepoint (nested `Tx`) does not check its context's shard; its statements do.
- An Around hook that replaces `*st` after `next` unpins the query's `With` loads, which run where `st.ran` says.
- `pinnedTo` allocates a DB copy per `All` with loads on a routed DB.
- The failover test's socket path (`refusedConnectError`) follows `TMPDIR`, which a socket path's length limit can break.
- Shard resolution is written twice in `route.go`.
- No sharded test of `Iter`, `Count` or `Exists`; they share `route` with `All` and `One`.
- The itest's 100/150 ms pin timing, and the pgx terminate test's assumptions (the connection is idle under a second; `pg_terminate_backend` has taken effect by the next read), can flake on a slow runner.
- The MySQL driver logs "closing bad idle connection" under `-v` in the failover itests.
- An empty database name in `VELOX_TEST_POSTGRES` fails late, with a connection error rather than a message naming the variable.
- A retried span after a failover keeps `velox.route.fallback=true`.
- The example's 50/150 ms pin timing flakes only on a stall of more than 50 ms between a write and the next read; widen it if that is ever seen.
- An ambiguous write (a timeout after commit, a scan error on `RETURNING`) does not pin its session, though it may have committed: a stale read within the window is possible. Spec §3 pins on a write that succeeded.
- The itest's CI guard keys on the generic `CI=true`, which a contributor's extra job without databases would trip, loudly. A velox-specific variable would be safer.

## Left by GraphQL plan 1

- Resolved in API plan 2: a plugin warns through the response's `warnings`.
- Resolved in API plan 2: `-plugin name:key=value,...`.
- The extension allowlist (`.go`, `.graphqls`) needed `.proto` for the gRPC plugin. Resolved in API plan 2: `.proto` is allowed.
- An entity with an auto ID gets no `id`/`idIn` filters: there is no field to annotate.
- `Create()`, `Update()` and `Delete()` read oddly as package-level calls; `MutationCreate()` or constants would match entgql.
- File names that Go ignores or build-constrains (`_x.go`, `x_linux.go`) pass the plugin path check.
- "No such tool" is not pinned by a test, and the error has no `go get -tool` hint.
- Untested or weak: the inverse join edge; the blog loader is duplicated in two tests; the round-trip test is weak.
- `Directive.Args` maps are shared between merged annotations.
- The `Op` constants lack doc comments.
- The deps test admits `schema/...` subpackages; tighten it to the exact package.
- A lone `\r` in a description is not normalized.
- `childrenIDs` where entgql has `childIDs`: frozen, because `plugin.Singular` names join columns.

## For GraphQL plan 2

- Resolved in GraphQL plan 2: `Node` binds to the Go interface `veloxgql.Noder` (`IsNode`, `IDGQL`), which every Node entity implements; gqlgen's type switch compiles (`example/graphql`).
- Resolved in GraphQL plan 2: a JSON field binds through a generated `<Field>GQL` getter returning `veloxgql.JSON`, so one scalar serves `[]string`, map and struct fields.
- Resolved in GraphQL plan 2: `Uint64` is a decimal string both ways (`veloxgql.MarshalUint64`), and `uint8`/`uint16`/`uint32` under `Int` bind to gqlgen's own marshalers, listed in the documented `gqlgen.yml`.
- Resolved in GraphQL plan 2: the Go connection, edge, order and where types take the GraphQL names; a renamed entity gets `type <GraphQL name> = <Entity>`, which real gqlgen binds (`example/graphql`'s `Tag` is `Label`). The root connection function takes the GraphQL type's plural too (`LabelsConnection`, not `TagsConnection`).
- Resolved in GraphQL plan 2: a connection method's parameters follow the SDL's argument order, and gqlgen binds them (`example/graphql` compiles under `gqlgen generate`).
- Resolved in GraphQL plan 2: `plugin.Field` carries the Go type's package path (`PkgPath`) and `Kind`.
- Still open: a custom-Go-type (application) enum binds with `@goModel` to the type's import path; until then velox-gen-declared enums bind, and a custom-Go-type enum field gets a gqlgen-generated model the application converts by hand.
- Still open: no test yet of a custom enum on a renamed entity; add it with the `@goModel` binding.
- Still open: when plan 2's successors emit `@goModel` themselves, refuse it as an application directive, as `@goField` is, or keep it in the copy velox-graphql validates.

## For GraphQL plan 3

- Resolved in API plan 3 (`veloxgql.ErrorPresenter`, `Extensions()` codes, fixed messages for core errors; the API cleanup design, §6c). Errors reach clients through gqlgen's default error presenter, verbatim. veloxgql's own errors are safe to show (they never echo an ID's key), but core's are not: `velox.ErrNoTenant` reads "velox: gen.Post: no tenant…", and once mutations make a `velox.ConstraintError` client-triggerable, its driver text and constraint names reach the client. Plan 3 ships an `ErrorPresenter` recipe (or a `veloxgql.ErrorPresenter`) for `srv.SetErrorPresenter`: pass `veloxgql`'s sentinels (`ErrBadID`, `ErrWrongIDType`, `ErrBadCursor`, `ErrPageTooLarge`, `ErrBadPagination`, `ErrFilterTooDeep`, `ErrFilterTooWide`), `velox.PolicyError`, `ErrNoTenant` and `velox.ValidationError` through, map a `ConstraintError` to a message built from its kind and not the driver's text, and replace everything else with a generic message after logging it.

## For GraphQL plan 4

- [x] Resolved in GraphQL plan 4 (`Rel.MaxRows` gives any load a cap of its own; a load with `Rel.LimitEach(n)` is bounded by `n` per owner; `Paginate`'s doc says a base query's `With` loads still share its `n+1` unless they set one of these). `Paginate`'s page query sets `MaxRows(n+1)`, and a query's `With` loads share that cap: Plan 4, which adds `With` loading to connections, must give loads their own cap. The generated code adds no `With` today, so nothing hits it yet (documented on `Paginate`).
- [x] Resolved in GraphQL plan 4 (`veloxgql.Limits.MaxCost`, `veloxgql.Cost`, `ErrTooCostly` with the code `COST_LIMIT_EXCEEDED`; `example/graphql` drops `FixedComplexityLimit`). A request cost limit that is first/last-aware: gqlgen's `FixedComplexityLimit` counts fields, not rows, so `first: 100` costs the same as `first: 1`. Add a veloxgql limit (an `OperationContextMutator`, or complexity functions in `Handler`) that multiplies by `first`/`last` across nested connection fields, and set it in `example/graphql`.
- [x] Resolved in GraphQL plan 4 (the generated edge methods read the edge in a batch for the field's own arguments and no longer read `x.<Edge>`; `gqlimpl.LoadedConnection` stays for older generated code). `LoadedConnection` assumes the rows a `With` loaded are the unfiltered edge in key order, and ignores a connection's arguments (the generated edge method calls it only when it has none). Plan 4's loader must load them so, or the edge method must use the loaded rows only when the arguments are the ones loaded.
- [x] Resolved in GraphQL plan 4 (a page, a node and an edge's nodes read the selected fields' columns, the key, the foreign keys, the tenant column and the order fields' columns; a selection with a field the plugin did not write reads every column). Every page query selects every column, `password_hash` included, until field collection selects only the columns asked for. Nothing returns the column (sensitive fields are in no type), but the row is read.
- [x] Resolved in GraphQL plan 4 (a batch is read by the first caller, under its context; nothing waits for another resolver; `TestBatchInMutation` pins a nested read inside a mutation). No waiting batcher (a dataloader that holds a field until others join it) may run inside a mutation: the handler runs a mutation's field resolvers one at a time under a lock, so a resolver that waits for another to join it deadlocks. Batch queries only, or load at once inside a mutation.

## Left by GraphQL plan 2

- Resolved in the core JSON-fields plan (2026-10-20, `2026-10-20-core-json-fields.md`): JSON fields are encoded on write and decoded on read through a codec velox-gen supplies, stored as `jsonb`, `JSON` and `TEXT`, with itests on PostgreSQL (pgx and database/sql), MySQL and SQLite, and `Owner.tags` back in the GraphQL e2e. `example/graphql` still declares no JSON field.
- JSON path predicates: a condition on a JSON column other than `IsNull`/`NotNull` is refused; querying inside a document (`->`, `JSON_EXTRACT`, `json_extract`) is a follow-up, per dialect.
- Custom time Go types are refused by velox-graphql in v1: `plugin.Field.Kind` cannot tell a defined type (`type Stamp time.Time`) from a wrapper, so such a field must expose the standard type. Support them with a conversion the model can describe.
- Binding application enums with `@goModel` (see "For GraphQL plan 2").
- [x] Resolved in GraphQL plan 3: `example/graphql` declares no mutations: it now declares create, update and delete for its entities, one transaction per operation.

## Left by GraphQL plan 4

From collection and loading (2026-10-22). The plan's own items are done: `Statement.Each`, `Rel.LimitEach`, `Rel.MaxRows`, `LoadEach`, `CountEach` and `Query.Selects` in the core, with `velox.RowScope`; batching at the edge, with `veloxgql.Batch`; column collection; `MaxCost`, `Cost`, `ErrTooCostly` and `CodeTooCostly`; `FOR UPDATE` in `UpdateX` and `DeleteX`; the collection itest on every dialect; and the nested-request benchmark and the GraphQL variant of bench/scale.

- `Bidi` self-edges: a symmetric edge (`friends`) needs both directions written, which is a model change in the plugin (for mutations) and then a write of the inverse row. Left from GraphQL plan 3 (see "Left by GraphQL plan 3"), as the plan's ruling 19 says.
- `Query.With[T]` is a method with a type parameter beyond `E`, against §6f ruling 21, and the plan left its function form alone (plan 4 ruling 7). `Rel.LimitEach`, `Rel.MaxRows` and `Query.Selects` were made methods because they need none. Measure what `With` costs every entity package (the compile-cost rows) before deciding, as for the "Before 1.0" item at the end of this file; this is the same item.
- The plan's other left-outs (ruling 19): JSON append; `@goModel` enums; a caller-supplied key in create inputs; the hidden O2O child on re-point; the one-sided M2M's join rows on delete. Each is already listed above or under "Left by GraphQL plan 3". gRPC, migrations and the pgx split have their own plans.
- The Linux re-measure: bench/scale's two keys and `BenchmarkNestedRequest`, and the allocation budget on linux/amd64. It is one item, under "1.0 release".
- `bench/scale`'s velox-gen environment for the GraphQL variant sets `GOFLAGS=-mod=mod` after `os.Environ()`, which overwrites the user's `GOFLAGS` where it should append to it. The same call is in `internal/tempmod`.
- `internal/tempmod.WriteGraphQL` runs `go mod tidy -e`, which can mask a resolution error in the temporary module: the build that follows fails instead, with a worse message. It is there for gqlparser's `levenshtein` test dependencies, which an offline module cache lacks; check that the error is only that one, or drop `-e` when the cache has them.
- A batch is read under its first caller's context, on the DB `DBFrom` reads, and its error is every row's. On a DB with replicas a batch may read a replica other than its parents'. Plan 4 ruling 13; no change planned.
- The `veloxgql.Cost` of a request counts every branch of a fragment and ignores `@skip` and `@include`, so it is an upper bound. A client that depends on a skipped branch for a large page pays for it.
- The allocation budget of `TestNestedRequestBudget` holds under the pinned gqlgen (v0.17.86) only, with 10% for the jitter of gqlgen's concurrent resolvers; under `scripts/gqlgen-latest.sh` the check is skipped.

## Before 1.0 (veloxgql)

- Resolved in API plan 3 (`veloxgql.Limits`, `WithLimits`, `LimitsFrom(ctx)`; zero means the default, negative is refused). Limits are mutable package globals (`veloxgql.MaxPage`, `MaxList`, `MaxFilterDepth`, `MaxFilterWidth`, `MaxFilterNodes`, `MaxOrderTerms`). Two servers in one process cannot differ, tests that change them cannot run in parallel, and changing them after 1.0 breaks callers. Make them options of `Handler`, carried in the context, with the current values as the defaults; `Handler.Validate` already refuses a non-positive limit.
- Resolved in API plan 3 (`veloxgql.NewExtension` returns a named `*Extension`). `Handler` returns an anonymous interface; a named type would read better in godoc. Settle its name and what it exposes.
- Resolved in API plan 3 (they moved to `gqlimpl`, unexported or for generated code only). Decide which exported helpers stay public: `CheckDepth`, `CheckWidth`, `Paginate` and `CursorOrder` are exported, but generated code does not need them. `AsInt` takes an unexported constraint.
- Resolved in API plan 3 (`ErrFilterTooLarge`, "filter is too large"). `ErrFilterTooWide`'s text, "filter list is too long", is wrong for the node budget (`MaxFilterNodes`): give the budget its own text or error.
- Resolved in API plan 2: move velox-gen's plural rule into the stdlib-only `plugin` package as `plugin.Plural`, frozen like `plugin.Singular`; gogen's root connection functions use a copy that no test pins to velox-gen's `Handles`.
- Resolved in API plan 3 (the README and main.go say only `__schema` is free). The example README and main.go say introspection is free under the complexity limit; only `__Schema` is, `__type(name:)` costs a few points.
- Resolved in API plan 3 (they use `gqlimpl.InputErrorf`, which writes the prefix and the code). Generated enum `UnmarshalGQL` errors lack the `veloxgql: ` prefix the docs promise.

## Left by API plan 1

From the final review of API plan 1 (2026-10-20). Each item names the plan that takes it.

- [x] **API plan 2 (DSL and velox-gen):** rules 37, 39 and 40 are three copies of one check (an `Optional`, non-`Nillable` field of a type whose unset value cannot be stored needs a `Default`). Make them one table of `{type, rule, noun}`, so that a rule 41 is one line.
- [x] **API plan 2:** `internal/gen` comments cite "Ruling N" of old plans (`lower.go`, `build.go`, `methods.go`). They are not public, but they rot; the velox-gen rewrite should drop them. The citation test scans only public packages.
- [x] **API plan 5 (dialects):** resolved in API plan 5 (`TestPolicySQLOnEveryDialect`). `SQL(ctx)` of a statement with a policy `Allow` or a zero-Pred failure is tested on PostgreSQL only; add it to the dialect test sweep.
- [x] **API plan 3:** the citation scan (`TestGodocCitesNoSpec`) did not cover the contrib modules. `contrib/graphql` has its own, over `graphql`, `veloxgql` and `gqlimpl`.
- [x] **API plan 4:** the same for `contrib/veloxotel`, whose `metrics.go` and `attrs.go` still say "spec §2.x". Resolved in API plan 4: `TestGodocCitesNoSpec` scans the module's package, tests included.
- [x] Resolved in API plan 5 (`Op.Raw()` and `Op.Reads()`, which `contrib/veloxotel` uses). **Moved from API plan 4, which kept the core to what it needs:** `contrib/veloxotel/attrs.go` re-implements the core's unexported `Op.raw()` and `Statement.reads()`. Export `Op.Raw()` and `Op.Reads()` (additive), so hooks and adapters share one definition.
- **Perf pass with the pgx split:** `Save` allocates its `cols` slice. No API impact, and no budget pins Save.
- Accepted, no change: an empty `InsertAll` skips the transaction and dry-run checks (it sends nothing; documented on `Exec`); the Save compile test's cgo note.

## Left by API plan 2

From the final tasks of API plan 2 (2026-10-21). The plan's own items are done: one table for the rules, the "Ruling N" comments dropped, plugin options, warnings, `.proto`, and `plugin.Plural`.

- `Lower`'s rule errors carry no position: only builder errors have `file:line`, so a rule error names the schema and the field and no more.
- Errors inside velox2's own mixins (`mixin.Time` and the like) print module-cache paths, since the position is where the constructor ran.
- A plugin panic's stack trace is dropped, as the response's error holds the value only. Make it available behind an environment variable.
- The case fold of plugin paths does no Unicode normalization: two spellings that differ only by normalization form are two files, and collide on a file system that normalizes.
- [x] Resolved in migrate plan 1 (derived names are capped at 63 bytes by `ddl.Cap`, after the suffix, and rule 44 runs on the capped names). **1.0 blocker, for the migrations plan:** derived index names (`<table>_<cols>[_unique][_<hash>]`) have no length cap. PostgreSQL truncates identifiers to 63 bytes and MySQL refuses names over 64, so two long derived names that differ only after byte 63 collide in the database while rule 44 passes them. Cap derived names with a hash suffix, and run rule 44 on the capped names. No database holds these names until the migration planner exists.
- Linux CI never runs the case-respell path of the stale-file sweep (a generated file whose name changed only in case, on a case-insensitive file system): the tests take it on macOS only. Run CI on a case-insensitive volume, or add a test hook that folds case on Linux.
- Since the final review's fix of `Number[T]`, every stored literal `Default` is its kind's base value, so the `Marshaler` exemption of model 2's `Field.Default` (§6b ruling 10) has no case left to protect for numbers. The ruling stands; it could be loosened later, additively.

## Left by API plan 3

From the final tasks of API plan 3 (2026-10-21). The plan's own items are done: `gqlimpl` and the version guard, limits from the context, the extension's options, `Pred(ctx)`, coded errors and `ErrorPresenter`, `XConnectionOf`, directives on where inputs, and the `gqlgen@latest` job.

- [x] Resolved in GraphQL plan 3 (velox's validation errors name the input field, `UpdatePostInput.title: invalid value: …`; gqlgen's own carry the code `GRAPHQL_VALIDATION_FAILED` and name it through their `path`, or give `locations` for an inline literal, and are left as gqlgen writes them): GraphQL validation messages (gqlgen's own, for a variable or an argument of the wrong shape) do not name the field. Where inputs name theirs (`OwnerWhereInput.hasEventsWith[0]: …`); the rest are gqlgen's text. Decide with the mutation inputs whether to wrap them.
- [x] Resolved in GraphQL plan 4 (`velox.Query.Selects` is the accessor; a base query that drops the key or an order column is refused with an internal error). `Columns` or `Omit` on a `ConnectionOf` base query is documented, not refused: one that drops the key or an order column breaks the cursors. `velox.Query.Ordered` made the `OrderBy` check possible; an accessor for the selected columns would make this one possible.
- `TestPredTwiceOnOneContext` asserts that the second call does not spend the first's budget, but not that the second call costs nothing extra (no allocation or context growth per call). Add that if it ever matters.
- [x] Resolved in GraphQL plan 4: the first/last-aware cost limit (`MaxCost`); `LoadedConnection` and the arguments it ignores (the edge methods no longer use it).
- [x] Resolved in GraphQL plan 3: `NOT_FOUND` (`CodeNotFound`) is defined and nothing returns it yet: the generated mutations return it for an ID that names no row the caller may see or change.
- The `gqlgen@latest` job fails red when a new gqlgen breaks velox-graphql, by design. The example's tests ignore error `locations` (gqlgen v0.17.97 adds them); a later gqlgen may change something else they compare.
- Type-level directives (e.g. federation) need an explicit opt-in. velox-graphql refuses an application directive on an entity, since gqlgen runs it on the type's objects only and the rows' values stay readable through cursors, filters, order and `totalCount`. If one is wanted (federation's `@key`, or a guard), add an opt-in then: either an annotation that marks the directive as not a guard, or copying a guard onto `XConnection`, `XEdge` and `XWhereInput` (declared `on OBJECT | INPUT_OBJECT`) and counting it for the `OrderField` rule.
- `ErrorPresenter` does not pass an error that is only the cancellation of the request to the application's `log` (the default records it at debug). An application that wants to count client disconnects has no hook for it; add an option if one asks.
- [x] Resolved in API plan 5 (the root test checks each comment group and matches "GraphQL plan 3" and the like). The root module's citation test (`TestGodocCitesNoSpec`) checked each comment line alone and did not match internal plans ("GraphQL plan 3"); `contrib/graphql`'s checks each comment group and matches them.

## Left by GraphQL plan 3

From the mutations (2026-10-22). The plan's items are done: the inputs with their directives, `gqlimpl`'s runtime, `CreateX`, `UpdateX` and `DeleteX` with their edges, the itest on every dialect, and the example's mutations. So are the final review's fixes: M2M `remove…IDs` and `clear…` scoped by the target's tenant and Read policy, the validators run before any statement (core `velox.Validate` and `ValidateSet`), `Statement.Writable` guarded against Rewrite hooks, and the example's `Post.views` `Optional`.

- Moved to the pgx split (API plan 5 ruling 10: `Open` takes a `*sql.DB`, and neither database/sql nor go-sql-driver/mysql exposes the DSN or the client flags behind it; the split reshapes `Open(d, Conn)`, which can then take the MySQL configuration and refuse a missing flag). `Open`'s doc already says to set it. A core check of the MySQL DSN at `Open`: a DSN without `clientFoundRows=true` makes the `UPDATE`'s rows-affected count the rows changed, not matched, so `UpdateX` of an unchanged row can fail with NOT_FOUND. Documented now (`contrib/graphql` doc, `UpdatePost`); the core could refuse or warn at `Open`, since velox's own `Update` needs it too.
- Self-edges are written one way, as the core's loads read them (the plugin model has no `Bidi`). A symmetric edge (`friends`) needs both directions written: add `Bidi` to the model and write the inverse row.
- A key the caller supplies is not in a create input (plan 1's refusal stands): an entity whose key is not generated cannot be created through GraphQL.
- JSON append and other partial updates of a JSON field (old velox's `Append…`).
- `@goModel` binding of application enums (also under "For GraphQL plan 2").
- [x] Resolved in GraphQL plan 4. An update with only edge inputs checks its row (`Writable` `COUNT`), then writes the edges: a time-of-check race, the same one ent has. Closed by GraphQL plan 4: the check is a `Writable().ForUpdate().Exists` read (not on SQLite), and the lock holds for the rest of the mutation's transaction; `DeleteX` does the same before its join rows.
- Clearing or removing children skips, silently, those the caller may not change: an O2M or O2O child the tenant scope or the child's Write policy hides, and an M2M link to a target the tenant or the target's Read policy hides (the final review's C1). A mutation that must report them needs a count of what was skipped.
- A created row is returned as written, and an updated one is read back without the Read policy (a tenant-scoped `velox.WithoutPolicy` read). Neither is checked against what the caller may then read.
- There is no savepoint inside the extension's transaction (binding call 3): a resolver that swallows a mutation's error commits its partial writes. `Atomic` could call `db.Tx` inside the extension's transaction too, which makes a savepoint (two extra statements per mutation). A swallowed error would then roll back only that mutation, and on PostgreSQL a failed mutation would not abort the operation's transaction for the mutations after it. `Atomic` would then be just `db.Tx(ctx, fn)`, and `shared.OperationTx` could go. Worth reconsidering before 1.0.
- `TestMutationsRun`'s inner `go test` runs without `-race`. Pass `-race` when the outer test binary is race-built (a `//go:build race` const). It adds build time to the slowest CI step, and the runtime under race is covered by `gqlimpl`'s and `veloxgql`'s own `-race` tests.
- [x] Resolved in migrate plan 1 (rule 49 refuses JSON fields of kinds that cannot be encoded, so the plugin, the core and the planner agree on `Nullable`). The plugin's `Nullable` says yes for an `Optional` JSON field of an interface, func or chan type, whose nil binds SQL NULL, but its `ColumnDesc` is not `Nullable` (`ColumnDesc.Nullable` refuses such a field). It works today. Before the migrations plan reads `ColumnInfo.Nullable` for DDL, either accept `Nullable` on such a column in `buildDesc` or have rule 48 refuse it.
- An O2O re-point on update can hit a hidden current child: the unlink of the current child is scoped by the child's tenant and Write policy, so a child the caller cannot change stays linked, and linking the new one fails with "unique constraint violated", which reveals the hidden child; with no unique index on the key, the row ends with two children. Check with a scope-free `COUNT` first and refuse with the "already linked" text, or document it.
- A one-sided M2M (an `edge.To` with no inverse, which velox-gen allows) leaves join rows on the target's delete: `deleteX` gathers join tables only from the entity's own M2M edges, so the FK refuses the delete with a `ConstraintError`, or the rows dangle where FKs are off. Gather the model's M2M edges whose `Target` is the entity too.
- `internal/gogen/mutation.go` repeats itself: the join `own`/`other` choice (three times), the "foreign key … is no field" error (four), the re-point emission and the owner-key `Exist` emission (two each), and the `ret` and `exec` closures (two). Factor `joinCols(ed)`, `fkOf(ent, ed)` and one `repoint(...)` emitter; the goldens do not change.
- The generated owner-key check is `Users.ID.In(k)` on one key; `EQ(k)` reads better and renders `= ?`. Cosmetic; it changes the goldens.
- The generated godoc is long: `UpdateX`'s is one paragraph of about 350 words on every entity. Move the shared semantics to `contrib/graphql/doc.go`, where most of it already is, and keep each function's doc to its own contract with a pointer.
- 1.0 design note: on an M2M add, binding ruling 4 checks only Read on the target, as ent does. So `updateCircle(addWriterIDs: [a frozen writer])` succeeds while `updateWriter(frozen, addCircleIDs)` is NOT_FOUND, and `updateArticle(writerID: frozen)` links to a writer the caller may not write. Decide knowingly before 1.0 whether a link needs Write on both ends.

## Left by API plan 4

From the final tasks of API plan 4 (2026-10-22), the OpenTelemetry module. The plan's own items are done: the rename to `contrib/veloxotel`, `Options` alone with `OmitCallers`, the scope's version and schema URL, semconv v1.43.0 and its test, the duration without `Iter`'s loop body (`Result.LoopBody`), `db.namespace`, `server.*` and `db.response.status_code` (`velox.DriverCode`), and the citation scan.

- A transaction's span has no namespace or server, and no `velox.route.shard`: `TxInfo` has no route. Adding one to the core would let `TargetFor` name it.
- An adapter given outside a retrying `Around` hook records, for an `Iter` that is run again after it yielded rows, a duration that includes the first attempt's loop body: the retry resets `Result`. It happens on an error only, and the documented order (the application's hook before `Options`) avoids it.
- `TargetFor`'s attributes are not known when a sampler runs, as the route is final only after the statement. A sampler cannot sample by server, replica or shard target (`velox.route.shard` is a start attribute).
- `velox.DriverCode` reads pgx first, then MySQL, then SQLite, whatever the order of an `errors.Join` that holds errors of more than one driver. This is documented there and harmless in practice.
- `Config.Filter` and `Config.Attributes` (the review's finding 16, a Could) are not built. Nor is `network.peer.address`: the adapter does not see the peer.
- The adapter's tests that need the SDK and pgx live in the module itself, so the adapter's `go.mod` lists both. A nested test module would keep them out of an application's `go mod graph` (the review's finding 18, a Could).
- `contrib/veloxotel`'s `record` still allocates its attribute set under a no-op `MeterProvider` (the entry under "Left by observability").
- `collection(st)` ran three times per statement (the summary, the span's attributes and the metric's) and, for an entity with a schema, concatenates strings. Fixed in the API plan 4 final review: it is computed once in `around` and passed along with the summary. `operation(st)` still runs three times; it is a switch and allocates nothing.
- The duration timing tests (`Result.LoopBody`, the adapter's loop-body points) use sleeps and wall-clock bounds. A loaded machine may need wider bounds.
- Each statement on a hooked DB with `Iter` costs two clock reads per yielded row for `LoopBody`. It is cheap beside a row scan, and 0 on a DB with no Around hook.

## Left by API plan 5

From the core additions (2026-10-22): `First` and `Exists`, `Sum`, `Avg`, `Min` and `Max`, `Rel.Of`, the locking reads, `Mutation.Conds`, `ValueIn`, `Affected` and `SetAffected`, `Op.Raw` and `Op.Reads`, the nested `Tx`'s refusal of options, and the `ESCAPE '!'` of LIKE. Execution changed three of the plan's calls: `Conds` returns the caller's conditions as given, before and after `next`; `Mutation.SetAffected` exists for a hook that makes the write itself (it amends the soft-delete call of the plan, which reported 0); and `ValueIn` records an error for a value of another type. It added a rule: on `Count`, `Exists` and aggregate statements `prepare` refuses a Rewrite hook that changes `Columns`.

- The `velox.aggregate` span attribute (optional): `contrib/veloxotel` could name the aggregate function (`SUM`, `AVG`, `MIN`, `MAX`) on a statement with `Statement.Aggregate`. Nothing asks for it.
- The `clientFoundRows` check at `Open` moves to the pgx split (see "Left by GraphQL plan 3").
- [x] Resolved in GraphQL plan 4, but for `Bidi` self-edges (see "Left by GraphQL plan 4"): the first/last-aware cost limit, `LoadedConnection`, a selected-columns accessor for `ConnectionOf` (`Query.Selects`), and adopting `ForUpdate` in `UpdateX`.
- [x] Resolved in migrate plan 1 (the cap, rule 49 for `Nullable`, and `Index.Where` refused on MySQL). Left to the migration planner: the derived index-name cap, `ColumnInfo.Nullable` for interface JSON fields, and MySQL's `Index.Where`.
- Left to the pgx split: `Save`'s `cols` allocation and the core's pgx dependency.
- Left to velox-gen: rule errors without positions, mixin paths, plugin panic stacks, Unicode case folding.
- Left to otel: `TxInfo`'s route (see "Left by API plan 4").
- ~~The review's godoc nits: `type Mutation[E any] mutation`, `Option func(*config)` and `ColumnInfo.Codec`'s long doc.~~ Done in the final review's fixes: `Mutation`, `Option` and `TxOption` are structs with unexported fields, and the codec's rules are doc.go's `# JSON fields` section.
- The aggregates take `NumberCol` only. A nullable column's aggregate goes through `Raw` until one is asked for; a sealed interface over both column types would need explicit type arguments.
- `Avg` is a `float64`, which holds integers exactly up to 2^53.
- A locking read has no sentinel for a failed lock: `DriverCode` gives `55P03` and `3572`. MySQL's InnoDB locks every row a locking read scans, so `SkipLocked` and `ForUpdate` want an index on the filter.
- A Rewrite hook may not change `Count`, `Exists`, `Aggregate`, `Lock` or `LockWait`, nor `Columns` of a `Count`, `Exists` or aggregate statement (`Count` added by the final review: it renders first, so a flip changed what the others returned).
- `Sum`, `Avg`, `Min` and `Max` stay functions: a `Query` method with its own type parameter costs every entity package (§6f ruling 21: a method when it needs no type parameter beyond `E`, a function when it does). `SetAffected` on an insert is a misuse, and a `Save` whose hook calls it stores nothing in its entity.
- The LIKE escape no longer depends on server settings (it is `!`), so plan ruling 12's "left with no plan" is superseded. A `\` in a `Like` pattern now matches a backslash, where before it escaped the next character (core-design's note of 2026-10-22).
- The compile-cost bounds (`TestGenericTypesCompileSmall`) keep a margin of about 1.2% over the measured size, as the old ones did; a task that grows a measured entity re-measures and moves its bound by that rule. `entityDescUse` is near its bound.
- The MySQL lock itests read `performance_schema` through `VELOX_TEST_MYSQL_ADMIN` (the CI MySQL's root; CI and `scripts/ci-local.sh` set it); without it they wait a fixed time.
- A temp-module test that fails with "module lookup disabled by GOPROXY=off" now says "run go mod download" (`internal/tempmod`, `bench/scale`, and the tests that run `go` offline).

## Left by migrate plan 1

From migrate plan 1 (2026-10-23): the model, `Tables`, `Diff`, `Lint`, `Render` on three dialects and `NewPlan`. Plan 2 owns:
- [x] Resolved in migrate plan 2, Tasks 1–2 (`FuzzSplit`, in CI; `TestDirGolden`). `FuzzSplit`, with the statement splitter, and the directory's goldens; the directory writes its files with plan 1's `writeScript`.
- [x] Resolved in migrate plan 2, Task 12 (`TestMigrateCases`: every case on SQLite, PostgreSQL and MySQL through `Up`, with rows and the three invariants, so the MySQL goldens run too). The real-database itests of every change kind, with the three invariants. Plan 1 executes its SQLite goldens in memory, and its PostgreSQL goldens on the CI database (`TestRenderPostgresExecutes`, under `VELOX_TEST_POSTGRES`, without rows); the MySQL goldens are not executed yet.
- [x] Resolved in migrate plan 2, Task 12 (`TestMigrateMySQLCombined`, `TestMigrateFKIndexReplacedMySQL`). Itests of MySQL's combined `ALTER TABLE`s: an `ADD COLUMN` with a `MODIFY COLUMN`; an index dropped and added again under one name; a foreign key's step-1 drop and step-7 add around a rename or an index change.
- [x] Resolved in migrate plan 2, Tasks 3 and 12 (the runner's `foreign_key_check`; `TestMigrateSQLiteDropCycle`). An itest of the SQLite drop cycle, run with `foreign_keys` off and `foreign_key_check` before `COMMIT`.
- [x] Resolved in migrate plan 2, Tasks 4 and 12 (`TestUpPostgresOnlineResume`, a cancel during an online build with rows; `TestMigrateRowsSurvivePostgresUnits`). The PostgreSQL online units' resume after a failure inside a statement, against a real server with rows. Plan 1 checks re-running each unit from its first statement after each of its statements, on empty tables (`TestRenderPostgresExecutes`).
- [x] Resolved in migrate plan 2, Task 12 (`TestMigrateRowsSurvive`). The rows: the casts of a type change and of the enum swap, `setval` after `add-identity`, and the validated checks of `SET NOT NULL`, each on a table that holds rows.
- [x] Resolved in migrate plan 2, Task 2 (`Dir.Add` writes the Config's lock timeout into an enum script that has none, `TestDirAddEnumLockTimeout`; the runner never reads a file's name). With `RenderOptions.LockTimeout` 0, the PostgreSQL enum script has no lock-timeout directive and runs with none, while the main script takes the Config's; `NewPlan` always passes one. The runner could apply the Config's timeout to the `_enum` file too: plan 2's call.
- [x] Resolved in migrate plan 2, Task 6 for `Inspect` (it fills `Sequence` from `pg_depend`, `TestInspectPostgresSequence`); baseline, which the refusal names, is plan 3's. An identity whose sequence `from` does not name: `Render` refuses it (and a serial whose `nextval` default it cannot read), asking for `velox-gen migrate baseline`; `Inspect` fills `Sequence` so that a snapshot never lacks it.

## Left by migrate plan 2

From migrate plan 2 (2026-10-24): the directory, the runner, `Inspect` and `Drift` on three dialects, the CLI's `diff`, `apply`, `status`, `drift`, `lint`, `hash` and `resolve`, and the itests. Plan 3 owns:
- [x] Resolved in migrate plan 3, Tasks 2–4 (`migrate.Baseline`, `WithBaseline` and `Report.Baselined`, `velox-gen migrate baseline` and `apply -baseline`, with the ent fixture). Baseline: `velox-gen migrate baseline`, `apply -baseline` and `WithBaseline`.
- Moved to "Left by migrate plan 3" by the user's ruling of 2026-10-24. The property test (the spec's Plans, item 3).
- Moved to "Left by migrate plan 3". The examples and `internal/testmodel` on the planner: their tables are still hand-written DDL.
- Moved to "Left by migrate plan 3". `migrate diff -check` in CI, for the examples' directories.
- [x] Resolved in migrate plan 3, Tasks 1 and 5 (`migrate`'s package doc and `example/README.md`'s "Migrations": the Atlas provider and adopting a database). The repo's own README is the 1.0 release's item. The documents: the README and the example's, beside `migrate`'s package doc.

Found while executing plan 2:
- PostgreSQL's primary-key index is not checked for the attributes the model lacks (a `DESC` column, an operator class); its constraint's `DEFERRABLE` is. MySQL notes a prefix or `DESC` primary key.
- When a deferrable unique constraint's note and its index's note are both given, the two warnings share one Object name.
- MySQL's binary and bit literal defaults: MySQL prints them as `0x…` and `b'…'`, and `Inspect` keeps them, so a model `'a'` on a `varbinary` column would drift. An `_binary'…'` expression default is not stripped either.
- A MySQL expression default with an introducer other than a UTF-8 one (`_latin1'a'`, from a latin1 client) stays as printed and gives a warning, not silence. The runner's connections print `_utf8mb4`.
- A default the column rounds is a change: a model `1.555` on `decimal(5,2)`, which MySQL stores as `1.56`. Drift is right that the stored default differs; `Tables` could refuse such a default instead.
- Trigger support: triggers, MySQL routines and events, and `BEGIN … END` blocks are refused on SQLite and MySQL, since the splitter would cut their bodies. Supporting them needs the splitter to read their bodies, or MySQL's `DELIMITER`.
- When pgconn cannot load a file a PostgreSQL DSN names (a missing `sslrootcert`), velox-gen says `-db is not a PostgreSQL connection string`, which misleads.
- `mysqlRowBytes` counts 0 for the types it does not know (`set`, `float(p)`, the spatial types), so a row of them is undercounted and MySQL refuses it at apply instead of at plan time.
- The normaliser's known limits: an interval literal always keeps its cast, so a cast one never matches an uncast one; it does not know a column's type, so on a `date` column `d >= '2020-01-01 10:00'::timestamp` matches `d >= '2020-01-01 10:00'`, which PostgreSQL reads apart; and a column cast to `text` matches the column, so `qty::text = '05'` matches `qty = '05'`.
- MySQL's row limit refuses `varchar(16382)` and `varchar(16383)` with any ID wider than `tinyint`, though §4 maps `MaxLen` up to 16383 to `varchar`. Moving the `varchar`-to-`text` cut-off (to 16381, say) would let every such field build; the error already points at `Text()`.

From the final review's fix wave:
- Lexer unification: `migrate` has four SQL lexers, each with its own quoting rules: the splitter and `lex` (split.go), `lexPredicate` (predicate.go) and `lexSQL` (inspect_sqlite_create.go). One lexer per dialect, which the others build on, would keep them from disagreeing about a quote or a comment.
- The inspect readers' shared helpers: the PostgreSQL, MySQL and SQLite readers each have their own `each`, `note`, `schema` and `tables`. One reader type that holds them once would leave each dialect only its queries.
- The splitter cuts PostgreSQL 14's `CREATE FUNCTION … BEGIN ATOMIC …; END` at the body's inner semicolons, so the trailing `END` is now refused as "END in a file run in a transaction", which points the wrong way. The splitter should read a `BEGIN ATOMIC` body as one statement.
- A file run in a transaction that changes its session's `search_path` (pg_dump's `set_config('search_path', '', false)`) breaks its own history write, which names the table unqualified; Up resets the session only after each file. Qualifying the history table, or resetting before the history write, would close it.

## Left by migrate plan 3

From migrate plan 3 (2026-10-24): `velox-gen migrate sql`, the Atlas provider; Render's owned sequences; `migrate.Baseline` and `velox-gen migrate baseline`; `WithBaseline` and `apply -baseline`; the ent fixture; the documents.

Moved out of the plan by the user's ruling of 2026-10-24 (migrations are not the focus; the ORM and GraphQL are):
- The property test (the spec's Plans, item 3).
- The examples and `internal/testmodel` on the planner: their tables are still hand-written DDL.
- `migrate diff -check` in CI, for the examples' directories.
- Drift through a scratch database.
- An Atlas cross-check of `migrate sql`'s output: no test runs Atlas on it.

Left by the plan:
- A SQLite ent fixture. The ent fixture (`cmd/velox-gen/testdata/ent`) covers PostgreSQL and MySQL; SQLite's baseline is covered by `TestMigrateBaselineSQLite` and `migrate`'s own tests on a velox-made database.
- MySQL `AUTO_INCREMENT` start values: a table's `AUTO_INCREMENT` is its next value, not its start, so baseline does not see a start value and gives no warning; `0001_baseline.sql` builds a fresh database whose counter starts at 1.

Found while executing it:
- PostgreSQL unique constraints: velox models one as its unique index, and baseline warns that it is read so. The next diff renames its index with `ALTER INDEX … RENAME`, which renames the constraint too (ent's `users_email_key` becomes `users_email_unique`), but a later drop renders `DROP INDEX`, which PostgreSQL refuses for a constraint's index. Render should write `ALTER TABLE … DROP CONSTRAINT` for it, which needs the model to tell a constraint's index from a plain one.
- The extension note matches only a column type's own `pg_depend` row. A column of an array of an extension's type (`citext[]`), or of a domain over one, gets no note, so `0001_baseline.sql` fails on a fresh database with no word of the missing `CREATE EXTENSION`.

## 1.0 release

- `apidiff` (API plan 5 ruling 11) is not wired: there is no tag to compare with, and a check against `main` would fail on the breaking changes still planned (the pgx split reshapes `Open`). After the first `v1.0.0` tag, CI runs `golang.org/x/exp/cmd/apidiff`, pinned, for the root module, `contrib/veloxotel` and `contrib/graphql` against the latest `v1` tag, and fails on an incompatible change. The generated-only names (`Desc`, `ColumnDesc`, …, `gqlimpl`) are outside semver and are reported, not failed.
- The repo needs a `README.md` and a `LICENSE`.
- bench/scale's thresholds (both keys, `500` and `500-graphql`) were measured on darwin only, on a loaded machine (GraphQL plan 4, Task 8: load average 4 to 6 or more at each run). Recalibrate them from the first Linux CI run, and re-measure on an idle Linux machine. Also re-measure `example/graphql`'s `BenchmarkNestedRequest` there (its times are not pinned; `TestNestedRequestBudget` pins the statements and the allocations, which do not depend on the machine's load, and the allocation budget was measured on darwin/arm64: check it on linux/amd64).
- **GraphQL plan 3 (final re-review):** CreateX's `x` and UpdateX's `set` are declared outside the `Atomic` closure that mutates them. Safe while Atomic never retries and a TxAround calls `next` once; if either changes, copy them at the top of the closure.
- **Before 1.0 (API plan 5 final review):** `Query.With[T]` is a method with a type parameter beyond `E`, against §6f ruling 21. Measure what it costs every entity package (the compile-cost rows); if it is material, give it the function form the aggregates have, or record why it stays.
