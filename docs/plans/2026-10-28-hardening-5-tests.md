# Pre-1.0 Hardening, Wave 5: tests — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before 1.0, close the test gaps the triage and Waves 2–4 left, and add the harness the quality backlog asked for:
- the triage's TEST rows of this wave: C18, C31, C38, C39, C44, C45, C46, C62, C64–C68, C92–C96, C98; G5, G8, G34, G46, G61; M16, M17, M20;
- M15, the planner's property test, built last of the tests (spec "Tests and harness"; builder ruling 17);
- Wave 3's and Wave 4's leftovers for this wave;
- the quality backlog's Wave 5 items: more fuzz targets, per-package coverage floors, and committed benchmark baselines compared with benchstat;
- bench/scale's build times measured against a reference build in the same run, not in absolute seconds.

**Architecture:** Seventeen tasks, each one commit that passes its check.
- Tasks 1–5 are root-package tests (`velox_test` and `velox`). Task 1 goes first: it makes the fake return errors, which Task 1's uint64 case needs. Tasks 2–5 are independent.
- Task 6 is `internal/itest` (D). Tasks 7–8 are the examples' migrations and the SQLite ent fixture. Task 9 is the migrate leftovers (D).
- Tasks 10–11 are contrib/graphql (and Task 10's G61 half touches the root package and contrib/veloxotel).
- Task 12 adds fuzz targets; Task 13 adds M15 and FuzzDiff, which share one generator.
- Tasks 14–16 change the harness: bench/scale's relative times, benchmark baselines, coverage floors. Task 16 runs last but the gate, so its floors are measured on the wave's final tests.
- Task 17 is the gate.
- Production code changes only in `bench/scale` (Task 14) and the new `scripts/covfloor` checker (Task 16). Everything else is tests, testdata, example programs, CI scripts and committed migration directories. No velox-gen output changes, and no generated-code version moves.

**Tech Stack:** Go 1.27. pgx v5.11.0, go-sql-driver/mysql v1.10.1, modernc sqlite v1.60.1 (go.mod). golangci-lint v2.14.0, revive v1.17.0 (`scripts/ci-local.sh`). gqlgen as the contrib/graphql module pins it. benchstat from `golang.org/x/perf` at `v0.0.0-20261009192801-be2c69fb417e` (the latest on 2026-10-28, `go list -m golang.org/x/perf@latest`). ent v0.14.6 only to make Task 8's fixture, outside the repo. Docker for PostgreSQL and MySQL (docker-compose.ci.yml).

**Spec:** `docs/specs/2026-10-25-pre-1.0-hardening-design.md`. Its final block, **"Rulings on the builder check"**, is binding and overrides the rulings above it.
- "Builder ruling N" is item N of the final block. Ruling 17 governs this wave: M15 has a budget of 60 s in the gate, with fixed seeds, on SQLite; PostgreSQL and MySQL run in CI. M16 and M17 stay. G32 is deferred.
- The evidence for each id is in `docs/reviews/2026-10-25-triage-{C,G,M}.md`. C44's list is `docs/plans/2026-10-14-core-1-followups.md:85`.
- The quality backlog is the user's 2026-10-27 proposal (fuzz, coverage, benchstat; folded into Waves 5–6).
- Every file:line below was read at 47c5edd.

**Scope.**
- **This wave:** C18, C31, C38, C39, C44, C45, C46, C62, C64, C65, C66, C67, C68, C92, C93, C94, C95, C96, C98; G5, G8, G34, G46, G61; M15, M16, M17, M20.
- **Done earlier, recorded here with evidence:**
  - C100, N2, C44's fuzz anchors, G21 and G22 landed in Wave 0 (plan 1). C37 landed in Wave 2 Task 5.
  - **C9** is closed by `TestCacheKeysMoreShapes` (render_cache_test.go:183), which pins Limit, Offset, OrderBy's order and nested And/Or as shape keys. No change.
  - **C44's "fuzz regex"** is closed: ci.yml:63 and ci-local.sh:42 run `-fuzz '^FuzzScope$'`.
  - **C44's "compares only up to the shorter length"** is closed: `SamePreds` checks the lengths first (export_test.go:445).
- **Deferred by the spec:** G32 (builder ruling 17).
- **Earlier plans' leftovers taken here:**
  - Wave 3 (docs/plans/2026-10-27-hardening-3-graphql.md:852, :880): G5, G8, G46, G34. From its execution: an e2e with a Write-only Profile policy (a readable but unwritable new child, a Write-hidden current child); `TestRequiredSelfEdge` additions (an owning prevID update, create's childIDs and nextID, SET NULL through an executed GraphQL query, gqlgen over the mutation schema); bench/scale measured against a reference build in the same run.
  - Wave 4 (docs/plans/2026-10-28-hardening-4-migrate.md, "Left by this plan (execution)"): the PostgreSQL stepwise path under a changed search_path; a quoted or dotted schema name; a domain over a user domain names only the nearest domain.
  - Wave 2 left nothing for this wave.

## Global Constraints

- **Shell.** Run `export GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2` before any go command.
  - The machine is memory-constrained and shared. Run one heavy command at a time: never two `go test` runs, two lints, or a lint beside a test. Do not kill other processes.
  - If golangci-lint's lock is held, run it with `--allow-parallel-runners`.
- **The per-task check (C).** Every task ends with it, on its committed tree.
  - Lint: `go vet ./... && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` in every module the task touches (root, `example`, `contrib/veloxotel`, `contrib/graphql`).
  - Tests: `go test -race -count=1 -timeout 20m ./...` in every module the task touches. It runs under D where the task says so.
  - G and the goldens' `-update` run only where the task names them.
  - The full gate, `bash scripts/ci-local.sh`, runs once, in Task 17.
  - On a failure, fix the cause, stage by name, `git commit --amend --no-edit`, then run C again.
- **Commits.**
  - The message is `Area: what changed (ids)`. It ends with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - Stage files by name with `git add <paths>`, never with `git add -A` or `.`, and never stage `.DS_Store`.
  - `git status --short` lists what a task changed, and every path in it but `.DS_Store` is staged.
  - Never push.
- **A test that fails on today's code is a finding, not a test to weaken.** These tasks pin behavior. Where a step says "Expected: PASS" and the run fails, stop and report the failure to the controller with the output; do not change production code, except where a task says to.
- **Fuzz is time-boxed.** Every fuzz target runs in CI and in ci-local.sh as `go test -run XXX -fuzz '^Name$' -fuzztime 10s <pkg>`, anchored. A crasher the short run finds is a finding: commit its `testdata/fuzz` file only with the fix the controller rules on.
- **Lint and revive stay at 0.**
- **Greps.** Apple's git greps with BSD regex, where `-E` never matches `\b`. Every grep here is `git grep -P`.
- **Test names.** Before adding a test function, run `git grep -nP 'func <Name>\(' -- '*.go'`. Expected: nothing. Every new name below was checked at 47c5edd, and a name added by an earlier task of this plan counts.
- **Goldens.** A golden changes only through its `-update` run, or a hand edit that a step names. Read every changed golden line: only what the task names may differ.
- **Historical documents.** Nothing under `docs/` changes but this plan. `example/README.md`, the examples' READMEs and comments change where a task names them.
- **The followups file** (`docs/plans/2026-10-14-core-1-followups.md`) is not touched. Wave 6 marks its items, citing these commits.
- **Compile cost.** No task touches the root package's non-test files, so compilecost_test.go's bounds hold untouched.
- **How the expected values were found.** Every "today" and "Expected" value below came from a run at 47c5edd: a throwaway `zz*_test.go` in the package, deleted after its run, a scratch program, or a grep.
  - A value marked **[D]** needs PostgreSQL or MySQL, and Docker was not run while planning. Where the same case ran on SQLite, the SQLite value is given. The implementer runs that step under D and records the result in its commit message. If it differs from the plan's expectation, stop and report.
  - Values for new code (messages, file formats) are this plan's decisions, written as exact copy.

### R, D and G

- **D: the databases.** Never run D while a gate runs: both use the compose project `velox2-ci`.

```bash
docker compose -f docker-compose.ci.yml -p velox2-ci up -d --wait
export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true'
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
# … the step's command …
docker compose -f docker-compose.ci.yml -p velox2-ci down -v
```

- **G** is CI's "generated files are up to date". Wave 3's plan has it verbatim (docs/plans/2026-10-27-hardening-3-graphql.md, "The generated check (G)"). Expected: every command exits 0, and the `git diff` is empty. No task here changes generated code, so G runs only in the gate.
- **RE** (under D): `go test -count=1 -run TestMigrateBaselineEnt ./cmd/velox-gen -update`. Only Task 8 runs it. The PostgreSQL and MySQL files under `cmd/velox-gen/testdata/ent/want/` must not change.

## Review Focus

1. **A pair of schemas whose plan applies cleanly but leaves the database unlike the model.** A user who edits a schema and runs `migrate diff`, then `apply`, expects `migrate drift` to be empty. The fixed cases cover what the authors thought of. Pin: Task 13 `TestMigrateRandom`, over fixed seeds on SQLite in the gate and on all three databases in CI.
2. **An upsert's update branch on a real database.** A user expects `OnConflict(…).Update(cols)` to write exactly the named columns and leave the rest of the existing row. Today the itests check one boolean. Pin: Task 6 `TestUpsertUpdateBranchReadBack`.
3. **The examples' committed migrations drifting from their schemas.** A user copies an example. They expect its migrations to build exactly what its schema says. Pin: Task 7 (every example built by `migrate.Up`) and its `migrate diff -check` loop in CI.
4. **A failed `INSERT … RETURNING` reported to an Around hook.** A metrics hook must not count rows for a statement that failed after its row came back. Pin: Task 1 `TestAffectedOfAFailedReturning`.
5. **A row whose parent is in another tenant.** A user loading `With(Parent)` expects the other org's parent to be hidden, not loaded and not an error. Today no seeded row has a parent in another org. Pin: Task 6 `TestScopeCrossOrgParent`.

---

### Task 1: The fake returns errors; write-path gaps (C18, C31, C66)

**Files:**
- Modify: `export_test.go:294-363` (`assign`, `assignReflect`).
- Create: `write_gaps_test.go` (package `velox_test`): the hand Desc entities `wgPair` and `wgBig`, and the tests below.
- Modify: `render_test.go` (after `TestRenderSchemaQualifiedTable`, :94), `render_cache_test.go` (after `TestCacheKeepsWriteShapesApart`, :135), `errclass_test.go` (after `TestLookAlikeErrorsAreLeftAlone`, :101), `tags_test.go:29` (`TestWithTagsNoPairs`), `result_test.go`.
- Pinned, and must still pass: every root test, which reads rows through `assign`.

**Interfaces:** Consumes nothing. Produces the fake's conversion, which Task 3 and later root tests rely on: an `int64` row value scans into any integer kind, with an overflow error.

- [ ] **Step 1: Write the failing tests.**
  - `TestFakeScanErrors` (in `write_gaps_test.go`): `db.Query[testmodel.Invoice]().Columns(I.ID).Limit(1).All(ctx)` over `FakeExec{Results: [][][]any{{{"x"}}}}` returns an error, not a panic, that contains `fake: cannot scan string into *int64`. `Columns(I.Total)` over `{{{nil}}}` returns one that contains `fake: cannot scan <nil> into *float64`.
  - `TestInsertReadsBackAUint64Key`: `wgBig{ID uint64; Name string}` (table `bigs`, columns `id`, `name`, key 0, Tenant -1). On PostgreSQL, a `FakeExec{Rows: [][]any{{int64(1) << 40}}}` insert of `wgBig{Name: "x"}` sets `ID == 1<<40`. A row of `int64(-1)` returns an error that contains `fake: -1 overflows *uint64`.
- [ ] **Step 2: Run them.** `go test -count=1 -run 'TestFakeScanErrors|TestInsertReadsBackAUint64Key' .` Expected: both panic today, with `panic: interface conversion: interface {} is string, not int64` (and `… is nil, not float64`) and `panic: fake: cannot scan int64 into *uint64`.
- [ ] **Step 3: Implement** in `export_test.go`.
  - The typed cases (`*int64`, `*string`, `*float64`, `*bool`, `*[]byte`, `*time.Time`, `**time.Time`, `**string`) use a checked assertion and return `fmt.Errorf("fake: cannot scan %T into %T", src, dst)` on a mismatch.
  - `assignReflect` returns, not panics, both of its errors. An `int64` source into any integer kind (signed or unsigned, or a pointer to one, or a named type of one) converts with `reflect.Value.OverflowInt` / a negative check, and returns `fmt.Errorf("fake: %d overflows %T", v, dst)` when it does not fit.
  - Fix the comment above `assignReflect` ("Like the typed cases it panics…").
- [ ] **Step 4: Run** the two tests, then `go test -race -count=1 .`. Expected: PASS.
- [ ] **Step 5: Add the C31 and C66 cases.** Each is expected to PASS on today's code; a failure is a finding.
  - `TestWriteQueriesAreSafeToExtendConcurrently` (query_test.go's `TestBaseQueryIsSafeToExtendConcurrently`, :232, is the model). 8 goroutines × 100 extend `base := db.Update[testmodel.Invoice]().Set(I.Total.Set(1)).Where(I.OrgID.EQ(1))` with `.Where(I.ID.EQ(id))`. `SQL(ctx)` must be `UPDATE "invoices" SET "total" = $1, "updated_at" = $2 WHERE "org_id" = $3 AND "id" = $4` with `args[2:] == []any{int64(1), id}` (args[1] is the time). Likewise `db.Delete[testmodel.Invoice]().Where(I.OrgID.EQ(1))` extended gives `DELETE FROM "invoices" WHERE "org_id" = $1 AND "id" = $2` with args `[1, id]`. Then `Exec(ctx)` each. The base's own SQL is unchanged after `wg.Wait()`.
  - `TestSaveCompositeKey`: `wgPair{A, B int64; Price *wgCents}` (`type wgCents int64`; table `pairs`, columns `a`, `b`, `price`; key `[]int{0, 1}`; `price` has `Default: wgCents(250)`). `db.Save(&p, wgPairs.B).Exec(ctx)` for `p = wgPair{A: 1, B: 2}` sends `UPDATE "pairs" SET "b" = $1 WHERE "a" = $2 AND "b" = $3` with args `[2, 1, 2]`.
  - `TestNamedNumericDefault`: inserting `wgPair{A: 1, B: 2}` sends `INSERT INTO "pairs" ("a", "b", "price") VALUES ($1, $2, $3)`, and sets `*p.Price == 250`. A variant `wgPairV` whose `Price` is a plain `wgCents` (not a pointer) with the same Default is refused on first use with exactly `velox: velox_test.wgPairV.Price: Default on bool/numeric field Price would overwrite an explicit false or 0 on insert; make the field a pointer (nil uses the default)`, and sends nothing.
  - `TestLookAlikeDriverTypeFromAnotherPackage` (errclass_test.go, package `velox`): a type declared in the test file named `PgError` with a `Code string` field and an `Error` method, value `&PgError{Code: "23505"}`, is neither a `*ConstraintError` from `dbErr(invInfo, err)`, nor `retryable`, nor has a `DriverCode`. (driverError checks the package path, errclass.go:140.)
  - `TestCacheKeepsWriteColumnsApart` (render_cache_test.go, package `velox`): pairs of statements that differ only in `Conflict.Columns`, `Conflict.Update`, `Returning`, or `Set[i].Column`. For each pair, a `renderCache` renders both, the texts differ, and each equals `render(d, st)` on Postgres and SQLite.
  - `TestRenderSchemaQualifiedWrites` (render_test.go): with `info := &EntityInfo{Name: "velox.inv", Schema: "billing", Table: "invoices", Tenant: -1, Columns: []ColumnInfo{{Name: "id"}, {Name: "org_id"}, {Name: "total"}}}` and `where := []Predicate{{node{op: opEQ, col: 1, v: int64(7)}}}`:

```go
// INSERT (org_id, total) RETURNING id; UPDATE total = total + v WHERE where;
// on PG and SQLite the upsert ON CONFLICT (org_id) DO UPDATE total WHERE where.
"postgres insert": `INSERT INTO "billing"."invoices" ("org_id", "total") VALUES ($1, $2) RETURNING "id"`,
"postgres update": `UPDATE "billing"."invoices" SET "total" = "total" + $1 WHERE "org_id" = $2`,
"postgres upsert": `INSERT INTO "billing"."invoices" ("org_id", "total") VALUES ($1, $2) ON CONFLICT ("org_id") DO UPDATE SET "total" = EXCLUDED."total" WHERE "billing"."invoices"."org_id" = $3 RETURNING "id"`,
"mysql insert":    "INSERT INTO `billing`.`invoices` (`org_id`, `total`) VALUES (?, ?)",
"mysql update":    "UPDATE `billing`.`invoices` SET `total` = `total` + ? WHERE `org_id` = ?",
"sqlite insert":   `INSERT INTO "billing"."invoices" ("org_id", "total") VALUES (?, ?) RETURNING "id"`,
"sqlite update":   `UPDATE "billing"."invoices" SET "total" = "total" + ? WHERE "org_id" = ?`,
"sqlite upsert":   `INSERT INTO "billing"."invoices" ("org_id", "total") VALUES (?, ?) ON CONFLICT ("org_id") DO UPDATE SET "total" = EXCLUDED."total" WHERE "billing"."invoices"."org_id" = ? RETURNING "id"`,
```

  - `TestRollbackOfAnEndedTxIsNotJoined` (tx_test.go): for `RollbackErr` = `sql.ErrTxDone` and `pgx.ErrTxClosed`, `db.Tx(ctx, fn)` where fn returns `boom` returns an error with `err.Error() == "boom"`, `errors.Is(err, boom)`, and `!errors.Is(err, rollbackErr)` (tx.go:186 `ended`).
  - `TestAffectedOfAFailedReturning` (result_test.go): an Around hook records `st.Result.Affected` after `next`. A PostgreSQL insert of a `testmodel.Invoice` whose FakeExec has `Err: errors.New("boom")` records `-1`; one whose FakeExec has `Rows: [][]any{{int64(5)}}, RowsErr: errors.New("cut")` returns `velox: testmodel.Invoice: cut`, records `-1`, and (today) leaves `ID == 5`. MySQL with `Err` records `-1` too.
  - `TestWithTagsNoPairs` (tags_test.go:29) is made non-vacuous: a parent `WithTags(bg, "route", "a")`, then `WithTags(parent)` is `parent` itself and `TagsOf` it is `[]velox.Tag{{"route", "a"}}`.
- [ ] **Step 6: Run** `go test -race -count=1 .`. Expected: PASS.
- [ ] **Step 7: Commit.** `velox: the fake returns scan errors and converts integer kinds; write-path, Affected and WithTags test gaps (C18, C31, C66)`. Then run C.

---

### Task 2: The render, comment and caller caches under their bounds (C64, C65, C67)

**Files:**
- Modify: `render_cache_test.go` (after `TestCacheBoundedInBytes`, :162), `comment_internal_test.go` (after `TestCommentCacheBoundedInBytes`, :91), `callers_internal_test.go` (after `TestPCCacheIsBounded`, :68).

**Interfaces:** None.

- [ ] **Step 1: Write the tests.** All in package `velox`; each is expected to PASS today.
  - `TestCacheBytesCountTheFingerprint`: fill a `renderCache` with 50 In-list shapes (`TestCacheBoundedInBytes`'s statements). Then `c.bytes` equals the sum, over every held entry, of `len(key) + len(e.text) + len(e.fp)`, with `len(e.fp) == 16` for each. Set `maxCacheBytes` to that sum minus 1 on a fresh cache, render the same shapes, and check that the last shape is not held. (render_cache.go:83, :95 count `len(e.fp)`.)
  - `TestPCCacheConcurrentAtBound`: `maxPCs = 2` (restored), 8 goroutines each `capture()` from three call sites 100 times, through `descend(c, g%3)` so the sites differ. Every result's `Line` is non-zero and `File` is `callers_internal_test.go`; after `Wait`, `len(c.pcs) <= 2`. Read `c.pcs` under the cache's lock if it has one, as `capture` does (callers.go:160).
  - `TestCommentCacheConcurrentEviction`: `maxComments = 8` (restored), 8 goroutines render 200 distinct route values each through `commentFor`. Every text equals `` ` /*app='app',route='<v>'*/` ``. After `Wait`, `len(c.m) <= 8`, `len(c.order) == len(c.m)`, and `c.bytes` is exactly the sum of `len(k) + len(c.m[k])` over `c.m`.
  - `TestCommentCacheExactBytes`: without concurrency, after each of `TestCommentCacheBoundedInBytes`'s 50 renders, `c.bytes` equals that same sum (comment.go:63 says `len(key) + len(text)`).
- [ ] **Step 2: Run** `go test -race -count=1 -run 'TestCacheBytesCountTheFingerprint|TestPCCacheConcurrentAtBound|TestCommentCacheConcurrentEviction|TestCommentCacheExactBytes' .` Expected: PASS.
- [ ] **Step 3: Commit.** `velox: tests of the caches' byte accounting and of concurrent use at their bounds (C64, C65, C67)`. Then run C.

---

### Task 3: Deadlines, row caps and transaction hooks (C38, C62, C68, C44's deadline cases)

**Files:**
- Modify: `deadline_test.go`, `rowcap_test.go` (after `TestRowCapCoversRelationLoads`, :158), `budget_test.go` (after `TestTxAllocationBudget`, :217), `txhook_test.go` (after `TestTxAroundSeesEveryAttempt`, :64), `tx_internal_test.go` (after `TestOverWaitsForAPassedDeadline`, :21).

**Interfaces:** None.

- [ ] **Step 1: Write the tests.** Each is expected to PASS today.
  - `TestMaxTxCapsALargerTx`: `WithDeadlines(velox.Deadlines{Tx: time.Hour, MaxTx: time.Second})`; a `Tx` that runs one read gives the FakeExec's `Deadline` between 0.9 s and 1.1 s after the call's start (measured 1.0000006 s).
  - `TestMaxTxAlone`: `Deadlines{MaxTx: time.Second}`, the same bounds (measured 1.0000002 s).
  - `TestRowCapAcrossNestedWith`: `WithMaxRows(2)`, results `{{invoiceRow(1)}, {itemRow(10, 1, "A", 1), itemRow(11, 1, "B", 1)}, {invoiceRow(1), invoiceRow(2), invoiceRow(3)}}`, query `db.Query[testmodel.Invoice]().With(I.Items.With(It.Invoice)).Limit(1)`:
    - without MaxRows: `errors.Is(err, velox.ErrTooManyRows)` and `err.Error() == "velox: testmodel.Item: loading Invoice: testmodel.Invoice: read more rows than the cap: more than 2; raise it with the query's MaxRows"`; the three statements' last args are `3` (the LIMIT cap+1);
    - with `.MaxRows(10)`: 1 invoice, no error, and the last args of the two loads are `11`.
    - The error names the inner load only, not "loading Items" (decision 4).
  - `TestRowCapFromARewrite`: a Rewrite hook sets `st.MaxRows = 2`; `db.Query[testmodel.Invoice]().Unbounded().All(ctx)` over three rows sends `… FROM "invoices" LIMIT $1` with args `[3]`, and fails with `velox: testmodel.Invoice: read more rows than the cap: more than 2; raise it with the query's MaxRows`.
  - `TestTxAroundAllocationBudget`: as `TestTxAllocationBudget`, with one pass-through `WithTxAround` hook. Measured: 6 allocations alone, 14 in a transaction (8 without the hook), the same under `-race`. Budget: `inTx-alone <= 8`. The comment names what the hook adds: the TxInfo and the hook's closure chain.
  - `TestRetryExhaustedWithTxAround`: `FakeExec{Errs: []error{ser, ser, ser}}` with `ser := &pgconn.PgError{Code: "40001"}`, a recording TxAround hook, `db.Tx(ctx, touch, velox.TxRetrySerializable(2))`. Expected: `errors.Is(err, ser)`; the hook is called once; `info.Attempt == 3`, `info.Outcome == velox.TxRollback`, `len(info.Retries) == 2`; the log is `BEGIN, UPDATE…, ROLLBACK` three times.
  - `TestOverWaitsWhileBlocked` (package `velox`, beside `TestOverWaitsForAPassedDeadline`): `over(lateCtx{inner})` blocks while `inner` is live: started in a goroutine, it has not returned after 20 ms, and returns true within 1 s of `cancel()`. An early cancel: `inner` canceled before the call makes `over` return true at once (under 50 ms).
- [ ] **Step 2: Run** `go test -race -count=1 -run 'TestMaxTx|TestRowCapAcrossNestedWith|TestRowCapFromARewrite|TestTxAroundAllocationBudget|TestRetryExhaustedWithTxAround|TestOverWaitsWhileBlocked' .` Expected: PASS.
- [ ] **Step 3: Commit.** `velox: tests of MaxTx, row caps across nested loads and from a Rewrite, the TxAround budget, an exhausted retry, and over's wait (C38, C62, C68, C44)`. Then run C.

---

### Task 4: Policy and scope test gaps; one order-dependent test (C44)

**Files:**
- Modify: `statement_test.go:10` (`TestOpRawAndReads`), `tenant_test.go:301` (`TestScopeNeverChangesASharedQuery`), `policy_test.go:85` (`TestPolicyOnCount`), `export_test.go:402` (`ExistsOn`), `hooks_internal_test.go:35` (the `Errs` case), `dryrun_test.go` (a split `InsertAll` `SQL(ctx)` case), `lazy_test.go:244` (`TestHasWhereErrorAtStatement`).
- Create the tests named below in the file of their neighbors.

**Interfaces:** None.

- [ ] **Step 1: Write the tests.** Each is expected to PASS today, but the last, which fails today.
  - `TestOpRawAndReads` gains `if got := velox.Op(99).String(); got != "unknown"` (statement.go:42).
  - `TestScopeNeverChangesASharedQuery`: `db0` gets an Around hook that, for every statement, counts the args equal to `st.Tenant` (an `int64`) and requires 3, and no other `int64` arg `>= 100`, as the per-goroutine check at :336 does. The shared `base.All(tenant(id))` goroutines now go through it.
  - `TestPolicyOnCountOfAnExists` (policy_test.go): `db.Query[testmodel.Ticket]().Where(Tk.Parent.Has()).Count(asUser("ann"))` sends `SELECT COUNT(*) FROM "tickets" WHERE EXISTS (SELECT 1 FROM "tickets" AS "vx1" WHERE "vx1"."id" = "tickets"."parent_id" AND "org_id" = $1 AND "owner" = $2) AND "org_id" = $3 AND "owner" = $4` with `int64(1), "ann", int64(1), "ann"`.
  - `TestPolicyDeleteOrder` (policy_test.go): `db.Delete[testmodel.Ticket]().Where(Tk.Done.EQ(true), Tk.Title.NEQ("x")).Exec(asUser("ann"))` sends `DELETE FROM "tickets" WHERE "done" = $1 AND "title" <> $2 AND "done" = $3 AND "org_id" = $4 AND "owner" = $5 AND "owner" = $6` with `true, "x", true, int64(1), "ann", "ann"`: the caller's conditions first, then the policy's.
  - `TestHiddenRowThroughRun` (policy_test.go, beside `TestSaveOfAHiddenRow`, :290): the same case as `TestSaveOfAHiddenRow`, on a DB with a pass-through Around hook, so it takes `run`'s path; the same error and the same statements sent.
  - `ExistsOn` (export_test.go:402): `l, ok := p.n.v.(link); if !ok { return nil }; return l.target`.
  - The `Errs` case (hooks_internal_test.go:35): `boom := errors.New("boom")`, and the check becomes `!errors.Is(err, boom)`.
  - `TestDryRunSplitInsertAllSQL` (dryrun_test.go): under `velox.SetMaxParams(velox.Postgres, 18)`, `db.InsertAll` of three `testmodel.Invoice{OrgID: 1, Number: "A"|"B"|"C"}` returns from `SQL(ctx)` the first statement, `INSERT INTO "invoices" ("created_at", "updated_at", "org_id", "number", "total", "paid", "paid_at", "note", "data") VALUES ($1, …, $9), ($10, …, $18)`, and 18 args whose non-time values are, per row, `int64(1), "A"|"B", 0.0, false, nil, "", nil` in columns 3–9.
- [ ] **Step 2: The order-dependent test.** `go test -count=2 .` fails today with `lazy_test.go:251: HasWhere built the owner's metadata (1 loads)`, and so does `go test -shuffle=2 -count=1 .`. `lzLateMeta` is a package-level `MetaOnce` that the first run, or another test, loads. Change the check to a delta: `before := lzLateLoads` before `HasWhere`, and fail if `lzLateLoads != before`. Comment that the check means something on the process's first load only.
- [ ] **Step 3: Package-level counters (C44).** `git grep -nP '^\s*(var\s+)?\w+(Loads|Calls|Affected|Seen)\s' -- '*_test.go' ':!contrib' ':!migrate' ':!cmd' ':!internal' ':!example'` prints today `bidi_test.go:77` (`bidiSeen`), `:298`, `:304`, `:379` (`bspouseAffected`), `hook_test.go:633`, `:643`, `:687` (`hUpAffected`), `lazy_test.go:226` (`lzLateLoads`), and `session_test.go:474` (a local, not a counter). Each test that reads one resets it at its start (bidi :379 and hook :687 already do) or reads a delta, so `go test -shuffle=on -count=2 .` passes. Do not move them into the tests: the hooks and Desc closures that write them are package-level.
- [ ] **Step 4: Run** `go test -race -count=1 .`, then `go test -count=2 -shuffle=1 .`, `-shuffle=2`, `-shuffle=3`. Expected: PASS each.
- [ ] **Step 5: Commit.** `velox: policy and scope test gaps; tests pass in any order and twice (C44)`. Then run C.

---

### Task 5: Sharded Iter, Count and Exists; the failover socket path (C93, C92)

**Files:**
- Modify: `shard_test.go` (after `TestShardWithLoads`, :128), `failover_test.go:68`.

**Interfaces:** None.

- [ ] **Step 1: Write `TestShardIterCountExists`** with `bothPaths`, as `TestShardFromTenant` (:111) does. Expected PASS today (measured on both paths):
  - tenant 2 (`us`): `usP.Results = {{invoiceRow(1), invoiceRow(2)}, {{int64(2)}}, {{int64(1)}}}`; an `Iter` of `Limit(5)` yields 2 rows; `Count` is 2; `Exists` is true; calls `[0 0 3]`; hooked routes `usPrimary` ×3; `s.lookups` is 3.
  - tenant 1 (`eu`): `euR.Results = {{invoiceRow(1)}, {{int64(3)}}, {{int64(1)}}}`; `Count` 3, `Exists` true; calls `[0 3 0]`; hooked routes `euR0` ×3.
  - tenant 3: `Count` fails with `velox: testmodel.Invoice: no shard for the statement: WithShardFor gave "mars", which is not a shard of the DB`.
- [ ] **Step 2: C92.** `refusedConnectError` makes its directory with `os.MkdirTemp("/tmp", "vx")` when `runtime.GOOS != "windows"`, and `os.MkdirTemp("", "vx")` otherwise, so a long `TMPDIR` cannot push the socket path over its limit. The comment says why.
- [ ] **Step 3: Run** `go test -race -count=1 -run 'TestShardIterCountExists|Failover|Refused' .`, then once with `TMPDIR=/private/tmp/claude-501/-Users-s-y-s-Jobs-go-velox2/307a7d09-1a6a-4283-a67c-6dfbe3e93815/scratchpad/a-long-temporary-directory-name-to-push-the-path-past-104-bytes` (create it first). Expected: PASS both.
- [ ] **Step 4: Commit.** `velox: sharded Iter, Count and Exists; the failover test's socket ignores TMPDIR (C93, C92)`. Then run C.

---

### Task 6: The itests: a schema-qualified upsert on PostgreSQL, a cross-org parent, the upsert read back, timing, logging, the DSN (C39, C45, C46, C94, C95, C96)

**Files:**
- Modify: `internal/itest/scope_test.go` (after `TestScopeRelations`, :190; after `TestScopeUpsert`, :362), `internal/itest/writes_test.go` (after `TestUpsert`, :100), `internal/itest/routing_test.go:347`, `:371` (`TestRoutingSendsWhereItSays`), `:574-590` (`endSessions`), `internal/itest/itest_test.go:32` (`TestMain`).

**Interfaces:** None.

- [ ] **Step 1: C45, `TestScopeCrossOrgParent`.** `eachCaller`; after setup, `tg.exec(t, "UPDATE tickets SET parent_id = 6 WHERE id = 1")` (ticket 1 is org 1's, ticket 6 org 2's). Then `With(Tk.Parent)` over visible tickets gives (measured on SQLite):
  - ann: `map[1:0 2:1 5:0]`, and `Where(Tk.Parent.Has())` gives `[2]`;
  - admin: `map[1:0 2:1 3:0 4:3 5:3]`, `Has` gives `[2 4 5]`;
  - anyone: `map[1:6 2:1 3:0 4:3 5:3 6:0 7:6 8:6]`, `Has` gives `[1 2 4 5 7 8]`.
  - The same on PostgreSQL and MySQL **[D]**. The seed is not changed (ruling 4).
- [ ] **Step 2: C46, `TestUpsertUpdateBranchReadBack`.** `each`; `over := newInvoice(1, "INV-1"); over.Total = 99; over.Note = "changed"`, then `OnConflict(I.OrgID, I.Number).Update(I.Total).Exec(ctx)`. Read row 1 back straight from the database (`tg.direct`): `total = 99`, `note = ""` (the seed's, not "changed"), `paid = true`, `number = "INV-1"`, `org_id = 1`. Through velox, `created_at` and `updated_at` are both `t0` (measured on SQLite: the update branch does not fire `UpdatedAt`'s UpdateDefault; decision 5). On PostgreSQL and SQLite, `over.ID == 1` (RETURNING). **[D]** for PostgreSQL and MySQL.
- [ ] **Step 3: C39, `TestUpsertSchemaQualifiedPostgres`** (scope_test.go). PostgreSQL only: skip elsewhere with `t.Skip("schema-qualified DO UPDATE … WHERE is PostgreSQL's")`.
  - Setup: `CREATE SCHEMA vx_billing`, `CREATE TABLE vx_billing.tickets (LIKE public.tickets INCLUDING ALL)`, and seed it as `tickets` is seeded; drop the schema in `t.Cleanup` (`DROP SCHEMA vx_billing CASCADE`).
  - Open the target's database again with `velox.WithRewrite` that replaces `st.Entity` with a copy whose `Schema` is `"vx_billing"`. A FakeExec run of the same hook (measured) renders `INSERT INTO "vx_billing"."tickets" (…) VALUES ($1, …, $7) ON CONFLICT ("org_id", "title") DO UPDATE SET "done" = EXCLUDED."done" WHERE "vx_billing"."tickets"."owner" = $8 AND "vx_billing"."tickets"."owner" = $9 RETURNING "id"`.
  - As ann, `TestScopeUpsert`'s A1 upsert updates `vx_billing.tickets` row 1 (done = true) and leaves `public.tickets` untouched; the B1 upsert, bob's row, returns a `*velox.PolicyError` with `Op == velox.OpUpdate`. **[D]**
- [ ] **Step 4: C94.** In `TestRoutingSendsWhereItSays`:
  - the pin window is `time.Second`, and the time of the in-session write is kept (`wrote := time.Now()` after `insertInvoice`);
  - before the in-window reads' checks, `if time.Since(wrote) >= window { t.Fatalf("the runner took %v for the in-window reads, past the %v window", …) }`;
  - the after-window wait is `time.Sleep(time.Until(wrote.Add(window + 100*time.Millisecond)))`.
  - `endSessions` on PostgreSQL, after `pg_terminate_backend`, polls `SELECT count(*) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()` every 20 ms until it is 0, for at most 5 s, then fails naming the count.
- [ ] **Step 5: C95.** The itest package's `TestMain` calls `mysql.SetLogger(log.New(io.Discard, "", 0))` before `m.Run()`, with a comment: the driver logs "closing bad idle connection" under -v when a test kills a session.
- [ ] **Step 6: C96.** `TestMain` refuses, before `m.Run()`, a `VELOX_TEST_POSTGRES` whose database name is empty: parse it with `pgx.ParseConfig`; if `cfg.Database == ""`, print `itest: VELOX_TEST_POSTGRES names no database: add one, as in postgres://user:pass@host:port/velox` to stderr and `os.Exit(1)`. Unit-test the check as `TestITestDSNNamesADatabase` on a helper `dsnDatabaseProblem(dsn string) string` (empty when fine): `postgres://u:p@h:5432/velox?sslmode=disable` fine; `postgres://u:p@h:5432/?sslmode=disable` and `postgres://u:p@h:5432` refused.
- [ ] **Step 7: Run** `go test -race -count=1 ./internal/itest` without D (SQLite). Expected: PASS. Then under D. Expected: PASS; record the PostgreSQL and MySQL results of Steps 1–3 in the commit message.
- [ ] **Step 8: Commit.** `itest: a schema-qualified upsert on PostgreSQL, a cross-org parent, the upsert read back; wider routing timing, quiet MySQL logs, an empty DB name refused (C39, C45, C46, C94, C95, C96)`. Then run C under D.

---

### Task 7: The examples are built by their committed migrations (M16, M17, C98)

**Files:**
- Create: `example/<x>/migrations/{0001_init.sql,schema.json,velox.json,velox.sum}` for each of the ten examples: start, relations, tenant, policy, hooks, tx, routing, otel, tour, graphql.
- Modify: `example/<x>/main.go` for each: the DDL constant at the `CREATE TABLE` lines leftover grep 2 lists, its comment (leftover grep 3), and its use; `example/graphql/main_test.go:53`, `example/graphql/bench_test.go:113`, `:344` (the savepoint counter's `Open("file::memory:")`); `example/README.md:38`; `example/tour/README.md:23-24`; `example/routing/main.go:154`, `:179`, `example/routing/README.md:17` (C98).
- Modify: `scripts/ci-local.sh` (the example block), `.github/workflows/ci.yml` (the `example` step).

**Interfaces:** Consumes nothing. Task 16 measures the examples' coverage after this task.

- [ ] **Step 1: Generate the directories.** From `example/`, for each example x: `go tool velox-gen migrate diff -dir ./x/migrations -dialect sqlite -name init ./x/schema`. Expected (measured): exit 0; it writes `0001_init.sql`, `schema.json`, `velox.json` (`{"version":1,"dialect":"sqlite","table":"velox_migrations","lock_timeout":"5s"}`) and `velox.sum`. start's `0001_init.sql` creates `users` with `"id" integer NOT NULL PRIMARY KEY AUTOINCREMENT` and the unique index `users_email_key`. Then `go tool velox-gen migrate diff -check -dir ./x/migrations ./x/schema` exits 0 for every x.
- [ ] **Step 2: Each main.go embeds and applies its directory.**
  - `//go:embed migrations` into an `embed.FS`, then `migrate.Up(ctx, db, sub)` with `sub, _ := fs.Sub(migrations, "migrations")`.
  - The database is a SQLite file in a fresh `os.MkdirTemp("", "velox-<x>-")` directory, removed at exit. `migrate.Up` on `file::memory:` with `SetMaxOpenConns(1)` loses its tables (measured: the next query fails with `no such table: users`), so no example keeps an in-memory database.
  - otel's seed INSERT runs after `Up`.
  - routing applies the directory to each shard's file.
  - graphql: `main_test.go:53`, `bench_test.go:113` and the savepoint counter's `Open` (:344) use one fixed path in a `t.TempDir()` / `b.TempDir()`, with the directory applied once.
  - The stale comment ("planning migrations from the schemas is a later velox2 sub-project") goes; the new one says the tables come from `migrations/`, made by `velox-gen migrate diff`.
- [ ] **Step 3: Run** `cd example && go test -race -count=1 ./...`. Expected (measured with the same change): every example's golden passes unchanged; `TestNestedRequestBudget` holds (45 allocations a node); `TestMutationBudget` measures 497 allocations (budget 550), 2 statements, 2 savepoints.
- [ ] **Step 4: Docs.** `example/README.md:38`: "Tables are created with plain DDL…" becomes: each example's tables come from its `migrations/` directory, made by `go tool velox-gen migrate diff -dialect sqlite`, and applied with `migrate.Up`; CI checks the directories with `migrate diff -check`. `example/tour/README.md:23-24` likewise.
- [ ] **Step 5: C98.** routing's pin window becomes `250*time.Millisecond`, the sleep `600*time.Millisecond` ("well past the 250 ms window"), and its README line 17 says 250 ms. The golden output names no duration (checked: `example/routing/testdata` has none), so it does not change.
- [ ] **Step 6: M17.** In ci-local.sh's example block and ci.yml's `example` step, after the `-check`/`verify` loop, add:

```bash
for d in */migrations; do
  x=${d%/migrations}
  go tool velox-gen migrate diff -check -dir "./$x/migrations" "./$x/schema"
done
```

- [ ] **Step 7: Run** the loop from `example/`. Expected: exit 0 for each of the ten.
- [ ] **Step 8: Commit.** `example: every example's tables come from its committed migrations, checked in CI; wider routing window (M16, M17, C98)`. Then run C in `example`.

---

### Task 8: A SQLite ent fixture for baseline (M20)

**Files:**
- Create: `cmd/velox-gen/testdata/ent/sqlite.sql`; `cmd/velox-gen/testdata/ent/want/sqlite/{0001_baseline.sql,schema.json,warnings.txt,diff.sql}`.
- Modify: `cmd/velox-gen/migrate_baseline_test.go:84-199` (`TestMigrateBaselineEnt`, `baselineEnt`, `entChange`).

**Interfaces:** None.

- [ ] **Step 1: The fixture** is ent v0.14.6's SQLite DDL for `testdata/ent/schema/{user,post,audit_entry}.go`, made outside the repo (a scratch module with `entc` and `migrate.WithGlobalUniqueID(false)`, `schema.Create` on a SQLite file, then `.schema`). Measured: `users` (`id integer NOT NULL PRIMARY KEY AUTOINCREMENT`, `name`, `email`, `role` text NOT NULL), `CREATE UNIQUE INDEX users_email_key`, indexes `user_name` and `user_email` (unique); `posts` with foreign keys `posts_users_posts` and `posts_users_drafts_written_but__f5ecc73789cbf6208f1deb12fd406c67` ON DELETE SET NULL, and index `post_title_reviewed_by_an_edito_610afc49e3060dc9162738c5b5b2de3d`; `audit_entries` AUTOINCREMENT. The scratch copy is at `/private/tmp/claude-501/-Users-s-y-s-Jobs-go-velox2/307a7d09-1a6a-4283-a67c-6dfbe3e93815/scratchpad/sqlite.sql`; regenerate it if that is gone. Statements end with `;` and a newline, as `execFile` splits them.
- [ ] **Step 2: The test.** `TestMigrateBaselineEnt` loops over `Postgres, MySQL, SQLite`. For SQLite, `baselineEnt` uses two files in `t.TempDir()`, `original.db` and `fresh.db`, in place of `testDSN`. The change-count map becomes `{Postgres: 5, MySQL: 0, SQLite: 3}`. `entChange` accepts, on SQLite:
  - `AddIndex` on `posts` named `posts_user_posts` or `ddl.Cap("posts_user_drafts_written_but_never_published_by_this_user_so_far")` (= `posts_user_drafts_written_but_never_published_by_this__c3344a61`);
  - `AddCheck` on `users` named `users_role_check` (`"role" IN ('admin', 'member')`).
  - The godoc of both says what ent spells differently on SQLite: the two foreign keys' indexes and the role enum's CHECK, which SQLite makes by rebuilding the table.
- [ ] **Step 3: Run** `go test -count=1 -run 'TestMigrateBaselineEnt/sqlite' ./cmd/velox-gen -update`, then without `-update`. Expected (measured): baseline exits 0 with stderr exactly `baselineHint`, so `warnings.txt` is the hint alone; `Verify` is nil; `apply -baseline 1` and `status` as on the other dialects; the next diff with `-allow alter-type=users.role` has the 3 changes, and `diff.sql` renders the CHECK as a table rebuild under `-- velox:foreign-keys off`.
- [ ] **Step 4: Run** RE under D. Expected: the PostgreSQL and MySQL files under `want/` do not change (`git diff --stat -- cmd/velox-gen/testdata/ent/want/postgres cmd/velox-gen/testdata/ent/want/mysql` is empty).
- [ ] **Step 5: Commit.** `velox-gen: migrate baseline adopts ent's SQLite tables, with goldens (M20)`. Then run C under D.

---

### Task 9: migrate's Wave 4 leftovers: the stepwise path under a changed search_path, quoted and dotted schema names, a nested domain (D)

**Files:**
- Modify: `migrate/stepwise_db_test.go` (after `TestUpMySQLConnectionKilled`), `migrate/runner_db_test.go` (after `TestUpPostgresSearchPath`, :144), `migrate/inspect_postgres_test.go` (after the `tag_words` case, :606).

**Interfaces:** None.

- [ ] **Step 1: `TestUpPostgresStepwiseSearchPath`.** A directory of two files: `0001_a.sql` creates a table; `0002_b.sql` starts with `-- velox:no-transaction`, runs `SELECT pg_catalog.set_config('search_path', '', false);`, then a statement that fails (`SELECT 1/0;`). Expected **[D]**:
  - `Up` returns a `*migrate.DirtyError` for version 2, statement 2 of 2;
  - the history row of version 2 is dirty and its `error` column holds the division-by-zero error: recordError's UPDATE is qualified (`s.historyTable()`, migrate/stepwise.go:276), so the emptied search_path does not hide the table;
  - `Status` lists version 2 dirty with that error;
  - `Resolve(ctx, db, dir, 2, migrate.ResolveApplied)` cleans the row, and `Status` lists it applied.
- [ ] **Step 2: `TestUpPostgresStepwiseLostConnection`.** As Step 1, but the failing statement is `SELECT pg_terminate_backend(pg_backend_pid());`. Expected **[D]**: a `*migrate.DirtyError`; the error is written through the pool (`persist`'s fallback, stepwise.go:160), so the row's `error` column is non-empty, and it holds the lost-connection message `lostMessage(1, 2)` or the driver's error. Record which in the commit message.
- [ ] **Step 3: `TestUpPostgresQuotedSchema`.** For each history schema `my.schema` and `we"ird`: `CREATE SCHEMA` it (quoted), and run a one-file directory with `Config.Table` qualified by it, as `TestUpPostgresSearchPath` sets a schema. Expected **[D]**: `Up` applies, `Status` reads the row, and a second `Up` applies nothing: `hasHistory` (`to_regclass(historyTable())`) finds the quoted table. On MySQL, a database named `my.db` (made through `VELOX_TEST_MYSQL_ADMIN`) does the same. **[D]**
- [ ] **Step 4: `TestInspectPostgresNestedDomain`.** Needs no extension. `CREATE DOMAIN d_inner AS text; CREATE DOMAIN d_outer AS d_inner; CREATE TABLE nd (id integer PRIMARY KEY, v d_outer);` Then `InspectNotes` on `nd`. Expected **[D]**: exactly one baseline note, `the type d_outer is a domain, which 0001_baseline.sql does not create; add its CREATE DOMAIN before the tables, then run migrate hash`, naming the nearest domain only (Wave 4 ruling made during execution 10, and its Wave 6 docs leftover).
- [ ] **Step 5: Run** `go test -count=1 -run 'TestUpPostgresStepwise|TestUpPostgresQuotedSchema|TestInspectPostgresNestedDomain' ./migrate` under D. Expected: PASS. If a PostgreSQL behavior differs from the plan, stop and report: it is a migrate finding.
- [ ] **Step 6: Commit.** `migrate: tests of the stepwise path under an empty search_path, quoted schema names and a nested domain`. Then run C under D.

---

### Task 10: The GraphQL test harness, and loop-body timing without upper bounds (G5, G8, G34, G46, G61)

**Files:**
- Create: `contrib/graphql/internal/modeltest/modeltest.go` (package `modeltest`), `contrib/graphql/internal/modeltest/model.json` (moved by `git mv` from `contrib/graphql/internal/sdl/testdata/model.json`).
- Modify: `contrib/graphql/internal/sdl/render_test.go:31-39`, `contrib/graphql/internal/gogen/render_test.go:38-46` (both `testModel`s), `contrib/graphql/annotation_test.go` (`TestAnnotationJSON`), `contrib/graphql/deps_test.go:21-27`, `contrib/graphql/veloxgql/gqlimpl/where_test.go:595` (`TestPredTwiceOnOneContext`), `contrib/graphql/cmd/velox-graphql/mutation_e2e_test.go:2276`.
- Create: `contrib/graphql/cmd/velox-graphql/race_enabled_test.go` and `race_disabled_test.go`, as the root's (`//go:build race` / `!race`, `const raceEnabled`).
- Modify: `result_test.go:161` (`TestResultLoopBody`), `contrib/veloxotel/metrics_test.go:556` (`TestIterDurationExcludesLoopBody`).

**Interfaces:** Produces `modeltest.Model(t testing.TB) *plugin.Model`, which Task 11 does not need but any later contrib test may use.

- [ ] **Step 1: G5.** `modeltest.Model` embeds `model.json` (`//go:embed model.json`) and unmarshals a fresh `*plugin.Model` per call, failing `t` on an error. Both `testModel`s are deleted and their call sites call `modeltest.Model(t)`. `git grep -nP 'model\.json' -- contrib` then prints only `contrib/graphql/internal/modeltest/modeltest.go`.
- [ ] **Step 2: G5's round-trip.** `TestAnnotationJSONCoversEveryField` (annotation_test.go): by reflection over `graphql.Annotation`'s fields, each of its 15 fields (Skip, Type, QueryField, Mutations, MultiOrder, FieldName, OrderField, OrderUnguarded, WhereOps, WhereEdge, Connection, CreateInput, UpdateInput, EnumValues, Directives) is non-zero in at least one of `TestAnnotationJSON`'s cases. Lift the cases into a package-level `annotationCases` that both tests read. Expected: PASS today.
- [ ] **Step 3: G8.** The direct-imports loop of `TestAnnotationsImportOnlySchema` accepts a non-std import only if it is exactly `github.com/syssam/velox2/schema` (today's direct imports: `fmt`, `github.com/syssam/velox2/schema`, `maps`, `slices`). The `-deps` loop keeps `isSchema`, since `schema/internal/desc` is a dependency.
- [ ] **Step 4: G34.** `TestPredTwiceOnOneContext` gains, after its three calls on `ctx`: `onCtx := testing.AllocsPerRun(100, func() { w.Pred(ctx) })` and `fresh := testing.AllocsPerRun(100, func() { w.Pred(context.Background()) })`, and requires `onCtx == fresh`. Measured: 30 and 30 for the 510-node input (5 and 5 for one `AgeIn`).
- [ ] **Step 5: G46.** `TestMutationsRun`'s inner run (mutation_e2e_test.go:2276) passes `-race` when `raceEnabled`: `args := []string{"test", "-count=1", "-v"}; if raceEnabled { args = append(args, "-race") }`. The other four inner `go test` runs (e2e_test.go:1432, :1458, collect_e2e_test.go:940, enum_e2e_test.go:238) are unchanged (decision 7).
- [ ] **Step 6: G61, root.** `TestResultLoopBody` (result_test.go:161) measures the wall time `w` around each `sleepIter` and replaces each upper bound `< 2*body` with `got.LoopBody <= w`. The lower bounds stay. The comment about room for overshoot goes.
- [ ] **Step 7: G61, veloxotel.** `TestIterDurationExcludesLoopBody`'s `check` takes the span's duration and the rows the body slept on, and asserts `0 <= sum && sum <= spanDur.Seconds()-float64(n)*body.Seconds()+1e-6`, replacing `sum < most`. This is exact: the point is `time.Since(start) − LoopBody` (contrib/veloxotel/statement.go:52) with LoopBody ≥ n·body, taken inside the span.
- [ ] **Step 8: Run** in contrib/graphql `go test -race -count=1 ./...`, in contrib/veloxotel and the root `go test -race -count=1 -run 'TestResultLoopBody|TestIterDurationExcludesLoopBody' ./...`. Expected: PASS.
- [ ] **Step 9: Commit.** `graphql: one model loader, a round-trip that covers every annotation field, exact deps, allocation-free repeat Pred, the mutation itest under -race; loop-body timing bounded by the wall clock (G5, G8, G34, G46, G61)`. Then run C in the three modules.

---

### Task 11: GraphQL e2e: a Write-only policy, the required self edge, the mutation schema served by gqlgen

**Files:**
- Modify: `contrib/graphql/cmd/velox-graphql/mutation_e2e_test.go`: `e2eMutProfilePolicy` (:196-212), `e2eMutRun` (the inner test file), `TestRequiredSelfEdge` (:1894).
- Create in the same file: `TestMutationSchemaServes` (an outer test) and its inner files.

**Interfaces:** Consumes `app(t, files, withGqlgen)` (e2e_test.go:127) and `e2eCollectGqlgen` (collect_e2e_test.go:100).

- [ ] **Step 1: The Write policy.** `e2eMutProfilePolicy` gains `Write: func(context.Context) (velox.Pred[Profile], error) { return Profiles.Handle.NEQ("frozen"), nil }`, and its comment says so. Measured: every existing inner test still passes on SQLite.
- [ ] **Step 2: `TestWriteHiddenProfile`** (inner, in `e2eMutRun`), with a profile `frozen` that is readable but not writable. Expected (measured on SQLite):
  - create linking it: NOT_FOUND `veloxgql: CreateWriterInput.profileID: no Profile has this ID`; no writer is created;
  - update linking it: NOT_FOUND `veloxgql: UpdateWriterInput.profileID: no Profile has this ID`;
  - re-pointing a writer whose current profile is `frozen` to a free one: BAD_USER_INPUT `veloxgql: UpdateWriterInput.profileID: the Writer is already linked to a Profile`; the current link stays, the free profile stays unlinked, and the writer's name is unchanged (rolled back);
  - `ClearProfile: true` over a `frozen` current profile: no error, and the link stays (decision 2).
- [ ] **Step 3: `TestRequiredSelfEdge` additions** (measured on SQLite):
  - `UpdateFolder(c, PrevID: s)` for a free `s`: ok; `c.folder_next = s`; c's children unchanged;
  - `UpdateFolder(c, PrevID: x)` for an `x` already held: the error is a `*velox.ConstraintError` whose text starts `velox: gen.Folder: unique constraint folders.folder_next violated: `, and the presenter maps it to BAD_USER_INPUT `unique constraint violated` (decision 3);
  - `CreateFolder(ChildIDs: [d], NextID: d)`: d's `folder_children` and `folder_next` are both the new folder;
  - `CreateFolder(NextID: x)` for a held `x`: BAD_USER_INPUT `veloxgql: CreateFolderInput.nextID: a Folder is already linked to another Folder`.
- [ ] **Step 4: `TestMutationSchemaServes`** (outer). `app(t, files, true)` with the mutation schema, `gqlgen.yml` from `e2eCollectGqlgen`, and resolvers for `Node`, `Nodes` and `UpdateFolder` (gqlgen stubs the rest). Its inner test, through gqlgen's handler, on SQLite with `_pragma=foreign_keys(1)`; expected (measured, about 6 s cached):
  - `{ node(id: c) { ... on Folder { name parent { name } prev { name } } } }` → `{"data":{"node":{"name":"c","parent":{"name":"r"},"prev":{"name":"r"}}}}`;
  - after a raw `DELETE` of `r` (ON DELETE SET NULL): `{"data":{"node":{"name":"c","parent":null,"prev":null}}}`;
  - `mutation { updateFolder(id: c, input: {parentID: o, prevID: o}) { name parent { name } prev { name } } }` → `{"data":{"updateFolder":{"name":"c","parent":{"name":"o"},"prev":{"name":"o"}}}}`.
  - It skips under `-short`, as the other app-building tests do.
- [ ] **Step 5: Run** `go test -race -count=1 -run 'TestMutationsRun|TestMutationSchemaServes' ./cmd/velox-graphql` in contrib/graphql, then under D. Expected: PASS; record PostgreSQL and MySQL in the commit message.
- [ ] **Step 6: Commit.** `graphql: e2e of a Write-only policy, the required self edge's owning updates, and the mutation schema served by gqlgen`. Then run C in contrib/graphql under D.

---

### Task 12: Fuzz targets for writes, driver errors and GraphQL cost; every fuzz target in CI

**Files:**
- Modify: `fuzz_test.go` (after `FuzzRender`, :123), `errclass_test.go`, `contrib/graphql/veloxgql/cost_test.go`, `scripts/ci-local.sh:40-44`, `.github/workflows/ci.yml:59-65` and its `contrib/graphql` step.

**Interfaces:** Consumes `fuzzStatement` (fuzz_test.go:50), `placeholders` (:99), `costOf`'s schema (cost_test.go:23, :51).

- [ ] **Step 1: `FuzzRenderWrite`** (package `velox`). From `(ops []byte, s string)`, ops capped at 64, build an INSERT of 1–3 rows of `invInfo` columns chosen by ops, an optional `ConflictClause` (Columns, Update, DoNothing, Where from `fuzzStatement`'s predicates), Returning on Postgres and SQLite; an UPDATE whose `Set` has 1–3 assignments of each `AssignOp`, with `fuzzStatement`'s Where; a DELETE with it. For each dialect, the invariants of `FuzzRender`: placeholders equal args; balanced quotes; the SQL does not depend on the argument value; the cache renders the same. A statement `render` refuses is skipped (`renderStmt`'s error), not failed. Seeds: one of each kind, and `FuzzRender`'s injection seeds.
- [ ] **Step 2: `FuzzConstraint`** (package `velox`), `(code int, msg string)`. For `mysqlKind(code, msg)` and `sqliteKind(code, msg)`: deterministic, and the returned name is a substring of `msg` (`""` included). And `dbErr(invInfo, myErr(uint16(code), msg))` never panics and keeps the driver's error reachable (`errors.Is`). Seeds: every code TestConstraintErrors and TestSQLiteKind use, with their messages. Measured: a 15 s run of the two-function property over these seeds passed (754 227 execs).
- [ ] **Step 3: `FuzzCost`** (package `veloxgql_test`), `(query string, maxPage, maxList uint8)`. Parse and validate against `costSDL`; skip an invalid document. With `l1 := veloxgql.Limits{MaxPage: int(maxPage), MaxList: int(maxList)}` and `l2` each one larger: `0 <= Cost(…, l1) <= saturated`, the same twice, and `Cost(l1) <= Cost(l2)` (cost is monotone in the multipliers: every term is a non-negative sum or product, and saturation keeps the order). Seeds: the queries of `TestCostConnections`, `TestCostDodges` and `TestCostSkipInclude`.
- [ ] **Step 4: Run** each for 30 s once: `go test -run XXX -fuzz '^FuzzRenderWrite$' -fuzztime 30s .`, `^FuzzConstraint$` likewise, and `^FuzzCost$` in `contrib/graphql/veloxgql`. Expected: PASS. A crasher is a finding.
- [ ] **Step 5: CI.** ci-local.sh after :44 and ci.yml after :65 run `-fuzztime 10s` for `^FuzzRenderWrite$` and `^FuzzConstraint$` (root); the contrib/graphql step and block run `go test -run XXX -fuzz '^FuzzCost$' -fuzztime 10s ./veloxgql`.
- [ ] **Step 6: Commit.** `velox, graphql: fuzz writes, driver-error classification and query cost, time-boxed in CI`. Then run C in the root and contrib/graphql.

---

### Task 13: The planner's property test (M15) and FuzzDiff

**Files:**
- Create: `migrate/random_test.go` (package `migrate_test`).
- Modify: `scripts/ci-local.sh`, `.github/workflows/ci.yml` (FuzzDiff's line).

**Interfaces:** Consumes `migrateCase`-style helpers in `migrate/itest_test.go` (`freshDB`, `fromDir`, `mustUp`, `invariants` :53, `insertRows`, `rowsOf`, `keptRows` :162, `extend`, `otherDatabases`), `tables`-style building (`gen.Build`, `gen.Tables`; cases_test.go:288), and `loadjson.Model`.

- [ ] **Step 1: The generator.** `randomModel(r *rand.Rand) *loadjson.Model` and `mutate(r *rand.Rand, m *loadjson.Model) *loadjson.Model` (a deep copy, edited).
  - A model has 1–4 schemas named `R0…R3`; each has 1–6 fields named `f0…f5`, of types string, int64, int, bool, float64, time, bytes and enum (values `a`, `b`, `c`), each Optional, Nillable, Unique or with a literal Default by `r`; 0–2 indexes over 1–2 of its fields, unique or not; 0–2 edges (To/From pairs, Unique or not, Required only to another schema).
  - `mutate` makes 1–3 edits: add or drop a schema, add or drop a field, flip Optional/Nillable/Unique, change or drop a Default, add or drop an index, add or drop an edge, change an enum's values.
  - The generator only makes models the DSL accepts. `gen.Build` failing on one is a generator bug: the test fails with the seed and the model's JSON.
- [ ] **Step 2: `TestMigrateRandom`.** For seeds 1…N on each dialect `freshDB` gives (SQLite always; PostgreSQL and MySQL when their variables are set):
  - from and to are `gen.Tables` of `randomModel(rand.New(rand.NewPCG(seed, 0)))` and its `mutate`;
  - then `migrateCase`'s steps, with `PlanOptions.Allow` covering every finding of the plan (`Allow` built from `plan.Findings`), so destructive changes apply;
  - `invariants` holds after each apply (Drift against the model empty, Diff of the snapshot and the model empty, Drift against the snapshot empty), and the rows `keptRows` checks survive.
  - N is 500, or 50 under `-short`. Measured: TestMigrateCases runs 37 SQLite cases in 0.40 s, so 500 seeds take a few seconds, under builder ruling 17's 60 s. The test logs its time; if SQLite exceeds 60 s, lower N and record why.
  - A failing seed is a finding. The message names the seed, the dialect, both models' JSON and the scripts (`sqlFiles`). Stop and report it with the seed; do not change the generator to avoid it, except to stop a DSL-refused model.
- [ ] **Step 3: `FuzzDiff`**, `(seed uint64)`: from `randomModel` and `mutate`, for each dialect, with no database: `migrate.Diff(a, a)` is empty; `migrate.Diff(a, b)` succeeds or returns a `*migrate.Error`; when it succeeds, `migrate.Render(a, b, changes, migrate.RenderOptions{})` succeeds; `a` round-trips through `json.Marshal` and `UnmarshalJSON` to an `Equal` schema. Seeds 1–5.
- [ ] **Step 4: Run** `go test -count=1 -run TestMigrateRandom ./migrate` (SQLite), then under D. Then `go test -run XXX -fuzz '^FuzzDiff$' -fuzztime 30s ./migrate`. Expected: PASS each; record the times in the commit message.
- [ ] **Step 5: CI.** ci-local.sh and ci.yml run `go test -run XXX -fuzz '^FuzzDiff$' -fuzztime 10s ./migrate` beside FuzzSplit. TestMigrateRandom needs no new line: the root module's `go test -race ./...` runs it, on all three databases in CI.
- [ ] **Step 6: Commit.** `migrate: a property test of the planner over random models on every database, and FuzzDiff (M15)`. Then run C under D.

---

### Task 14: bench/scale's times relative to a reference build in the same run

**Files:**
- Modify: `bench/scale/main.go` (`report` :38-45, `run` :94-210, `check` :391-420, `main`'s print at :64), `bench/scale/thresholds.json`, `bench/scale/scale_test.go` (`TestScaleHarnessSmall` :18-40, `TestScaleHarnessSmallGraphQL` :122-150, `TestCheckPassesAGenerousThreshold` :153, `TestCheckRefusesAMissingOrUnknownKey` :173, `TestCommittedThresholds` :229-285).

**Interfaces:** Produces `report.RefBuildS`, `report.ColdBuildX`, `report.SchemaEditRebuildX` (JSON `ref_build_s`, `cold_build_x`, `schema_edit_rebuild_x`); the threshold keys `cold_build_x`, `schema_edit_rebuild_x`, `service_binary_mb`, `link_peak_mb`.

- [ ] **Step 1: The reference.** `run` writes, in its temporary directory, a module `ref` whose `main.go` is:

```go
package main

import (
	"fmt"
	"net/http"
)

func main() { fmt.Println(http.StatusOK) }
```

  It builds it cold, with its own fresh GOCACHE and the same environment and `-toolexec` wrapper as the app's build, once before the app's cold build and once after the schema-edit rebuild. `RefBuildS` is the mean of the two. `ColdBuildX = ColdBuildS / RefBuildS`, `SchemaEditRebuildX = SchemaEditRebuildS / RefBuildS`. The absolute seconds stay in the report but are not checked.
  - Why this reference: the app's cold build also compiles the standard library from an empty cache, so a slower or loaded machine slows both (decision 6).
- [ ] **Step 2: The check** compares `cold_build_x` and `schema_edit_rebuild_x` with their thresholds; `cold_build_s` and `schema_edit_rebuild_s` become unknown keys. The tests change to these keys; `TestScaleHarnessSmall` and its GraphQL twin also require `RefBuildS > 0`.
- [ ] **Step 3: Measure.** Three runs of each variant, one at a time, recording the 1-minute load average before each: `go run ./bench/scale -n 500 -s 100 -json` and with `-graphql`. One bracketed run of each was made while planning (load 10–25; the reference measured with a shell script, not yet the harness):

| variant | ref before, after (s) | cold (s) | edit (s) | cold_x | edit_x | binary MB | link MB |
|---|---|---|---|---|---|---|---|
| 500 | 6.03, 5.00 | 30.04 | 24.24 | 5.45 | 4.40 | 5.83 | 273.9 |
| 500-graphql | 4.74, 4.60 | 92.28 | 84.19 | 19.76 | 18.03 | 11.49 | 676.1 |

- [ ] **Step 4: Thresholds.** Each time ratio is 2× the largest of the three runs, sizes and memory unchanged (8.5, 420, 16.7, 993). Provisional, from the table: 500 `cold_build_x` 11, `schema_edit_rebuild_x` 9; 500-graphql 40 and 36. Rewrite each entry's `_measured` with the runs, the loads, the reference's times, and the rule; say the absolute times are no longer checked.
- [ ] **Step 5: Run** `go test -count=1 ./bench/scale`, then `go run ./bench/scale -n 500 -s 100 -check bench/scale/thresholds.json` and its `-graphql` twin. Expected: PASS and exit 0.
- [ ] **Step 6: Commit.** `bench/scale: build times are checked as ratios to a reference build in the same run`. Then run C.

---

### Task 15: Committed benchmark baselines, compared with benchstat

**Files:**
- Create: `bench/baseline/root.txt`, `bench/baseline/veloxotel.txt`, `bench/baseline/example-graphql.txt`, `bench/baseline/README.md`.
- Create: `scripts/bench-compare.sh`.
- Modify: `scripts/ci-local.sh`, `.github/workflows/ci.yml` (a step after the scale steps).

**Interfaces:** None.

- [ ] **Step 1: The script.** `scripts/bench-compare.sh` runs, from the repository root, one module at a time:
  - `go test -run '^$' -bench . -benchmem -count 6 -benchtime 200ms .` (root: BenchmarkOneRow, BenchmarkTenantScopedOneRow, BenchmarkHundredRows, BenchmarkOneRowWithDeadline, BenchmarkOneRowObserved, BenchmarkMetadataFirstUse);
  - the same in `contrib/veloxotel` (BenchmarkAdapter, BenchmarkBareSpan) and `example` with `./graphql` (BenchmarkNestedRequest);
  - each into a temporary file, then `go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e bench/baseline/<name>.txt <new>`.
  - With `-update` it writes the baselines instead.
  - It exits 0 whatever benchstat reports: the comparison is printed, not enforced (decision 9). Allocation counts are enforced by the budget tests already.
  - Measured at `-count 3`: the root's benchmarks took 13 s, veloxotel's about 2 s, example/graphql's 4.2 s. At `-count 6` the script takes about 40 s.
- [ ] **Step 2: Baselines.** Run `scripts/bench-compare.sh -update` on an idle machine (1-minute load under 4; record it). The README says where, when, the load, the Go version, and that CI's numbers come from other hardware, so CI's time deltas are informational.
- [ ] **Step 3: CI.** ci-local.sh and ci.yml run `scripts/bench-compare.sh` after the scale lines.
- [ ] **Step 4: Run** `scripts/bench-compare.sh`. Expected: exit 0, and a benchstat table per module.
- [ ] **Step 5: Commit.** `bench: committed benchmark baselines, compared with benchstat in CI`. Then run C.

---

### Task 16: Per-package coverage floors

**Files:**
- Create: `scripts/covfloor/main.go`, `scripts/covfloor/main_test.go`, `scripts/coverage-floors.txt`.
- Modify: `scripts/ci-local.sh:39`, `:62`, `:84`, `:98` (the four `go test -race` lines of the first pass); `.github/workflows/ci.yml:58`, `:83`, `:102`, `:115`; a checker step after the contrib/graphql step.

**Interfaces:** Produces the checker `go run ./scripts/covfloor -floors scripts/coverage-floors.txt <profile>...`.

- [ ] **Step 1: The checker.** It reads Go coverage profiles from several modules, merges blocks by `file:start,end` (a block counted in any profile is covered), and computes each package's statement coverage as `covered / total`. It reads the floors file: one `<import path> <percent>` per line; `#` starts a comment.
  - It fails, listing every problem, when a package's coverage is under its floor; when a package in the profiles has no line ("add a floor for <pkg>: measured <x>%"); and when a line names a package no profile has ("<pkg> has a floor but no coverage: remove it").
  - Tests: a two-package profile against floors that pass, one under, a missing line, and a stale line.
- [ ] **Step 2: CI.** Each module's `go test -race -count=1 …` gains `-coverprofile=<root>/cover-<module>.out` (root, example, contrib/veloxotel, contrib/graphql), and the checker runs once after them, from the root, over the four files. Add `cover-*.out` to `.gitignore`.
- [ ] **Step 3: Measure** the floors on this task's tree, without database variables, one module at a time. The values at 47c5edd, with no database, are below; each floor is the measured value minus 1.0, at least 0, one decimal. A measured value below 47c5edd's is a finding to report, not a floor to lower.

```text
# root module
github.com/syssam/velox2 93.6
github.com/syssam/velox2/bench/scale 73.6
github.com/syssam/velox2/cmd/velox-gen 90.3
github.com/syssam/velox2/internal/bindmodel 7.7
github.com/syssam/velox2/internal/bindmodel/bindtype 0.0
github.com/syssam/velox2/internal/bindmodel/schema 0.0
github.com/syssam/velox2/internal/buildinfo 58.3
github.com/syssam/velox2/internal/ddl 96.1
github.com/syssam/velox2/internal/gen 95.8
github.com/syssam/velox2/internal/handlegen 0.0
github.com/syssam/velox2/internal/handlegen/gen 0.0
github.com/syssam/velox2/internal/tempmod 83.3
github.com/syssam/velox2/internal/testmodel 0.0
github.com/syssam/velox2/internal/testmodel/kind 0.0
github.com/syssam/velox2/internal/testmodel/schema 0.0
github.com/syssam/velox2/internal/testrepo 0.0
github.com/syssam/velox2/migrate 82.1
github.com/syssam/velox2/plugin 96.5
github.com/syssam/velox2/schema 100.0
github.com/syssam/velox2/schema/edge 72.9
github.com/syssam/velox2/schema/field 85.8
github.com/syssam/velox2/schema/index 87.5
github.com/syssam/velox2/schema/internal/desc 85.1
github.com/syssam/velox2/schema/load 81.4
github.com/syssam/velox2/schema/mixin 57.1
# contrib/veloxotel
github.com/syssam/velox2/contrib/veloxotel 96.8
# contrib/graphql
github.com/syssam/velox2/contrib/graphql 100.0
github.com/syssam/velox2/contrib/graphql/cmd/velox-graphql 90.9
github.com/syssam/velox2/contrib/graphql/internal/english 100.0
github.com/syssam/velox2/contrib/graphql/internal/gogen 94.2
github.com/syssam/velox2/contrib/graphql/internal/sdl 94.1
github.com/syssam/velox2/contrib/graphql/veloxgql 93.3
github.com/syssam/velox2/contrib/graphql/veloxgql/gqlimpl 96.6
github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared 0.0
# example: each example's package; every */gen, */schema, graphql/graph and internal/golden is 0.0
github.com/syssam/velox2/example/graphql 60.3
github.com/syssam/velox2/example/hooks 81.5
github.com/syssam/velox2/example/otel 92.6
github.com/syssam/velox2/example/policy 82.4
github.com/syssam/velox2/example/relations 82.3
github.com/syssam/velox2/example/routing 81.1
github.com/syssam/velox2/example/start 84.2
github.com/syssam/velox2/example/tenant 87.8
github.com/syssam/velox2/example/tour 88.9
github.com/syssam/velox2/example/tx 89.0
```

  These are statement coverage per package from `go test -coverprofile`, merged as the checker does (a scratch program agreed with `go test`'s own per-package figures). `internal/itest` has no statements and no line. `scripts/covfloor` gets its own line from its measurement.
- [ ] **Step 4: Run** the four modules' tests with `-coverprofile`, then the checker. Expected: exit 0. Then lower one floor's package by editing a floor to 100.0 and check it fails naming the package; restore.
- [ ] **Step 5: Commit.** `scripts: per-package coverage floors, checked in CI`. Then run C.

---

### Task 17: The wave's gate

- [ ] **Step 1:** Run `VELOX_CI_MATRIX=1 bash scripts/ci-local.sh` with nothing else running. Expected: rc=0. If only a bench/scale time ratio fails, run it once more. Never raise a threshold.
- [ ] **Step 2:** Run `go test -count=2 -shuffle=on .` once at the root (Task 4's order check). Expected: PASS; record the seed.
- [ ] **Step 3:** Run the leftover greps below. Expected: each prints what it says.
- [ ] **Step 4:** Record PostgreSQL 14's results for Tasks 6, 9 and 13's [D] values from the matrix run, if those tasks did not.

## Leftover greps

Each was run at 47c5edd.

1. **Task 1.** `git grep -nP 'panic\(fmt\.Sprintf\("fake:|src\.\(\w+\)' -- export_test.go` prints today `export_test.go:299`, `:301`, `:303`, `:305`, `:321`, `:345`, `:353`. After Task 1: nothing (checked assertions use the `v, ok :=` form).
2. **Task 7.** `git grep -nP 'CREATE TABLE' -- example` prints today 18 lines in the ten examples' main.go (graphql :136, :144, :153, :158; hooks :33, :40; otel :35; policy :31, :32; relations :30, :31, :33; routing :33; start :29; tenant :29; tour :32, :43; tx :32). After Task 7: only lines in `example/*/migrations/0001_init.sql`.
3. **Task 7.** `git grep -nP 'later velox2 sub-project|plain DDL' -- example` prints today `example/README.md:38`, `example/tour/README.md:23-24`, and the ten main.go comments. After Task 7: nothing.
4. **Task 10.** `git grep -nP 'model\.json' -- contrib` prints today `contrib/graphql/internal/gogen/render_test.go:45`, `contrib/graphql/internal/sdl/render_test.go:31`, `:36`. After Task 10: only `contrib/graphql/internal/modeltest/modeltest.go`.
5. **Task 5.** `git grep -nP 'MkdirTemp\(""' -- failover_test.go` prints today `failover_test.go:68`. After Task 5: the one windows branch.
6. **Task 14.** `git grep -nP 'cold_build_s|schema_edit_rebuild_s' -- bench` prints today `bench/scale/main.go:40`, `:41`, `:407`; `bench/scale/scale_test.go:30`, `:156`, `:183`, `:243`, `:270`, `:272`; `bench/scale/thresholds.json:4`, `:5`, `:11`, `:12`. After Task 14: only main.go's two JSON tags, and scale_test.go's check that the keys are now unknown.

## Rulings this plan makes

1. **(evidence) C9 is closed** by `TestCacheKeysMoreShapes` (render_cache_test.go:183).
2. **(evidence) Two of C44's items are closed:** the CI fuzz regex is anchored (ci.yml:63, ci-local.sh:42), and `SamePreds` compares lengths (export_test.go:445). **(call)** FuzzScope's "copy is a rebuild" stays: building the conditions again from the same input is a deterministic, independent copy, which is what the check needs.
3. **(call) The fake converts integer kinds.** database/sql converts an `int64` into a `uint64` destination, and the fake now does too, so a uint64 key can be tested at all (Task 1). It refuses an overflow with an error.
4. **(call) C45's cross-org parent is set in its own test,** not added to `seedTickets`: the seed's counts are asserted across a dozen itests.
5. **(call) C39 qualifies the table through a Rewrite hook,** which replaces `st.Entity` with a copy in schema `vx_billing`. No generated model has a schema, and the hook path is supported (render_cache.go:43).
6. **(call) Examples use a temporary SQLite file.** `migrate.Up` on a one-connection in-memory database loses its tables.
7. **(call) Coverage floors are measured without databases,** so a local run without Docker passes; CI, with databases, runs above them. Floors are the measured value minus 1.0.
8. **(call) The order check (`-shuffle=on -count=2`) runs in the gate, not in CI:** a random order in CI would fail at random on a bug the gate can find once.
9. **(call) G46 covers TestMutationsRun only,** the triage's row; see decision 7.

## Decisions for the controller

1. **M16's itest half.** The triage's M16 names `internal/itest` too. Its DDL is deliberately unlike the generated tables: identities that start at 100, named unique constraints (`invoices_org_number_uq`, `tickets_org_title_uq`) whose names tests assert, items without autoincrement, no foreign keys. Moving it to migrations means changing testmodel's schema or the assertions. **Recommendation:** examples only in this wave (Task 7); the itest keeps its DDL, with a comment saying why. `binding_test.go` already builds its tables with migrate.
2. **`ClearProfile: true` over a Write-hidden current link** returns no error and leaves the link (Task 11 Step 2), consistent with TestUnlinkScope's rule for a scoped unlink. **Recommendation:** pin it as is, and document it in Wave 6 with the scoped-unlink rule.
3. **An owning prevID update onto a held folder** surfaces the database's unique violation (`velox: gen.Folder: unique constraint folders.folder_next violated: …`, BAD_USER_INPUT "unique constraint violated"), where create gets a named message (`a Folder is already linked to another Folder`). **Recommendation:** pin as is (ent behaves so); a pre-check would cost a read on every update.
4. **A nested With load's cap error names only the inner load** (`velox: testmodel.Item: loading Invoice: …`, not `testmodel.Invoice: loading Items: …`), as existsError drops the middle of an EXISTS path (followups :79). **Recommendation:** pin today's text in Task 3, and record it with the existsError item for after 1.0.
5. **An upsert's update branch does not fire `UpdatedAt`'s UpdateDefault** (measured on SQLite: `updated_at` stays the seed's after `Update(I.Total)`), while Update and Save do. **Recommendation:** pin it in Task 6 as today's behavior and rule on it as a possible defect for Wave 6's docs or a post-1.0 fix; ent's upsert behavior should be checked first.
6. **bench/scale's reference build** is a cold build of a fixed net/http program, the mean of one before and one after, and the ratio thresholds are 2× the largest of three runs. Alternatives: CPU time (`user+sys`) instead of wall time, or a velox-free app of the same shape. **Recommendation:** as planned; the wall-time ratio is what a developer feels, and the reference compiles the same standard library from the same empty cache.
7. **G46's -race** covers TestMutationsRun's inner run only. The other four inner runs could take it too, at a cost in CI time not measured here. **Recommendation:** TestMutationsRun only.
8. **Shuffle in CI.** **Recommendation:** gate only (ruling 8).
9. **Benchstat report-only.** Baselines come from the developer's machine, and CI runs on other hardware, so a time delta in CI is not a regression. **Recommendation:** print, never fail; allocations stay enforced by the budget tests.

## Left by this plan

- **Wave 6 (docs):** decisions 2, 4 and 5 as ruled; the examples' README sections on migrations (Task 7 writes the short version); bench/scale's ratio thresholds and the benchmark baselines in the contributor docs.
- **After 1.0:** G32 (a case-insensitive volume in CI); the itest's DDL (decision 1) if not ruled into this wave.
