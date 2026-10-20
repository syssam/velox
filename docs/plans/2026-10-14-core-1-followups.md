# Core 1 — follow-ups

Findings from the reviews of Core 1 and of the plans after it that were deliberately left for later. Items already fixed are not listed; the schema DSL plans (1 to 4) closed the old struct-first path's items, which are gone from this file.

## Still open from before core plan 3

- The LIKE escape depends on server settings: MySQL's `ESCAPE '\\'` assumes `NO_BACKSLASH_ESCAPES` is off, and PostgreSQL's `ESCAPE '\'` assumes `standard_conforming_strings` is on. A non-backslash escape character (e.g. `!`) removes both dependencies.
- Golden SQL for MySQL and SQLite on the read-path render cases that still assert PostgreSQL only (writes cover all three since core plan 3), an OR-between-siblings case, and the NEQ/GTE/LT/LTE operators.

## Left by core plan 4 (relations)

- A rewrite hook that points a statement at another table (tenancy, sharding) does not redirect the tables inside `EXISTS` subqueries; relation loads do go through the hooks.
- `One` loads relations for the two rows it reads before returning `ErrNotSingular`.
- A has-one relation with several matching rows loads the first in the relation's order.
- On MySQL, an `UPDATE` or `DELETE` whose `EXISTS` reads the table being changed fails with error 1093 (documented on `Has`).
- A `time.Time` cursor made in memory, finer than the column's precision, can skip or repeat a row with `After` (documented: `x` must be a row a query returned).

## Left by core plan 5 (scale guards)

- `bench/scale`'s time thresholds are 4x the largest of three runs on an M3 Max (sizes and memory 1.5x, re-baselined on 2026-10-19). Recalibrate them from the first GitHub run: set each to a margin over the figures that run reports.
- The object-size guard's large rows (`entityDescUse` and similar) carry a fixed per-package cost, so a 2x threshold on them tolerates a bigger absolute growth than on the small rows.
- The spec's "98 lines per entity" (the generated code's size, §2) was not re-measured for the schema DSL's generated `Desc` literals.
- `TestRewriteCanReplaceTheEntity` can only catch a regression when the allocator reuses an address, and the bypass that renders uncached when a hook replaced the entity has no test pinning that the cache stays flat. The hook-replaced-entity cache check was not mutation-tested.
- `TestCacheKeepsShapesApart` does not pin limit/offset presence, order or nesting as separate shapes (`FuzzRender` compares the cached path with the uncached one, which covers them only for the inputs it generates).
- `Sum` and `Avg` on `NumberCol` await aggregates (sub-project 2 or later).

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
- There is no `Sum`, `Avg` or `GROUP BY`: the example totals revenue per organization in Go, over `Iter`.
- A policy function that only allows or denies must still return a zero `Pred[E]{}` beside its error.
- The core package imports pgx, so an application that uses only SQLite or MySQL still links pgx.
- On SQLite, `ConstraintError.Constraint` holds the columns (`invoices.org_id, invoices.number`), not the constraint's name.
- The function given to `Tx` gets no context, so code inside it uses the outer one. `Statement.Tx` and `contrib/otel`'s parenting on `Tx.Context` work around it for spans only.

API rough spots found while splitting `./example` into one directory per topic:
- A policy function gets no `*DB`, so an inherited policy cannot check on insert that the parent is visible: `example/policy`'s `Comment` inherits `Doc`'s `Read` through `Has`, which covers reads only.
- A `Write` policy filters rather than denies, so a forbidden update returns 0 rows, not an error.
- `Mutation.Value` returns `any`, so an update hook type-asserts the value of a column it named through a typed handle.
- `RetrySerializable` cannot be shown retrying on single-connection SQLite: `example/tx`'s retry runs once.

Left by the observability plan:
- §6's 300 ns is read from benchmarks and recorded, not asserted.
- A serialization failure or deadlock that `RetrySerializable` retries has `error.type` `_OTHER`; there is no value of its own.
- A statement refused before execution (guard, scope, policy, hooks) makes no statement span and no metric point; it shows only as the error of a transaction it ends.
- Inside a transaction, a statement span's parent is always `velox.tx`, even when the application started a span of its own inside `fn` and passed its context (a spec choice).
- A statement with no application frame within 64 frames (an `Iter` driven through `iter.Pull`, say) has a zero `Caller`.
- The `TxInfo` allocated per transaction when hooks are configured has no budget of its own; `TestTxAllocationBudget` pins only that a DB with nothing configured makes none.
- `contrib/otel`'s `go.mod` requires velox2 at a placeholder version, with a `replace` to the repository root, until velox tags releases.
- Test gaps left by the task reviews:
  - fingerprints: no test asserts that the render cache's byte bound counts the fingerprint's bytes;
  - call sites: no concurrent test of the program-counter cache at the cache's bound;
  - results: no test of `Affected` on a failed `INSERT … RETURNING`; the no-pairs `WithTags` test's identity check is vacuous;
  - comments: no concurrent-eviction test; the cache's byte accounting is not checked exactly;
  - transactions: no test of `RetrySerializable` exhausted with a `TxAround` hook.
- Doc and cost nits:
  - `render` stays in non-test code only for the tests.
  - `contrib/otel`'s `record` allocates its attribute set even under a no-op `MeterProvider`; an instrument-enabled check would skip it.

## Left by schema plan 1

- `Index.Where` is PostgreSQL-only, and nothing refuses it on other dialects yet.
- `edge.Symbol` and `edge.Immutable` are carried into the Graph's `Edge` (`symbols`, `immutable`); the migration planner is the first to use the symbols. Rule 34 refuses an edge whose immutability differs from its bound field's, as ent does.
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

- `Optional` means a pointer field (Ruling 2): the column is NULL-able and velox scans a NULL only into a pointer. Ent gives `T`, so a ported schema's `Optional()` fields become `*T`. This is an ent difference.
- Join entity naming: each many-to-many join table becomes an entity named `pascal(singular(table))` (`user_groups` gives `UserGroup`), which can collide with a schema of that name; the generator refuses the collision, and the only remedy is a different join table name (`edge.Table`).
- The test model is generated from `internal/testmodel/schema`. A field's Go name comes from `pascal` of its name, so `SKU` is declared `field.String("SKU").StorageKey("sku")`: the name `sku` would give `Sku`. The named types `Status`, `Cents` and `Score` live in `internal/testmodel/kind` because the schema package names them in `GoType` and the generated package imports them; `testmodel` re-exports them as aliases.

## Left by schema plan 4

- The link peak grows with N, though a service's binary does not: the linker reads the whole `gen` package's object file. It was 185 MB at N=100 and 279 MB at N=500 (about 0.2 MB per entity). This is inherent to the choice of one generated package (core spec, A.6, note of 2026-10-19); splitting `gen` by domain is out of scope until after 1.0.
- A relation's `Rel` handle is checked at every use, not at compile time: a hand-edited `Rel[User, Comment]` over a `Many[Post]` field compiles, and fails with a `*SchemaError` each time a statement uses it, whichever handle used the relation first (and in `velox-gen verify`, which calls `CheckRelation` for every generated handle).

## Left by routing

- Read-only transactions on replicas: `db.Tx(ctx, fn, ReadOnly())` runs on the primary, like every transaction.
- `TxInfo` has no shard, so `contrib/otel`'s transaction spans carry no `velox.route.shard`; their statements' spans do.
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

## 1.0 release

- The repo needs a `README.md` and a `LICENSE`.
- bench/scale's thresholds were measured on darwin only. Recalibrate them from the first Linux CI run.
