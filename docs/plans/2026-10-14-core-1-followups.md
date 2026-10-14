# Core 1 — follow-ups for plans 2–5

Findings from the reviews of Core 1 that were deliberately left for later. Each later plan takes the items under its name before it is written. Items already fixed on the branch are not listed.

## Still open from before plan 3

- The LIKE escape depends on server settings: MySQL's `ESCAPE '\\'` assumes `NO_BACKSLASH_ESCAPES` is off, and PostgreSQL's `ESCAPE '\'` assumes `standard_conforming_strings` is on. A non-backslash escape character (e.g. `!`) removes both dependencies.
- Golden SQL for MySQL and SQLite on the read-path render cases that still assert PostgreSQL only (writes cover all three since plan 3), an OR-between-siblings case, and the NEQ/GTE/LT/LTE operators.

## Plan 2 (generator)

- Resolved in plan 2: nested mixins and embedded structs that add no columns are refused by velox-gen; a field that is not a column is named in its `SchemaError`.
- Still open: a field's type change (e.g. `float64` to a decimal) does not raise `ErrStale`. `Shape` would have to carry type names, and reflect's spelling of types (`[]uint8`, package paths inside type arguments) differs from go/types'; decide in plan 5 whether it is worth it.

## Left by plan 4 (relations)

- velox-gen does not refuse a relation to a type that is not an entity (the generated `RelationTo` then fails to compile), and has no fixture with a relation target in an aliased package.
- A rewrite hook that points a statement at another table (tenancy, sharding) does not redirect the tables inside `EXISTS` subqueries; relation loads do go through the hooks.
- `HasWhere` does not check that `Rel`'s `T` is the relation's resolved target (only a hand-built `Rel` can differ).
- `One` loads relations for the two rows it reads before returning `ErrNotSingular`.
- A has-one relation with several matching rows loads the first in the relation's order.
- On MySQL, an `UPDATE` or `DELETE` whose `EXISTS` reads the table being changed fails with error 1093 (documented on `Has`).
- A `time.Time` cursor made in memory, finer than the column's precision, can skip or repeat a row with `After` (documented: `x` must be a row a query returned).

## Plan 5 (scale guards)

- The render cache bounds the number of shapes, not bytes; large `IN` arities each store long text, and once full the cache admits nothing new.
- No test pins handle method sets (e.g. that `Col` has no `GT`); a generator change could grant wrong methods silently. `methods_test.go` exempts any method named `column` on any generic type and checks only that type parameters exist, not that they are `[_ Lazy[...]]`.
- Test gaps to close: a pointer outside the entity, explicit and composite `PrimaryKey`, `Index`, `OnUpdate`'s value, zero-size fields, `ColumnInfo.Type`; `TestHandleMethodsBuildNodes` lacks NEQ/NotIn/GT*/Asc/empty In; `TestCacheKeepsShapesApart` does not pin limit/offset presence, order or nesting; the budget test does not assert a cache hit; the connection-leak path after an early `Iter` break was not mutation-tested; `snake` has no case for a capital after a digit (`V2Name` → `v2_name`).
- `TestRewriteCanReplaceTheEntity` can only catch a regression when the allocator reuses an address; the bypass that renders uncached when a hook replaced the entity has no test pinning that the cache stays flat.

## Sub-project 2 (guardrails and policy)

- Hook misuse hardening: a rewrite hook that sets `st.Entity` to nil panics; a zero `Predicate{}` passes the column check; a hook-replaced `EntityInfo` whose columns differ from the Go type's scan plan is not detected; `WithRewrite(nil)`/`WithAround(nil)` panic at query time rather than at `Open`.
- Hook errors are returned without `velox: <Entity>:`; wrapping them would double the prefix of the sentinel errors hooks return (`ErrUnbounded`), so decide the convention with the guardrails.
- The hook-statement copy is one level deep: a hook editing a predicate's own argument slice in place still shares it with the query.
- `Count` with `OrderBy`/`Limit`/`Offset` set renders them; `Count` clears them, but the contract is not documented on `Statement.Count`. A non-nil empty `Columns` (e.g. `Omit` of every column) renders `SELECT  FROM`.

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
- An UPDATE or DELETE with neither Where nor All runs on every row until sub-project 2's guardrails raise ErrNoWhere.
- InsertAll batches of many distinct sizes each add a render-cache shape.
- Test gaps: value semantics of UpdateQuery/DeleteQuery extended twice; Save on a composite key; a named numeric type (type Cents int64) under the Default rule; a uint64 key read back through RETURNING; a look-alike driver error type from another package; cache keys that differ only in Conflict/Update/Returning/Set columns; schema-qualified INSERT and UPDATE golden SQL; that ErrTxDone/ErrTxClosed from rollback is not joined.
- CI tests MySQL 8.4 only, while the spec supports 8.0.19 and later.
