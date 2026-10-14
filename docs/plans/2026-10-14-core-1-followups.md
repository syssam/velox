# Core 1 — follow-ups for plans 2–5

Findings from the reviews of Core 1 that were deliberately left for later. Each later plan takes the items under its name before it is written. Items already fixed on the branch are not listed.

## Still open from before plan 3

- The LIKE escape depends on server settings: MySQL's `ESCAPE '\\'` assumes `NO_BACKSLASH_ESCAPES` is off, and PostgreSQL's `ESCAPE '\'` assumes `standard_conforming_strings` is on. A non-backslash escape character (e.g. `!`) removes both dependencies.
- Golden SQL for MySQL and SQLite on the read-path render cases that still assert PostgreSQL only (writes cover all three since plan 3), an OR-between-siblings case, and the NEQ/GTE/LT/LTE operators.

## Plan 2 (generator)

- Resolved in plan 2: nested mixins and embedded structs that add no columns are refused by velox-gen; a field that is not a column is named in its `SchemaError`.
- Resolved in plan 5: a field's type change fails to compile (velox-gen's type-check function).

## Left by plan 4 (relations)

- velox-gen does not refuse a relation to a type that is not an entity (the generated `RelationTo` then fails to compile), and has no fixture with a relation target in an aliased package.
- A rewrite hook that points a statement at another table (tenancy, sharding) does not redirect the tables inside `EXISTS` subqueries; relation loads do go through the hooks.
- `HasWhere` does not check that `Rel`'s `T` is the relation's resolved target (only a hand-built `Rel` can differ).
- `One` loads relations for the two rows it reads before returning `ErrNotSingular`.
- A has-one relation with several matching rows loads the first in the relation's order.
- On MySQL, an `UPDATE` or `DELETE` whose `EXISTS` reads the table being changed fails with error 1093 (documented on `Has`).
- A `time.Time` cursor made in memory, finer than the column's precision, can skip or repeat a row with `After` (documented: `x` must be a row a query returned).

## Left by plan 5 (scale guards)

- `bench/scale`'s time thresholds are 4x an M3 Max (sizes and memory 2x). Recalibrate them from the first GitHub run: set each to a margin over the figures that run reports.
- The object-size guard's large rows (`entityUse` and similar) carry a fixed per-package cost, so a 2x threshold on them tolerates a bigger absolute growth than on the small rows.
- The spec's "98 lines per entity" (the generated code's size, §2) was not re-measured after the type-check function was added to generated code.
- `TestRewriteCanReplaceTheEntity` can only catch a regression when the allocator reuses an address, and the bypass that renders uncached when a hook replaced the entity has no test pinning that the cache stays flat. The hook-replaced-entity cache check was not mutation-tested.
- `TestCacheKeepsShapesApart` does not pin limit/offset presence, order or nesting as separate shapes (`FuzzRender` compares the cached path with the uncached one, which covers them only for the inputs it generates).
- `Sum` and `Avg` on `NumberCol` await aggregates (sub-project 2 or later).
- Resolved in plan 5's final review: `snake` keeps a digit with the acronym before it (`ID2` → `id2`, `OrgID2` → `org_id2`, `SHA256Sum` → `sha256_sum`).
- Resolved in plan 5's final review: a zero-size field is refused as a column (a `SchemaError` asking for `velox:"-"`): it holds no value and shares its address with the next field. velox-gen still writes a handle for one; `verify` reports the error.

## Sub-project 2 (guardrails and policy)

- Resolved in policy plan 1: hook misuse hardening. `WithRewrite(nil)` and `WithAround(nil)` panic when called (in `Open`'s argument list); a rewrite hook cannot set `st.Entity` to nil or swap it for an entity that is not a copy of the original (so a replaced `EntityInfo` whose columns differ from the scan plan is refused); a zero `Predicate{}` is refused.
- Resolved in policy plan 1: the hook-error convention. A `Rewrite` hook's error is returned as `velox: <Entity>: rewrite: …`; an `Around` hook's error is returned unchanged, because it normally wraps `next`'s error, which already carries the prefix (policy design §6). Sentinels are bare phrases wrapped with `%w`, so the prefix is never doubled.
- Resolved in policy plan 1: the one-level copy. A predicate holds its own copy of `In`'s values, so neither the caller nor a hook can change a built query's arguments through the slice (`TestInCopiesItsValues`).
- Resolved in policy plan 1: `Statement.Count`'s contract is documented (velox clears `Columns`, `OrderBy`, `Limit` and `Offset`), and an empty `Columns` list is refused instead of rendering `SELECT  FROM`.

## Sub-project 3 (observability)

- Document on `Around`: `Iter`'s loop body runs inside the around hooks, so a hook that recovers a panic and returns normally hits Go's "range function recovered a loop body panic" error.

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

## Left by plan 2 (generator)

- `velox-gen verify`'s temporary directory survives SIGINT/SIGKILL: there is no signal handling and no sweep of stale `.velox-verify-*` directories. Applications should ignore `.velox-verify-*/` in git (said in velox-gen's package comment).
- `apply` is not atomic and, on an error, does not say which files it already changed.
- Entities in nested `internal/` packages of the module are not reachable from verify's program at the module root; they fail to compile instead of being skipped.
- Naming edge cases: the plural of names ending in an uppercase S or X ("DNSs"), a digit before y, and lowerCamel of "IDs" ("iDs").
- `resolved` does not look inside func, chan or struct types; registered types take precedence over the underlying kind (untested).
- verify's messages start `velox-gen verify: `, and a compile failure of its program is not explained beyond the compiler's output.

## Left by plan 3 (writes)

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

- Compile cost: a Policy and the three entity hooks add about 99 KB to an entity's package, about 50 KB of it velox's generic registration (`Policy`, `BeforeInsert`, `BeforeUpdate`, `BeforeDelete`, `mutationHook` instantiated per entity). Cut velox's share, for example with non-generic registration through layout-identical func types (policy design §7).
- PostgreSQL with a schema-qualified table in `DO UPDATE … WHERE` is verified only on SQLite.
- Errors inside nested `EXISTS`: `existsError` names the statement's entity and the innermost target only, dropping the middle of the path.
- `Mutation`'s `Set` clones the assignments for every replacement; `ConflictClause`'s unexported policy flag lives on an exported type.
- `scoper.node`'s walk is O(nodes) per statement.
- Test gaps: `Op.String`'s "unknown"; `TestSchemaPolicyHooks` indexes `beforeDelete[0]` without a length check; the Review Focus 1 test of policy 2's Task 2 runs its base query on a shared DB without per-run argument checks; the count case with only an `EXISTS` does not assert its SQL; policy tests use package-level counters; the order of a Delete's predicates is not asserted; the hidden-row check through `run` (with hooks); the post-hook guard backstop; `hooks_internal_test`'s text-compare case and its `Errs` case assert only `err != nil`; `TestOverWaitsForAPassedDeadline` lacks block and early-cancel cases; the split `InsertAll` `SQL(ctx)` test does not compare arguments; `FuzzScope`'s input-tree check compares only up to the shorter length and its "copy" is a rebuild; `ExistsOn` makes an unchecked type assertion; the CI fuzz regex is looser than `^FuzzScope$`.
- The itest seed has no cross-org parent, so `With(Parent)`'s tenant condition on the load is not exercised against a row it must hide.
- The upsert's update branch is asserted on the real databases only through `done`.
- A Rewrite hook that drops an insert's RETURNING skips the hidden-row check: the upsert then reports less than it should, but writes nothing across tenants.
- A Read policy's own parameters are not counted when `With` loads are chunked under the parameter limit; a chunk that goes over fails closed with the parameter-limit error.
- Doc nits: `run`'s doc and `Exec`'s doc exceed the line width; `node`'s doc wrap; in `TestUpsertPolicyAndCache`, the comment "A policy that denies denies the upsert, as an update" is misleading: the Insert policy denies first.
