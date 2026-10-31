# GraphQL Plan 4: Collection and Limits — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A nested GraphQL request costs a bounded number of statements per level, whatever the number of parents:
- each nested connection gets its own page per parent (`LimitEach`), read in one windowed statement per edge per level;
- each nested `totalCount` is one grouped count per edge per level;
- a page reads only the columns the selection needs;
- a request whose cost (multiplied by `first`/`last` across nested connections) is over a limit is refused before any statement;
- tenant scope, policies and directives apply unchanged.

**Architecture:** The core gains a per-owner read: `Statement.Each`, rendered with `ROW_NUMBER() OVER (PARTITION BY …)` on every dialect, used by `Rel.LimitEach` (With loads), `velox.LoadEach` (rows for many owners, grouped) and `velox.CountEach` (a grouped count). `gqlimpl` batches at the edge: every page, node lookup and batch registers its rows as a group in a request-scoped table, and the first call of an edge method for any row of a group reads the edge for the whole group, with the typed arguments gqlgen already decoded; the other rows read the result. A connection's columns come from the selection set (`gqlimpl.Selected`). `veloxgql`'s Extension computes a request's cost before it runs (`OperationContextMutator`) and refuses one over `Limits.MaxCost`. The generated edge methods call the new `gqlimpl` functions; nothing in the generated surface is removed.

**Tech Stack:** Go 1.27; gqlgen v0.17.86 and gqlparser v2.5.59, unchanged in every `go.mod`. SQLite (modernc), PostgreSQL (pgx and database/sql) and MySQL 8, as today.

**Spec:** `docs/history/specs/2026-10-19-graphql-design.md`: Goals 4–5, Anti-goals, Constraints, §3 Connections and its dated notes, §4 (all of it), §6, Plans item 4, and the rulings of plans 1, 2 and 3. Also `docs/history/specs/2026-10-20-api-cleanup-design.md` §6a–6f (the core additions so far, and §6f ruling 21, the method rule). Follow-ups: "For GraphQL plan 4", "Before 1.0 (veloxgql)", and the items of "Left by API plan 3", "Left by GraphQL plan 3" and "Left by API plan 5" that name GraphQL plan 4, in `docs/history/plans/2026-10-14-core-1-followups.md`. Where this plan's rulings differ from §4, the rulings stand (see "Rulings this plan makes").

## Global Constraints

- **No aliases.** No renamed or removed API keeps an alias or a deprecated copy. Every caller moves in the same commit.
- **The core changes additively.** `GeneratedCodeIsVersion4` stays 4, and velox-gen's output does not change. New core API follows §6f ruling 21: a method only when it needs no type parameter beyond the receiver's own (`[_ Lazy[…]]` is allowed), a function otherwise.
- **`veloxgql.GeneratedCodeIsVersion1` stays 1.** Code generated before this plan compiles and runs against the new `veloxgql` and `gqlimpl`: `LoadedConnection`, `Term`, `NewConnection` and every other `gqlimpl` name stay, with their signatures. Bump the version only if a task cannot keep this, and stop and report if one cannot.
- **`veloxgql`'s public API grows only by:** `Limits.MaxCost`, `Cost`, `Batch`, `ErrTooCostly` and `CodeTooCostly`. `TestPublicAPI`'s list gains exactly these.
- **Generated code imports** the standard library, `velox`, `veloxgql`, `gqlimpl` and the application's own type packages, and nothing else (no gqlgen, no gqlparser).
- **Every statement goes through the pipeline.** The windowed and grouped statements are `Statement`s on the target entity: the tenant scope, the Read policy, the Rewrite and Around hooks and the guards see them. Scope conditions are inside the window.
- **Every dialect.** PostgreSQL (pgx and database/sql), MySQL 8 and SQLite 3.25+. Every new SQL shape has a golden on all three in `TestRender`, and an itest on all three.
- **Budgets and escape check unchanged.**
  - `go test -run Budget -v .`: one-row 6, tenant 7, hundred rows 105, tx 9/6, observed 7/6, metadata 19, Insert 13, replica one-row 6, First 6, Exists 3.
  - `go build -gcflags=-m . 2>&1 | grep "db.go:$(grep -n 'func (db \*DB) plain' db.go | cut -d: -f1):"` prints `leaking param content: st` and `read does not escape`.
  - `TestGenericTypesCompileSmall`: a row a task moves is re-measured, and its bound set to the new size plus the same margin of about 1.2% (§6f ruling 18). A task that moves a budget stops and reports it.
- **The full gate passes at every task's commit:** `<scratchpad>/fix1.sh`, run on the committed tree, with Docker. If golangci-lint's lock is held, run it with `--allow-parallel-runners`. Run `scripts/gqlgen-latest.sh` in every task that changes `example/graphql`.
- **CI.** `scripts/ci-local.sh` and `.github/workflows/ci.yml` stay identical in what they run; a task that changes one changes both.
- **No machine-specific paths** in committed files. Committed text says `<scratchpad>`.
- **Shell only:** `GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2`, the shared cache the gate uses. The machine is loaded: one heavy command at a time. Do not kill other processes.
- **Regenerating the example** is `(cd example && go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema && go -C graphql tool gqlgen generate)`. Commit `gen/` and `graph/` together.
- **Commits** end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.

## Review Focus

1. **A row the scope hides, inside the window.** User 1 has posts in acme and in globex, and globex's sorts first. Under acme, `users { posts(first: 1, orderBy: TITLE) }` must give acme's first post, and `hasNextPage` must count acme's posts only: the tenant and Read conditions are inside `ROW_NUMBER()`'s subquery, so a hidden row takes no rank. Task 1 pins this in `TestLimitEachScoped`, Task 2 in `TestCountEachScoped`, and Task 7 on every dialect in `TestTenantInsideWindow`.
2. **The same edge twice with different arguments.** `a: posts(first: 1) b: posts(where: {title: "Beta"})` under each user. Each alias gets the page of its own arguments, batched apart (one statement each), and neither reads the other's rows. Task 4 pins this in `TestEdgeAliases`, Task 5 in the example's `TestAliasesBatch`.
3. **An application field on a generated type.** `extend type User { initials: String! }`, resolved by the application from `x.Name`. Column collection cannot know what the resolver reads, so a selection with a field velox did not generate reads every column. Task 5 pins this in the example's `TestAppFieldReadsAllColumns`.
4. **A cost limit dodged.** `first` given as a variable, the connection repeated under aliases, nested inside a named fragment spread many times, `first: 1000000` at five levels (no integer overflow), and inline fragments on `node`. Each is counted, and a request over `MaxCost` fails with `COST_LIMIT_EXCEEDED` before any statement; the introspection query costs nothing. Task 3 pins this in `TestCostDodges` and `TestTooCostlyRunsNothing`.
5. **Batching inside a mutation.** The extension runs a mutation's field resolvers one at a time under a lock, and the follow-ups forbid a batcher that waits for others to join. `createPost(…) { author { posts { totalCount edges { node { tags { totalCount } } } } } }` must finish, read the transaction's own writes, and send one statement per edge per level. Task 5 pins this in the example's `TestBatchInMutation`.

---

### Task 1: Core: `Statement.Each`, `Rel.LimitEach` and load caps

**Files:**
- Modify: `statement.go` (`EachClause`, `Statement.Each`), `render.go` (the windowed and joined forms), `render_cache.go` (the shape key), `db.go` (`prepare`: a Rewrite hook may not change `Each`; `rowCap`), `relation.go` (`Rel.LimitEach`, `Rel.MaxRows`), `load.go` (`loadSpec.each`, `loadSpec.maxRows`; `fetchPart` and a one-statement many-to-many path for a load with `LimitEach`).
- Create: `each_test.go` (root, SQLite).
- Modify: `render_test.go`, `render_cache_test.go`, `compilecost_test.go` (only if a row moves), `internal/itest/relations_test.go`, `internal/itest/through_test.go`.
- Modify: `doc.go` (a short `# Per-owner pages` section).

**Interfaces:**

```go
// EachClause makes a SELECT read, or count, the rows of each owner apart: the
// rows whose owner column holds one of Keys. velox sets it for a With load
// with Rel.LimitEach, for LoadEach and for CountEach; a Rewrite hook may not
// change it.
type EachClause struct {
	Keys   []any // the owners' keys, distinct
	Column int   // without Join: the column of Entity that holds an owner's key
	// Join is the join entity of a many-to-many relation; nil otherwise. The
	// statement joins it on Join.Columns[JoinTarget] = Entity.Columns[Target]
	// and partitions by Join.Columns[JoinOwner].
	Join                     *EntityInfo
	JoinOwner, JoinTarget    int
	Target                   int
	// Limit is the most rows per owner, in the statement's OrderBy; 0 is no
	// limit, and is always 0 on a Count.
	Limit int
}

// in Statement:
	Each *EachClause // SELECT: a relation read or counted per owner; a Rewrite hook may not change it

// LimitEach returns the relation loading at most n entities for each owner,
// the first in its order: each owner's own page. n <= 0 removes the limit.
func (r Rel[E, T]) LimitEach[_ Lazy[T]](n int) Rel[E, T]

// MaxRows returns the relation's load allowed up to n rows per statement, in
// place of the owner query's MaxRows and the DB's cap, for its nested loads
// too. n <= 0 restores the owner's.
func (r Rel[E, T]) MaxRows[_ Lazy[T]](n int) Rel[E, T]
```

- **The SQL** (call: a window function on every dialect; no `LATERAL`, which SQLite lacks):
  - one-to-many or one-to-one, `Limit` > 0:
    `SELECT <cols> FROM (SELECT <cols>, ROW_NUMBER() OVER (PARTITION BY "<owner col>" ORDER BY <order>) AS "vx_rn" FROM "<t>" WHERE "<owner col>" IN (…) AND <conditions>) AS "vx" WHERE "vx_rn" <= ? ORDER BY "vx_rn"`;
  - keyed by the target's one-column primary key (a many-to-one load) or `Limit` 0: no window, `… WHERE "<col>" IN (…) AND <conditions> ORDER BY <order>`, which is what a load sends today;
  - many-to-many: the inner query is `SELECT "<t>".<cols>, "vx_j"."<join owner>" AS "vx_owner", ROW_NUMBER() OVER (PARTITION BY "vx_j"."<join owner>" ORDER BY <order, qualified>) AS "vx_rn" FROM "<t>" JOIN "<join>" AS "vx_j" ON "vx_j"."<join target>" = "<t>"."<target col>" WHERE "vx_j"."<join owner>" IN (…) AND <conditions, qualified>`, and the outer query selects `"vx_owner"` last. With `Limit` 0 it is the same join without the window.
  - Every condition, the scope's and a Rewrite hook's included, renders inside the inner query. In the join form, each top-level column is qualified with the target's table (the writer's `qualify`).
- **Reading the owner's key** of a many-to-many row: through a `rows` wrapper that adds one destination, typed by `reflect.New` of the join column's type, to each `Scan`. `kit` does not change, so no entity package pays for it.
- **Row caps:** a statement with `Each.Limit` > 0 may return `Limit × len(Keys)` rows, in place of the owner query's `MaxRows` and the DB's `WithMaxRows`: it is bounded by construction. A load keyed by the target's one-column primary key may return `len(Keys)` rows. `Rel.MaxRows`, when given, wins over both.
- A With load without `LimitEach` sends what it sends today: every existing golden stays.

- [ ] **Step 1: Write the failing tests.**
  - `TestRender` gains, each on PostgreSQL, MySQL and SQLite: `each: window`, `each: keyed by the target key` (no window), `each: through` (window), `each: through, no limit`, and `each: through, a condition with EXISTS` (the aliases `vx1` and `vx_j` do not clash).
  - `TestCacheKeepsShapesApart` gains: `Each` nil, `Each` with `Limit` and without, and with `Join`, are four shapes.
  - In `each_test.go`, on the test model (invoices and items) and the root tests' through entities:
    - `TestLimitEachWith`: five invoices `With(I.Items.LimitEach(1).OrderBy(It.SKU.Desc()))` give each invoice its own top item, in one statement for the items;
    - `TestLimitEachThrough`: the same on the many-to-many relation, a target linked to two owners appearing for both;
    - `TestLimitEachCap`: on a DB `WithMaxRows(2)`, five invoices with `LimitEach(2)` load without `ErrTooManyRows`; `.MaxRows(3)` on the relation fails with it, naming the relation;
    - `TestLimitEachScoped` (Review Focus 1): under a tenant, a target row of another tenant that sorts first takes no rank, and every owner still gets `n` rows when it has them;
    - `TestRewriteMayNotChangeEach`: a Rewrite hook that clears `Each`, or changes its `Limit`, fails the load with `velox: <Entity>: a Rewrite hook changed the statement's Each`;
    - `TestLimitEachRemoved`: `LimitEach(0)` after `LimitEach(2)` sends the plain load's SQL.
  - In `internal/itest`: `TestLimitEachOnRealDatabases` (O2M, ordered by a column then the key, with `hasNext`-style `n+1`) in `relations_test.go`, and `TestLimitEachThroughOnRealDatabases` (M2M, with the tenant on the target) in `through_test.go`.
- [ ] **Step 2: Run them.** Run `go test -run 'Each|TestRender|TestCache' .`. Expected: FAIL (`LimitEach` undefined).
- [ ] **Step 3: Implement** as the Interfaces say. `prepare` compares `Each` as it compares `Lock` (pointer and fields). The render cache keys `Each` by presence, `Join`'s identity and `Limit > 0`; `Keys` are arguments, as a load's `IN` list is today.
- [ ] **Step 4: Run the root tests and the budgets.** Run `go test -count=1 ./...` and `go test -run Budget -v .`. Expected: PASS, budgets unchanged. If a `TestGenericTypesCompileSmall` row moved, re-measure it and set its bound by the 1.2% rule in the same commit.
- [ ] **Step 5: Run the full gate.** Expected: rc=0 for every step, the itests on all three databases included.
- [ ] **Step 6: Commit** `Core: Rel.LimitEach, a window per owner on every dialect, and load caps`.

### Task 2: Core: `LoadEach`, `CountEach` and `Query.Selects`

**Files:**
- Create: `each.go` (`LoadEach`, `CountEach` and their non-generic bodies).
- Modify: `query.go` (`Selects`), `render.go` (the grouped count), `each_test.go`, `render_test.go`, `internal/itest/relations_test.go`, `internal/itest/through_test.go`, `doc.go`.

**Interfaces:**

```go
// LoadEach returns, for each of owners in order, its rows of q related
// through r: q's conditions, After, Columns and scope, r's Where, and the
// order q's OrderBy, then r's, then the target's key. r's LimitEach is the
// most rows per owner, in that order, and r's With loads apply to the rows.
// One statement per chunk of owners under the dialect's parameter limit.
// q's Columns gain the relation's key columns when it leaves them out. A q
// with a Limit, an Offset or a lock is refused; a nil owner fails, naming
// the entity; an owner whose key is NULL gets no rows.
func LoadEach[E, T any](ctx context.Context, q Query[T], r Rel[E, T], owners []*E) ([][]T, error)

// CountEach returns, for each of owners in order, the number of q's rows
// related to it through r, as LoadEach would read them without a limit:
// SELECT <owner key>, COUNT(*) … GROUP BY <owner key>, one statement per
// chunk of owners. An owner with none gets 0.
func CountEach[E, T any](ctx context.Context, q Query[T], r Rel[E, T], owners []*E) ([]int64, error)

// Selects reports whether the query reads every one of cs: true for a query
// with no Columns or Omit.
func (q Query[E]) Selects[_ Lazy[E]](cs ...Column[E]) bool
```

- Both functions build `Statement`s with `Each` (Task 1), so they share its SQL, caps and guard. `CountEach`'s is `Count` with `Each`: `SELECT "<owner col>", COUNT(*) FROM "<t>" WHERE "<owner col>" IN (…) AND <conditions> GROUP BY "<owner col>"`, and in the join form `SELECT "vx_j"."<join owner>", COUNT(*) FROM "<t>" JOIN "<join>" AS "vx_j" ON … GROUP BY "vx_j"."<join owner>"`. Its row cap is `len(Keys)`.
- They are functions because they take `T` and `E` (§6f ruling 21). Their generic bodies convert the owners as `Rel.Of` does (`unsafe.Slice` of the pointers) and call one non-generic function each, so a package that calls them compiles little.
- `Selects` is a method, `[_ Lazy[E]]`. It is the "selected-columns accessor for `ConnectionOf`" of the follow-ups.

- [ ] **Step 1: Write the failing tests.**
  - `TestRender` gains `count each` and `count each: through` on all three dialects.
  - In `each_test.go`:
    - `TestLoadEach`: three invoices, `LoadEach(ctx, db.Query[Item]().OrderBy(It.SKU.Asc()), I.Items.LimitEach(2), owners)` gives each invoice its first two items, aligned with `owners`, in one statement; an invoice with none gets an empty slice;
    - `TestLoadEachAfter`: with `q.After(x)`, each owner's rows start after `x` in the order;
    - `TestLoadEachRefuses`: a `q` with `Limit`, `Offset` or `ForUpdate` fails with an error naming the entity and the method; a nil owner fails naming the entity;
    - `TestLoadEachAddsKeyColumns`: `q.Columns(It.SKU)` still groups by owner;
    - `TestCountEach`: counts aligned with owners, 0 for an owner with none, the M2M form counting each link once;
    - `TestCountEachScoped` (Review Focus 1): another tenant's rows are not counted;
    - `TestSelects`: `Selects(c)` is true without `Columns`, false for a column `Columns` leaves out or `Omit` names.
  - In `internal/itest`: `TestLoadEachOnRealDatabases` and `TestCountEachOnRealDatabases`, O2M and M2M, with the tenant.
- [ ] **Step 2: Run them.** Run `go test -run 'Each|Selects|TestRender' .`. Expected: FAIL.
- [ ] **Step 3: Implement.** `LoadEach` returns copies, one slice per owner; owners sharing a key get equal slices.
- [ ] **Step 4: Run the root tests and the budgets.** Expected: PASS, budgets unchanged, compile-cost rows within their bounds (or moved by the rule).
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `Core: LoadEach and CountEach, and Query.Selects`.

### Task 3: The request cost limit in `veloxgql`

**Files:**
- Create: `contrib/graphql/veloxgql/cost.go`, `cost_test.go`.
- Modify: `limits.go` (`MaxCost`), `errors.go` (`ErrTooCostly`), `presenter.go` (`CodeTooCostly`), `extension.go` (it is an `OperationContextMutator`; `Validate` keeps the schema; the doc's paragraph on request cost), `extension_test.go`, `limits_test.go`, `presenter_test.go`, `api_test.go`, `doc.go`.
- Modify: `example/graphql/main.go` (drop `extension.FixedComplexityLimit` and `complexityLimit`), `main_test.go` (`TestRequestCost`), `README.md`.

**Interfaces:**

```go
// in Limits:
	// MaxCost is the most a request may cost (Cost): the bound on a whole
	// request that the other limits, each of one argument, do not give. More
	// is ErrTooCostly, before any statement runs.
	MaxCost int

// DefaultLimits gains MaxCost 100000.

// Cost is what op costs under l, with vars its variables:
//   - each field costs 1, plus its children's cost times its multiplier;
//   - a connection field (one with first and last arguments whose type has
//     edges and pageInfo) multiplies by first or last, or by l.MaxPage when
//     neither is given; the edges field of a connection type multiplies by 1;
//   - the root field nodes multiplies by the number of its ids;
//   - any other field of a list of objects multiplies by l.MaxList;
//   - every branch of a fragment counts, @skip and @include are not read,
//     and __schema and __type cost nothing.
// The sum saturates at 1<<62. schema is the server's.
func Cost(schema *ast.Schema, op *ast.OperationDefinition, vars map[string]any, l Limits) int

// ErrTooCostly: a request whose Cost is over the MaxCost of the Extension's
// limits.
ErrTooCostly = errors.New("request is too costly")

// CodeTooCostly is the code of ErrTooCostly.
CodeTooCostly = "COST_LIMIT_EXCEEDED"
```

- `Extension` implements `graphql.OperationContextMutator`. `MutateOperationContext` computes `Cost` with the extension's limits and the schema `Validate` stored (`es.Schema()`; `Validate(nil)`, from `Context`, stores none, and no request reaches it). Over `MaxCost` it returns a `*gqlerror.Error` with the message `veloxgql: request is too costly: it costs <n>, and the most is <max>`, the code `CodeTooCostly` in its extensions, wrapping a coded error that wraps `ErrTooCostly`. gqlgen runs mutators before any resolver, so no statement is sent.
- A named fragment's cost is computed once per request (memoized by name), so the walk is linear in the document.
- `ErrorPresenter` shows the error with its code and does not log it.

- [ ] **Step 1: Write the failing tests** in `cost_test.go`, over a small SDL parsed with gqlparser (`User` with a `posts` connection, a `tags` list, `node` and `nodes`):
  - `TestCostConnections`: `users(first: 10) { edges { node { name } } }` costs 1 + 10 × (1 + 1 + 1) = 31; without `first`, `MaxPage` replaces 10; `last: 5` multiplies by 5; a nested `posts(first: 2) { totalCount }` under the node adds 10 × 3;
  - `TestCostVariables`: `first: $n` with `{"n": 7}` multiplies by 7;
  - `TestCostDodges` (Review Focus 4): 50 aliases of a connection cost 50 times one; a fragment spread at 30 places costs 30 times the fragment and the walk visits it once (a counter in the test); five levels of `first: 1000000` cost `1<<62`, not a negative number; `node(id:) { ... on User { posts { totalCount } } ... on Post { title } }` counts both branches;
  - `TestCostNodesAndLists`: `nodes(ids: [a, b, c])` multiplies by 3; a list edge by `MaxList`; the introspection query of `example/graphql`'s test costs 0 and `__typename` 1;
  - `TestTooCostlyRunsNothing` (Review Focus 4), in `extension_test.go`, over the test server: with `WithLimits(Limits{MaxCost: 30})`, a request of cost 31 answers `data` null and one error, code `COST_LIMIT_EXCEEDED`, message `veloxgql: request is too costly: it costs 31, and the most is 30`, and the test DB's Around hook saw no statement; cost 30 runs;
  - `TestMaxCostDefault` in `limits_test.go`: `DefaultLimits().MaxCost` is 100000, a zero `MaxCost` takes it, a negative one is refused by `Validate` naming `Limits.MaxCost`;
  - `TestTooCostlyPresented` in `presenter_test.go`;
  - `TestPublicAPI` with the five new names.
- [ ] **Step 2: Run them.** Run `(cd contrib/graphql && go test ./veloxgql/...)`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Move the example to the cost limit.** `server` drops `extension.FixedComplexityLimit`; the default `MaxCost` applies. `TestRequestCost` sends `users(first: 100) { edges { node { posts(first: 100) { edges { node { tags(first: 100) { totalCount } } } } } } }`, which costs over 100000, and expects `COST_LIMIT_EXCEEDED` and no statement; the same with `first: 5` at each level runs; the introspection query still works. The README replaces the paragraph on gqlgen's complexity limit with `MaxCost`. Run `(cd example && go test ./graphql/...)`. Expected: PASS.
- [ ] **Step 5: Run `scripts/gqlgen-latest.sh`.** Expected: `gqlgen v0.17.<n>: ok`.
- [ ] **Step 6: Run the full gate.** Expected: rc=0.
- [ ] **Step 7: Commit** `GraphQL: a request cost limit that multiplies by first and last`.

### Task 4: Batches and column collection in `gqlimpl`

**Files:**
- Create: `contrib/graphql/veloxgql/internal/shared/batch.go` (the request's table of groups), `contrib/graphql/veloxgql/gqlimpl/select.go`, `select_test.go`, `edge.go`, `edge_test.go`, and `contrib/graphql/veloxgql/batch.go`, `batch_test.go` (`veloxgql.Batch`).
- Modify: `veloxgql/extension.go` and `veloxgql/db.go` (`withState` adds the table), `gqlimpl/paginate.go` (a page registers its rows; the edge mode of `Connection`; `Page`'s doc on caps), `gqlimpl/doc.go`.

**Interfaces:**
- In `shared`, unexported to applications: a table of groups, one per operation, created by `withState` (so `Extension.Context` has one too, and with no gqlgen field context nothing is ever batched):
  - a group is a set of rows (a `[]*E`, held as `any`) that one statement read: a page, a node lookup, or a batch;
  - each group holds, per `*ast.Field` of an edge, one batch: a `sync.Once` for the rows and one for the counts, their results and their error;
  - each row pointer maps to its group; the table is guarded by a mutex.
- In `veloxgql`:

```go
// Batch makes rows one batch for their edges: the first edge field gqlgen
// resolves on any of them reads that edge for all of them, in one statement.
// The generated code batches its pages and nodes; Batch is for the rows an
// application's own resolver returns. Outside a gqlgen operation it does
// nothing.
func Batch[E any](ctx context.Context, rows []*E)
```

- In `gqlimpl`, for generated code only:

```go
// Selected returns the names of the fields selected on the objects of the
// GraphQL type typ that the field of ctx returns: through edges and node for
// a connection field, the field's own selection otherwise, with fragments
// whose type is typ or Node, and without __typename. ok is false outside a
// gqlgen field, and when the field returns neither typ nor a connection to
// it nor Node. The result is computed once per *ast.Field per operation.
func Selected(ctx context.Context, typ string) (fields []string, ok bool)

// Narrow returns q reading only those of cols that q selects; nil cols
// leave q as it is.
func Narrow[E any](q velox.Query[E], cols []velox.Column[E]) velox.Query[E]

// EdgeConnection returns c, a connection over a query of every T that
// XConnectionOf made with the edge's arguments, as the connection of x's
// edge r. When x is in a group, its page is read with the pages of every
// row of the group in one statement (velox.LoadEach, with LimitEach of the
// page size + 1 and c's order and cursor), and its totalCount with theirs
// (velox.CountEach), on the first call of either for any row; the rows
// read become a group. Otherwise its queries are c's, filtered by r.Of(x).
func EdgeConnection[E, T any](ctx context.Context, c *Connection[T], x *E, r velox.Rel[E, T]) *Connection[T]

// EdgeOne returns x's node of the to-one edge r, or nil: read with the
// other rows of x's group in one statement (LoadEach, LimitEach(1)), or
// alone with q.Where(r.Of(x)).First. q selects the target's columns.
func EdgeOne[E, T any](ctx context.Context, q velox.Query[T], x *E, r velox.Rel[E, T]) (*T, error)

// EdgeList returns x's nodes of the list edge r, in key order, batched as
// EdgeOne is, with LimitEach of MaxList + 1: an owner with more than
// MaxList fails with velox.ErrTooManyRows, in an error naming edge
// ("Post.reviews"), and the others do not.
func EdgeList[E, T any](ctx context.Context, q velox.Query[T], x *E, r velox.Rel[E, T], edge string) ([]*T, error)
```

- A batch is keyed by its group and the edge field's `*ast.Field` (`graphql.GetFieldContext(ctx).Field.Field`), so its arguments are the same for every row (one field, one set of variables), and two aliases are two batches. The first caller loads for all; the others wait on the `sync.Once`, never for one another: a batch waits for no other field to join it.
- A batch's error is every row's error for that field. The batch runs on the DB `DBFrom(ctx)` reads, under the first caller's context.
- `Connection.Page` registers its rows as a group, and in edge mode takes them from the batch; paging backward reverses pointers, not values. Its doc drops "until loads get their own cap": a base query's With loads share the page's cap unless they have `Rel.MaxRows` or `Rel.LimitEach`.

- [ ] **Step 1: Write the failing tests.** The `gqlimpl` tests use two hand-written entities with a relation (an owner and its children, and a many-to-many pair), as `veloxgql`'s `person_test.go` does, on SQLite, with an Around hook counting statements. They build gqlgen field contexts from a query parsed with gqlparser (`graphql.WithOperationContext`, `graphql.WithFieldContext`).
  - `TestSelectedConnection`: `owners { edges { node { name ...F } } }` with `fragment F on Owner { age }` gives `name, age`; two `edges` aliases merge; `TestSelectedObject`; `TestSelectedNotOurs` (ok false for a field of another type, and outside a field).
  - `TestEdgeConnectionBatches`: a page of three owners, then each owner's `EdgeConnection` page and `TotalCount`: two statements (one `ROW_NUMBER()` load, one `GROUP BY` count), and each owner's page, cursors and `hasNextPage` equal those of its own unbatched connection;
  - `TestEdgeConnectionBackwardAndAfter`: `last: 2`, and `after: <cursor>`, batched, give each owner the page its own query gives;
  - `TestEdgeConnectionAlone`: an owner in no group (a context without the table) runs its own queries, with `r.Of(x)`;
  - `TestEdgeAliases` (Review Focus 2): two field contexts of two `*ast.Field`s with different arguments make two batches, and neither's rows appear in the other's pages;
  - `TestEdgeOneBatches` and `TestEdgeListCap`: a nil foreign key gives nil without a statement of its own; one owner with `MaxList + 1` children fails naming the edge, and its siblings succeed;
  - `TestBatchErrorShared`: a failing batch gives every row the error, and runs once;
  - `TestBatchRace`, under `-race`: 50 goroutines resolve the edge of 50 rows of one group;
  - `TestBatchPublic` in `veloxgql/batch_test.go`: rows given to `veloxgql.Batch` batch their edges.
- [ ] **Step 2: Run them.** Run `(cd contrib/graphql && go test ./veloxgql/...)`. Expected: FAIL.
- [ ] **Step 3: Implement.**
- [ ] **Step 4: Run the module tests.** Run `(cd contrib/graphql && go test -race ./...)`. Expected: PASS, `TestPublicAPI` with `Batch`.
- [ ] **Step 5: Run the full gate.** Expected: rc=0.
- [ ] **Step 6: Commit** `GraphQL: batches per edge per level, and the columns a selection needs`.

### Task 5: The generated code batches and collects

**Files:**
- Modify: `contrib/graphql/internal/gogen/conn.go` (`writeEdge`, `writeConnection`; `filter` goes), `render.go` (`nodeFile`; a new `<label>ColumnsGQL` per Node type), `render_test.go`, the goldens in `internal/gogen/testdata/golden`.
- Modify: `contrib/graphql/cmd/velox-graphql/e2e_test.go` (expected Go and the many-to-many edge without an inverse).
- Regenerate: `example/graphql/gen`, `example/graphql/graph`. Modify: `example/graphql/graph/app.graphqls` and `schema.resolvers.go` (`User.initials`), `main_test.go`, `README.md`.

**Interfaces:**
- For each Node type, in its `gql_<label>.go`:

```go
// postColumnsGQL is the columns a selection of the Post fields fields reads:
// the key, every foreign key on the row, every order field's column, and the
// column of each field. It is nil, every column, when a field is not one
// velox-graphql wrote, such as an application's own.
func postColumnsGQL(fields []string) []velox.Column[Post]
```

- `XConnectionOf` gains, before anything else: a refusal, with an internal (uncoded) error naming the function, of a `q` that does not select (`q.Selects`) the key or a column of an `orderBy` term; then `if fields, ok := gqlimpl.Selected(ctx, "<Type>"); ok { q = gqlimpl.Narrow(q, <label>ColumnsGQL(fields)) }`.
- Edge methods, with their signatures unchanged:
  - connection: `c, err := <Plural>ConnectionOf(ctx, db.Query[T](), <args>)`, then `return &XConnection{c: gqlimpl.EdgeConnection(ctx, c.c, x, <Handles>.<Edge>)}, nil`;
  - unique: `return gqlimpl.EdgeOne(ctx, <narrowed db.Query[T]()>, x, <Handles>.<Edge>)`;
  - list: `return gqlimpl.EdgeList(ctx, <narrowed db.Query[T]()>, x, <Handles>.<Edge>, "<Type>.<edge>")`.
  - They no longer read `x.<Edge>`: a With load may hold a filtered or limited page (call).
- `Node` and `Nodes` narrow each type's query with `Selected` and pass the rows to `veloxgql.Batch`.
- A many-to-many edge no longer needs an inverse edge (`Rel.Of` reads the join), so `filter`'s refusal goes: the e2e model gains such an edge and it generates.

- [ ] **Step 1: Write the failing tests.**
  - In `render_test.go`:
    - `TestEdgesBatch`: every edge method calls `gqlimpl.EdgeConnection`, `EdgeOne` or `EdgeList`, and none reads `x.<Edge>.Get()`;
    - `TestColumnsGQL`: each Node type has `<label>ColumnsGQL`; its switch lists every GraphQL field of the type, getters included, and a sensitive field's column is in none of them;
    - `TestConnectionOfChecksSelects`: `XConnectionOf`'s body calls `q.Selects` before `NewConnection`;
    - `TestM2MWithoutInverse`: a model whose M2M edge has no inverse renders.
  - In the example's `main_test.go`, each with its response and its statements:
    - `TestNestedEdges` (updated): its three-level query sends four statements: the users page, one windowed posts load, one grouped posts count and one windowed tags load, each selecting only the columns asked for;
    - `TestNoNPlusOne`: the same query under `users(first: 1)` and `users(first: 3)` sends the same four statements, only their arguments differing;
    - `TestColumnsCollected`: `users { edges { node { name } } }` selects `id` and `name` only, never `password_hash`;
    - `TestAliasesBatch` (Review Focus 2): `a: posts(first: 1) b: posts(where: {title: "Beta"})` under users: one load each, and each alias its own page;
    - `TestAppFieldReadsAllColumns` (Review Focus 3): `initials` (`extend type User { initials: String! }`, resolved from `x.Name`) gives `"A"` for Ada, and that page selects every column;
    - `TestBatchInMutation` (Review Focus 5): `createPost(input: {title: "Zeta", authorID: user1, tagIDs: [label1]}) { author { posts { totalCount edges { node { title tags { totalCount } } } } } }` answers with Zeta among Ada's posts and its tag counted, and after the writes sends one statement for the author, one posts load, one posts count and one tags count;
    - `TestNodeEdgesBatch`: `nodes(ids: [user1, user2, user3]) { ... on User { posts { totalCount } } }` sends one users query and one grouped count;
    - every other test whose statements change (column lists, the M2M fallback through `Of`) is updated to the new SQL, its response unchanged.
- [ ] **Step 2: Run them.** Run `(cd contrib/graphql && go test ./internal/gogen ./cmd/velox-graphql)`. Expected: FAIL.
- [ ] **Step 3: Implement.** Regenerate the gogen goldens with `-update` and read every diff. Regenerate the example and write `initials`' resolver.
- [ ] **Step 4: Run the tests.** Run `(cd contrib/graphql && go test ./...)` and `(cd example && go test ./graphql/...)`. Expected: PASS.
- [ ] **Step 5: Run `scripts/gqlgen-latest.sh`.** Expected: `gqlgen v0.17.<n>: ok`.
- [ ] **Step 6: Run the full gate.** Expected: rc=0.
- [ ] **Step 7: Commit** `GraphQL: generated edges read in batches, and pages only the columns selected`.

### Task 6: `UpdateX` and `DeleteX` lock the row they check

**Files:**
- Modify: `contrib/graphql/internal/gogen/mutation.go` (the two `Writable().Count` checks), `render_test.go`, the goldens.
- Modify: `contrib/graphql/cmd/velox-graphql/mutation_e2e_test.go` (the statements of `TestUpdateEmpty` and the delete tests, per target).
- Regenerate: `example/graphql/gen`; update `example/graphql/main_test.go`'s statements where they change.

**Interfaces:**
- The check of an update without setters, and of a delete before its join rows go, becomes:

```go
q := db.Query[E]().Where(<pk>.EQ(key)).Writable()
if db.Dialect() != velox.SQLite {
	q = q.ForUpdate() // the row stays the caller's until the edges are written
}
if ok, err := q.Exists(ctx); err != nil { … } else if !ok { return …NotFound… }
```

- `Atomic` always runs it in a transaction, which a locking read needs. SQLite has no locking reads, and its transactions lock the database.

- [ ] **Step 1: Write the failing tests.**
  - `TestUpdateLocksTheRow` in `render_test.go`: `UpdateX` and `DeleteX` of an entity with an M2M edge call `ForUpdate()` under `Dialect() != velox.SQLite`, and `Exists`, not `Count`.
  - In `e2eMutRun`: `TestUpdateEmpty` expects, on PostgreSQL and MySQL, `SELECT 1 FROM … LIMIT 1 FOR UPDATE`, and on SQLite the same without the lock; `TestDelete`'s check the same.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement.** Regenerate the goldens and the example, and read the diffs.
- [ ] **Step 4: Run the itest on the databases.** Start the containers as `fix1.sh` does and run `(cd contrib/graphql && go test -count=1 -run TestMutationsRun -v ./cmd/velox-graphql)`. Expected: PASS on `sqlite`, `postgres` and `mysql`.
- [ ] **Step 5: Run `scripts/gqlgen-latest.sh`.** Expected: ok.
- [ ] **Step 6: Run the full gate.** Expected: rc=0.
- [ ] **Step 7: Commit** `GraphQL: UpdateX and DeleteX lock the row they check, where the dialect can`.

### Task 7: The collection itest on every dialect, through real gqlgen

**Files:**
- Create: `contrib/graphql/cmd/velox-graphql/collect_e2e_test.go` (the app, its schema, its `gqlgen.yml`, resolvers and tests, and `TestCollectRun`).
- Modify: `contrib/graphql/cmd/velox-graphql/e2e_test.go` (`app` takes the extra go.sum and tool it needs).
- Modify (not committed): `<scratchpad>/fix1.sh`.

**Interfaces:**
- The app is a module like `app(…)`'s, with `github.com/99designs/gqlgen` also a tool. Its `go.sum` concatenates the root's, `contrib/graphql`'s and `example`'s, which hold gqlgen's own dependencies; everything resolves offline from the module cache. `TestCollectRun` runs `velox-gen` with the plugin, then `go tool gqlgen generate`, then `go test -count=1 -v .` in the app, and requires `--- PASS:` for each test below and each target.
- `e2eCollectSchema`, tables named apart from the other e2e apps':
  - `Author`: tenant `org_id`; `name` with `OrderField("NAME")`; edge `books` (O2M, connection); `QueryField("authors")`.
  - `Book`: tenant `org_id`; `title` with `OrderField("TITLE")` and `WhereOps()`; edge `author` (M2O, required); edge `genres` (M2M, connection); edge `reviews` (O2M, `graphql.Connection(false)`).
  - `Genre`: `name` with `OrderField("NAME")`; edge `books`, the inverse of `Book.genres`.
  - `Review`: `stars`; edge `book` (M2O).
- Each target (SQLite always; PostgreSQL and MySQL under their variables, as `e2eMutRun`'s `targets`) seeds 2 orgs, 6 authors, 4 books each with an org's mix, 3 genres, and reviews; serves the schema with `NewExtension`, `ErrorPresenter` and a statement-recording Around hook; and runs:
  - `TestNestedPerLevel`: `authors(first: 6) { edges { node { books(first: 2, orderBy: {field: TITLE}) { totalCount pageInfo { hasNextPage } edges { node { title author { name } genres(first: 1) { totalCount edges { node { name } } } reviews { stars } } } } } } }` sends exactly seven statements (the authors page, the books load, the books count, the books' authors, the genres load, the genres count and the reviews load), and each author's books and each book's genres equal what that parent's own unbatched query returns (the test runs those queries itself, on the same DB);
  - `TestBackwardAndCursorNested`: `books(last: 2)` and `books(after: <cursor>)` under authors, batched, against each author's own pages;
  - `TestAliasesNested` (Review Focus 2);
  - `TestTenantInsideWindow` (Review Focus 1): an author whose first book by title is another org's gets its own org's first, and `totalCount` its own org's count;
  - `TestCostRefused`: a request over the default `MaxCost` answers `COST_LIMIT_EXCEEDED` and sends no statement;
  - `TestListEdgeCap`: under `WithLimits(Limits{MaxList: 2})`, a book with three reviews fails its `reviews` field naming `Book.reviews`, and the others answer.
- `fix1.sh`'s line after `export CI=true` becomes `(cd contrib/graphql && step go test -count=1 -run 'TestMutationsRun|TestCollectRun' -v ./cmd/velox-graphql)`. `ci.yml`'s `contrib/graphql` step already runs the module's tests with the database variables, and `scripts/gqlgen-latest.sh` with `CI` unset; neither changes in what it runs. The comment above the step names the collection itest too, in both files.

- [ ] **Step 1: Write the app and its tests.** Expected: `TestCollectRun` FAILS only where the code is wrong, not on setup: get the app generating and compiling first.
- [ ] **Step 2: Run it on the databases.** Start the containers as `fix1.sh` does, export its variables, and run `(cd contrib/graphql && go test -count=1 -run TestCollectRun -v ./cmd/velox-graphql)`. Expected: PASS, with `--- PASS: TestNestedPerLevel/postgres` and `--- PASS: TestNestedPerLevel/mysql` among the lines.
- [ ] **Step 3: Check the dialect differences.** If a test fails on one dialect only, fix the core or the generated code, not the test, and say what differed in the commit message.
- [ ] **Step 4: Run `scripts/gqlgen-latest.sh`.** Expected: ok (the app's gqlgen follows the module's).
- [ ] **Step 5: Run the full gate,** with the new `fix1.sh` line. Expected: rc=0.
- [ ] **Step 6: Commit** `GraphQL: the collection itest on every dialect, through gqlgen`.

### Task 8: The benchmarks

**Files:**
- Create: `example/graphql/bench_test.go`.
- Modify: `bench/scale/main.go` (`-graphql`), `scale_test.go`, `thresholds.json`, `internal/tempmod/tempmod.go` (`WriteGraphQL`), `tempmod_test.go`.
- Modify: `scripts/ci-local.sh`, `.github/workflows/ci.yml` (the GraphQL scale check, in both).

**Interfaces:**
- `example/graphql/bench_test.go`:
  - `seedBench(t testing.TB, db *velox.DB, users, posts, tags int)` seeds `users` users, `posts` posts each under acme, and `tags` tags on each post;
  - `const nested = …`: `users(first: $n) { edges { node { name posts(first: 10) { totalCount edges { node { title author { name } tags(first: 5) { edges { node { name } } } } } } } } }`;
  - `BenchmarkNestedRequest` serves `nested` through `server(db)`'s handler (`ServeHTTP` on an `httptest.ResponseRecorder`, no network) at `n` = 10 and 50, and reports `stmts/op` and `nodes/op` with `b.ReportMetric`, beside `allocs/op`;
  - `TestNestedRequestBudget`:
    - statements: exactly 5 at `n` = 10 and at `n` = 50 (users, posts load, posts count, authors, tags load): the target, flat in the number of parents;
    - allocations: `testing.AllocsPerRun(20, …)` divided by the nodes the response holds, at `n` = 50, is at most 1.1 times that at `n` = 10, so the cost grows with the rows and not with the parents;
    - under gqlgen v0.17.86 only (read with `debug.ReadBuildInfo`; another version skips this check), allocations per request at `n` = 50 are at most the recorded budget: the measured figure plus 10%, gqlgen's concurrent resolvers giving a few allocations of jitter (call). The constant's comment records the measurement, its date and the platform.
- `bench/scale -graphql`: the schemas carry `graphql.QueryField`, `OrderField` on `name` and `WhereOps` on `name`, and each service calls the root connection function and one edge method of each of its five entities. The module is written by `tempmod.WriteGraphQL(dir, repo string, files map[string]string) error`, which requires `contrib/graphql` by replace, declares velox-graphql as a tool, and concatenates the root's and `contrib/graphql`'s `go.sum`. velox-gen runs with `-plugin velox-graphql`. The thresholds' key is `"500-graphql"`, set by the existing rule (times 4x the largest of three runs, sizes and memory 1.5x) and recorded with the runs in its `_measured` note.
- The CI line, in both files, after the existing scale check: `go run ./bench/scale -n 500 -s 100 -graphql -check bench/scale/thresholds.json`.

- [ ] **Step 1: Write the failing tests.** `TestNestedRequestBudget` (with a budget of 0 until measured); `TestScaleHarnessSmallGraphQL` (n=20, s=2, `-graphql`) and `TestGraphQLServiceLinksOnlyItsEntities` (a service's binary holds the GraphQL code of its five entities and of no other, as `TestServiceLinksOnlyItsEntities` checks for the entities); `TestCommittedThresholds` requires the `"500-graphql"` key; `TestWriteGraphQL` in `tempmod_test.go`.
- [ ] **Step 2: Run them.** Expected: FAIL.
- [ ] **Step 3: Implement,** then measure: `go test -run XXX -bench Nested -benchmem ./graphql/` in `example`, three times; set the budget. Run `go run ./bench/scale -n 500 -s 100 -graphql -json` three times, waiting for a 1-minute load average under 4 as the existing note does, and record the thresholds. Record the statements and allocations of the same request, measured in a git worktree of the commit before Task 5, in the commit message and the budget's comment, so the improvement is on record.
- [ ] **Step 4: Run the tests.** Expected: PASS.
- [ ] **Step 5: Run `scripts/gqlgen-latest.sh`.** Expected: ok, the budget check skipped under the newer gqlgen.
- [ ] **Step 6: Run the full gate.** Expected: rc=0.
- [ ] **Step 7: Commit** `Bench: statements and allocations of a nested request, and the GraphQL scale variant`.

### Task 9: Documents

**Files:**
- `docs/history/specs/2026-10-19-graphql-design.md`: a section "Rulings made in plan 4", after plan 3's, listing this plan's rulings and those found while executing it. Dated notes in Constraints (bench/scale's variant, the window function on every dialect), §3 Connections (the cost limit), §4 (batching at the edge, where the rows live, aliases, cursors) and §6 (where the collection itest lives).
- `docs/history/specs/2026-10-20-api-cleanup-design.md`: a section "6g. Notes made while implementing GraphQL plan 4", listing the core additions (`Statement.Each`, `Rel.LimitEach`, `Rel.MaxRows`, `LoadEach`, `CountEach`, `Query.Selects`) and the rule each follows.
- `docs/history/plans/2026-10-14-core-1-followups.md`:
  - mark resolved: in "For GraphQL plan 4", the loads' own cap, the first/last-aware cost limit, `LoadedConnection`'s arguments, every column read, and the waiting batcher; in "Left by API plan 3", `Columns` or `Omit` on a `ConnectionOf` base query and the GraphQL plan 4 line; in "Left by GraphQL plan 3", the `FOR UPDATE` item; in "Left by API plan 5", the GraphQL plan 4 line, but for `Bidi`;
  - add "Left by GraphQL plan 4" with what this plan found and left.
- `contrib/graphql/doc.go`: a section on loading (batches per edge per level, `veloxgql.Batch`, column collection and what turns it off, the edge methods no longer reading With loads) and one on the cost limit. `veloxgql/doc.go`, `gqlimpl/doc.go`, the Extension's doc and the root `doc.go` match the code.
- `example/graphql/README.md`: the statements of a nested request, and `MaxCost`.

- [ ] **Step 1:** Edit the documents. Check every snippet against the code: `go doc` for each named symbol.
- [ ] **Step 2:** Run `go test -run TestGodocCitesNoSpec .` and `(cd contrib/graphql && go test -run TestGodocCitesNoSpec ./...)`. Expected: PASS.
- [ ] **Step 3:** Run the full gate. Expected: rc=0.
- [ ] **Step 4:** Commit `Documents: GraphQL collection, LimitEach and the cost limit`.

---

## Rulings this plan makes

Each is a call made while writing this plan. An implementer who finds one wrong stops and reports it instead of changing it.

1. **(call) Batch at the edge, not a With walk.** §4 had the root's collect function walk the selection set and add `With` loads with each nested connection's `where` and `orderBy`. Generated code cannot decode those arguments: gqlgen decodes them in the application's `graph` package, for the resolver only, and a second decoder for every where input would be large generated code. So each edge method batches on its first call: it gets the typed arguments from gqlgen, and reads the edge for every row of its group in one statement. This gives the same bound, one statement per edge per level, with no second decoder and no lookahead. The selection set is still walked, for the columns only.
2. **(call) The rows live in the request's table, not in the entity.** §4 put the eager-loaded page in `Many[T]`. Batched rows stay in the request's table of groups, and the generated edge methods no longer read `x.<Edge>`: once `Rel.LimitEach` exists, an application's With load may hold a filtered or limited page, which an edge field must not show as the edge. An application's own loads are not needed for batching any more. `gqlimpl.LoadedConnection` stays, for code generated before this plan.
3. **(call) Every alias batches.** §4's call batched only the first of two uses of one edge. With batches keyed by the field, each alias is its own batch: one statement per alias per level, which the cost limit bounds.
4. **(call) Nested `last`, `after` and `before` batch too.** §4 left a nested cursor to one query per parent. A field's cursor is one value for every parent (one field, one set of variables), so it is one more condition of the batch: `q.After(x)` inside the window. `last` reverses the order as the root does.
5. **(call) A window function on every dialect.** `ROW_NUMBER() OVER (PARTITION BY …)` in a derived table, on PostgreSQL, MySQL 8 and SQLite 3.25+. A lateral join was rejected: SQLite has none, and one shape on all three keeps one golden per case. A load keyed by the target's one-column primary key (many-to-one) needs no window. A many-to-many page joins the join table, so the window partitions by the join's owner column in one statement.
6. **(call) `Statement.Each`.** The per-owner shape is a field of the statement, so hooks and adapters see it, and the scope's conditions render inside the window: a row the tenant or a policy hides takes no rank. A Rewrite hook may not change it, as with `Lock` (§6f ruling 7).
7. **(call) The core API.** `Rel.LimitEach` and `Rel.MaxRows` are methods, and `Query.Selects` too: none needs a type parameter beyond the receiver's. `LoadEach` and `CountEach` are functions: they take both `E` and `T` (§6f ruling 21). `Query.With[T]` stays as it is: it is the "Before 1.0" follow-up's, not GraphQL's.
8. **(call) Caps.** A statement with a per-owner limit `n` may return `n` rows per owner in it, in place of the DB's and the owner query's cap: it is bounded by construction. A load keyed by the target's primary key may return one row per key. `Rel.MaxRows` gives any load its own cap. A connection's base query keeps sharing the page's cap of `n+1` with its With loads unless they set one of these; `Paginate`'s doc says so. List edges read `MaxList + 1` rows per owner, and an owner with more fails alone.
9. **(call) The columns collected.** A page reads the selected fields' columns, the key, every foreign key on the row and every order field's column. The foreign keys are cheap and let any edge or an application's With work; the order columns keep every cursor valid. A field velox-graphql did not write (an application's `extend type`) turns narrowing off for that page. Narrowing never adds a column the base query left out (`Selects`), and a base query that drops the key or a requested order column is refused with an internal error, which closes the `ConnectionOf` follow-up. A mutation's returned row is not narrowed.
10. **(call) The cost.** gqlgen's recipe, multiplying by `first`/`last`, as entgql and GitHub do: a field costs 1 plus its children's cost times its multiplier. A connection multiplies by `first` or `last` or `MaxPage`; `nodes` by its IDs; any other list of objects by `MaxList`. Fragments all count, as an upper bound; `@skip` and `@include` are not read; `__schema` and `__type` cost nothing, so introspection works. Named fragments are costed once each, and the sum saturates, so neither a fragment DAG nor huge arguments can make the check itself expensive or wrap. The default `MaxCost` is 100000: two levels of full pages pass (about 30000), three do not. The example drops gqlgen's `FixedComplexityLimit`, which counted fields, not rows.
11. **(call) The error.** `ErrTooCostly`, code `COST_LIMIT_EXCEEDED` (a new `CodeTooCostly`), returned by the extension's `MutateOperationContext`, which gqlgen runs before any resolver. A client can tell it from a bad argument.
12. **(call) `veloxgql.Batch`.** Rows an application's own resolver returns join batching through one public function; the generated pages, `node` and `nodes` batch theirs. Rows from anywhere else (a mutation's result) resolve their edges one row at a time, which is one row.
13. **(call) A batch's error and context.** The batch runs once, under the first caller's context, on the DB `DBFrom` reads; its error is every row's. On a DB with replicas a batch may read a replica other than its parents', as the per-parent queries did before.
14. **(call) A many-to-many edge needs no inverse.** The edge methods find nodes with `Rel.Of`, which reads the join table, so velox-graphql no longer refuses a many-to-many edge without an inverse edge. The where input's `has` filters keep using `HasWhere`.
15. **(call) `FOR UPDATE` in `UpdateX` and `DeleteX`.** The check before an edge-only update, and before a delete's join rows, becomes a `Writable().ForUpdate().Exists` read on PostgreSQL and MySQL, which closes the time-of-check race the follow-ups name. SQLite keeps the plain read: its transactions lock the database. `Exists` replaces `Count`, which cannot lock.
16. **(call) The collection itest runs real gqlgen,** in a `contrib/graphql` e2e app, on SQLite always and PostgreSQL and MySQL when their variables are set: batching needs gqlgen's field contexts, which only a server makes. The core's new SQL has its own itests in `internal/itest`.
17. **(call) The bench.** A request-level benchmark in `example/graphql`, whose test pins the target: the statements of a nested request do not grow with its parents, and its allocations grow with its rows only. Its allocation budget holds under the pinned gqlgen only, with 10% for the jitter of gqlgen's concurrent resolvers; the 1.2% rule stays for the core's budgets and compile-cost bounds. `bench/scale` gains the GraphQL variant the spec's Constraints ask for, with thresholds by the existing rule.
18. **(call) Versions.** `GeneratedCodeIsVersion4` stays 4 (velox-gen's output is unchanged) and `veloxgql.GeneratedCodeIsVersion1` stays 1 (every `gqlimpl` name stays, so old generated code compiles; the new code only adds calls).
19. **(call) Left out:** `Bidi` self-edges (a plugin model change, for mutations); the function form of `Query.With`; JSON append; `@goModel` enums; a caller-supplied key in create inputs; the hidden O2O child on re-point; the one-sided M2M's join rows on delete; and gRPC, migrations and the pgx split, which have their own plans.
