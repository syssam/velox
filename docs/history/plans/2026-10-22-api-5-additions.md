# API Plan 5: The Core Additions — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the core API that 1.0 still lacks, all of it additive to generated code:
- `Query.First` and `Query.Exists`, and a `One` that loads relations only for a single row;
- `Rel.Of(owners...)`, to query from loaded entities' edges;
- `velox.Sum`, `Avg`, `Min` and `Max` over a `Query` and a `NumberCol`;
- locking reads (`ForUpdate`, `ForShare`, `SkipLocked`, `NoWait`), refused outside a `Tx` and on SQLite;
- `Mutation.Conds`, `Mutation.Affected` and a typed `ValueIn(m)` on every column handle, for soft delete and audit;
- a nested `Tx` given options returns an error;
- `Op.Raw()` and `Op.Reads()`, shared with the OpenTelemetry adapter;
- the `SQL(ctx)` dialect sweep, the remaining read-path goldens, and the documents.

**Architecture:** Each read addition is a field of `Statement` that the guard, the row cap, the render cache's shape key and the renderer read: `Exists`, `Aggregate`, `Lock` and `LockWait`. A Rewrite hook may not change them, as it may not change `Writable`. `Rel.Of` resolves its relation when it is called and becomes an `IN` on the target's key, or, through a join entity, a new condition node `opThrough`. The mutation additions live in `scope_api.go` and `internal/handlegen`, which writes `handles.go`. Generated code does not change, so `GeneratedCodeIsVersion4` stays 4. Every task moves its callers in its own commit and passes the full gate.

**Tech Stack:** Go 1.27. PostgreSQL 16 (pgx v5.11.0, native and `database/sql`), MySQL 8.4 (go-sql-driver/mysql v1.10.1), SQLite (modernc.org/sqlite v1.60.1). No new dependency.

**Spec:** `docs/history/specs/2026-10-20-api-cleanup-design.md` §6 and §7.5, read with §6a–6e (the core API added so far and its style). The evidence:
- `docs/history/reviews/2026-10-20-api-review-core.md`, findings 9–13;
- `docs/history/plans/2026-10-14-core-1-followups.md`: "Left by core plan 5" (aggregates), "Left by core plan 4" (`One`'s loads), "Left by observability" (`Mutation.Value` returns `any`; no `Sum`), "Left by API plan 1" to "Left by API plan 4", and "Left by GraphQL plan 3".

## Global Constraints

- **Additive for generated code.** No generated file changes. `GeneratedCodeIsVersion4` stays 4. `bin/velox-gen -check` and `verify` pass unchanged on `internal/testmodel` and every example.
- **The core's style (§6a–6e).**
  - A method on a generic type is generic, `[_ Lazy[E]]` (or `[_ Lazy[V]]` on a handle, `[_ Lazy[T]]` on a `Rel`), as `Query.Writable` and `Query.Ordered` are.
  - "For generated code." opens the doc of generated-only names only. Nothing this plan adds is generated-only, so `TestGeneratedOnlyAPIIsMarked`'s list is unchanged.
  - **No aliases.** A replaced name is removed, and every caller moves in the same commit. Plans and reviews keep their history.
  - **Godoc cites no spec, plan or ruling.** `TestGodocCitesNoSpec` keeps passing; Task 8 tightens it.
- **Budgets and escape check unchanged.**
  - `go test -run Budget -v .`: one-row 6, tenant 7, hundred rows 105, tx 9/6, observed 7/6, metadata 19, Insert 13, replica one-row 6.
  - `go build -gcflags=-m . 2>&1 | grep "db.go:$(grep -n 'func (db \*DB) plain' db.go | cut -d: -f1):"` prints `leaking param content: st` and `read does not escape`.
  - Task 2 adds two budget lines, `First` and `Exists`, each at most the one-row figure measured in the same test. No other new path is hot enough for a line.
  - A task that moves a number stops and reports it.
- **The full gate passes at every task's commit:** `<scratchpad>/fix1.sh`, run on the committed tree, with Docker. It runs the itests on every dialect: PostgreSQL through pgx and `database/sql`, MySQL and SQLite. Every new behavior that reaches a database has an itest. `fix1.sh` needs no change.
- **CI.** `scripts/ci-local.sh` and `.github/workflows/ci.yml` do not change.
- **No machine-specific paths** in committed files. Committed text says `<scratchpad>` and `<tmp>/velox2-gocache`.
- **Shell only:** `GOCACHE=<tmp>/velox2-gocache GOFLAGS=-p=2`. The machine is short of memory and loaded: one heavy command at a time. Do not kill other processes.
- **Commits** end with a blank line and `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`. Never push.
- **Test comments do not cite this plan.** Write what a test pins, not "Review Focus N".

## Review Focus

1. **An aggregate over no matching row, or over a named number type.** PostgreSQL returns `numeric` for `SUM(bigint)`, and MySQL returns `DECIMAL` as bytes. `Sum` must give 0, and `Avg`, `Min` and `Max` must give `ok == false`, on every dialect. A `kind.Cents` column must scan into `kind.Cents`. Task 4 pins this in `TestAggregatesOnRealDatabases`.
2. **A locking read built on `db` and run inside `Tx`'s fn, or run in a savepoint.** The first fails with `ErrOutsideTx`, before any lock check. The second locks: a savepoint's queries have `InTx` set. Task 5 pins both in `TestLockInsideAndOutsideTx`.
3. **`Of` given a nil owner, an owner whose key is NULL, no owners, or the same owner twice.** A nil owner fails the statement, naming the entity. A NULL key is skipped. No owners match no row. A repeated owner is harmless. Task 3 pins this in `TestOfEdgeCases`.
4. **A soft-delete hook on a delete that said `All`.** `Conds()` is then empty, and the hook must use `All()`. The conditions must be the caller's and the earlier hooks', never the tenant's or the policy's, which `next` adds. Changing the returned slice must not change the statement. Task 6 pins this in `TestSoftDeleteThroughConds`.
5. **`ValueIn` on a nullable handle.** `SetNull()` gives `(nil, true)`, `Set(v)` gives a pointer to a copy of `v` and `true`, and `Add` gives `(nil, false)`. On an insert it records the misuse, as `Set` does. Task 6 pins this in `TestValueIn`.

---

### Task 1: `Op.Raw`, `Op.Reads`, the nested `Tx` and the sentinel docs

**Files:**
- Modify: `statement.go` (`Op.raw` becomes `Op.Raw`; `Statement.reads` becomes `Op.Reads`), `db.go`, `route.go`, `raw.go` (callers).
- Modify: `tx.go` (`Tx` refuses options when nested; its doc).
- Modify: `contrib/veloxotel/attrs.go` (`raw` and `returnsRows` go; callers use `st.Op.Raw()` and `st.Op.Reads()`).
- Test: `statement_test.go` (create if absent, package `velox_test`), `tx_test.go`, `errors_test.go`.

**Interfaces:**
- Produces:

```go
// Raw reports whether o is raw SQL: OpRawQuery or OpRawExec.
func (o Op) Raw() bool

// Reads reports whether o reads: OpSelect, or Raw's query (OpRawQuery).
// Every other Op writes, Exec's raw SQL included.
func (o Op) Reads() bool
```

- A nested `Tx` (`db.inTx`) with `len(opts) > 0` returns `velox: transaction: a nested Tx (a savepoint) takes no options; give them to the outermost Tx` and sends nothing. The check runs after the `ErrOutsideTx` and `ErrDryRun` checks and before the savepoint.

- [ ] **Step 1: Write the failing tests.**
  - `TestOpRawAndReads` in `statement_test.go`: a table over every Op. `Raw` is true for `OpRawQuery` and `OpRawExec` only. `Reads` is true for `OpSelect` and `OpRawQuery` only. `Op(0)` and `Op(99)` are neither.
  - `TestNestedTxRefusesOptions` in `tx_test.go`, on the fake executor:
    - `tx.Tx(ctx, fn, velox.TxReadOnly())` returns the text above;
    - so do `TxIsolation`, `TxTimeout` and `TxRetrySerializable(0)`;
    - fn did not run, and the fake's log has no `SAVEPOINT`;
    - `tx.Tx(ctx, fn)` still makes a savepoint.
  - `TestSentinelDocs` in `errors_test.go`: parse `errors.go` with `go/parser`. Every exported `Err…` var's doc opens with its name and ` is returned`. It passes today; it keeps the Go style.
  - Remove the old test that a nested `Tx` ignores options, if there is one (`git grep -n 'ignores options' -- '*_test.go'`).
- [ ] **Step 2: Run them.** Run `go test -count=1 -run 'TestOpRawAndReads|TestNestedTxRefusesOptions|TestSentinelDocs' .`. Expected: FAIL (`Raw` and `Reads` undefined).
- [ ] **Step 3: Implement.** Rename `raw` and `reads` and move every caller, so no unexported copy is left. In the veloxotel adapter, delete `raw` and `returnsRows` and call the core's. In `Tx`'s doc, "A nested Tx (a savepoint) ignores options" becomes "A nested Tx (a savepoint) takes no options and returns an error when given any".
- [ ] **Step 4: Check the docs §6 asks for.** `DB`'s doc says it is safe for concurrent use (`db.go`). `Tx`'s doc says a panic rolls back and panics again. Both are done; change nothing.
- [ ] **Step 5: Run the tests.** Run `go test -race -count=1 .` and `(cd contrib/veloxotel && go test -count=1 ./...)`. Expected: PASS. `git grep -n 'func (o Op) raw\|func (st \*Statement) reads\|func raw(st\|func returnsRows' -- '*.go'` prints nothing.
- [ ] **Step 6: Commit** `Op.Raw and Op.Reads; a nested Tx given options is an error`.
- [ ] **Step 7: Run the full gate** on the committed tree. Expected: rc=0.

### Task 2: `First`, `Exists`, and `One`'s loads

**Files:**
- Modify: `statement.go` (`Statement.Exists`), `query.go` (`First`, `Exists`, `One`), `guard.go`, `db.go` (`rowCap`, `prepare`'s Rewrite check), `render.go` (`selectStmt`), `render_cache.go` (`shapeKey`), `errors.go` (`ErrNotFound`'s doc).
- Test: `query_test.go`, `render_test.go`, `render_cache_test.go`, `budget_test.go`, `load_test.go`, `internal/itest/itest_test.go`.

**Interfaces:**
- Produces:

```go
type Statement struct {
	// …after Count:
	Exists bool // SELECT 1 … LIMIT 1: Exists clears Columns, OrderBy and Offset and sets Limit to 1; a Rewrite hook may not change it
}

// First runs the query for its first row, in the query's order: a query
// without OrderBy returns any row that matches. It loads the query's
// relations for that row. Its error wraps ErrNotFound when no row matches.
func (q Query[E]) First[_ Lazy[E]](ctx context.Context) (*E, error)

// Exists reports whether a row matches the query's conditions, After's
// among them, with SELECT 1 … LIMIT 1. Like Count it ignores the query's
// ordering, limit, offset, columns and relations.
func (q Query[E]) Exists[_ Lazy[E]](ctx context.Context) (bool, error)
```

- `First` sets `Limit` to 1 and keeps `Offset`, `OrderBy`, `After` and `With`.
- `Exists` renders `SELECT 1 FROM <table> WHERE … LIMIT <arg>`, with the limit an argument as today. It reads one row and does not scan it. The guard treats it as `Count` (no `ErrUnbounded`), and `rowCap` gives it no cap.
- `prepare` refuses a Rewrite hook that changes `Exists`: `velox: <E>: a Rewrite hook changed the statement's Exists`.
- `One` reads its two rows without loading. It returns `ErrNotFound` or `ErrNotSingular` before any load, and loads the relations of the single row, pinned where the read ran (`pinnedTo`). `All` and `One` share one unexported helper that returns the rows, the columns they ran with and the target.
- `ErrNotFound`'s doc: "is returned by One and First when they find no row, and by a Save's Exec when …".

- [ ] **Step 1: Write the failing tests.**
  - `TestFirst` in `query_test.go`, on the fake executor: on Postgres, `Where(I.Paid.EQ(true)).OrderBy(I.ID.Desc()).First(ctx)` sends `… ORDER BY "id" DESC LIMIT $2` with args `[true 1]` and returns the fake's row. With no rows, the error is `velox: testmodel.Invoice: not found` and matches `ErrNotFound`. With `With(I.Items)`, one load statement follows.
  - `TestExists` in `query_test.go`: on Postgres, `Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).Limit(50).Offset(5).Columns(I.Number).Exists(ctx)` sends exactly `SELECT 1 FROM "invoices" WHERE "paid" = $1 LIMIT $2` with args `[true 1]`. A fake row gives `true`, none gives `false`. With an unordered `After`, it fails as `Count` does. It does not fail with `ErrUnbounded` without a Limit.
  - `TestExistsRenders` in `render_test.go`: the same statement on MySQL and SQLite renders `SELECT 1 FROM \`invoices\` WHERE \`paid\` = ? LIMIT ?` and `SELECT 1 FROM "invoices" WHERE "paid" = ? LIMIT ?`.
  - `TestExistsHasItsOwnShape` in `render_cache_test.go`: a cached `Exists` and a cached `Limit(1).All` of the same conditions render different texts and fingerprints.
  - `TestRewriteCannotChangeExists` in `query_test.go`: a Rewrite hook that sets `st.Exists` on an `All`, and one that clears it on an `Exists`, fail with the text above.
  - `TestOneLoadsNothingWhenNotSingular` in `load_test.go`: a fake with two invoice rows and `With(I.Items)`. `One` returns `ErrNotSingular`, and the fake's log holds one statement. With one row, the load runs.
  - `TestFirstAllocationBudget` and `TestExistsAllocationBudget` in `budget_test.go`: on the one-row fake, after a warm-up run, `testing.AllocsPerRun(200, …)` of `First` and of `Exists` is at most that of the one-row `All` measured in the same test. Log the three figures.
  - `TestFirstAndExistsOnRealDatabases` in `internal/itest/itest_test.go`, through `each`: `First` with `OrderBy(I.ID.Desc())` gives invoice 5; `Where(I.ID.EQ(99)).First` matches `ErrNotFound`; `Where(I.Paid.EQ(true)).Exists` is true; `Where(I.ID.EQ(99)).Exists` is false.
- [ ] **Step 2: Run them.** Run `go test -count=1 -run 'First|Exists|NotSingular' .`. Expected: FAIL (no `First`, `Exists` or `Statement.Exists`).
- [ ] **Step 3: Implement** as in Interfaces. `shapeKey` adds `flag(st.Exists)` after `flag(st.Count)`.
- [ ] **Step 4: Run the root tests and the itests.** Run `go test -race -count=1 .`, then the itests with Docker as `fix1.sh` starts them. Expected: PASS.
- [ ] **Step 5: Check the budgets and the escape.** Expected: the numbers in Global Constraints, unchanged. Report the `First` and `Exists` figures.
- [ ] **Step 6: Commit** `First and Exists; One loads relations only for a single row`.
- [ ] **Step 7: Run the full gate** on the committed tree. Expected: rc=0.

### Task 3: `Rel.Of`

**Files:**
- Modify: `relation.go` (`Rel.Of`), `pred.go` (`opThrough`, and the walks `predErr`, `hasZero` and `resolvePreds` meet it), `render.go` (`pred`), `render_cache.go` (`nodeKey`), `guard.go` (`alwaysTrue`, `alwaysFalse`), `db.go` (`checkColumns`' walk), `scope.go` (the node walk passes `opThrough` through unchanged).
- Test: `relation_test.go`, `through_test.go`, `render_cache_test.go`, `internal/itest/relations_test.go`, `internal/itest/through_test.go`.

**Interfaces:**
- Produces:

```go
// Of is true for the entities related through r to any of owners: the
// rows of T that loading r for owners would load, without r's ordering.
// r's Where conditions apply too; its OrderBy and With, which say how to
// load, do not.
//
//	db.Query[gen.Post]().Where(gen.Users.Posts.Of(&ada)).Limit(20).All(ctx)
//
// Of reads the owners' keys when it is called, so later changes to them do
// not change the condition. It reads E's and T's metadata: inside a Policy
// or Hooks body, outside the functions it returns, use HasWhere. A nil
// owner fails the statement that holds the condition; an owner whose key is
// NULL relates to nothing; with no owners Of is false. Each owner is one
// parameter, under the dialect's limit.
func (r Rel[E, T]) Of[_ Lazy[T]](owners ...*E) Pred[T]
```

- **Representation.** `Of` resolves the link (`relRef{…}.link()`); an error becomes `Pred[T]{node{op: opInvalid, v: err}}`. It reads each owner's `ownerCol` with `fieldValue(metaOf[E], …)`, dereferences a pointer field, and skips nil.
  - **A direct relation** (`link.join == nil`) is `node{op: opIn, col: targetCol, v: keys}`: an ordinary `IN`.
  - **A many-to-many relation** is `node{op: opThrough, col: targetCol, v: link.join, w: link.joinTarget, kids: []node{{op: opIn, col: joinOwner, v: keys}}}`. It renders as `EXISTS (SELECT 1 FROM <join> AS "vxN" WHERE "vxN".<joinTarget> = <T or its alias>.<targetCol> AND <joinOwner> IN (…))`, through `w.subquery`, as `exists` does.
  - With `r.where`, the result is `opAnd` of the node and the conditions.
- A nil owner gives `opInvalid` with `velox: <E's Name>: Of was given a nil owner`.
- `nodeKey` for `opThrough` appends the join entity's `Name` and `w`, so two joins to one target never share a shape.
- `checkColumns` checks an `opThrough`'s kids against the join entity, not the statement's.
- The scope adds nothing for `opThrough`: a join entity has no tenant column or Read policy (refused when its relation resolves).
- `alwaysFalse(opThrough)` is `alwaysFalse(kid)`. `alwaysTrue(opThrough)` is false.

- [ ] **Step 1: Write the failing tests.**
  - `TestOfDirect` in `relation_test.go`, on the fake executor: `db.Query[testmodel.Item]().Where(testmodel.Invoices.Items.Of(&inv1, &inv2)).Limit(10).All(ctx)` on Postgres sends `… WHERE "invoice_id" IN ($1, $2) LIMIT $3` with args `[1 2 10]`. The inverse, `testmodel.Items.Invoice.Of(&item)`, sends `"id" IN ($1)` with the item's `InvoiceID`. Changing `inv1.ID` after `Of` does not change the args.
  - `TestOfEdgeCases` (Review Focus 3) in `relation_test.go`:
    - `Of(nil)` fails `All` with `velox: testmodel.Invoice: Of was given a nil owner`;
    - `testmodel.Tickets.Parent.Of(&ticket)` with a nil `ParentID` sends `IN` of nothing (the rendering of `In()` with no values) and returns no rows;
    - `Of()` returns no rows, and an `Update` whose only condition is it is not refused by the guard as matching every row;
    - `Of(&inv1, &inv1)` sends two parameters and returns each row once.
  - `TestOfWhere` in `relation_test.go`: `testmodel.Invoices.Items.Where(testmodel.Items.Qty.GT(0)).Of(&inv1)` sends both conditions, joined by `AND`.
  - `TestOfThroughRenders` in `through_test.go`, beside `TestThroughHasRenders` and on its entities: on Postgres and MySQL, the `EXISTS` on the join entity as in Interfaces, with the owners' keys as arguments. Nested in a `HasWhere`, the correlation names the outer alias.
  - `TestOfThroughCacheKey` in `through_test.go`: two relations through different join entities to one target, with the same number of owners, render different cached texts.
  - `TestOfOnRealDatabases` in `internal/itest/relations_test.go`, through `each`: the items of invoices 1 and 2 are items 1, 2 and 3; the invoice of item 3 is invoice 2.
  - `TestOfThroughOnRealDatabases` in `internal/itest/through_test.go`, after `throughSetup`: the tags of one book, and the books of one tag, match what `With` loads for the same owners (`bookTags`).
- [ ] **Step 2: Run them.** Run `go test -count=1 -run 'TestOf' .`. Expected: FAIL (no method `Of`).
- [ ] **Step 3: Implement** as in Interfaces. `opThrough` goes after `opInvalid` in the op list, with a comment in the list's style.
- [ ] **Step 4: Run the root tests, `FuzzRender` for 30 s, and the itests.** Run `go test -race -count=1 .`, `go test -run '^$' -fuzz '^FuzzRender$' -fuzztime 30s .`, then the itests. Expected: PASS.
- [ ] **Step 5: Check the budgets and the escape.** Expected: unchanged.
- [ ] **Step 6: Commit** `Rel.Of: query from loaded entities' edges`.
- [ ] **Step 7: Run the full gate** on the committed tree. Expected: rc=0.

### Task 4: `Sum`, `Avg`, `Min` and `Max`

**Files:**
- Create: `aggregate.go`, `aggregate_test.go`.
- Modify: `statement.go` (`Aggregate`, `Statement.Aggregate`), `guard.go`, `db.go` (`rowCap`, `prepare`'s Rewrite check), `render.go` (`selectStmt`), `render_cache.go` (`shapeKey`).
- Modify: `example/tour/main.go` (one `velox.Sum` line in the report), `example/tour/README.md`, and the tour's golden output, regenerated.
- Test: `render_test.go`, `render_cache_test.go`, `internal/itest/itest_test.go` (or a new `internal/itest/aggregate_test.go`).

**Interfaces:**
- Produces:

```go
// Aggregate is the function an aggregate read computes over its one column:
// what Sum, Avg, Min and Max set.
type Aggregate uint8

// The aggregate functions.
const (
	AggregateSum Aggregate = iota + 1 // SUM
	AggregateAvg                      // AVG
	AggregateMin                      // MIN
	AggregateMax                      // MAX
)

type Statement struct {
	// …after Exists:
	Aggregate Aggregate // SELECT SUM(c) …: Columns holds c alone; like Count, it clears OrderBy, Limit and Offset; a Rewrite hook may not change it
}

// Sum returns the sum of column c over the rows matching q's conditions,
// After's among them, as Count counts them: the tenant scope and the policy
// apply, and the ordering, limit, offset, columns and relations are
// ignored. With no row, or only NULLs, it is 0.
func Sum[E, V any](ctx context.Context, q Query[E], c NumberCol[E, V]) (V, error)

// Avg returns the average of column c over the rows Sum would add up, and
// false when there is none. The database computes it: an integer column's
// average is a decimal that MySQL rounds to four places.
func Avg[E, V any](ctx context.Context, q Query[E], c NumberCol[E, V]) (float64, bool, error)

// Min returns the least value of column c over the rows Sum would add up,
// and false when there is none.
func Min[E, V any](ctx context.Context, q Query[E], c NumberCol[E, V]) (V, bool, error)

// Max returns the greatest value of column c, as Min does.
func Max[E, V any](ctx context.Context, q Query[E], c NumberCol[E, V]) (V, bool, error)
```

- Render: `SELECT SUM("total") FROM …`, and likewise `AVG`, `MIN` and `MAX`. No `COALESCE`: NULL is handled in Go, the same on every dialect.
- Scan: into a `**V` (`**float64` for `Avg`). Both database/sql and pgx set it to nil on NULL. NULL gives `Sum` 0 and the others `false`.
- The guard treats an aggregate as `Count`, and `rowCap` gives it no cap. `prepare` refuses a Rewrite hook that changes `Aggregate`: `velox: <E>: a Rewrite hook changed the statement's Aggregate`.
- The four functions share one unexported generic runner: `q.meta.err`, `q.err`, `q.prepare`, then `run` or `plain`, as `Count` does.

- [ ] **Step 1: Write the failing tests.**
  - `TestAggregateSQL` in `aggregate_test.go`, on the fake executor and Postgres: `Sum(ctx, db.Query[testmodel.Invoice]().Where(I.Paid.EQ(true)).OrderBy(I.ID.Asc()).Limit(5), I.Total)` sends `SELECT SUM("total") FROM "invoices" WHERE "paid" = $1` with args `[true]`; `Avg`, `Min` and `Max` send `AVG`, `MIN` and `MAX`.
  - `TestAggregateNull` in `aggregate_test.go`: a fake row holding `nil` gives `Sum` 0 and nil, and the others `false` and nil. A row holding `30.0` gives `Max` `30, true`.
  - `TestAggregateRefusals` in `aggregate_test.go`: the query's `q.err` (a nil `After`) and a tenant-scoped entity without a tenant (`ErrNoTenant`) fail as `Count` fails. A Rewrite hook that clears `Aggregate` fails with the text above.
  - `TestAggregateRenders` in `render_test.go`: `SUM` on MySQL (backquoted) and SQLite.
  - `TestAggregateShapes` in `render_cache_test.go`: `Sum` and `Max` of one column, and `Sum` of two columns, are three shapes.
  - `TestAggregatesOnRealDatabases` (Review Focus 1), through `each`:
    - `Sum` of `I.Total` where paid is 90, `Avg` 30 and true, `Min` 10, `Max` 50;
    - `Sum` of `testmodel.Items.Qty` (`int64`, so `numeric` on PostgreSQL) is 7;
    - `Where(I.ID.EQ(99))` gives `Sum` 0, and `Avg`, `Min` and `Max` `false`;
    - `Sum` of `testmodel.Samples.Price` over prices 100 and 250 is `kind.Cents(350)`. The itests have no samples table: the test creates one per dialect, with the columns `testmodel.Sample`'s Desc names, as `event_test.go` creates its own.
- [ ] **Step 2: Run them.** Run `go test -count=1 -run 'Aggregate' .`. Expected: FAIL (undefined `Sum`, `Aggregate`).
- [ ] **Step 3: Implement** as in Interfaces. `shapeKey` adds `byte(st.Aggregate)` after `flag(st.Exists)`.
- [ ] **Step 4: Use `Sum` in the tour.** In `example/tour/main.go`, the report gains a line before the raw `GROUP BY`: the total of the paid invoices through `velox.Sum`. The `GROUP BY` stays `Raw`, as the core has no `GroupBy`. Regenerate the golden with the tour's update flag. Read the diff: only the new line.
- [ ] **Step 5: Run the root tests, the example and the itests.** Expected: PASS.
- [ ] **Step 6: Check the budgets and the escape.** Expected: unchanged.
- [ ] **Step 7: Commit** `Sum, Avg, Min and Max over a Query and a NumberCol`.
- [ ] **Step 8: Run the full gate** on the committed tree. Expected: rc=0.

### Task 5: Locking reads

**Files:**
- Modify: `statement.go` (`Lock`, `LockWait`, the two `Statement` fields), `query.go` (four methods; `All` refuses `With`), `guard.go`, `db.go` (`prepare`'s Rewrite check), `render.go` (`selectStmt`), `render_cache.go` (`shapeKey`).
- Test: `lock_test.go` (create), `render_test.go`, `render_cache_test.go`, `internal/itest/lock_test.go` (create).

**Interfaces:**
- Produces:

```go
// Lock is the lock a locking read takes on the rows it reads.
type Lock uint8

// The locks.
const (
	LockForUpdate Lock = iota + 1 // FOR UPDATE
	LockForShare                  // FOR SHARE
)

// LockWait is what a locking read does about rows another transaction has
// locked. The zero value waits for them.
type LockWait uint8

// The ways of not waiting.
const (
	LockSkipLocked LockWait = iota + 1 // SKIP LOCKED: leave them out
	LockNoWait                         // NOWAIT: fail at once
)

type Statement struct {
	// …after Aggregate:
	Lock     Lock     // SELECT: ForUpdate or ForShare; 0 for a read that locks nothing; a Rewrite hook may not change it
	LockWait LockWait // SELECT: SkipLocked or NoWait; a Rewrite hook may not change it
}

// ForUpdate returns the query locking the rows it reads until the
// transaction ends, as SELECT … FOR UPDATE: another transaction cannot
// change or lock them. A locking read runs only inside Tx, on PostgreSQL
// and MySQL; SQLite has none, as its transactions lock the database. It
// loads no relations, and Count, Sum, Avg, Min and Max refuse it.
func (q Query[E]) ForUpdate[_ Lazy[E]]() Query[E]

// ForShare returns the query taking a shared lock on the rows it reads, as
// SELECT … FOR SHARE: other transactions may read and share-lock them, but
// not change them. The rules of ForUpdate apply.
func (q Query[E]) ForShare[_ Lazy[E]]() Query[E]

// SkipLocked returns the locking query leaving out the rows another
// transaction has locked, as SKIP LOCKED: a queue's workers each take rows
// no other worker holds. It needs ForUpdate or ForShare.
func (q Query[E]) SkipLocked[_ Lazy[E]]() Query[E]

// NoWait returns the locking query failing at once, rather than waiting,
// when a row it reads is locked, as NOWAIT. DriverCode gives the failure's
// code: PostgreSQL's 55P03, MySQL's 3572. It needs ForUpdate or ForShare.
func (q Query[E]) NoWait[_ Lazy[E]]() Query[E]
```

- The last of `ForUpdate` and `ForShare` wins, and so does the last of `SkipLocked` and `NoWait`.
- Render: after `LIMIT` and `OFFSET`, ` FOR UPDATE` or ` FOR SHARE`, then ` SKIP LOCKED` or ` NOWAIT`. The text is the same on PostgreSQL and MySQL (8.0.1 and later). `shapeKey` adds `byte(st.Lock), byte(st.LockWait)`.
- **The guard** (the caller's statement, before the hooks) refuses, in this order, each with `velox: <E>: ` before it:
  - `LockWait` without `Lock`: `SkipLocked and NoWait need ForUpdate or ForShare`;
  - `Lock` on SQLite: `SQLite has no locking reads; its transactions lock the whole database`;
  - `Lock` without `InTx`: `a locking read (ForUpdate or ForShare) runs only inside Tx`;
  - `Lock` with `Count` or `Aggregate`: `Count, Sum, Avg, Min and Max cannot lock rows; lock them with All, One, First, Iter or Exists`.
- `All` refuses a locking query with `With`: `velox: <E>: a locking read loads no relations; load them in another query`. `Iter` already refuses `With`.
- `prepare` refuses a Rewrite hook that changes `Lock` or `LockWait`: `velox: <E>: a Rewrite hook changed the statement's lock`.
- `ErrOutsideTx` runs before the guard (`prepare`), so a locking query built on `db` and run under fn's ctx gets it, not the guard's text.

- [ ] **Step 1: Write the failing tests.**
  - `TestLockRenders` in `render_test.go`, on Postgres and MySQL: `Where(I.ID.EQ(1)).Limit(1).ForUpdate()` ends in ` LIMIT $2 FOR UPDATE`; `ForShare().SkipLocked()` ends in ` FOR SHARE SKIP LOCKED`; `ForUpdate().NoWait()` ends in ` FOR UPDATE NOWAIT`; with `Offset(5)`, the lock follows ` OFFSET`.
  - `TestLockShapes` in `render_cache_test.go`: the plain query and its four locked forms are five shapes, and the cache never returns one shape's text for another.
  - `TestLockRefusals` in `lock_test.go`, on the fake executor, each with its exact text: `SkipLocked()` alone; `ForUpdate()` on SQLite inside `Tx`; `ForUpdate()` outside `Tx` on Postgres; `ForUpdate().Count`; `Sum` of a `ForUpdate()` query; `ForUpdate().With(I.Items).All`; a Rewrite hook that sets `st.Lock`. The fake's log holds no statement for any of them.
  - `TestLockInsideAndOutsideTx` (Review Focus 2) in `lock_test.go`: inside `db.Tx`, `tx.Query…ForUpdate()` runs, as do `First` and `Exists` with it, and the same query in a nested `tx.Tx` runs. A query built on `db` and run under fn's ctx fails with `ErrOutsideTx`. On a DB with a replica, a locking read in `Tx` runs on the transaction's connection.
  - `TestLocksOnRealDatabases` in `internal/itest/lock_test.go`, through `each`. On SQLite the first refusal above is the expected result, and the test stops there. On PostgreSQL and MySQL, two transactions on two connections, coordinated by channels, with a test deadline:
    - transaction A reads invoice 1 with `ForUpdate()` and waits;
    - transaction B's `Where(I.ID.In(1, 2)).ForUpdate().SkipLocked()` returns invoice 2 only;
    - B's `Where(I.ID.EQ(1)).ForUpdate().NoWait()` fails, and `velox.DriverCode` gives `55P03` on PostgreSQL and `3572` on MySQL;
    - B's `ForShare()` of invoice 2 succeeds while A holds invoice 1;
    - A commits, and B's `ForUpdate()` of invoice 1 then succeeds.
- [ ] **Step 2: Run them.** Run `go test -count=1 -run 'Lock' .`. Expected: FAIL (undefined `ForUpdate`, `Lock`).
- [ ] **Step 3: Implement** as in Interfaces.
- [ ] **Step 4: Run the root tests and the itests.** Expected: PASS, and `TestLocksOnRealDatabases` ran on every target (`-v`).
- [ ] **Step 5: Check the budgets and the escape.** Expected: unchanged.
- [ ] **Step 6: Commit** `Locking reads: ForUpdate, ForShare, SkipLocked and NoWait`.
- [ ] **Step 7: Run the full gate** on the committed tree. Expected: rc=0.

### Task 6: `Mutation.Conds`, `Mutation.Affected` and `ValueIn`

**Files:**
- Modify: `scope_api.go` (`Conds` and `Affected` added; `Value` and `Result` removed; `mutation.res` becomes `affected int64`), `hook.go` and the writes that set it (`write.go`, `insert.go`).
- Modify: `internal/handlegen/handlegen.go` (`ValueIn` on every kind with `Set`); `handles.go`, regenerated with `go generate`.
- Modify: callers of `Value` and `Result`: `internal/testmodel/ticket.go`, `example/hooks/gen/task_hooks.go` (hand-written; velox-gen does not own it), `example/hooks/README.md`, `hook_test.go`, `hooks_internal_test.go`, `schema_policy_internal_test.go`.
- Modify: `oldapi_test.go` (the removed names).
- Test: `hook_test.go`, `hooks_internal_test.go`.

**Interfaces:**
- Produces:

```go
// Conds returns the conditions of an update or a delete: the caller's Where
// and those the hooks before this one added, in order, as copies (changing
// the slice changes nothing). It holds neither the tenant's nor the
// policy's conditions, which next adds. It is empty only when the
// statement said All, and nil for an insert. A soft delete is a delete hook
// that does not call next:
//
//	q := m.DB().Update[Task]().Set(Tasks.DeletedAt.Set(now))
//	if cs := m.Conds(); len(cs) > 0 {
//		q = q.Where(cs...)
//	} else {
//		q = q.All()
//	}
//	_, err := q.Exec(ctx)
//	return err
//
// The delete then reports 0 rows: it sent nothing.
func (m *Mutation[E]) Conds[_ Lazy[E]]() []Pred[E]

// Affected returns the rows the write inserted, matched or deleted, once
// next has returned. It is 0 before then, after next returned an error (an
// insert sets it only when every statement succeeded and no upsert was
// denied), and under SQL(ctx), which sends nothing.
func (m *Mutation[E]) Affected[_ Lazy[E]]() int64
```

- On each handle with `Set` (`Col`, `OrderedCol`, `NumberCol`, `StringCol` and `JSONCol`):

```go
// ValueIn returns the value m's update assigns to the column with Set, and
// whether it assigns one; an Add or Sub assigns no value. On an insert it
// records an error, as Mutation's Set does: read Rows.
func (c NumberCol[E, V]) ValueIn[_ Lazy[V]](m *Mutation[E]) (V, bool)
```

- On each nullable handle (`NullCol`, `NullOrderedCol`, `NullNumberCol`, `NullStringCol` and `NullJSONCol`), the same, returning `(*V, bool)`: `SetNull` gives `(nil, true)`, and `Set(v)` gives a pointer to a copy of `v`.
- The bodies call two unexported helpers in `scope_api.go`, `valueIn[E, V any](m *Mutation[E], col int32) (V, bool)` and `nullValueIn[E, V any](…) (*V, bool)`, over the existing `assigned`.
- `errInsertMutation`'s text: `Set and Where are for updates and deletes; change Rows`. Its comment says the misuse is that of `ValueIn`, `Set` or `Where`.

- [ ] **Step 1: Write the failing tests.**
  - `TestValueIn` (Review Focus 5) in `hooks_internal_test.go`, replacing `TestMutationValue`: an update hook reads `hn.Title.ValueIn(m)` as the title set, `string`-typed; `hn.N.ValueIn(m)` after `hn.N.Add(1)` gives `0, false`. On a nullable handle: `SetNull()` gives `nil, true`; `Set("x")` gives a pointer to `"x"` and `true`, and changing the pointee does not change the statement; no assignment gives `nil, false`. On an insert, `ValueIn` fails the write with `errInsertMutation`'s text.
  - `TestConds` in `hook_test.go`: an update with `Where(a, b)`, under a first hook that adds `c`, shows the second hook `a, b, c` in order. Appending to or overwriting the returned slice changes neither the statement's conditions nor the next call's. An insert gives nil.
  - `TestSoftDeleteThroughConds` (Review Focus 4) in `hook_test.go`, on a test entity declared in that file as its others are, tenant-scoped, with a `deleted` bool column and a delete hook that updates `deleted` through `m.DB()` and `m.Conds()` (or `All()` when empty) without calling next:
    - `Delete…Where(id = 1).Exec` sends one `UPDATE … SET "deleted" = … WHERE "id" = … AND "org_id" = …` (the tenant once, added by the update's own scope), no `DELETE`, and returns 0;
    - `Delete….All().Exec` sends an `UPDATE` with the tenant condition only.
  - `TestMutationAffected` in `hook_test.go`, replacing `TestMutationResult` and `TestMutationResultAfterAnError`: the same cases, through `Affected`.
  - `oldapi_test.go`: add `^func \(m \*Mutation\[E\]\) Value\[` and `^func \(m \*Mutation\[E\]\) Result\[` to the names that must be gone; add `^func \(m \*Mutation\[E\]\) Conds\[` to the kept list.
  - The handles test that compares `handles.go` with handlegen's output keeps passing after `go generate`.
- [ ] **Step 2: Run them.** Run `go test -count=1 -run 'ValueIn|Conds|SoftDelete|MutationAffected|OldAPI' .`. Expected: FAIL (undefined `ValueIn`, `Conds`, `Affected`).
- [ ] **Step 3: Implement** as in Interfaces, then run `go generate` in the module root to rewrite `handles.go`.
- [ ] **Step 4: Move the callers.** `ticket.go`'s `needsTitle` reads `Tickets.Title.ValueIn(m)`; `task_hooks.go`'s update hook reads `Tasks.Title.ValueIn(m)`, and the README says so. Then `git grep -n '\.Value(\|\.Result()' -- '*.go' '*.md' ':!docs/history/plans' ':!docs/history/reviews' ':!docs/history/specs'` prints no use on a `Mutation`.
- [ ] **Step 5: Check generated code.** Run `bin/velox-gen -check` and `verify` on `internal/testmodel`, and `go tool velox-gen -check` on `example/hooks`. Expected: up to date; no generated file changed.
- [ ] **Step 6: Run the root tests, the example and the itests.** Expected: PASS. Check the budgets and the escape: unchanged.
- [ ] **Step 7: Commit** `Mutation.Conds and Affected; ValueIn on every column handle`.
- [ ] **Step 8: Run the full gate** on the committed tree. Expected: rc=0.

### Task 7: The dialect sweep

**Files:**
- Test: `policy_test.go`, `render_test.go`. No non-test file changes, unless a golden finds a bug: then fix it in the same commit and say so in the message.

**Interfaces:**
- Consumes: `OpenFake(d, e)` for each of `velox.Postgres`, `velox.MySQL`, `velox.SQLite`; the policy test entities of `policy_test.go` (`pnote`, `pnoteReading`, `pnoteWriting`).

- [ ] **Step 1: Write the tests.**
  - `TestPolicySQLOnEveryDialect` in `policy_test.go`, a table over the three dialects:
    - a query whose Read policy returns `velox.Allow[pnote]()`: `SQL(ctx)` equals the unscoped query's text on that dialect, written out as a golden per dialect;
    - a Read policy returning the zero `Pred`: `SQL(ctx)` fails with the message `TestPolicyZeroPredFails` asserts, on each dialect;
    - an `Update`'s `SQL(ctx)` with a zero Write `Pred`: the Write message, on each dialect.
  - In `render_test.go`, MySQL and SQLite goldens for every read-path case of `TestRender`'s table that asserts PostgreSQL only. Add an OR between siblings (`Or(a, b), c`), and the `NEQ`, `GTE`, `LT` and `LTE` operators, on all three.
  - In `render_test.go`, `TestNewClausesOnEveryDialect`: one golden per dialect for `Exists`, `Sum`, an `Of` through a join, and `ForUpdate().SkipLocked()` (Postgres and MySQL; SQLite's refusal). Tasks 2–5 pinned each piece; this pins them side by side.
- [ ] **Step 2: Run them.** Run `go test -count=1 -run 'TestPolicySQLOnEveryDialect|TestRender|TestNewClausesOnEveryDialect' .`. Expected: PASS. A failure is a bug in the renderer or the scope: fix it, and say which in the commit message.
- [ ] **Step 3: Commit** `SQL(ctx) with a policy, and the read-path goldens, on every dialect`.
- [ ] **Step 4: Run the full gate** on the committed tree. Expected: rc=0.

### Task 8: Documents and the citation scan

**Files:**
- Modify: `godoc_test.go` (the root's `TestGodocCitesNoSpec`), and any comment it then finds.
- Modify: `doc.go`, checked against the code: a section on reads names `First`, `Exists`, `Sum`/`Avg`/`Min`/`Max`, `Of` and the locking reads; the hooks section names `Conds`, `Affected` and `ValueIn`.
- Modify: `docs/history/specs/2026-10-20-api-cleanup-design.md`: Status "API plans 1–5 implemented"; a §6f listing this plan's rulings and any found while executing it.
- Modify: `docs/history/plans/2026-10-14-core-1-followups.md` (see Step 3).

- [ ] **Step 1: Align the citation scan.** The root test checks each comment group (`cg.Text()`), as `contrib/graphql`'s does, not each line. Its pattern adds `\b(GraphQL|API|core|policy|schema)\s+plan\s+\d`. Run `go test -count=1 -run TestGodocCitesNoSpec .`. Fix each comment it finds, saying what the cited text said. Expected then: PASS.
- [ ] **Step 2: Edit `doc.go`.** Check each named symbol with `go doc`.
- [ ] **Step 3: Edit the follow-ups.**
  - Mark resolved: "`Sum` and `Avg` on `NumberCol` await aggregates"; "`One` loads relations for the two rows it reads"; "There is no `Sum`, `Avg` or `GROUP BY`" (resolved but for `GROUP BY`, which stays `Raw`); "`Mutation.Value` returns `any`"; API plan 1's dialect-sweep item and its `Op.Raw()`/`Op.Reads()` item; API plan 3's citation-scan alignment item; the read-path goldens item under "Still open from before core plan 3".
  - Under "Left by GraphQL plan 3": the `FOR UPDATE` item now has the core's `ForUpdate`, for GraphQL plan 4 to adopt where the dialect has it. The `clientFoundRows` item moves to the pgx split (ruling 10).
  - Under "1.0 release": the `apidiff` job (ruling 11), with what it compares and when it starts.
  - Add a "Left by API plan 5" list with what execution leaves, and ruling 12's items.
- [ ] **Step 4: Edit the spec.** Status line and §6f. Each ruling below that stood becomes a line, plus any that execution changed, saying what changed.
- [ ] **Step 5: Commit** `Documents: the core additions before 1.0`.
- [ ] **Step 6: Run the full gate** on the committed tree. Expected: rc=0.

---

## Rulings this plan makes

Each is a call made while writing this plan. An implementer who finds one wrong stops and reports it instead of changing it.

1. **(call)** `Exists` is a `Statement` field, rendered `SELECT 1 … LIMIT 1`, not a one-column `All`. A hook and the adapter see what the statement is, and the shape key tells it apart. `First` adds no ordering: an unordered `First` is any matching row, as ent's is.
2. **(call)** `One` checks its row count before it loads relations. That closes the follow-up of core plan 4. Its results do not change.
3. **(call)** `Rel.Of` resolves its relation and reads the owners' keys when called. Like `In`, it holds its own copy of the values. Unlike `HasWhere`, it therefore reads metadata, and its doc says to use `HasWhere` in Policy and Hooks bodies. `r.Where` applies; `r.OrderBy` and `r.With` do not, as a condition has no order. A many-to-many `Of` is the new node `opThrough`, keyed in the render cache by its join entity.
4. **(call)** The aggregates take `NumberCol` only. A Go function cannot take either of two struct types and still infer `V`. A sealed interface over `NumberCol` and `NullNumberCol` would need explicit type arguments, or method-based inference over an unexported method. A nullable column's aggregate goes through `Raw` until one is asked for, and can be added later. `GroupBy` stays out (the review's "nice-to-have"); the tour keeps its `Raw` `GROUP BY`.
5. **(call)** `Sum` returns `(V, error)`, 0 on no row. `Avg`, `Min` and `Max` return `(…, bool, error)`, false on no row: no zero is a safe stand-in for a missing minimum. `Avg` is `float64`. NULL is handled in Go through a `**V` scan, not with `COALESCE`, so the SQL is the same shape on every dialect.
6. **(call)** Locks are two fields, `Lock` and `LockWait`, with typed constants, not a bit set: an invalid combination cannot be written. The last call of each pair wins. A wait without a lock, SQLite, a read outside `Tx`, and `Count` or an aggregate (PostgreSQL refuses `FOR UPDATE` with aggregates) are refused by the guard, with plain errors: they are programming errors, not sentinels (§6a ruling 4's reasoning). `With` is refused as the review proposed, since the loads would not be locked. A lock failure has no sentinel; `DriverCode` gives `55P03` and `3572`.
7. **(call)** A Rewrite hook may not change `Exists`, `Aggregate`, `Lock` or `LockWait`, as it may not change `Writable`. The guard checked them, and the scan depends on the first two. `Count` stays as it is.
8. **(call)** `Mutation.Value` and `Mutation.Result` are removed. `ValueIn` and `Affected` replace them, and every caller moves in Task 6. Keeping both would be the duplicate the no-alias rule forbids, and nothing is released. `Conds` returns nil on an insert without recording a misuse, as `Rows` returns nil on an update.
9. **(call)** A soft-delete hook does not call `next`, and the delete reports 0 rows. Letting a hook change a write's kind or count is a larger design and not asked for. `Conds`' doc shows the pattern, `All` included.
10. **(call)** No `clientFoundRows` check at `Open`. `Open` takes a `*sql.DB`, and neither database/sql nor go-sql-driver/mysql exposes the DSN or the client flags behind it. A probe needs a table and sends a statement from a function that sends none. The pgx split reshapes `Open(d, Conn)`, so it can take the MySQL configuration and refuse a missing flag. The item moves there. `Open`'s doc already says to set it.
11. **(call)** `apidiff` is not wired now. There is no tag to compare with. A check against `main` would fail on the breaking changes still planned (the pgx split reshapes `Open`). The "1.0 release" follow-ups gain the job: after the first `v1.0.0` tag, CI runs `golang.org/x/exp/cmd/apidiff`, pinned, for the root module, `contrib/veloxotel` and `contrib/graphql` against the latest `v1` tag, and fails on an incompatible change. The generated-only names (`Desc`, `ColumnDesc`, …, `gqlimpl`) are outside semver and are reported, not failed.
12. **(call)** Taken from the follow-ups: `Op.Raw()`/`Op.Reads()` (from API plan 4); the `SQL(ctx)` dialect sweep (from API plan 3); the read-path goldens (open since before core plan 3); `One`'s loads (core plan 4); the root citation scan's alignment (API plan 3). Left to their plans, and said so in the follow-ups:
    - GraphQL plan 4: the first/last-aware cost limit, `LoadedConnection`, a selected-columns accessor for `ConnectionOf`, `Bidi` self-edges, and adopting `ForUpdate` in `UpdateX`;
    - the migration planner: the derived index-name cap, `ColumnInfo.Nullable` for interface JSON fields, and MySQL's `Index.Where`;
    - the pgx split: `clientFoundRows`, `Save`'s `cols` allocation, and the core's pgx dependency;
    - velox-gen: rule errors without positions, mixin paths, plugin panic stacks, Unicode case folding;
    - otel: `TxInfo`'s route.
    Left with no plan: the LIKE escape's dependency on server settings. `HasPrefix`, `HasSuffix` and `Contains` can change their escape character later without breaking anything; `Like`'s documented `\` would break, and it is not query-API work. Also left: the review's godoc nits (`type Mutation[E any] mutation`, `Option func(*config)`, `ColumnInfo.Codec`'s long doc).
13. **(call)** The four new read fields are the only `Statement` changes. Exported types `Aggregate`, `Lock` and `LockWait` give hooks and adapters typed values. None is "For generated code", and none touches the generated files, so `GeneratedCodeIsVersion4` stays 4.
14. **(call)** Only `First` and `Exists` get budget lines. Each is held to the one-row `All`'s count, measured in the same test, rather than to a fixed number, so the line moves with the one-row budget. Aggregates, `Of` and locking reads share the measured paths of `Count`, `In` and `All`.
15. **(call)** A nested `Tx` refuses every option, `TxRetrySerializable(0)` included. Telling a harmless option from a harmful one would need the opaque `TxOption` to be read, and the outermost `Tx` is where options belong. The §6 docs on `DB`'s concurrency, `Tx`'s re-panic and the sentinels' Go style are already done; Task 1 checks them and adds `TestSentinelDocs` to keep the sentinels so.
