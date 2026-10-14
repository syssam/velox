# Core 1 — follow-ups for plans 2–5

Findings from the reviews of Core 1 that were deliberately left for later. Each later plan takes the items under its name before it is written. Items already fixed on the branch are not listed.

## Before plan 3 (writes)

- Duplicate column names are accepted silently: a second field renamed with `Name("id")`, or a mixin and the entity both declaring `ID`. Reads survive; inserts will not. Make it a `SchemaError`. The default primary-key loop also picks the last `ID`.
- `Unique()`/`Index()` with no fields, a column repeated in one index, and `PrimaryKey()` with no fields are accepted.
- A rewrite hook that changes `Statement.Op` from SELECT panics in `render` (only SELECT is implemented in Core 1).
- `Table("schema.table")` becomes one quoted identifier; there is no way to name a PostgreSQL schema or MySQL database. Add a qualified form or document the limit.
- The LIKE escape depends on server settings: MySQL's `ESCAPE '\\'` assumes `NO_BACKSLASH_ESCAPES` is off, and PostgreSQL's `ESCAPE '\'` assumes `standard_conforming_strings` is on. A non-backslash escape character (e.g. `!`) removes both dependencies.
- Golden SQL for MySQL and SQLite on every render case (several cases assert PostgreSQL only), an OR-between-siblings case, and the NEQ/GTE/LT/LTE operators.

## Plan 2 (generator)

- A struct embedding a mixin that embeds another mixin with its own `Mixin` method shadows the inner one, which is never called.
- A field's type change (e.g. `float64` to a decimal) does not raise `ErrStale`; `Shape` could carry type names.
- `flatten` descends into any embedded struct (`time.Time`, `sync.Mutex`), not only mixins; the generator should refuse non-mixin embeds.
- The "is not a column" `SchemaError` leaves `Field` empty, though the candidate fields are known.

## Plan 4 (relations)

- `IN` lists are not bounded by dialect parameter limits (65,535 on PostgreSQL and MySQL, 32,766 on SQLite). Chunk them with `With`'s eager loading.
- `EQ` with a nil value (e.g. a nil `[]byte`) renders `= $n` with NULL, which matches nothing; consider rendering `IS NULL` or refusing it.

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
