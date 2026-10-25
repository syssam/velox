# Pre-1.0 Hardening, Wave 2: the Core Runtime — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before 1.0 freezes them, the root package and veloxotel get their final runtime behaviour:
- the error tree is complete and safe: every supported dialect's constraint errors are classified, a typed-nil driver error cannot panic, a failed lock is `ErrLocked`, and a timeout keeps the driver's error;
- transactions cannot hang or escape: rollbacks are time-bounded, savepoints keep the transaction's deadline and shard, and the transaction's route reaches its hooks and its span;
- the scope and policy backstops hold against Rewrite hooks;
- `WithTenant` converts the ids it safely can;
- a bidirectional self many-to-many edge writes and removes both join rows, as ent's does;
- the perf items are done, and `Query.With`/`Rel.With` are settled by measurement;
- the compile-cost and allocation bounds are re-measured once, at the end.

**Architecture:** Eleven tasks, each building and passing the gate at its commit.
- Task 1 measures `With` first, because its outcome rewrites call sites the later tasks' tests use.
- Tasks 2–5 are the error, transaction, telemetry and pipeline items, file by file: errclass.go, tx.go, txhook.go with veloxotel, then db.go.
- Tasks 6–8 are the statement, load and tenant items: scope.go, insert.go, load.go and scope_api.go.
- Task 9 is G17's core part. It is the one task that touches velox-gen (`Desc.Bidi`).
- Task 10 is the perf items.
- Task 11 re-measures.

Everything is in the root package and `contrib/veloxotel`, except Task 9's emitter line and its blog golden, and Task 8's tenant example.

**Tech Stack:** Go 1.27. golangci-lint v2.14.0 and revive v1.17.0, as `scripts/ci-local.sh` pins them. OpenTelemetry Go v1.47.0 (`metric.Float64Histogram.Enabled`). Docker for PostgreSQL 18 and MySQL 8.4.

**Spec:** `docs/specs/2026-10-25-pre-1.0-hardening-design.md`. Its final block, **"Rulings on the builder check"**, is binding and overrides the rulings above it where they differ.
- "Spec ruling N" is item N of the spec's Rulings, and "builder ruling N" is item N of that final block.
- The evidence for each id is in `docs/reviews/2026-10-25-triage-{C,G,M}.md`.
- Every file:line below was taken at b3348ba.

## Global Constraints

- **Shell.** Run `export GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2` before any go command.
  - The machine is memory-constrained and shared. Run one heavy command at a time: never two `go test` runs, two lints, or a lint beside a test. Do not kill other processes.
  - The gate is `bash scripts/ci-local.sh` with Docker. Only one runs at a time. If golangci-lint's lock is held, run it with `--allow-parallel-runners`.
  - `bench/scale` can fail on time alone when the machine is loaded. If the gate fails only on a bench/scale time threshold, run the gate once more with nothing else running. Never raise a threshold.
- **Commit, then check.** Every task commits first, then runs G (where it says) and the gate on the committed tree.
  - On a failure, the task fixes the cause and amends its own commit (`git commit --amend --no-edit`, staging by name), then runs both again.
  - The gate passes at every task's commit: rc=0.
- **Commits.**
  - The message is `Area: what changed`. It ends with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - Stage files by name with `git add <paths>`, never with `git add -A` or `.`, and never stage `.DS_Store`.
  - `git status --short` lists what a task changed. Every path in it but `.DS_Store` is staged.
  - Never push.
- **No deprecated aliases.** A replaced name is deleted. A method replaced by a function goes into `oldapi_test.go`'s old list.
- **Lint and revive stay at 0** in every module, generated code included.
- **Greps.** Apple's git greps with BSD regex, where `-E` never matches `\b`. Every leftover grep here uses `git grep -P`.
- **Test names.** Before adding a test function or a test type, run `git grep -nP 'func <Name>\(|type <name>\b' -- '*.go'`. Expected: nothing. The names below were checked at b3348ba. A name added by an earlier task of this plan counts.
- **Goldens.** A golden changes only through its `-update` run or a hand edit that a step names. Read every changed golden line: only what the task changes may differ.
- **Historical documents.** Nothing under `docs/reviews/` or `docs/plans/` changes, except this plan. Under `docs/specs/`, only the api-cleanup spec's ruling 21 changes (Task 1).
- **The followups file** (`docs/plans/2026-10-14-core-1-followups.md`) is not touched. Wave 6 marks its items, citing these commits.
- **No machine-specific paths** in committed files.
- **Generated-code version.** No task bumps `GeneratedCodeIsVersion6`. Task 9 adds a `Desc` field that velox-gen writes, but version 6 has never been released (plan ruling 14).

### R: the regeneration steps

Each task runs the steps it names, in this order, from the repo root.

- **R2** `go build -o bin/velox-gen ./cmd/velox-gen && bin/velox-gen -out internal/testmodel -package testmodel ./internal/testmodel/schema && bin/velox-gen -out internal/bindmodel -package bindmodel ./internal/bindmodel/schema`
- **R3** `go test ./internal/gen -update`. This rewrites `internal/gen/testdata/golden/*/want`, `internal/gen/testdata/golden/*/plugin.json` and `internal/gen/testdata/tables/*.json`.
- **R5** `(cd example && for x in start relations tenant policy hooks tx routing otel tour; do go tool velox-gen -out ./$x/gen ./$x/schema; done && go tool velox-gen -out ./graphql/gen -plugin velox-graphql ./graphql/schema)`
- **R7** `(cd example && go test ./start ./relations ./tenant ./policy ./hooks ./tx ./routing ./otel ./tour -update)`. This rewrites each `testdata/output.golden`.

**D: the databases**, for a step that names them. Never run D while a gate runs: both use the compose project `velox2-ci`.

```bash
docker compose -f docker-compose.ci.yml -p velox2-ci up -d --wait
export VELOX_TEST_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
export VELOX_TEST_MYSQL='velox:velox@tcp(127.0.0.1:53306)/velox?parseTime=true&clientFoundRows=true'
export VELOX_TEST_MYSQL_ADMIN='root:velox@tcp(127.0.0.1:53306)/?parseTime=true&clientFoundRows=true'
# … the step's command …
docker compose -f docker-compose.ci.yml -p velox2-ci down -v
```

**The generated check (G)** is CI's "generated files are up to date". It runs on the committed tree, since it diffs against HEAD. Expected: every command exits 0, `verify` prints `velox-gen verify: N entities, 0 with errors` per package, and the `git diff` is empty.

```bash
go build -o bin/velox-gen ./cmd/velox-gen
for m in testmodel bindmodel; do bin/velox-gen -check -out internal/$m -package $m ./internal/$m/schema && bin/velox-gen verify -out internal/$m ./internal/$m/schema; done
(cd example && for d in */schema; do x=${d%/schema}; [ "$x" = graphql ] && continue; go tool velox-gen -check -out ./$x/gen ./$x/schema && go tool velox-gen verify -out ./$x/gen ./$x/schema; done \
  && go tool velox-gen -check -out ./graphql/gen -plugin velox-graphql ./graphql/schema \
  && go tool velox-gen verify -out ./graphql/gen -plugin velox-graphql ./graphql/schema \
  && go -C graphql tool gqlgen generate && git diff --exit-code HEAD -- graphql/graph \
  && test -z "$(git ls-files --others --exclude-standard -- graphql/graph)")
```

## Review Focus

1. **One statement whose entities have tenant columns of different Go types.** A statement on an entity with an `int64` tenant, holding an EXISTS on an entity with an `int32` tenant, runs under `WithTenant(ctx, 7)`. Each condition must carry its own column's type. If the conversion happens once per statement, the EXISTS gets the wrong type, and on PostgreSQL that is a wrong comparison or an error. Pin: Task 8 `TestTenantConversionPerTarget`.
2. **A self-link on a bidirectional edge.** Linking a node to itself must write one row, not two, or the primary key refuses the insert. Pin: Task 9 `TestBidiLinks`, its a–a case.
3. **A rollback on a dead connection after the caller canceled.** `Tx` must return within the rollback timeout, not hang on the detached context. Pin: Task 3 `TestRollbackIsBounded`.
4. **A Rewrite hook that gives an InsertAll upsert outside a transaction its update branch.** It must be refused before any row is sent. A hidden row must never fail the statement after the visible rows are written. Pin: Task 6 `TestHookCannotConditionAMultiRowUpsert`.
5. **A write that fails after it reached the database** (a timeout after the commit, or a RETURNING scan error). It must pin the session, so the caller's next read goes to the primary and sees its own write. A write refused before sending must not pin. Pin: Task 5 `TestFailedWritePinsTheSession`.

---

### Task 1: `With` measured; methods kept or replaced by `velox.With` and `velox.WithRel` (G18, M28)

This covers spec ruling 11 and builder ruling 10. The measurement comes first, and its result picks Branch A or Branch B. Only one branch is carried out.

**Files:**
- Modify: `compilecost_test.go:105-125` (entityDescUseSrc's `Use`) and `:251-277` (the bound comments).
- Modify: `docs/specs/2026-10-20-api-cleanup-design.md:282`, ruling 21, whose last sentence is replaced (Step 5).
- Branch B only: `query.go:52-67`, `relation.go:335-343`, `oldapi_test.go`, `doc.go`, and every `.With(` call site (98 lines today; see Step 6).

**Interfaces:**
- Consumes: nothing.
- Produces, in Branch B only:

```go
// query.go
// With returns q also loading relation r of every entity it returns, … (Query.With's doc, moved)
func With[E, T any](q Query[E], r Rel[E, T]) Query[E]

// relation.go
// WithRel returns r also loading relation next of each entity r loads.
func WithRel[E, T, U any](r Rel[E, T], next Rel[T, U]) Rel[E, T]
```

`Query.With` and `Rel.With` are deleted. Branch A keeps them unchanged. Later tasks write their loads with whichever form this task leaves.

- [ ] **Step 1: Make `Use` call both forms.** In entityDescUseSrc, change the first statement of `Use` to:

  ```go
  if _, err := db.Query[E]().Where(eKids.Has()).With(eParent).With(eKids.With(eParent)).Limit(1).All(ctx); err != nil {
  ```

  Run `go test -count=1 -run 'TestGenericTypesCompileSmall/^entityDescUse$' -v . 2>&1 | grep 'entityDescUse: +'`. Run it twice: the sizes are deterministic, and the two runs must agree. Record the number as **M_method**.
- [ ] **Step 2: Measure the function form in a scratch worktree.** Nothing from the worktree is committed.

  ```bash
  git worktree add /private/tmp/claude-501/velox2-with-measure HEAD
  ```

  In that worktree:
  - delete `Query.With` and `Rel.With`;
  - add `With` and `WithRel` with the signatures above, whose bodies are the methods' bodies;
  - write `Use`'s first statement as `if _, err := velox.With(velox.With(db.Query[E]().Where(eKids.Has()), eParent), velox.WithRel(eKids, eParent)).Limit(1).All(ctx); err != nil {`.

  Run the Step 1 command there, twice. Record the number as **M_func**. Then run `git worktree remove --force /private/tmp/claude-501/velox2-with-measure`.
- [ ] **Step 3: Decide.** Δ = M_method − M_func. The threshold is 1% of entityDescUse's bound, 766976 bytes, which is 7670.
  - If Δ > 7670, take **Branch B**.
  - Otherwise take **Branch A**, a negative Δ included.

  Write M_method, M_func and Δ in the task report.
- [ ] **Step 4, Branch A: Keep the methods.**
  - Keep Step 1's `Use`.
  - Re-set the `entityDescUse` and `entityDescHooks` bounds to their new measurements plus about 1.2%, rounded up to the next KiB. Measure both with `go test -count=1 -run 'TestGenericTypesCompileSmall' -v .` and read the `entityDescUse: +` and `entityDescHooks: +` lines.
  - Append to each row's comment: `remeasured on 2026-10-26 after Use called Rel.With (G18): <old> before`.
  - `policyGrowthMax` keeps its value unless the `policy and hooks:` line exceeds it, in which case stop and report.
- [ ] **Step 4, Branch B: Replace the methods with functions.**
  - Add `With` and `WithRel` and delete the two methods.
  - Rewrite every call site in Step 6's grep: `q.With(r)` becomes `velox.With(q, r)`, and `r.With(n)` becomes `velox.WithRel(r, n)`. In the root package's internal tests the `velox.` qualifier is dropped.
  - Write `Use` as in Step 2.
  - `oldapi_test.go`'s old list gains `^func \(q Query\[E\]\) With\[` and `^func \(r Rel\[E, T\]\) With\[`.
  - doc.go's example uses `velox.With`.
  - Re-set the two bounds as in Branch A, with the comment `remeasured on 2026-10-26 when With and WithRel became functions (G18): <old> before`.
- [ ] **Step 5: Record the ruling in the api-cleanup spec.** Ruling 21's last sentence, "`Query.With[T]`, which predates this rule, takes a `Rel[E, T]`.", is replaced.
  - Branch A: `` `Query.With[T]` and `Rel.With[U]` stay methods, as the exception to this rule: on 2026-10-26, with both called, `entityDescUse` measured M_method bytes with the methods and M_func with functions `velox.With` and `velox.WithRel`, a difference of Δ, under the 1% (7670 bytes) the pre-1.0 hardening spec set (its ruling 11 and builder ruling 10).``
  - Branch B: `` `Query.With[T]` and `Rel.With[U]` became the functions `velox.With(q, r)` and `velox.WithRel(r, next)` on 2026-10-26: with both called, `entityDescUse` measured M_method bytes with the methods and M_func with the functions, a difference of Δ, over the 1% (7670 bytes) the pre-1.0 hardening spec set (its ruling 11 and builder ruling 10).``

  Write the measured numbers in place of the names.
- [ ] **Step 6: Leftover grep (Branch B only).** `git grep -nP '\.With\(' -- '*.go' | grep -vP 'With(Tenant|out|AllTenants)|velox\.With'`. Expected: nothing.
  - At b3348ba this prints 98 lines. The most are in load_test.go (24), each_test.go (14) and through_test.go (11). It also prints internal/itest (9), internal/gen/testdata/e2e/main_test.go (5), example/relations/main.go (2), contrib/graphql/cmd/velox-graphql's e2e tests (3), doc.go (1) and compilecost_test.go (1).
  - Generated code emits no `.With(` (neither `internal/gen` nor `gogen`), so no golden changes.
- [ ] **Step 7: Run** `go test ./...`, then `(cd example && go test ./...)`, then `(cd contrib/graphql && go test ./...)`, one at a time. Expected: PASS. In Branch A only the first matters.
- [ ] **Step 8: Commit.**
  - Branch A: `velox: Query.With and Rel.With stay methods, measured (G18)`.
  - Branch B: `velox: With and WithRel are functions, as Sum is (G18)`.
- [ ] **Step 9: Run G (Branch B), then the gate.** Expected: G clean, gate rc=0.

### Task 2: Error classification: MySQL 1364, MariaDB 4025, typed nils, SQLite's NOT NULL column, and `ErrLocked` (C26, C27, C28, G70)

**Files:**
- Modify: `errclass.go`, `errors.go:12-75` (add `ErrLocked`), `db.go:173-183` (`dbErr`), `route.go:651` (`failoverable`'s PgError), `query.go` (the `NoWait` godoc names `ErrLocked`).
- Test: `errclass_test.go`, `internal/itest/lock_test.go:228-231`.

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// errors.go, in the var block after ErrTooManyRows
// ErrLocked is returned by a statement that could not take a lock without
// waiting: a NoWait read that met a row another transaction holds, or a
// PostgreSQL lock_timeout. It matches PostgreSQL's 55P03 (lock_not_available)
// and MySQL's 3572; the driver's error is wrapped with it.
ErrLocked = errors.New("lock not available")

// errclass.go
func pgError(err error) *pgconn.PgError // errors.As, but nil for a typed-nil *pgconn.PgError
func locked(err error) bool             // 55P03, or MySQL 3572
```

- **dbErr** classifies in this order: an existing `*ConstraintError`, then `constraint`, then `locked`. A locked error becomes `fmt.Errorf("velox: %s: %w: %w", info.Name, ErrLocked, err)`. Everything else is as now.
- **driverError** returns nil for a typed-nil pointer before it reads the error or calls any method on it: `if v := reflect.ValueOf(err); v.Kind() == reflect.Pointer && v.IsNil() { return nil }`.
  - Every `errors.As(err, &pg)` (errclass.go:22, :178, :194 and route.go:651) becomes `pgError(err)`, with a nil check.
  - The mysql and sqlite readers go through `driverError` already.
- **mysqlKind** gains two cases:
  - `case 1364: return ConstraintNotNull, between(msg, "Field '", "'")` (strict mode, "Field 'code' doesn't have a default value");
  - `case 4025: return ConstraintCheck, between(msg, "CONSTRAINT `", "`")` (MariaDB, "CONSTRAINT `p_n_ck` failed for `velox`.`p`").
- **sqliteKind**, for code 1299 only, cuts the subject after its last `.`: `p.code` becomes `code`. `ConstraintError.Constraint`'s doc then holds for SQLite too.

- [ ] **Step 1: Write the failing tests** in `errclass_test.go`:
  - `TestConstraintErrors` gains:
    - `{myErr(1364, "Field 'code' doesn't have a default value"), ConstraintNotNull, "code"}`;
    - `{myErr(4025, "CONSTRAINT `p_n_ck` failed for `velox`.`p`"), ConstraintCheck, "p_n_ck"}`.
  - `TestSQLiteKind`: `sqliteKind(1299, "constraint failed: NOT NULL constraint failed: p.code (1299)")` gives `ConstraintNotNull, "code"`. The UNIQUE case keeps `p.code`.
  - `TestTypedNilDriverErrors`. For each of `(*pgconn.PgError)(nil)`, `fmt.Errorf("x: %w", (*mysql.MySQLError)(nil))` and `errors.Join(io.EOF, (*pgconn.PgError)(nil))`, none of these panics:
    - `dbErr(invInfo, err)` is not a `*ConstraintError`, and `!errors.Is(…, ErrLocked)`;
    - `DriverCode(err)` gives `"", false`;
    - `retryable(err)` and `failoverable(err)` are false.
  - `TestErrLocked`:
    - `dbErr(invInfo, &pgconn.PgError{Code: "55P03"})` and `dbErr(invInfo, myErr(3572, "Statement aborted because lock(s) could not be acquired immediately and NOWAIT is set."))` match `ErrLocked`. Each still `errors.As` its driver type, and its text starts `velox: velox.inv: lock not available: `.
    - `&pgconn.PgError{Code: "40001"}` and `myErr(1205, "Lock wait timeout exceeded")` do not match `ErrLocked`.
- [ ] **Step 2: Run** `go test -run 'TestConstraintErrors|TestSQLiteKind|TestTypedNilDriverErrors|TestErrLocked' .`. Expected: FAIL. `TestTypedNilDriverErrors` panics, and `ErrLocked` is undefined.
- [ ] **Step 3: Implement** as in Interfaces. Add a sentence to `NoWait`'s godoc: `On PostgreSQL and MySQL the error matches ErrLocked.`
- [ ] **Step 4: Extend the itest.** In internal/itest/lock_test.go:228-231, after the `DriverCode` check, add `if !errors.Is(err, velox.ErrLocked) { t.Errorf("NoWait: %v does not match ErrLocked", err) }`.
- [ ] **Step 5: Leftover grep.** `git grep -nP 'errors\.As\(err, &pg\)' -- '*.go' ':!*_test.go'`. Expected: nothing. At b3348ba it prints errclass.go:22, :178, :194 and route.go:651.
- [ ] **Step 6: Run** `go test .`, then run D with `go test -count=1 -run 'TestLocks' ./internal/itest`. Expected: PASS.
- [ ] **Step 7: Commit** `velox: classify MySQL 1364 and MariaDB 4025, SQLite NOT NULL names the column, typed-nil driver errors cannot panic; ErrLocked`.
- [ ] **Step 8: Run the gate.** Expected: rc=0.

### Task 3: Bounded rollbacks, deadline-bound savepoints, the savepoint's shard (C23, C34, C89)

**Files:**
- Modify: `tx.go:202-338`.
- Modify, the test fake: `export_test.go:16-155`.
- Test: `tx_test.go`, `deadline_test.go`, `shard_test.go`.

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// tx.go
// rollbackTimeout bounds every rollback velox sends on a context it detached
// from the caller's cancellation: the transaction's ROLLBACK and a savepoint's
// ROLLBACK TO and its RELEASE. A var, so that a test can lower it.
var rollbackTimeout = 5 * time.Second

// rollbackContext is ctx without its cancellation, with rollbackTimeout from
// now; for a savepoint also no later than the transaction's deadline (also).
func rollbackContext(ctx context.Context, also time.Time) (context.Context, context.CancelFunc)

// export_test.go
func SetRollbackTimeout(d time.Duration) (restore func())
// FakeExec gains:
//   BlockRollback bool         // ROLLBACK and ROLLBACK TO wait until their context ends, then return its error
//   RollbackDeadline time.Time // the deadline of the last ROLLBACK's or ROLLBACK TO's context; zero when none
```

- **C23.** Both `end := context.WithoutCancel(ctx)` (tx.go:218, :305) become `rollbackContext`. The transaction passes a zero `also`, because its ROLLBACK must run after its deadline has passed. A savepoint passes `db.txDeadline`.
  - database/sql's `Tx.Rollback` takes no context, so on `Open`'s DBs the bound covers ROLLBACK TO only. Tx's godoc says so, in a sentence after the paragraph on deadlines: `A rollback velox sends after fn fails, or once ctx has ended, is bounded to 5 seconds; database/sql's Tx.Rollback takes no context, so on a DB from Open that bound covers a savepoint's ROLLBACK TO only.`
- **C34.** `SAVEPOINT` and the `RELEASE SAVEPOINT` after fn succeeds run on `sctx, cancel := bound(ctx, 0, 0, db.txDeadline, db.txCause)`, with the statement deadlines left out.
  - When such a statement fails and `timedOut(sctx)` holds, the error is `errors.Join(errTimeout("savepoint"), fmt.Errorf("velox: savepoint: %w", err))`, and the same with `release savepoint`.
- **C89.** Before `SAVEPOINT`, when `db.txShard != nil`, `savepoint` reads the context's route as `routeTx` does.
  - An error of `shardOf` is returned as `fmt.Errorf("velox: savepoint: %w", err)`.
  - A shard other than `db.txShard` gives `fmt.Errorf("velox: savepoint: for shard %q in a transaction on shard %q", sh.name, db.txShard.name)`.
  - A context that names no shard passes, as a statement's does.
  - Nothing is sent, and fn does not run.

- [ ] **Step 1: Write the failing tests.**
  - `TestRollbackIsBounded` (tx_test.go):
    - With `defer velox.SetRollbackTimeout(50*time.Millisecond)()` and `e := &velox.FakeExec{BlockRollback: true}`, `db.Tx(ctx, fn)` where fn returns `boom` returns within 2 s.
    - The error matches `boom` and `context.DeadlineExceeded`, but not `velox.ErrTimeout`.
    - `e.RollbackDeadline` is non-zero and no later than the start plus 50 ms plus 20 ms.
    - The same holds for a nested `tx.Tx` whose fn returns `boom`: the ROLLBACK TO is bounded, and the error text holds `velox: rollback to savepoint: `.
    - The same holds for a canceled ctx: fn cancels it and returns nil, and Tx returns within 2 s.
  - `TestSavepointIsBoundedByTheTxDeadline` (deadline_test.go):
    - Inside `db.Tx(ctx, fn, velox.TxTimeout(time.Hour))`, read fn's deadline `dl` from its ctx. Then `tx.Tx(context.Background(), nested)`.
    - In nested, before any statement, `e.Deadline` (SAVEPOINT's) equals `dl`. After nested returns nil, `e.Deadline` (RELEASE's) equals `dl`.
    - Under `TxTimeout(20*time.Millisecond)` with `e.Block = true` set before the nested Tx, the nested Tx's error matches `velox.ErrTimeout` and `context.DeadlineExceeded`, and its text holds `velox: savepoint: `.
  - `TestSavepointChecksItsShard` (shard_test.go), on an OpenShards DB of shards "a" and "b" (the file's helper), inside `db.Tx(velox.WithShard(ctx, "a"), …)`:
    - `tx.Tx(velox.WithShard(ctx, "b"), fn)` fails with `velox: savepoint: for shard "b" in a transaction on shard "a"`. fn did not run, and shard a's Log has no `SAVEPOINT`.
    - `tx.Tx(velox.WithShard(ctx, "zz"), fn)` matches `velox.ErrNoShard`.
    - `tx.Tx(ctx, fn)` runs fn.
- [ ] **Step 2: Run** `go test -run 'TestRollbackIsBounded|TestSavepointIsBoundedByTheTxDeadline|TestSavepointChecksItsShard' .`. Expected: FAIL to compile on `SetRollbackTimeout` and `BlockRollback`.
- [ ] **Step 3: Implement** as in Interfaces. `fakeTx.rollback` and FakeExec's `ROLLBACK TO` record `RollbackDeadline` and honour `BlockRollback`.
- [ ] **Step 4: Leftover grep.**
  - `git grep -nP 'context\.WithoutCancel\(ctx\)' -- tx.go`. Expected: only inside `rollbackContext`. At b3348ba it prints tx.go:218 and :305.
  - `git grep -nP 'db\.ex\.exec\(ctx, "(SAVEPOINT|RELEASE)' -- tx.go`. Expected: nothing. At b3348ba it prints :302 and :323. The RELEASE in `rollbackSavepoint` (:336) runs on the rollback context it is given, and stays.
- [ ] **Step 5: Run** `go test -race -count=1 -run 'Tx|Savepoint|Rollback|Shard|Deadline' .`. Expected: PASS.
- [ ] **Step 6: Commit** `velox: rollbacks bounded to 5s, savepoints keep the transaction's deadline and check its shard`.
- [ ] **Step 7: Run the gate.** Expected: rc=0.

### Task 4: `TxInfo.Route`; veloxotel reads it, counts a fallback once per span, and skips disabled instruments (C87, G53, C97, C70 = G59)

**Files:**
- Modify: `txhook.go:25-85`, `tx.go:111-143`, `statement.go:190-197` (the `Route` doc), `route.go:298-305` (`Route.Fallback`'s doc).
- Modify: `contrib/veloxotel/tx.go`, `contrib/veloxotel/statement.go:44-101`, `contrib/veloxotel/metrics.go:54-80`, `contrib/veloxotel/doc.go` (the tx span's attributes).
- Test: `txhook_test.go`, `contrib/veloxotel/tx_test.go`, `contrib/veloxotel/metrics_test.go`, `contrib/veloxotel/statement_test.go`, a new `contrib/veloxotel/record_internal_test.go`.

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// txhook.go, TxInfo gains, after ReadOnly:
// Route is where the transaction runs: its shard ("" on an unsharded DB), on
// the primary, so Replica is always -1 and Fallback always false.
Route Route

// newTxInfo(o txOptions, sh *shard) *TxInfo // Route.Shard is sh.name when sh is not nil
```

- **The tx span and its point.** The span gets, as start attributes:
  - `velox.route.shard` when `tx.Route.Shard != ""`;
  - Config.Target's attributes, or `TargetFor(tx.Route)`'s when TargetFor is set.

  The `velox.tx.duration` point gets the same shard and target attributes, as a statement's duration point does.
- **C97.** Core keeps `Route.Fallback` sticky across an Around hook's retries, because "set it to true to send the retry to the primary" is the documented hook contract (plan ruling 2). `Route.Fallback`'s doc says: `It stays set for the statement's later executions, an Around hook's retry, which run on the primary.`
  - veloxotel computes `fellOver := st.Route.Fallback && !wasFallback` once.
  - `routeAttributes(kv, st, fellOver)` adds `velox.route.fallback` only when `fellOver` holds, and `record` takes `fellOver` as now.
- **C70.** `record` returns at once when `!a.duration.Enabled(ctx) && !a.returned.Enabled(ctx) && !(fellOver && a.fallbacks.Enabled(ctx))`. It records each instrument only when that instrument is enabled. `txRecord` returns when `!a.txDuration.Enabled(ctx)`.

- [ ] **Step 1: Write the failing tests.**
  - `TestTxInfoRoute` (txhook_test.go), with a TxAround hook that keeps `tx.Route`:
    - on `OpenFake(Postgres, e)` it is `velox.Route{Replica: -1}`;
    - on a DB with `WithReplicas` it is `velox.Route{Replica: -1}`;
    - on an OpenShards DB under `WithShard(ctx, "a")` it is `velox.Route{Shard: "a", Replica: -1}`.
  - `TestTxSpanRoute` (contrib/veloxotel/tx_test.go):
    - On a sharded DB with `TargetFor: func(r velox.Route) veloxotel.Target { return veloxotel.Target{Namespace: "db_" + r.Shard, Address: "h"} }`, the `velox.tx` span has `velox.route.shard="a"`, `db.namespace="db_a"` and `server.address="h"`, and its `velox.tx.duration` point has the same three.
    - With `Config{Target: veloxotel.Target{Namespace: "n"}}` on an unsharded DB, the span has `db.namespace="n"` and no `velox.route.shard`.
  - `TestFallbackSpanOncePerFailover` (metrics_test.go, beside TestFallbackCountedOncePerFailover, with the same `twice` hook and replica): of the two statement spans, the first has `velox.route.fallback=true` and the second has no `velox.route.fallback` attribute.
  - `TestRecordSkipsDisabledInstruments` (record_internal_test.go, package veloxotel): with an adapter from `newAdapter(Config{TracerProvider: tracenoop.NewTracerProvider(), MeterProvider: metricnoop.NewMeterProvider()})` and a `*velox.Statement` of `OpSelect`, `testing.AllocsPerRun(100, …)` is 0 for each of `a.record(ctx, st, "invoices", "SELECT invoices", nil, 0.001, failure{}, false)` and `a.txRecord(ctx, time.Now(), &velox.TxInfo{Attempt: 1}, velox.TxCommit)`.
  - Update the tests that assert the exact attribute set of `velox.tx.duration` (metrics_test.go) to the new set, shard and target included where the DB has them.
- [ ] **Step 2: Run** `go test -run TestTxInfoRoute .`, then `(cd contrib/veloxotel && go test -run 'TestTxSpanRoute|TestFallbackSpanOncePerFailover|TestRecordSkipsDisabledInstruments' .)`. Expected: FAIL.
- [ ] **Step 3: Implement** as in Interfaces. `Tx` passes the shard that `shardForTx` returned to `newTxInfo`. The veloxotel doc's list of tx span attributes names the shard and target.
- [ ] **Step 4: Leftover grep.** `git grep -nP 'if st\.Route\.Fallback \{' -- contrib/veloxotel`. Expected: nothing. At b3348ba it prints statement.go:97.
- [ ] **Step 5: Run** `go test -run 'Tx|Route' .`, then `(cd contrib/veloxotel && go test -race ./...)`. Expected: PASS.
- [ ] **Step 6: Commit** `velox: TxInfo.Route; veloxotel: tx spans name their shard and server, a fallback marks only the span that fell over, no attribute sets under a disabled meter`.
- [ ] **Step 7: Run the gate.** Expected: rc=0.

### Task 5: `Open`'s dialect, unknown conditions, timeouts that keep the driver's error, one entity prefix, the EXISTS backstop, and pinning ambiguous writes (C15, C33, C35, C37, C99)

**Files:**
- Modify: `db.go:120-132` (Open), `route.go:108-112` (OpenShards), `dialect.go` (`valid`), `render.go:430-527` (`pred`'s default), `db.go:228-279` (C33), `db.go:337-341` and `errors.go:146-162` (C35), `db.go:567-575` (C37's comment), `db.go:729-822` (C99).
- Test: `db_test.go` (create if absent; package velox_test), `render_test.go`, `deadline_test.go:128`, `convention_test.go`, `pipeline_internal_test.go`, `session_test.go`.

**Interfaces:**
- Consumes: Task 2's `dbErr`.
- Produces:

```go
// dialect.go
func (d Dialect) valid() bool // Postgres, MySQL or SQLite

// errors.go: hookError becomes stageError
// stageError is the error of a hook stage: "velox: <Entity>: <stage>: <err>",
// stage "hook" or "rewrite"; an err whose text starts "velox: <Entity>: " does
// not name the entity twice.
type stageError struct {
	entity, stage string
	err           error
}
```

- **C15.**
  - `Open` panics, before `newDB`, with `fmt.Sprintf("velox: Open: unknown %v; use velox.Postgres, velox.MySQL or velox.SQLite", d)`. `%v` gives `Dialect(0)`.
  - `OpenShards` panics the same way, as its first check, with `velox: OpenShards: `.
  - `writer.pred` gains `default: w.fail(fmt.Errorf("velox: %s: a condition of unknown kind %d", w.in.Name, n.op))`. It records an error, not a panic (plan ruling 12).
- **C33.** Both `return errTimeout(st.Entity.Name)` (db.go:239, :272) become `return errors.Join(errTimeout(st.Entity.Name), err)` (plan ruling 18).
- **C35.** `prepare`'s rewrite error is `&stageError{entity.Name, "rewrite", err}`. hook.go's four `&hookError{in.Name, …}` become `&stageError{in.Name, "hook", …}`.
- **C37.** The `opExists` branch of `checkColumns` stays, with this comment: `A backstop: a hook can put another entity's relation here through a Predicate conversion, and an owner column out of this entity's range would make rendering panic.`
- **C99.** A write marks the session (`db.wrote(ctx)`) once it has reached the driver, whatever the error.
  - In `exec`'s `read == nil` path, `db.wrote` runs after `t.ex.exec` returns, before the error check.
  - In the query path, for `!st.Op.Reads()`, it runs after `t.ex.query` returns, whatever came back.
  - The final `if !st.Op.Reads() && err == nil` becomes `if !st.Op.Reads()`, and is then not called twice.
  - A statement refused before sending (`checkParams`, a dry run, the guard) does not pin.
  - `WithSession`'s doc says: `A write pins it once it has reached the database, even when it then fails: its outcome is unknown.`

- [ ] **Step 1: Write the failing tests.**
  - `TestOpenRefusesAnUnknownDialect` (db_test.go):
    - `velox.Open(0, new(sql.DB))` panics with `velox: Open: unknown Dialect(0); use velox.Postgres, velox.MySQL or velox.SQLite`;
    - `velox.Open(4, …)` panics with `Dialect(4)` in its text;
    - `velox.OpenShards(0, map[string]velox.Shard{"a": {Primary: velox.FakeConn(&velox.FakeExec{})}}, velox.WithShardFor(…))` panics with `velox: OpenShards: unknown Dialect(0); …`.
  - `TestUnknownOpIsRefused` (render_test.go, package velox): `renderStmt(Postgres, &Statement{Op: OpSelect, Entity: invInfo, Where: []Predicate{{node{op: 200}}}})` returns the error `velox: velox.inv: a condition of unknown kind 200`.
  - deadline_test.go:128's want becomes `"velox: testmodel.Invoice: deadline set by velox passed: context deadline exceeded\nvelox: testmodel.Invoice: context deadline exceeded"`. The `errors.Is` checks stay.
  - `TestRewriteErrorNamesTheEntityOnce` (convention_test.go):
    - a Rewrite hook returning `fmt.Errorf("velox: %s: %w", st.Entity.Name, refused)` gives `velox: testmodel.Invoice: rewrite: refused`, and `errors.Is(err, refused)`;
    - one returning `errors.New("velox: other.X: nope")` gives `velox: testmodel.Invoice: rewrite: velox: other.X: nope`.
  - `TestCheckColumnsRefusesAForeignExists` (pipeline_internal_test.go): a Rewrite hook on a `pcomment` query appends `Predicate(r.Has())` for a relation r of an entity whose owner column index is 2 or more (pcomment has 2 columns). The statement fails with `a Rewrite hook set column`, and nothing is sent.
  - `TestFailedWritePinsTheSession` (session_test.go), on a DB with `WithReplicas` and `sctx := velox.WithSession(ctx)`:
    - an Update whose primary returns `errors.New("connection reset")` from exec: afterwards `velox.PinnedUntil(sctx)` is after now;
    - an Insert with RETURNING whose row scan fails pins too;
    - with a fresh session, an Update refused by `checkParams` (with `velox.SetMaxParams(velox.Postgres, 1)`) leaves `PinnedUntil` zero;
    - an Update without Where (ErrNoWhere) leaves it zero too.
- [ ] **Step 2: Run** `go test -run 'TestOpenRefusesAnUnknownDialect|TestUnknownOpIsRefused|TestStatementOnTheTxContextTimesOut|TestRewriteErrorNamesTheEntityOnce|TestCheckColumnsRefusesAForeignExists|TestFailedWritePinsTheSession' .`. Expected: FAIL.
- [ ] **Step 3: Implement** as in Interfaces.
- [ ] **Step 4: Leftover grep.**
  - `git grep -nP 'return errTimeout\(st\.Entity\.Name\)|rewrite: %w|hookError' -- '*.go'`. Expected: nothing. At b3348ba it prints db.go:239, :272 and :339, errors.go:146, :149, :154 and :162, and hook.go:121, :136, :148 and :152.
  - `git grep -nP 'Op\.Reads\(\) && err == nil' -- db.go`. Expected: only :813, the Affected line. At b3348ba it prints :813 and :818.
- [ ] **Step 5: Run** `go test -race .`. Expected: PASS.
- [ ] **Step 6: Commit** `velox: Open and OpenShards refuse an unknown dialect, an unknown condition fails rendering, a timeout keeps the driver's error, a rewrite's error names the entity once, an ambiguous write pins its session`.
- [ ] **Step 7: Run the gate.** Expected: rc=0.

### Task 6: Upsert backstops, unshared defaults, field names, and the EXISTS path (C42, C47, C48, C29, C81, C40)

**Files:**
- Modify: `statement.go:94-105,203-206`, `scope.go:100-180,483-545`, `insert.go:244-299,486-498`, `db.go:287-425` (C48's check), `validate.go:86`.
- Test: `insert_scope_test.go`, `insert_test.go`, `validate_test.go:40`, `policy_test.go:227`, `tenant_test.go`.

**Interfaces:**
- Consumes: Task 5's `prepare`.
- Produces:

```go
// statement.go
type ConflictClause struct {
	Columns   []int
	Update    []int
	DoNothing bool
	Where     []Predicate
} // no unexported field

// Statement gains, beside ran and handed:
// upsertPolicy is set by the scope when it added the policy's conditions to
// the upsert's update branch: an upsert that then affected fewer rows than it
// sent met a row the policy hides.
upsertPolicy bool

// insert.go
func cloneValue(v reflect.Value) reflect.Value // a deep copy, through pointers, slices, maps, arrays and exported struct fields

// scope.go: existsError's target string becomes
path []string // the EXISTS targets, outermost first
```

- **C42.** `conditionUpsert` sets `st.upsertPolicy = extra > 0`, and `checkUpserted` reads it.
- **C47.** In `scopeInsert`, after `conditionUpsert`, a statement with `st.upsertPolicy && len(st.Rows) > 1 && !st.InTx` is refused, before anything is sent, with:
  > `velox: <E>: a Rewrite hook gave a multi-row upsert outside a transaction an update branch the policy conditions; a row the policy hides would fail it after the others were written`

  Only a hook can reach this, because `conditionedUpsert` puts every such InsertAll in a transaction.
- **C48.** `prepare` records `returning := len(st.Returning) > 0` before the Rewrite hooks. After `db.scope`, `returning && len(st.Returning) == 0 && st.upsertPolicy` is refused with:
  > `velox: <E>: a Rewrite hook dropped the RETURNING of an upsert whose update branch the policy conditions; velox reads the row back to find a row the policy hides`
- **C29.** `defaultValue` returns `cloneValue(reflect.ValueOf(d))`.
  - A `[]byte` is copied by its bytes.
  - A nil slice or map stays nil.
  - Unexported struct fields are copied shallowly with their struct (plan ruling 7).
- **C81.** validate.go:86 and insert.go:247 name `c.Field`, not `c.Name`: `velox: <E>: <GoField>: column is immutable`. ErrImmutable's doc says `the error names the Go field`.
- **C40.** In `scoper.node`, an error from inside an EXISTS on t, from its kids or from t's tenant and Read, gets t at the front of its path.
  - An error that is not yet an `*existsError` becomes `&existsError{entity: sc.entity, path: []string{t.Name}, err: err}`.
  - `*cycleError` passes unchanged.
  - Text: `"velox: " + entity + ": in the EXISTS on " + strings.Join(path, ", then on ") + ": " + strings.TrimPrefix(err.Error(), "velox: "+path[len(path)-1]+": ")`.

- [ ] **Step 1: Write the failing tests.**
  - `TestUpsertPolicyFlagIsTheStatements` (insert_scope_test.go):
    - `reflect.TypeFor[velox.ConflictClause]()` has 4 fields, all exported;
    - on the file's policy-conditioned upsert entity, a Rewrite hook that replaces `st.Conflict` with `&velox.ConflictClause{Columns: …, Update: …}` (a copy built by hand), with a FakeExec affecting 0 rows, still fails with a `*velox.PolicyError` matching `ErrDenied`.
  - `TestHookCannotConditionAMultiRowUpsert` (insert_scope_test.go): an `InsertAll` of 2 rows with `OnConflict(…).DoNothing()`, outside a transaction, on an entity with a Read policy. A Rewrite hook sets `DoNothing = false` and `Update = []int{<a column>}`. The insert fails with the C47 text. `e.Log` holds no `BEGIN` and no `INSERT`.
  - `TestHookCannotDropUpsertReturning` (insert_scope_test.go): a single Insert with a zero integer key and `OnConflict(…).Update(…)` on PostgreSQL, on an entity with a Read policy. A Rewrite hook sets `st.Returning = nil`. The insert fails with the C48 text, and nothing is sent.
  - `TestDefaultsAreNotShared` (insert_test.go), with a hand-written entity `dshared`:
    - its fields are `ID int64`, `B []byte` with Default `[]byte{1, 2}`, `S []string` with Default `[]string{"a"}`, `M map[string]int` with Default `map[string]int{"a": 1}`, and `N map[string][]string` with Default `map[string][]string{"k": {"v"}}`;
    - insert two entities, then set `x1.B[0] = 9`, `x1.S[0] = "z"`, `x1.M["a"] = 9` and `x1.N["k"][0] = "z"`;
    - `x2`'s values equal the defaults, and a third insert gets the defaults.
  - validate_test.go:40's want becomes `velox: velox_test.dAuthor: CreatedAt: column is immutable`, and the upsert case at :51 checks the same text.
  - policy_test.go:227's want becomes `velox: velox_test.pbox: in the EXISTS on velox_test.pnote, then on testmodel.Invoice: no tenant in the context`.
  - `TestExistsErrorNamesThePath` (policy_test.go): `db.Query[pbox]().Where(pboxNote.HasWhere(pn.Invoice.Has())).Limit(1).All(ctx)`, without a tenant, gives the same text.
- [ ] **Step 2: Run** `go test -run 'TestUpsertPolicyFlagIsTheStatements|TestHookCannotConditionAMultiRowUpsert|TestHookCannotDropUpsertReturning|TestDefaultsAreNotShared|TestUpdateValidatesAndRefusesImmutable|TestPolicyConditionIsScopedAndChecked|TestExistsErrorNamesThePath' .`. Expected: FAIL.
- [ ] **Step 3: Implement** as in Interfaces.
- [ ] **Step 4: Leftover grep.**
  - `git grep -nP '\bcf\.policy\b|policy bool // the scope|(c|col)\.Name, ErrImmutable|e\.target\b' -- '*.go'`. Expected: nothing.
  - At b3348ba it prints insert.go:494, scope.go:177, statement.go:104, insert.go:247, validate.go:86 and scope.go:544.
- [ ] **Step 5: Run** `go test -race .`. Expected: PASS.
- [ ] **Step 6: Commit** `velox: the upsert policy flag moves to the Statement, hooks cannot unguard an upsert, defaults are copied per row, ErrImmutable names the field, an EXISTS error names its path`.
- [ ] **Step 7: Run the gate.** Expected: rc=0.

### Task 7: With loads re-chunk when a policy's parameters overflow (C49)

**Files:**
- Modify: `db.go:836-843` (`checkParams`), `db.go:253-279` (`run` checks before the Around hooks), `load.go:228-404`, `each.go:210-310`.
- Test: `load_test.go`, `each_test.go`.

**Interfaces:**
- Consumes: Task 1's With form.
- Produces:

```go
// db.go
// paramsError is a rendered statement with more parameters than its dialect
// allows; nothing was sent. Its text is checkParams's.
type paramsError struct {
	entity string
	d      Dialect
	n, max int
}
func (e *paramsError) Error() string

// load.go
// byChunks runs read on keys[lo:hi] in chunks of at most per keys, in order.
// A chunk whose statement had too many parameters (*paramsError) and more than
// one key is read again, from lo, in chunks smaller by the excess, and the rest
// use the smaller size; with one key, the error is returned.
func byChunks(n, per int, read func(lo, hi int) error) error
```

- `run` calls `checkParams` after `route` and before `execute`, so the Around hooks never see an overflowing statement. `exec`'s own check stays, for an Around hook that changes Args.
- `fetch`, `fetchEach` and each.go's two chunk loops (:220, :299) use `byChunks`.
- `fetchThrough` keeps whole owners per chunk. On a `*paramsError`, it rebuilds its chunks with `per` reduced by the excess and reads them all again, until `per` reaches 1. The parts read before the overflow are dropped.
- `keysPer` keeps its 64-parameter reserve, and its doc says: `a statement that still overflows, because a policy's or a hook's conditions take more, is split again (byChunks)`.

- [ ] **Step 1: Write the failing tests**, each with `defer velox.SetMaxParams(velox.Postgres, 100)()` and a Read policy on the target, through the test file's policy context helper, whose condition is an `In` of 80 values. That leaves `keysPer` 36 keys, a statement of 116 parameters.
  - `TestLoadReChunksOnParameterOverflow` (load_test.go): a `With` load of a one-to-many relation for 50 owners loads every owner's rows. Every sent statement has at most 100 args. The first statement that overflowed was not sent: `e.Calls` equals the statements sent.
  - The same for a many-to-many relation (`fetchThrough`), with each owner's targets in the order its OrderBy gives, and for a `LimitEach` many-to-many load (`fetchEach`).
  - `TestEachReChunksOnParameterOverflow` (each_test.go): the same for `LoadEach` and `CountEach`.
  - A policy `In` of 100 values fails with `the statement has 101 parameters; postgres allows 100`, since one key cannot fit.
- [ ] **Step 2: Run** `go test -run 'ReChunksOnParameterOverflow' .`. Expected: FAIL with the parameter-limit error.
- [ ] **Step 3: Implement** as in Interfaces.
- [ ] **Step 4: Leftover grep.** `git grep -nP 'start < len\((p\.)?keys\); start \+= per' -- '*.go'`. Expected: nothing. At b3348ba it prints each.go:220 and :299, and load.go:323 and :346. InsertAll's batch loop (insert.go:610) is not a load and keeps its loop.
- [ ] **Step 5: Run** `go test -race .`. Expected: PASS.
- [ ] **Step 6: Commit** `velox: With loads and LoadEach split a chunk again when the policy's parameters overflow`.
- [ ] **Step 7: Run the gate.** Expected: rc=0.

### Task 8: `WithTenant` converts predeclared integers and strings; Policy's godoc on rules that query (C51, C55)

This covers spec rulings 12 and 13, and builder ruling 8.

**Files:**
- Modify: `scope.go:26-86,100-132,182-194,283-299,375-413,505-506`, `insert.go:420-484`, `scope_api.go:27-35` (WithTenant's doc) and `:130-160` (Policy's doc), `statement.go:173` (`Tenant`'s doc), `errors.go:37-39`.
- Modify: `example/tenant/main.go:108-109`, and `example/tenant/testdata/output.golden` by R7.
- Test: `tenant_test.go`.

**Interfaces:**
- Consumes: Task 6's scope.go.
- Produces:

```go
// scope.go
// tenantFor returns id as the Go type of in's tenant column: unchanged when it
// is of that type; converted when it is a predeclared integer type and the
// column's is a predeclared integer type, or it is string and the column's
// kind is string; refused with ErrTenantMismatch otherwise, or when it does
// not fit; and refused with ErrNoTenant when the result is zero.
func tenantFor(in *EntityInfo, id any) (any, error) // replaces checkTenantValue

// scoper.tenantOf(t *EntityInfo) (id any, ok bool, err error) // id is converted for t
```

- **The conversion rules** (builder ruling 8, plan ruling 8):
  - A named source type (`OrgID`, `UserID`) never converts: it must be the column's type exactly.
  - Integers convert only between predeclared types (`int`, `int8` … `int64`, `uint`, `uint8` … `uint64`). `uintptr` is not one of them.
  - A predeclared `string` converts to a column of any string kind.
- **Range checks.**
  - A signed source whose value is negative is refused for an unsigned column.
  - An unsigned source above `math.MaxInt64` is refused for a signed column.
  - Otherwise the check is `OverflowInt` or `OverflowUint` on the column's type.
  - The refusal is `velox: <E>: tenant does not match: the context's tenant %v (a %T) does not fit the column %s, a %s`. The existing "is a %T, the column … is a %s" text stays for a type that never converts.
- **Where the conversion happens.** It happens per entity, inside each statement:
  - `scope` uses the statement entity's converted id for `checkTenantAssignments` and `tenantPredicate`, and records it as `st.Tenant`;
  - `scopeInsert` uses it for `checkTenantRows`;
  - each EXISTS target gets its own (`scoper.node`);
  - `fillTenant` sets and checks the converted value.
- **What stays raw.** `RowScope.Tenant` and `WithShardFor`'s `tenant` are the context's raw id. `Statement.Tenant`'s doc: `the tenant scope applied, as the type of the entity's tenant column; nil when unscoped`.
- **WithTenant's doc** replaces "id's dynamic type must be the tenant column's Go type exactly" with:
  > id is converted to the tenant column's Go type when it is a predeclared integer type (an untyped constant is an int) and the column's type is a predeclared integer type whose range holds it, or when it is a string and the column's kind is string. A named type, such as an OrgID, is never converted: it must be the column's type. Any other id fails the statement with ErrTenantMismatch.

  ErrTenantMismatch's doc says `cannot be converted to the tenant column's type` in place of `is not of the tenant column's type`.
- **C55.** Policy's godoc gains this paragraph, after "Any field may be nil …":
  > A policy function decides from ctx and, for Insert, the row; it does not query. A rule that must see other rows, such as that an inserted comment's parent is one the caller may read, is a hook (Hooks): it queries through Mutation.DB, in the write's transaction, and returns the error that refuses the write. This is ent's split: privacy rules decide, hooks query.
- **The example.** main.go:108-109 becomes three lines:
  - `db.Query[gen.Project]().Count(velox.WithTenant(ctx, 1))`, where 1 is an int converted to the column's int64, printed with `say("tenant 1 as an int: %d projects, err=%v", n, err)`;
  - then `type orgID int64` (declared in the function) with `WithTenant(ctx, orgID(1))`, printed with `say("tenant of a named type: ErrTenantMismatch=%t", …)`.

- [ ] **Step 1: Write the failing tests** in tenant_test.go. Hand-write `tconvA`, with an `int64` tenant column `OrgID` and an `EdgeTo[tconvB]`; `tconvB`, with an `int32` tenant column; `tconvU`, with a `uint32` tenant; and `tconvS`, with a tenant of a named string type `tslug`.
  - `TestTenantConversion`, a table of id → outcome on a Count:
    - on `testmodel.Invoice` (`int64`): `7`, `int32(7)`, `uint8(7)` and `uint64(7)` pass, and the sent arg is `int64(7)`;
    - `uint64(1 << 63)` fails with ErrTenantMismatch and `does not fit`;
    - `0` and `int8(0)` fail with ErrNoTenant;
    - `"7"` fails with ErrTenantMismatch;
    - `orgID(7)`, with `type orgID int64` declared in the test, fails with ErrTenantMismatch;
    - on `tconvU`: `-1` and `int64(1 << 40)` fail with ErrTenantMismatch, and `7` passes as `uint32(7)`;
    - on `tconvS`: `"acme"` passes as `tslug("acme")`;
    - a Rewrite hook that keeps `st.Tenant` sees `int64(7)` for `WithTenant(ctx, 7)` on Invoice.
  - `TestTenantInsertConverts`:
    - an Insert of `testmodel.Invoice{}` under `WithTenant(ctx, 7)` fills `OrgID == 7`;
    - an Insert with `OrgID: 7` set passes;
    - one with `OrgID: 8` fails with ErrTenantMismatch.
  - `TestTenantConversionPerTarget`: `db.Query[tconvA]().Where(tconvAB.Has())` under `WithTenant(ctx, 7)` sends args holding `int32(7)` (the EXISTS's) and `int64(7)` (tconvA's own).
  - `TestRowScopeKeepsTheRawTenant`: `velox.RowScopeFromContext(velox.WithTenant(ctx, 7)).Tenant` is `int(7)`.
- [ ] **Step 2: Run** `go test -run 'TestTenantConversion|TestTenantInsertConverts|TestTenantConversionPerTarget|TestRowScopeKeepsTheRawTenant' .`. Expected: FAIL.
- [ ] **Step 3: Implement** as in Interfaces, and update the tenant_test.go tests that assert today's exact-type refusal of an `int`.
- [ ] **Step 4: Regenerate:** R7 for `./tenant` only: `(cd example && go test ./tenant -update)`.
  - The golden's line `tenant of type int: ErrTenantMismatch=true` becomes `tenant 1 as an int: <n> projects, err=<nil>`, where n is acme's project count as the earlier lines print it.
  - A line `tenant of a named type: ErrTenantMismatch=true` is added.
  - No other line changes.
- [ ] **Step 5: Leftover grep.**
  - `git grep -nP 'checkTenantValue|reflect\.TypeOf\(id\) != t|must be the tenant column.s Go type exactly' -- '*.go'`. Expected: nothing. At b3348ba it prints scope.go:298, :378, :383 and :385, insert.go:454, and scope_api.go:29-30.
  - `git grep -nP 'tenantPredicate\(t, sc\.id\)' -- scope.go`. Expected: nothing. At b3348ba it prints :506.
- [ ] **Step 6: Run** `go test -race .`, then `(cd example && go test ./tenant)`, then `(cd contrib/graphql && go test ./veloxgql/...)`. Expected: PASS.
- [ ] **Step 7: Commit** `velox: WithTenant converts predeclared integers and strings to the tenant column's type, per entity; Policy's doc says a rule that queries is a hook`.
- [ ] **Step 8: Run the gate.** Expected: rc=0.

### Task 9: Bidirectional self many-to-many edges write and remove both join rows (G17, core part)

This covers builder ruling 18. velox2 has no link API. A many-to-many link is a row of its join entity, written with `Insert`, `InsertAll` and `Delete`, as `EdgeThrough`'s doc says. So "the core link path" is the join entity's writes. ent writes `(a,b)` and `(b,a)` and deletes both (`dialect/sql/sqlgraph/graph.go:1661-1735`, `addM2MEdges` and `clearM2MEdges`). velox does the same for a join entity marked `Bidi`.

**Files:**
- Modify: `desc.go` (the `Bidi` field and its load checks), `meta.go:21-37` (`EntityInfo.bidi`), `insert.go:244-252,339-363,528-567`, `write.go:373-410` (`writeCore`, `saveCore`).
- Modify: `internal/gen/out.go` (`OutEntity.Bidi`), `internal/gen/lower.go:777-836` (`through`), `internal/gen/render.go:285`.
- Modify: `internal/gen/testdata/golden/blog/schema/user.go:35-36` (adds `edge.To("friends", User.Type)`), and the blog golden by R3.
- Test: a new `bidi_test.go` (package velox_test), `desc_test.go`, `internal/gen/render_test.go`, `internal/itest/through_test.go`.

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// desc.go, Desc[E] gains, after Tenant:
// Bidi marks the join entity of a self-referencing many-to-many edge that has
// no inverse, which is bidirectional, as in ent: each link is the two rows
// (a, b) and (b, a). Insert and InsertAll write both, Delete removes both, and
// Update and Save are refused. Its two columns are its primary key and have
// one Go type. For generated code.
Bidi bool

// meta.go: EntityInfo gains bidi bool, set from Desc.Bidi.
```

- **Load.** It refuses, with a SchemaError, a `Bidi` Desc that:
  - has other than 2 columns, or a primary key other than `[]int{0, 1}`;
  - has columns of two Go types;
  - has a tenant column, a Policy or Hooks.
- **Insert, InsertAll and upserts.** A row `(a, b)` adds `(b, a)` after it, in the same statement, unless `a == b`.
  - `a == b` is compared with `bytes.Equal` for `[]byte` and `==` otherwise.
  - The mirrored row takes the statement's conflict clause, so `OnConflict(…).DoNothing()` covers both rows.
  - An upsert with an Update list is refused in `check`: `velox: <E>: the join entity of a bidirectional edge cannot be updated; delete the link and insert it again`.
  - InsertAll's rows per statement halve: `max(1, maxParams[d]/(2*len(cols)))`.
  - The entity's hooks see the caller's rows. Rewrite hooks, Around hooks and `Mutation.Affected` see the written ones, two per link.
- **Delete.** In `writeCore`, before the statement is executed or rendered, a Bidi entity's non-empty Where `ps` becomes `[]Predicate{Or(And(ps…), And(swap(ps)…))}`.
  - `swap` copies each condition with column 0 and column 1 exchanged, at every depth.
  - A delete with All is unchanged.
- **Update and Save** on a Bidi entity are refused in `writeCore` and `saveCore`, with the text above.
- **velox-gen.**
  - `through` sets `js.out.Bidi = ed.Bidi`. `ed.Bidi` holds only for the self M2M assoc edge with no inverse (build.go:358-361).
  - `render.go` writes `Bidi: true,` after `Tenant: …,` when it is set.
  - `plugin.Edge` is unchanged.

**What Wave 3 must add** (the GraphQL part of builder ruling 18). The mutations write join rows with `db.InsertAll(rows).OnConflict(jo, jt).DoNothing()` and remove them with `db.Delete[J]().Where(…)` (gogen/mutation.go:536, :936, :959, :1158). So they write and remove both rows through this task with no generator change. Wave 3 adds:
1. A bidirectional self edge (for example `User.friends`, `edge.To("friends", User.Type)` with no inverse) to the GraphQL test model, and an e2e test:
   - `addFriendIDs: [b]` on a makes `a.friends` hold b and `b.friends` hold a;
   - `removeFriendIDs: [b]` empties both;
   - `clearFriends` empties both;
   - deleting a removes a from `b.friends`.
2. The UpdateX and CreateX godoc sentence "A self-edge is written one way, as the core's loads read it." (gogen/mutation.go:390 and its UpdateX twin) and contrib/graphql/doc.go:220's bullet say instead: `A bidirectional self-edge (a self-referencing many-to-many edge with no inverse) is written and removed both ways, as in ent; a self-edge with an inverse is written one way.` The GraphQL goldens are regenerated.
3. Nothing in the SDL or in gqlimpl changes.

- [ ] **Step 1: Write the failing tests.**
  - `TestBidiLinks` (bidi_test.go), with hand-written `bnode` (ID int64) and `bnodeFriend` (`NodeID`, `FriendID int64`, `Bidi: true`, primary key `{0, 1}`), and an `EdgeThrough[bnodeFriend, bnode]("Friends", 0, 0, 1, 0)` on bnode. On `OpenFake(Postgres, e)`:
    - `Insert(&bnodeFriend{1, 2})` sends `INSERT INTO "bnode_friends" ("node_id", "friend_id") VALUES ($1, $2), ($3, $4)` with `1, 2, 2, 1`;
    - `Insert(&bnodeFriend{3, 3})` sends one row;
    - `InsertAll([]bnodeFriend{{1, 2}, {1, 3}}).OnConflict(…).DoNothing()` sends 4 rows and `ON CONFLICT ("node_id", "friend_id") DO NOTHING`;
    - `Delete[bnodeFriend]().Where(F.NodeID.EQ(1), F.FriendID.In(2, 3))` sends `WHERE (("node_id" = $1 AND "friend_id" IN ($2, $3)) OR ("friend_id" = $4 AND "node_id" IN ($5, $6)))`;
    - `Update[bnodeFriend]().Set(…).All()` and an upsert with `Update(…)` fail with the refusal text, and nothing is sent.
  - `TestBidiDescRefused` (desc_test.go): a Bidi Desc with 3 columns, one with an `int64` and a `string` column, and one with `Tenant: 0` are each refused with a SchemaError naming the entity.
  - internal/gen/render_test.go: the blog golden's `user_friend_velox.go` Desc holds `Bidi: true,`, and `user_group_velox.go` does not.
  - `TestBidiOnRealDatabases` (internal/itest/through_test.go), with the itest model gaining `person` and `personFriend` (table `person_friends`) in its own CREATE TABLE, on each dialect:
    - `Insert(&personFriend{1, 2})`, then a `With` load of `Friends` on persons 1 and 2, shows 2 under 1 and 1 under 2;
    - `Delete…Where(NodeID.EQ(2), FriendID.In(1))` leaves no rows, counted with `Count`.
- [ ] **Step 2: Run** `go test -run 'TestBidiLinks|TestBidiDescRefused' . && go test -run TestGolden ./internal/gen`. Expected: FAIL. `Bidi` is not a field, and the golden lacks UserFriend.
- [ ] **Step 3: Implement** as in Interfaces.
- [ ] **Step 4: Regenerate:** R3, then R2 and R5. Expected:
  - R3 adds `user_friend_velox.go` and its plugin.json and tables entries to the blog golden, and changes nothing else;
  - R2 and R5 change nothing, because no other model has a bidirectional edge (`git status --short` shows none of their files).
- [ ] **Step 5: Leftover grep.** `git grep -nP 'Bidi: true' -- '*.go'`. Expected: the blog golden's user_friend_velox.go, bidi_test.go, desc_test.go and internal/itest's model. At b3348ba it prints nothing.
- [ ] **Step 6: Run** `go test -race . ./internal/gen ./cmd/velox-gen`, then run D with `go test -count=1 -run TestBidiOnRealDatabases ./internal/itest`. Expected: PASS.
- [ ] **Step 7: Commit** `velox: a bidirectional self many-to-many edge writes and removes both join rows, as ent's does (Desc.Bidi)`.
- [ ] **Step 8: Run G, then the gate.** Expected: G clean, gate rc=0.

### Task 10: Perf: large inserts skip the cache, Set clones once, the scope skips trees without EXISTS, loads pin without a DB copy (C30, C41, C43, C91)

**Files:**
- Modify: `render_cache.go:50-90` (C30), `scope_api.go:340,438-456` and `hook.go:85-99` (C41), `pred.go:35-40,85-117`, `query.go:300-310`, `relation.go:480-485` and `scope.go:455-462` (C43), `load.go:106-215`, `each.go:241`, `query.go:444`, `route.go:448-495`, `db.go:29-31` and `statement.go:203` (C91).
- Test: `render_cache_test.go`, `hooks_internal_test.go`, `scope_fuzz_test.go` or a new `scope_internal_test.go`, `budget_test.go`.

**Interfaces:**
- Consumes: Task 7's load.go and Task 9's insert.go.
- Produces:

```go
// render_cache.go
// maxCachedRows is the most rows an INSERT may have and be cached: each row
// count is a shape of its own, and a batch of thousands is rendered once.
const maxCachedRows = 32

// pred.go: node gains, after op (it fits the padding, so node stays 64 bytes):
deep bool // an opExists is among the kids, at any depth; set by group
func group(o op, kids []node) node // And, Or, Not and every internal opAnd/opOr node are made by it

// hook.go: mutation gains
ownSet bool // st.Set is the mutation's own array, cloned once
// scope_api.go
func mutateSet(mu *mutation, a Assignment) // was mutateSet(st, errp, a)

// statement.go: Statement gains
pin target // a With load's: the connection its query ran on, which its reads go to; zero otherwise
```

- **C30.** `renderCache.get` renders without the cache, and without computing the key, an `OpInsert` with `len(st.Rows) > maxCachedRows`. Its fingerprint is still set.
- **C41.** `mutateSet` clones `st.Set` once per mutation: on the first replacement or append, it sets `mu.ownSet`. Later calls write in place.
- **C43.** `scoper.node`'s `case opAnd, opOr, opNot` returns `n, false, nil` when `!n.deep`. EXISTS nodes are always walked. `resolveNode` uses the same test. A node made by a Rewrite hook can only come from these constructors, because node is unexported, so the skip is complete (plan ruling 6).
- **C91.**
  - `DB.pin` and `pinnedTo` are deleted.
  - `loadRelations`, `loadOne`, `fetch`, `fetchThrough`, `fetchEach`, `fetchPart` and `readPart` take `pin target`. `readPart` sets `st.pin`.
  - The callers pass `at` when `db.rt != nil && !db.inTx && at.ex != nil`, else the zero target.
  - `route` reads `st.pin` where it read `db.pin`.

- [ ] **Step 1: Write the failing tests.**
  - `TestInsertAllAboveCachedRows` (render_cache_test.go):
    - an InsertAll of 33 rows leaves `velox.CacheSize(db)` unchanged, and an InsertAll of 32 rows grows it by 1;
    - both statements' `Fingerprint`, read by a Rewrite hook, is 16 hex characters;
    - 33 rows twice gives the same fingerprint.
  - `TestMutationSetClonesOnce` (hooks_internal_test.go, package velox):
    - with `st := &Statement{Op: OpUpdate, Set: orig}`, where orig is 2 assignments, and `mu := &mutation{st: st}`, three `mutateSet(mu, Assignment{Column: orig[0].Column, Op: AssignSet, Value: i})` calls leave `orig` unchanged;
    - `testing.AllocsPerRun(100, …)` of a fresh mu doing the three calls is 1.
  - `TestScopeSkipsTreesWithoutExists` (scope_internal_test.go):
    - `And(a, Or(b, c))` of comparisons has `deep == false` at every level, and `And(a, Or(b, rel.Has()))` has `deep == true` on the And and the Or;
    - `sc.node` of a 1000-leaf `And` without EXISTS returns `changed == false` with 0 allocations (`testing.AllocsPerRun`);
    - the policy_test.go and tenant_test.go EXISTS tests still pass.
  - `TestWithLoadOnReplicasAllocations` (budget_test.go): a cached `All` with one `With` load makes the same allocations on a DB with two replicas as on one without, measured as `TestReplicaOneRowBudget` measures.
- [ ] **Step 2: Run** `go test -run 'TestInsertAllAboveCachedRows|TestMutationSetClonesOnce|TestScopeSkipsTreesWithoutExists|TestWithLoadOnReplicasAllocations' .`. Expected: FAIL.
- [ ] **Step 3: Implement** as in Interfaces.
- [ ] **Step 4: Leftover grep.**
  - `git grep -nP 'pinnedTo|db\.pin\b|node\{op: op(And|Or|Not)\b' -- '*.go' ':!*_test.go'`. Expected: nothing.
  - At b3348ba it prints each.go:241, load.go:106, :112, query.go:444, route.go:460, :461, :476 and :477, pred.go:88, :102, :113 and :117, query.go:307 and :310, and relation.go:484.
- [ ] **Step 5: Run** `go test -race .`, then `go test -run 'Budget|Alloc' -count=1 -v . 2>&1 | grep -E 'allocations|^(ok|FAIL)'`. Expected: PASS, with no budget over its bound.
- [ ] **Step 6: Commit** `velox: inserts above 32 rows skip the render cache, Mutation.Set clones once, the scope skips conditions without EXISTS, With loads pin without copying the DB`.
- [ ] **Step 7: Run the gate.** Expected: rc=0.

### Task 11: Re-measure the compile-cost and allocation bounds (builder ruling 20, Wave 2)

**Files:**
- Modify: `compilecost_test.go:186-283` (the measured comments and the rows the rule moves).
- Modify, only if a budget's comment states a measured figure that changed: `budget_test.go`.

**Interfaces:**
- Consumes: every earlier task.
- Produces: nothing new.

- [ ] **Step 1: Measure.** Run `go test -count=1 -run TestGenericTypesCompileSmall -v . 2>&1 | grep -E ': \+[0-9]+ bytes'`. Record every row, and `policy and hooks`.
- [ ] **Step 2: Apply the rules.**
  - `entityDescUse` and `entityDescHooks`: the bound is the measurement plus about 1.2%, rounded up to the next KiB. Append to the comment: `remeasured on 2026-10-26, darwin/arm64, Go 1.27.x, after Wave 2 of the pre-1.0 hardening (<old measurement> under <old bound> before)`.
  - Every other row and `policyGrowthMax`: update its `// measured N` and keep its bound.
    - A measurement over its bound stops the task: report the row, the number and the commit of this wave that grew it.
    - A row whose bound the generic rule (measurement plus the larger of 4096 and a quarter of it, the quarter capped at 65536, rounded up to the next KiB) now puts lower moves down to it, as Mutation's did.
- [ ] **Step 3: Measure the allocations.** Run `go test -count=1 -run 'Budget' -v . 2>&1 | grep -E 'allocations'` and `(cd contrib/veloxotel && go test -count=1 -run Budget -v ./... 2>&1 | grep -E 'allocations')`.
  - Every budget holds, or the task stops and reports, as above.
  - Write the numbers in the report.
  - A comment that quotes a measured figure (TestReplicaOneRowBudget's "TestOneRowAllocationBudget's 6") is updated to the measurement.
- [ ] **Step 4: Run** `go test -count=1 -run TestGenericTypesCompileSmall .`. Expected: PASS.
- [ ] **Step 5: Commit** `velox: compile-cost and allocation bounds re-measured after Wave 2`.
- [ ] **Step 6: Run the gate.** Expected: rc=0.

## Rulings this plan makes

Each is a call made while writing this plan. An implementer who finds one wrong stops and reports it instead of changing it.

1. **(call) G18's measurement and names.** Both forms are called in the same `Use` statement. The function form is measured in a scratch worktree with the methods deleted, because a method costs every package that uses a `Query` whether it is called or not (ruling 21's Sum finding). Δ above 7670 bytes, 1% of the bound, selects functions. The functions are `velox.With` and `velox.WithRel`, as the builder check named them.
2. **(call) C97 is fixed in veloxotel, by delta.** Core's `Route.Fallback` stays sticky, because the Around doc makes "set it to true and the retry goes to the primary" a hook contract. Resetting it per execution would break that contract. Telemetry reads the change during its own span.
3. **(call) C99 pins on any error once the statement reached the driver.** This errs toward the primary, which keeps read-your-writes. A statement refused before sending does not pin.
4. **(call) C49 re-chunks on overflow, and does not merely count the policy's parameters.** Re-chunking holds whatever the policy or a Rewrite hook adds. Counting would still fail when a hook adds more than 64 parameters. The 64-parameter reserve stays, so that the first try usually fits. The overflow is caught before the Around hooks, so no span records it.
5. **(call) C30 leaves inserts above 32 rows uncached, and does not bucket their sizes.** Bucketing would split one InsertAll into more statements, which is more round trips. Not caching costs only the render time that a large insert already spends collecting arguments.
6. **(call) C43 uses a per-node flag, not the triage's per-entity precomputation.** A per-entity flag is unsafe: `Pred[E](Predicate(otherRel.Has()))` converts another entity's EXISTS into E's conditions. Skipping by entity would then drop that target's tenant condition. The node flag is set by the only constructors of And, Or and Not nodes, fits node's padding, and skips exactly the trees that hold no EXISTS.
7. **(call) C29 copies deeply**, through pointers, slices, maps, arrays and exported struct fields. A shallow copy would leave a `map[string][]string` Default shared.
8. **(call) C51.**
   - `Statement.Tenant` shows the value converted for the statement's entity, as builder ruling 8's source asked ("show hooks the converted value").
   - `RowScope.Tenant` and `WithShardFor` see the context's raw id: they describe the context, not a statement.
   - Integers convert only between predeclared types, as builder ruling 8 says, so `WithTenant(ctx, 1)` does not convert into a column of a named `OrgID int64`. A predeclared string converts into any string-kind column, as the ruling says "between string and a string type".
9. **(call) C87 and G53 reuse `Route` as `TxInfo.Route`** (builder ruling 15). Its fields match: Replica is always -1 and Fallback always false. The tx duration point gets the shard and target too, as a statement's does.
10. **(call) C23's bound is 5 s.** database/sql's `Tx.Rollback` takes no context, so on `Open`'s DBs the bound covers ROLLBACK TO only, and Tx's doc says so.
11. **(call) C34: a savepoint's ROLLBACK TO is bounded by the earlier of the rollback timeout and the transaction's deadline.** Past the deadline the transaction's own ROLLBACK undoes everything anyway. The transaction's ROLLBACK is bounded by the timeout alone, since it must run after the deadline.
12. **(call) C15's unknown condition records a rendering error, not a panic.** The statement is refused and the caller gets an error.
13. **(call) C37 keeps the branch, as a backstop.** It is reachable through a Predicate conversion, and the test proves it.
14. **(call) G17's core link path is the join entity's Insert, InsertAll and Delete.** velox2 has no link API.
    - Update, Save and an upsert's Update are refused, because they would break the symmetry.
    - An a–a link is one row.
    - `Mutation.Affected` counts rows, two per link.
    - No generated-code bump: version 6 has not been released.
15. **(call) C55 lands here**, beside the Policy code that Task 8 edits.
16. **(call) `ErrLocked`'s text is "lock not available"**, PostgreSQL's name for 55P03, which a `lock_timeout` raises too.
17. **(call) C35 generalizes `hookError` into `stageError`**, so the hook and rewrite stages share one trimming rule.
18. **(call) C33 joins the timeout first:** `errors.Join(errTimeout(…), err)`, as the transaction's does (tx.go:166).

## Decisions for the controller before execution

- **G17's O2O half.** velox-gen also marks a self O2O with no inverse `Bidi` (build.go:362-364), and ent sets both rows' keys for one (a "spouse" edge). Builder ruling 18 names only M2M, so this plan leaves O2O one-way. Should O2O be built in this wave, or recorded as a documented difference from ent?
- **C51's integer rule.** Builder ruling 8 read literally refuses `WithTenant(ctx, 1)` on a column of a named integer type such as `OrgID int64`. That is the very case C51 cites when the column type is named. The plan follows the literal ruling (plan ruling 8). Allowing predeclared → named of the same kind, while still never converting named → named, would cover it.
- **C43's approach** departs from the triage's "precomputed per entity" for the security reason in plan ruling 6. It needs the controller's acknowledgement.
