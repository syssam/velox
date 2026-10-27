# Pre-1.0 Hardening, Wave 2: the Core Runtime — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Before 1.0 freezes them, the root package and veloxotel get their final runtime behaviour:
- the error tree is complete and safe: every supported dialect's constraint errors are classified, a typed-nil driver error cannot panic, a failed lock is `ErrLocked`, and a timeout keeps the driver's error;
- transactions cannot hang or escape: rollbacks are time-bounded, savepoints keep the transaction's deadline and shard, and the transaction's route reaches its hooks and its span;
- the scope and policy backstops hold against Rewrite hooks;
- `WithTenant` converts the ids it safely can;
- a bidirectional self edge is written both ways, as ent's is: a many-to-many edge writes and removes both join rows, and a one-to-one edge (ent's spouse) keeps both rows' keys pointing at each other;
- velox-gen's foreign keys take ent's defaults: a one-to-one key is unique, and a NULL-able key is ON DELETE SET NULL;
- the perf items are done, and `Query.With`/`Rel.With` are settled by measurement;
- the compile-cost and allocation bounds are re-measured once, at the end.

**Architecture:** Thirteen tasks, each building and passing the gate at its commit.
- Task 1 measures `With` first, because its outcome rewrites call sites the later tasks' tests use.
- Tasks 2–5 are the error, transaction, telemetry and pipeline items, file by file: errclass.go, tx.go, txhook.go with veloxotel, then db.go.
- Tasks 6–8 are the statement, load and tenant items: scope.go, insert.go, load.go and scope_api.go.
- Task 9 is G17's many-to-many half (`Desc.Bidi`).
- Task 10 gives velox-gen ent's foreign-key defaults, and rewrites testmodel's two parent edges as trees. Task 11 needs both: a unique key, and no tree that would become a spouse edge.
- Task 11 is G17's one-to-one half (`EdgeBidi`).
- Tasks 9–11 are the only tasks that touch velox-gen.
- Task 12 is the perf items.
- Task 13 re-measures.

Everything is in the root package and `contrib/veloxotel`, except:
- the velox-gen work of Tasks 9–11, with its goldens and testmodel's schema;
- Task 5's and Task 8's example goldens;
- Task 10's ent fixture and its note in the schema-design spec.

C14 (`Like` keeps `string`, with a godoc) is not in this wave: it is Wave 6's, as docs only (spec ruling 3).

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
- **Historical documents.** Nothing under `docs/reviews/` or `docs/plans/` changes, except this plan. Under `docs/specs/`, only the api-cleanup spec's ruling 21 (Task 1) and the schema-design spec's OnDelete line, by a dated note (Task 10), change.
- **The followups file** (`docs/plans/2026-10-14-core-1-followups.md`) is not touched. Wave 6 marks its items, citing these commits.
- **No machine-specific paths** in committed files.
- **Generated-code version.** No task bumps `GeneratedCodeIsVersion6`. Task 9 adds a `Desc` field and Task 11 a constructor that velox-gen writes, but version 6 has never been released (plan ruling 14).

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
3. **Re-pointing a bidirectional one-to-one key onto a taken partner.** Pointing A at C, when C's key is already set, must fail and leave A, its old partner B and C as they were. Step 2 clears B before step 3 finds C taken, so only the savepoint's rollback restores B. With consistent data the unique index refuses step 1 first, so the pin makes C's key one-way with raw SQL to reach step 3. Pin: Task 11 `TestBidiOneOnRealDatabases`, its one-way-key case, and the same inside a caller's Tx.
4. **A rollback on a dead connection after the caller canceled.** `Tx` must return within the rollback timeout, not hang on the detached context. Pin: Task 3 `TestRollbackIsBounded`.
5. **A Rewrite hook that gives an InsertAll upsert outside a transaction its update branch.** It must be refused before any row is sent. A hidden row must never fail the statement after the visible rows are written. Pin: Task 6 `TestHookCannotConditionAMultiRowUpsert`.
6. **A write that fails after it reached the database** (a timeout after the commit, or a RETURNING scan error). It must pin the session, so the caller's next read goes to the primary and sees its own write. A write refused before sending must not pin. Pin: Task 5 `TestFailedWritePinsTheSession`.

---

### Task 1: `With` measured; methods kept or replaced by `velox.With` and `velox.WithRel` (G18, M28)

This covers spec ruling 11 and builder ruling 10. The measurement comes first, and its result picks Branch A or Branch B. Only one branch is carried out.

**Files:**
- Modify: `compilecost_test.go:105-125` (entityDescUseSrc's `Use`) and `:251-277` (the bound comments).
- Modify: `docs/specs/2026-10-20-api-cleanup-design.md:282`, ruling 21, whose last sentence is replaced (Step 5).
- Branch B only: `query.go:52-67`, `relation.go:335-343`, `oldapi_test.go`, `doc.go`, `example/relations/README.md:39-41`, and every `.With(` call site (100 lines today; see Step 6).

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

  This is the method form, which Branch A keeps.
- [ ] **Step 2: Measure both forms in one scratch copy, outside the repo** (plan ruling 1). Nothing from the copy is committed.
  - Sizes depend on the module's path, about 59 bytes per character of it. So both forms are measured in the same directory, and neither in the repo.
  - With the methods deleted, the root package's other tests no longer compile, so the copy keeps only `compilecost_test.go` among the root `*_test.go` files.

  ```bash
  M=/private/tmp/claude-501/velox2-with-measure
  rm -rf "$M" && mkdir -p "$M"
  git archive HEAD | tar -x -C "$M"
  find "$M" -maxdepth 1 -name '*_test.go' ! -name compilecost_test.go -delete
  cp compilecost_test.go "$M"/   # carries Step 1's Use
  (cd "$M" && go test -count=1 -run 'TestGenericTypesCompileSmall/^entityDescUse$' -v . 2>&1 | grep 'entityDescUse: +')
  ```

  - Run the last line twice. The two runs must agree within ±64 bytes: the test's TempDir name varies in length. Record the larger number as **M_method**.
  - Then, in `$M` only:
    - delete `Query.With` and `Rel.With`;
    - add `With` and `WithRel` with the signatures above, whose bodies are the methods' bodies;
    - write `Use`'s first statement as `if _, err := velox.With(velox.With(db.Query[E]().Where(eKids.Has()), eParent), velox.WithRel(eKids, eParent)).Limit(1).All(ctx); err != nil {`.
  - Run the last line twice more, with the same ±64-byte agreement. Record the larger as **M_func**.
  - Then `rm -rf "$M"`.
  - The builder check measured this way at b02a241, at a 149-character path: M_method 770806 (twice) and M_func 769178 and 769180, so Δ ≈ +1627. Branch A is the expected outcome. The step still runs, and its numbers are the ones recorded.
- [ ] **Step 3: Decide.** Δ = M_method − M_func. The threshold is 1% of entityDescUse's bound, 766976 bytes, which is 7670.
  - If Δ > 7670, take **Branch B**.
  - Otherwise take **Branch A**, a negative Δ included.

  Write M_method, M_func, Δ and the scratch path's length in the task report.
- [ ] **Step 4, Branch A: Keep the methods.**
  - Keep Step 1's `Use`.
  - Re-set the `entityDescUse` and `entityDescHooks` bounds to their new measurements plus about 1.2%, rounded up to the next KiB. Measure both with `go test -count=1 -run 'TestGenericTypesCompileSmall' -v .` and read the `entityDescUse: +` and `entityDescHooks: +` lines.
  - Append to each row's comment: `remeasured on 2026-10-26 after Use called Rel.With (G18): <old> before`.
  - `policyGrowthMax` keeps its value unless the `policy and hooks:` line exceeds it, in which case stop and report.
- [ ] **Step 4, Branch B: Replace the methods with functions.**
  - Add `With` and `WithRel` and delete the two methods.
  - Rewrite every call site in Step 6's grep: `q.With(r)` becomes `velox.With(q, r)`, and `r.With(n)` becomes `velox.WithRel(r, n)`. In the root package's internal tests the `velox.` qualifier is dropped.
  - Rewrite example/relations/README.md:39-41 the same way.
  - Write `Use` as in Step 2.
  - `oldapi_test.go`'s old list gains `^func \(q Query\[E\]\) With\[` and `^func \(r Rel\[E, T\]\) With\[`.
  - doc.go's example uses `velox.With`.
  - Re-set the two bounds as in Branch A, with the comment `remeasured on 2026-10-26 when With and WithRel became functions (G18): <old> before`.
- [ ] **Step 5: Record the ruling in the api-cleanup spec.** Ruling 21's last sentence, "`Query.With[T]`, which predates this rule, takes a `Rel[E, T]`.", is replaced.
  - Branch A: `` `Query.With[T]` and `Rel.With[U]` stay methods, as the exception to this rule: on 2026-10-26, with both called, `entityDescUse` measured M_method bytes with the methods and M_func with functions `velox.With` and `velox.WithRel`, a difference of Δ, under the 1% (7670 bytes) the pre-1.0 hardening spec set (its ruling 11 and builder ruling 10).``
  - Branch B: `` `Query.With[T]` and `Rel.With[U]` became the functions `velox.With(q, r)` and `velox.WithRel(r, next)` on 2026-10-26: with both called, `entityDescUse` measured M_method bytes with the methods and M_func with the functions, a difference of Δ, over the 1% (7670 bytes) the pre-1.0 hardening spec set (its ruling 11 and builder ruling 10).``

  Write the measured numbers in place of the names.
- [ ] **Step 6: Leftover grep (Branch B only).** `git grep -nP '(?<!velox)\.With\(' -- '*.go' '*.md' ':!docs'`. Expected: nothing.
  - At b02a241 the `*.go` part prints 100 lines:
    - load_test.go 25, each_test.go 16, through_test.go 11;
    - internal/itest 9 (through_test.go 5, relations_test.go 3, scope_test.go 1), internal/gen/testdata/e2e/main_test.go 5;
    - lazy_test.go 4, failover_test.go 4, rowcap_test.go 3;
    - 2 each in tenant_test.go, query_test.go, policy_test.go, lock_test.go, json_test.go, desc_test.go, example/relations/main.go and contrib/graphql/cmd/velox-graphql/mutation_e2e_test.go;
    - 1 each in shard_test.go, route_test.go, result_test.go, after_test.go, doc.go, compilecost_test.go and contrib/graphql/cmd/velox-graphql/e2e_test.go.
  - The `*.md` part prints example/relations/README.md:39, :40 and :41.
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
  - `TestTypedNilDriverErrors`. For each of `(*pgconn.PgError)(nil)`, `fmt.Errorf("x: %w", (*mysql.MySQLError)(nil))` and `errors.Join(errors.New("x"), (*pgconn.PgError)(nil))`, none of these panics. (Not `io.EOF`: route.go:636 makes any error holding io.EOF failoverable.)
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
- [ ] **Step 6: Run** `go test .`, then run D with `go test -count=1 -run 'TestLock' ./internal/itest`, which also runs `TestLockWithScopeOnRealDatabases` (lock_test.go:174 calls `wantLockFailure`). Expected: PASS.
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
    - The same holds for a nested `tx.Tx` whose fn returns `boom`: the ROLLBACK TO is bounded, and the error text holds `velox: rollback to savepoint: `. Inside the outer fn, take the start just before the nested Tx, and read `e.RollbackDeadline` right after it returns. Read later, it would hold the outer ROLLBACK's deadline.
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
  - `git grep -nP 'db\.ex\.exec\(ctx, "(SAVEPOINT|RELEASE)' -- tx.go`. Expected: only the line of `rollbackSavepoint`'s RELEASE, which runs on the rollback context it is given and stays. At b02a241 it prints :302, :323 and :336.
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
  - `TestRecordSkipsDisabledInstruments` (record_internal_test.go, package veloxotel): with an adapter from `newAdapter(Config{TracerProvider: tracenoop.NewTracerProvider(), MeterProvider: metricnoop.NewMeterProvider()})` and a `*velox.Statement` of `OpSelect` with an Entity (`testmodel.Invoice`'s info), `testing.AllocsPerRun(100, …)` is 0 for each of `a.record(ctx, st, "invoices", "SELECT invoices", nil, 0.001, failure{}, false)` and `a.txRecord(ctx, start, tx, velox.TxCommit)`. `start := time.Now()` and `tx := &velox.TxInfo{Attempt: 1}` are built before the measured closure, not inside it.
  - Update the tests that assert the exact attribute set of `velox.tx.duration` (metrics_test.go) to the new set, shard and target included where the DB has them.
- [ ] **Step 2: Run** `go test -run TestTxInfoRoute .`, then `(cd contrib/veloxotel && go test -run 'TestTxSpanRoute|TestFallbackSpanOncePerFailover|TestRecordSkipsDisabledInstruments' .)`. Expected: FAIL.
- [ ] **Step 3: Implement** as in Interfaces. `Tx` passes the shard that `shardForTx` returned to `newTxInfo`. The veloxotel doc's list of tx span attributes names the shard and target.
- [ ] **Step 4: Leftover grep.** `git grep -nP 'if st\.Route\.Fallback \{' -- contrib/veloxotel`. Expected: nothing. At b3348ba it prints statement.go:97.
- [ ] **Step 5: Run** `go test -run 'Tx|Route' .`, then `(cd contrib/veloxotel && go test -race ./...)`. Expected: PASS.
- [ ] **Step 6: Commit** `velox: TxInfo.Route; veloxotel: tx spans name their shard and server, a fallback marks only the span that fell over, no attribute sets under a disabled meter`.
- [ ] **Step 7: Run the gate.** Expected: rc=0.

### Task 5: `Open`'s dialect, unknown conditions, timeouts that keep the driver's error, one entity prefix, the EXISTS backstop, pinning ambiguous writes, and an unknown row count (C15, C25, C33, C35, C37, C99)

**Files:**
- Modify: `db.go:120-132` (Open), `route.go:108-112` (OpenShards), `dialect.go` (`valid`), `render.go:430-527` (`pred`'s default), `db.go:228-279` (C33), `db.go:337-341` and `errors.go:146-162` (C35), `db.go:567-575` (C37's comment), `db.go:729-822` (C99), `executor.go:75-78`, `insert.go:486-498,605-620` and `statement.go:217-220` (C25).
- Modify: `example/tour/testdata/output.golden` by R7 (C33).
- Test: `db_test.go` (create if absent; package velox_test), `render_test.go`, `deadline_test.go:128`, `convention_test.go`, `pipeline_test.go`, `session_test.go:76-92`, `executor_internal_test.go`.

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
- **C25.** `sqlExecutor.exec` no longer fails a statement that ran because `RowsAffected` failed (executor.go:75-78). It returns `result{affected: -1, …}` and no error, and the write pins its session under C99.
  - `Result.Affected`'s doc says: `It is -1 for a read, for a failed statement, and for a write whose driver could not report its count.` `Mutation.Affected`'s doc says the same of -1.
  - `insertBatches` gives -1 when any batch gave -1.
  - `checkUpserted` cannot tell a hidden row without the count, so it fails closed: affected -1 on a policy-conditioned upsert is `&PolicyError{…, Err: errUnknownAffected}`, with `errUnknownAffected = errors.New("the driver did not report the rows the upsert affected, so a hidden row cannot be told apart")` (plan ruling 24).
  - Save's ErrNotFound test (`affected == 0`) is unchanged, so -1 is not ErrNotFound.

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
  - `TestCheckColumnsRefusesAForeignExists` (pipeline_test.go, package velox_test, where desc_model2_test.go's `pcomment` is visible): a Rewrite hook on a `pcomment` query appends `Predicate(r.Has())` for a relation r of an entity whose owner column index is 2 or more (pcomment has 2 columns). The statement fails with `a Rewrite hook set column`, and nothing is sent.
  - `TestFailedWritePinsTheSession` (session_test.go) replaces `TestFailedWriteDoesNotPin` (session_test.go:76-92), which asserts the opposite of C99. It keeps that test's `bothPaths` and `seeded(hooked)` setup and `sctx := velox.WithSession(ctx)`:
    - the insert that the primary fails with "boom" now pins: `velox.PinnedUntil(sctx)` is after now, and the following `readOne` goes to the primary (`s.check` expects the primary's count one higher and the replica's unchanged);
    - an Update whose primary returns `errors.New("connection reset")` from exec pins too;
    - an Insert with RETURNING whose row scan fails pins too;
    - with a fresh session, an Update refused by `checkParams` (with `velox.SetMaxParams(velox.Postgres, 1)`) leaves `PinnedUntil` zero;
    - an Update without Where (ErrNoWhere) leaves it zero too.
  - `TestRowsAffectedErrorIsUnknown` (executor_internal_test.go, package velox): an `sqlExecutor` over a stub `sqlConn` whose `ExecContext` returns a `sql.Result` with `RowsAffected` failing returns `affected == -1` and a nil error. `checkUpserted` on a policy-conditioned upsert with -1 returns a `*PolicyError` wrapping `errUnknownAffected`.
- [ ] **Step 2: Run** `go test -run 'TestOpenRefusesAnUnknownDialect|TestUnknownOpIsRefused|TestStatementOnTheTxContextTimesOut|TestRewriteErrorNamesTheEntityOnce|TestCheckColumnsRefusesAForeignExists|TestFailedWritePinsTheSession|TestRowsAffectedErrorIsUnknown' .`. Expected: FAIL.
- [ ] **Step 3: Implement** as in Interfaces.
- [ ] **Step 4: Leftover grep.**
  - `git grep -nP 'return errTimeout\(st\.Entity\.Name\)|rewrite: %w|hookError' -- '*.go'`. Expected: nothing. At b3348ba it prints db.go:239, :272 and :339, errors.go:146, :149, :154 and :162, and hook.go:121, :136, :148 and :152.
  - `git grep -nP 'Op\.Reads\(\) && err == nil' -- db.go`. Expected: only :813, the Affected line. At b3348ba it prints :813 and :818.
  - `git grep -nP 'func TestFailedWriteDoesNotPin' -- '*.go'`. Expected: nothing. At b02a241 it prints session_test.go:76.
- [ ] **Step 4b: Regenerate:** R7 for `./tour` only: `(cd example && go test ./tour -update)`. The only change: after golden line 55, `  velox: gen.Invoice: deadline set by velox passed: context deadline exceeded`, a line `velox: gen.Invoice: context deadline exceeded` is added, unindented, as `%v` prints the joined error (C33).
- [ ] **Step 5: Run** `go test -race .`, then `(cd example && go test ./tour)`. Expected: PASS.
- [ ] **Step 6: Commit** `velox: Open and OpenShards refuse an unknown dialect, an unknown condition fails rendering, a timeout keeps the driver's error, a rewrite's error names the entity once, an ambiguous write pins its session, an unreported row count is -1`.
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
    - on the file's policy-conditioned upsert entity, an **Around** hook that replaces `st.Conflict` with `&velox.ConflictClause{Columns: …, Update: …}` (a copy built by hand) before calling next, with a FakeExec affecting 0 rows, still fails with a `*velox.PolicyError` matching `ErrDenied`. It must be an Around hook: a Rewrite hook runs before `conditionUpsert` copies the clause, so it passes at b3348ba too, while an Around hook's copy reaches `checkUpserted` and drops the unexported flag today.
  - `TestHookCannotConditionAMultiRowUpsert` (insert_scope_test.go): an `InsertAll` of 2 rows with `OnConflict(…).DoNothing()`, outside a transaction, on an entity with a Read policy. A Rewrite hook sets `DoNothing = false` and `Update = []int{<a column>}`. The insert fails with the C47 text. `e.Log` holds no `BEGIN` and no `INSERT`.
  - `TestHookCannotDropUpsertReturning` (insert_scope_test.go): a single Insert with a zero integer key and `OnConflict(…).Update(…)` on PostgreSQL, on an entity with a Read policy. A Rewrite hook sets `st.Returning = nil`. The insert fails with the C48 text, and nothing is sent.
  - `TestDefaultsAreNotShared` (insert_test.go), with a hand-written entity `dshared`:
    - its fields are `ID int64`, `B []byte` with Default `[]byte{1, 2}`, `S []string` with Default `[]string{"a"}`, `M map[string]int` with Default `map[string]int{"a": 1}`, and `N map[string][]string` with Default `map[string][]string{"k": {"v"}}`;
    - insert two entities, then set `x1.B[0] = 9`, `x1.S[0] = "z"`, `x1.M["a"] = 9` and `x1.N["k"][0] = "z"`;
    - `x2`'s values equal the defaults, and a third insert gets the defaults.
  - validate_test.go:40's want becomes `velox: velox_test.dAuthor: CreatedAt: column is immutable`, and the upsert case at :51 checks the same text.
  - policy_test.go:227's want becomes `velox: velox_test.pbox: in the EXISTS on velox_test.pnote, then on testmodel.Invoice: no tenant in the context`.
  - `TestExistsErrorNamesThePath` (policy_test.go): `db.Query[pbox]().Where(pboxNote.HasWhere(pn.Invoice.Has())).Limit(1).All(context.Background())` gives the same text. Not the package `ctx`: it is `WithAllTenants` (query_test.go:18), so it has a tenant scope.
- [ ] **Step 2: Run** `go test -run 'TestUpsertPolicyFlagIsTheStatements|TestHookCannotConditionAMultiRowUpsert|TestHookCannotDropUpsertReturning|TestDefaultsAreNotShared|TestUpdateValidatesAndRefusesImmutable|TestPolicyConditionIsScopedAndChecked|TestExistsErrorNamesThePath' .`. Expected: FAIL.
- [ ] **Step 3: Implement** as in Interfaces.
- [ ] **Step 4: Leftover grep.**
  - `git grep -nP '\bcf\.policy\b|policy bool // the scope|(c|col)\.Name, ErrImmutable|e\.target\b' -- '*.go' ':!contrib'`. Expected: nothing.
  - At b02a241 it prints insert.go:247, :494, scope.go:177, :544, statement.go:104 and validate.go:86. (`':!contrib'` leaves out contrib/graphql's mutation_e2e_test.go:1207 and :1239, whose `e.target` is unrelated.)
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
- Modify: `example/tenant/main.go:104` (the comment) and `:108-109`, `example/tenant/README.md:13`, and `example/tenant/testdata/output.golden` by R7.
- Test: `tenant_test.go`, `insert_scope_test.go:56-60`, `budget_test.go`.

**Interfaces:**
- Consumes: Task 6's scope.go.
- Produces:

```go
// scope.go
// tenantFor returns id as the Go type of in's tenant column: unchanged when it
// is of that type; converted when it is of a predeclared integer type and the
// column's kind is an integer kind, or it is a string and the column's kind
// is string, whether the column's type is predeclared or named; refused with
// ErrTenantMismatch otherwise, or when it does not fit; and refused with
// ErrNoTenant when the result is zero.
func tenantFor(in *EntityInfo, id any) (any, error) // replaces checkTenantValue

// scoper.tenantOf(t *EntityInfo) (id any, ok bool, err error) // id is converted for t
```

- **The conversion rules** (builder ruling 8, as the controller refined it: plan ruling 8):
  - A named source type (`OrgID`, `UserID`) never converts: it must be the column's type exactly. A `UserID` given for an `OrgID` column fails with ErrTenantMismatch, even when both are int64.
  - A source of a predeclared integer type (`int`, `int8` … `int64`, `uint`, `uint8` … `uint64`) converts into a column of any integer kind, predeclared or named, so `WithTenant(ctx, 1)` serves an `OrgID int64` column. `uintptr` is neither a source nor a column kind that converts.
  - A predeclared `string` converts into a column of string kind, predeclared or named.
- **Range checks.**
  - A signed source whose value is negative is refused for an unsigned column.
  - An unsigned source above `math.MaxInt64` is refused for a signed column.
  - Otherwise the check is `OverflowInt` or `OverflowUint` on the column's type. All three checks read the column type's underlying kind, so a named column is checked as its kind is.
  - The refusal is `velox: <E>: tenant does not match: the context's tenant %v (a %T) does not fit the column %s, a %s`. The existing "is a %T, the column … is a %s" text stays for a type that never converts.
- **Where the conversion happens.** It happens per entity, inside each statement:
  - `scope` uses the statement entity's converted id for `checkTenantAssignments` and `tenantPredicate`, and records it as `st.Tenant`;
  - `scopeInsert` uses it for `checkTenantRows`;
  - each EXISTS target gets its own (`scoper.node`);
  - `fillTenant` sets and checks the converted value.
- **Allocations** (plan ruling 8). The exact type, the hot path, adds no allocation, as today.
  - A conversion to a predeclared target type uses a kind switch, such as `any(int64(v))`, which the runtime serves from its cache for values below 256. Only a named target type goes through `reflect.Value.Convert`.
  - `scoper` memoizes the converted id per column type, so a statement converts once per distinct tenant type, not once per EXISTS.
  - `fillTenant` converts once per InsertAll, not once per row.
  - A budget row in budget_test.go, `TestTenantConvertedOneRowBudget`, holds a one-row query under `WithTenant(ctx, 1)` on testmodel.Invoice to at most `TestTenantScopedOneRowBudget`'s allocations + 1.
- **What stays raw.** `RowScope.Tenant` and `WithShardFor`'s `tenant` are the context's raw id. `Statement.Tenant`'s doc: `the tenant scope applied, as the type of the entity's tenant column; nil when unscoped`.
- **WithTenant's doc** replaces "id's dynamic type must be the tenant column's Go type exactly" with:
  > id is converted to the tenant column's Go type when it is of a predeclared integer type (an untyped constant is an int) and the column's type, predeclared or named such as an OrgID int64, is an integer type whose range holds it; or when it is a string and the column's kind is string. An id of a named type is never converted: it must be the column's type, so a UserID never serves an OrgID column. Any other id fails the statement with ErrTenantMismatch.

  ErrTenantMismatch's doc says `cannot be converted to the tenant column's type` in place of `is not of the tenant column's type`.
- **C55.** Policy's godoc gains this paragraph, after "Any field may be nil …":
  > A policy function decides from ctx and, for Insert, the row; it does not query. A rule that must see other rows, such as that an inserted comment's parent is one the caller may read, is a hook (Hooks): it queries through Mutation.DB, in the write's transaction, and returns the error that refuses the write. This is ent's split: privacy rules decide, hooks query.
- **The example.** main.go:108-109 becomes three lines:
  - `db.Query[gen.Project]().Count(velox.WithTenant(ctx, 1))`, where 1 is an int converted to the column's int64, printed with `say("tenant 1 as an int: %d projects, err=%v", n, err)`;
  - then `type userID int64` (declared in the function) with `WithTenant(ctx, userID(1))`, printed with `say("tenant of another named type: ErrTenantMismatch=%t", …)`.
  - The comment at main.go:104, "Fail closed: no tenant, the wrong Go type, …", says `a named type other than the column's` in place of `the wrong Go type`.
  - README.md:13's comment becomes `// acme is an int64, the tenant column's type; WithTenant(ctx, 1) would be converted to it`.

- [ ] **Step 1: Write the failing tests** in tenant_test.go. Hand-write `tconvA`, with an `int64` tenant column `OrgID` and an `EdgeTo[tconvB]`; `tconvB`, with an `int32` tenant column; `tconvU`, with a `uint32` tenant; `tconvS`, with a tenant of a named string type `tslug`; and `tconvN`, with a tenant of a named integer type `torg` (`type torg int64`), and `tconvNU`, with one of `type tuorg uint16`.
  - `TestTenantConversion`, a table of id → outcome on a Count:
    - on `testmodel.Invoice` (`int64`): `7`, `int32(7)`, `uint8(7)` and `uint64(7)` pass, and the sent arg is `int64(7)`;
    - `uint64(1 << 63)` fails with ErrTenantMismatch and `does not fit`;
    - `0` and `int8(0)` fail with ErrNoTenant;
    - `"7"` fails with ErrTenantMismatch;
    - `orgID(7)`, with `type orgID int64` declared in the test, fails with ErrTenantMismatch;
    - on `tconvN`: `7` and `uint8(7)` pass as `torg(7)`; `orgID(7)` fails with ErrTenantMismatch, as a named source never converts into another named type; `torg(7)` passes unchanged;
    - on `tconvNU`: `-1` and `70000` fail with ErrTenantMismatch and `does not fit`, as the named column is checked as a uint16; `7` passes as `tuorg(7)`;
    - on `tconvU`: `-1` and `int64(1 << 40)` fail with ErrTenantMismatch, and `7` passes as `uint32(7)`;
    - on `tconvS`: `"acme"` passes as `tslug("acme")`, and a `type otherSlug string` value fails with ErrTenantMismatch;
    - an **Around** hook that keeps `st.Tenant`, as tenant_test.go:146-170 does, sees `int64(7)` for `WithTenant(ctx, 7)` on Invoice. (A Rewrite hook runs before the scope, db.go:337 then :400, so it sees nil.)
  - `TestTenantInsertConverts`:
    - an Insert of `testmodel.Invoice{}` under `WithTenant(ctx, 7)` fills `OrgID == 7`;
    - an Insert with `OrgID: 7` set passes;
    - one with `OrgID: 8` fails with ErrTenantMismatch.
  - `TestTenantConversionPerTarget`: `db.Query[tconvA]().Where(tconvAB.Has())` under `WithTenant(ctx, 7)` sends args holding `int32(7)` (the EXISTS's) and `int64(7)` (tconvA's own).
  - `TestRowScopeKeepsTheRawTenant`: `velox.RowScopeFromContext(velox.WithTenant(ctx, 7)).Tenant` is `int(7)`.
- [ ] **Step 2: Run** `go test -run 'TestTenantConversion|TestTenantInsertConverts|TestTenantConversionPerTarget|TestRowScopeKeepsTheRawTenant|TestTenantConvertedOneRowBudget' .`. Expected: FAIL.
- [ ] **Step 3: Implement** as in Interfaces. Update the tests that assert today's exact-type refusal of an `int`, which C51 now converts:
  - insert_scope_test.go:56-60, whose Ticket insert under `WithTenant(…, 7)` now passes with `tk.OrgID == 7`;
  - `TestTenantTypeMustMatch` (tenant_test.go:181-190), which becomes the named-type refusal: `WithTenant(…, orgID(7))`, with its doc comment and Review Focus wording updated.
- [ ] **Step 4: Regenerate:** R7 for `./tenant` only: `(cd example && go test ./tenant -update)`.
  - The golden's line `tenant of type int: ErrTenantMismatch=true` becomes `tenant 1 as an int: <n> projects, err=<nil>`, where n is acme's project count as the earlier lines print it.
  - A line `tenant of another named type: ErrTenantMismatch=true` is added.
  - No other line changes.
- [ ] **Step 5: Leftover grep.**
  - `git grep -nP 'checkTenantValue|reflect\.TypeOf\(id\) != t|dynamic type must be' -- '*.go'`. Expected: nothing. At b02a241 it prints insert.go:454, scope.go:298, :378, :383 and :385, and scope_api.go:29.
  - `git grep -nP 'exact Go type|the wrong Go type' -- example/tenant`. Expected: nothing. At b02a241 it prints README.md:13 and main.go:104.
  - `git grep -nP 'tenantPredicate\(t, sc\.id\)' -- scope.go`. Expected: nothing. At b3348ba it prints :506.
- [ ] **Step 6: Run** `go test -race .`, then `(cd example && go test ./tenant)`, then `(cd contrib/graphql && go test ./veloxgql/...)`. Expected: PASS.
- [ ] **Step 7: Commit** `velox: WithTenant converts predeclared integers and strings to the tenant column's type, per entity; Policy's doc says a rule that queries is a hook`.
- [ ] **Step 8: Run the gate.** Expected: rc=0.

### Task 9: Bidirectional self many-to-many edges write and remove both join rows (G17, core part)

This covers builder ruling 18. velox2 has no link API. A many-to-many link is a row of its join entity, written with `Insert`, `InsertAll` and `Delete`, as `EdgeThrough`'s doc says. So "the core link path" is the join entity's writes. ent writes `(a,b)` and `(b,a)` and deletes both (`dialect/sql/sqlgraph/graph.go:1661-1735`, `addM2MEdges` and `clearM2MEdges`). velox does the same for a join entity marked `Bidi`.

**Files:**
- Modify: `desc.go` (the `Bidi` field, its load checks, and EdgeThrough's doc at :351-355), `meta.go:21-37` (`EntityInfo.bidi`), `insert.go:244-252,339-363,528-567`, `write.go:373-410` (`writeCore`, `saveCore`).
- Modify: `internal/gen/out.go` (`OutEntity.Bidi`, and `Bound.PolicyConds`), `internal/gen/methods.go:45-80`, `internal/gen/lower.go:777-836` (`through`), `internal/gen/render.go:285`.
- Modify: `internal/gen/testdata/golden/blog/schema/user.go:35-36` (adds `edge.To("friends", User.Type)`), and the blog golden by R3.
- Test: a new `bidi_test.go` (package velox_test), `desc_test.go`, `internal/gen/render_test.go`, `internal/gen/methods_test.go`, `internal/itest/through_test.go`.

**Interfaces:**
- Consumes: nothing.
- Produces:

```go
// desc.go, Desc[E] gains, after Tenant:
// Bidi marks the join entity of a self-referencing many-to-many edge that has
// no inverse, which is bidirectional, as in ent: each link is the two rows
// (a, b) and (b, a). Insert and InsertAll write both, Delete removes both, and
// Update and Save are refused. Its two columns are its primary key and have
// one Go type. Its Policy may have Insert, which sees the rows the caller
// gave, and it may have Hooks; it cannot have Read or Write, whose conditions
// would show or delete one row of a link. For generated code.
Bidi bool

// meta.go: EntityInfo gains bidi bool, set from Desc.Bidi.
```

- **Load.** It refuses, with a SchemaError, a `Bidi` Desc that:
  - has other than 2 columns, or a primary key other than `[]int{0, 1}`;
  - has columns of two Go types;
  - has a tenant column;
  - has a Policy whose Read or Write is set. Load reads `desc.Policy()`'s `policy.read` and `policy.write`. The text is `velox: <E>: the join entity of a bidirectional edge cannot have a Read or Write policy: its conditions would show or delete one row of a link; check links in Policy.Insert and deletes in a hook` (plan ruling 21).
  - Policy.Insert and Hooks are allowed.
- **Insert, InsertAll and upserts.** A row `(a, b)` adds `(b, a)` after it, in the same statement, unless `a == b`.
  - `a == b` is compared with `bytes.Equal` for `[]byte` and `==` otherwise.
  - The mirrored row takes the statement's conflict clause, so `OnConflict(…).DoNothing()` covers both rows.
  - An upsert with an Update list is refused in `check`: `velox: <E>: the join entity of a bidirectional edge cannot be updated; delete the link and insert it again`.
  - InsertAll's rows per statement halve: `max(1, maxParams[d]/(2*len(cols)))`.
  - The entity's hooks and Policy.Insert see the caller's rows only. The mirror row is integrity maintenance, as ent's `addM2MEdges` writes it: neither judges it (plan ruling 22).
  - Rewrite hooks, Around hooks and `Mutation.Affected` see the written rows, two per link.
- **Delete.** In `writeCore`, before the statement is executed or rendered, a Bidi entity's non-empty Where `ps` becomes `[]Predicate{Or(And(ps…), And(swap(ps)…))}`.
  - `swap` copies each condition with column 0 and column 1 exchanged, at every depth of And, Or and Not.
  - It does not descend into the kids of an `opExists` or `opThrough` node, which hold another entity's columns. Such a node is kept as it is.
  - It copies a node (`m := n; m.kids = swapped`) and never writes a `node{op: opAnd…}` literal, so Task 12's `deep` flag is carried over unchanged (swap moves no EXISTS) and Task 12's grep finds no literal.
  - A delete with All is unchanged.
- **Update and Save** on a Bidi entity are refused in `writeCore` and `saveCore`, with the text above.
- **velox-gen.**
  - `through` sets `js.out.Bidi = ed.Bidi`. `ed.Bidi` holds only for the self M2M assoc edge with no inverse (build.go:358-361).
  - `render.go` writes `Bidi: true,` after `Tenant: …,` when it is set.
  - `plugin.Edge` is unchanged.
  - **A Read or Write policy is refused at generate time** when velox-gen can see it. `Methods` records, in `Bound.PolicyConds` (`"path:line"`), the first `Read:` or `Write:` key of a composite literal of type `velox.Policy[…]` in an entity's Policy method body. For a Bidi join entity with `PolicyConds` set, velox-gen fails with a `*FileError` at that path and line: `<Join>.Policy sets Read or Write: the join entity of a bidirectional edge allows only Insert, as a condition would show or delete one row of a link`. A Policy that builds its value another way is left to Load's check.
- **EdgeThrough's doc** (desc.go:351-355). Its last sentence gains: `When J is the join entity of a bidirectional edge (Desc.Bidi), its Policy cannot have Write either, since a condition would delete one row of a link: check deletes in a hook on J.`

**What Wave 3 must add** (the GraphQL part of builder ruling 18). The mutations write join rows with `db.InsertAll(rows).OnConflict(jo, jt).DoNothing()` and remove them with `db.Delete[J]().Where(…)` (gogen/mutation.go:536, :936, :959, :1158). So they write and remove both rows through this task with no generator change. Wave 3 adds:
1. A bidirectional self edge (for example `User.friends`, `edge.To("friends", User.Type)` with no inverse) to the GraphQL test model, and an e2e test:
   - `addFriendIDs: [b]` on a makes `a.friends` hold b and `b.friends` hold a;
   - `removeFriendIDs: [b]` empties both;
   - `clearFriends` empties both;
   - deleting a removes a from `b.friends`.
2. The UpdateX and CreateX godoc sentence "A self-edge is written one way, as the core's loads read it." (gogen/mutation.go:390 and its UpdateX twin) and contrib/graphql/doc.go:220's bullet say instead: `A bidirectional self-edge (a self-referencing edge with no inverse) is written both ways, as in ent: a many-to-many one writes and removes both join rows, and a one-to-one one keeps both rows' keys pointing at each other. A self-edge with an inverse is written one way.` The GraphQL goldens are regenerated.
3. Nothing in the SDL or in gqlimpl changes.

- [ ] **Step 1: Write the failing tests.**
  - `TestBidiLinks` (bidi_test.go), with hand-written `bnode` (ID int64) and `bnodeFriend` (`NodeID`, `FriendID int64`, `Bidi: true`, primary key `{0, 1}`), and an `EdgeThrough[bnodeFriend, bnode]("Friends", 0, 0, 1, 0)` on bnode. On `OpenFake(Postgres, e)`:
    - `Insert(&bnodeFriend{1, 2})` sends `INSERT INTO "bnode_friends" ("node_id", "friend_id") VALUES ($1, $2), ($3, $4)` with `1, 2, 2, 1`;
    - `Insert(&bnodeFriend{3, 3})` sends one row;
    - `InsertAll([]bnodeFriend{{1, 2}, {1, 3}}).OnConflict(…).DoNothing()` sends 4 rows and `ON CONFLICT ("node_id", "friend_id") DO NOTHING`;
    - `Delete[bnodeFriend]().Where(F.NodeID.EQ(1), F.FriendID.In(2, 3))` sends `WHERE (("node_id" = $1 AND "friend_id" IN ($2, $3)) OR ("friend_id" = $4 AND "node_id" IN ($5, $6)))`;
    - `Update[bnodeFriend]().Set(…).All()` and an upsert with `Update(…)` fail with the refusal text, and nothing is sent.
  - In TestBidiLinks too, on a second bnodeFriend type `bnodeFriendP` with a Policy of Insert only and one Hook: `Insert(&bnodeFriendP{1, 2})` calls Policy.Insert once, with `{1, 2}`, and the hook sees one row; the statement still writes 2 rows, and `Mutation.Affected` in an outer hook reads 2.
  - `TestBidiDescRefused` (desc_test.go): a Bidi Desc with 3 columns, one with an `int64` and a `string` column, one with `Tenant: 0`, and one whose Policy has Write are each refused with a SchemaError naming the entity.
  - internal/gen/render_test.go: the blog golden's `user_friend_velox.go` Desc holds `Bidi: true,`, and `user_group_velox.go` does not.
  - internal/gen/methods_test.go: a Policy method returning `velox.Policy[UserFriend]{Write: …}` sets `PolicyConds` to its file and the `Write:` line; one returning `velox.Policy[UserFriend]{Insert: …}` leaves it empty. Generating the blog model with that Write Policy on UserFriend fails with the `*FileError` at that line.
  - `TestBidiOnRealDatabases` (internal/itest/through_test.go), with the itest model gaining `person` and `personFriend` (table `person_friends`) in its own CREATE TABLE, on each dialect:
    - `Insert(&personFriend{1, 2})`, then a `With` load of `Friends` on persons 1 and 2, shows 2 under 1 and 1 under 2;
    - `Delete…Where(NodeID.EQ(2), FriendID.In(1))` leaves no rows, counted with `Count`.
- [ ] **Step 2: Run** `go test -run 'TestBidiLinks|TestBidiDescRefused' . && go test -run 'TestGolden|TestMethods' ./internal/gen`. Expected: FAIL. `Bidi` is not a field, the golden lacks UserFriend, and `PolicyConds` is undefined.
- [ ] **Step 3: Implement** as in Interfaces.
- [ ] **Step 4: Regenerate:** R3, then R2 and R5. Expected:
  - R3 adds `user_friend_velox.go` and its plugin.json and tables entries to the blog golden;
  - R3 also changes `user_velox.go`: the `Friends velox.Many[User]` field, its handle and `Rel` in the handles struct, the `velox.EdgeThrough[UserFriend, User]("Friends", …)` entry in the Desc's relations, and the EdgeAddr case for it. `velox.go` lists no entities and does not change. Nothing else changes;
  - R2 and R5 change nothing, because no other model has a bidirectional edge (`git status --short` shows none of their files).
- [ ] **Step 5: Leftover grep.** `git grep -nP 'Bidi: true' -- '*.go'`. Expected: the blog golden's user_friend_velox.go, bidi_test.go, desc_test.go and internal/itest's model. At b3348ba it prints nothing.
- [ ] **Step 6: Run** `go test -race . ./internal/gen ./cmd/velox-gen`, then run D with `go test -count=1 -run TestBidiOnRealDatabases ./internal/itest`. Expected: PASS.
- [ ] **Step 7: Commit** `velox: a bidirectional self many-to-many edge writes and removes both join rows, as ent's does (Desc.Bidi)`.
- [ ] **Step 8: Run G, then the gate.** Expected: G clean, gate rc=0.

### Task 10: ent's foreign-key defaults: a one-to-one key is unique, and a NULL-able key is ON DELETE SET NULL (controller ruling; plan ruling 20)

These defaults freeze at 1.0, and an ent schema must generate ent's DDL to be adopted. velox-gen copies them from ent's `entc/gen/graph.go`:
- `fkColumn` (:856-868): `column.Unique = e.Rel.Type == O2O`. Every one-to-one foreign-key column is unique, the implicit one and one bound with `.Field()` alike.
- `deleteAction` (:936-945): the default is `SET NULL` when the key column is NULL-able, and `NO ACTION` otherwise. An explicit `OnDelete` wins.

**Old velox** does the same: `compiler/gen/graph_tables.go:234` (`column.Unique = e.Rel.Type == O2O && !e.NonUniqueFK()`) and `:311` `deleteAction`, which calls `Edge.DeleteAction` (`compiler/gen/type_edge.go:315-339`, "Matches Ent's deleteAction"). Old velox adds one opt-out, the `sqlschema.NonUniqueFK` annotation, for a unidirectional `To(…).Unique()` edge meant as many-to-one. velox2 adds no such annotation. Its own spelling of a tree is ent's: `edge.To("children", T.Type).From("parent").Unique()`, which is O2M/M2O and not unique (plan ruling 20).

velox2 today: an implicit O2O key is unique (build.go:499 passes `unique = O2O`), a bound one is not (build.go:494 binds the field as written), and an edge without `OnDelete` has none (`onDeletes`, build.go:1055-1085).

**Files:**
- Modify: `internal/gen/build.go:470-495` (the bound-field branch) and `:1055-1085` (`onDeletes`).
- Modify: `internal/testmodel/schema/ticket.go:36-41` and `internal/testmodel/schema/sample.go:31-36` (the tree form, B1), and internal/testmodel by R2.
- Modify: `cmd/velox-gen/testdata/ent/schema/user.go:34-35` (drops the two `.OnDelete(edge.SetNull)`).
- Modify: `docs/specs/2026-10-15-schema-design.md:119`, a dated note (Step 6).
- Modify: the goldens by R3 and R5, and `internal/gen/build_test.go`, `internal/gen/tables_test.go` and `internal/gen/dbnames_test.go` where they assert the old defaults.
- Test: `internal/gen/build_test.go`, `cmd/velox-gen/migrate_baseline_test.go` (unchanged; it must still pass).

**Interfaces:**
- Consumes: nothing.
- Produces no Go API. The model changes:
  - **Unique.** In the bound-field branch, when `ed.Rel.Kind == "O2O"`, the bound field gets `Unique = true`, as `implicit` gives the implicit one. A field already `Unique()` is unchanged. Its unique index then reaches the Desc's `Indexes`, the DDL and `describe` as any unique field's does.
  - **ON DELETE.** In `onDeletes`, after `act` is decided, a relation that is not M2M with `act == ""` gets `act = "SET NULL"` when `!b.notNullable(s)`. A NOT NULL key keeps `""`: the DDL then has no clause, which is NO ACTION on all three dialects. migrate already treats "", `NO ACTION` and `RESTRICT` as one (`restricts`, migrate/drift.go:282 and :376), so ent's explicit `NO ACTION` adopts unchanged.
  - Rule 46 (`SET NULL needs a NULL-able foreign key`) still checks only an explicit `OnDelete`, so a default never trips it.
  - A join table's keys stay `CASCADE` (dbnames.go:198).
- **B1, testmodel's trees.** `Ticket.parent` and `Sample.parent` are `edge.To("parent", X.Type).Unique().Field("parent_id")` with no inverse. build.go:362-364 marks that O2O Bidi, which this task would make unique and Task 11 would make a spouse edge, while every test uses it as a tree. Both become ent's tree form:

  ```go
  edge.To("children", Ticket.Type).From("parent").Unique().Field("parent_id"),
  ```

  and the same for Sample. Their relation `Parent` keeps its name and its key column 7 (Ticket) and 11 (Sample), and each gains `Children velox.Many[X]`, keyed by the same column. The comments above both Edges methods say `Children and Parent, a tree whose key is parent_id`.

- [ ] **Step 1: Write the failing tests** in internal/gen/build_test.go:
  - `TestOneToOneKeyIsUnique`: a bound O2O key (`edge.To("profile", Profile.Type).Unique().Field("profile_id")` with `edge.From("owner", …).Ref("profile").Unique()`) gives a field with `Unique`, and so does a self O2O with a bound field. An M2O bound key (`edge.To("invoice", …).Unique().Field("invoice_id")`, no inverse, another type) stays not unique.
  - `TestDefaultOnDelete`: an optional O2M/M2O key gets `Rel.OnDelete == "SET NULL"`; a `Required()` one gets `""`; an explicit `OnDelete(edge.Cascade)` stays `CASCADE`; an M2M gets `""`.
  - The existing tests that assert `OnDelete == ""` for an edge without one are updated to the default, each named in the commit message.
- [ ] **Step 2: Run** `go test -run 'TestOneToOneKeyIsUnique|TestDefaultOnDelete' ./internal/gen`. Expected: FAIL.
- [ ] **Step 3: Implement** as in Interfaces, and rewrite testmodel's two edges.
- [ ] **Step 4: Drop the fixture's redundant OnDelete.** cmd/velox-gen/testdata/ent/schema/user.go:34 and :35 lose `.OnDelete(edge.SetNull)`: both keys are NULL-able, so the default gives the same `ON DELETE SET NULL` that ent's postgres.sql and mysql.sql hold. Its want/ goldens do not change.
- [ ] **Step 5: Regenerate:** R2, R3, then R5. Read every changed line. Expected:
  - R2: testmodel's ticket and sample files gain the `Children` relation (field, handle, `EdgeTo` entry, EdgeAddr case); `Parent`'s `EdgeTo[Ticket]("Parent", 7, 0)` and `EdgeTo[Sample]("Parent", 11, 0)` are unchanged; no unique index is added. bindmodel changes only where a bound O2O key gains its unique index.
  - R3: `internal/gen/testdata/tables/*.json` gain `"on_delete": "SET NULL"` (or each dialect's spelling) on every NULL-able foreign key without an explicit OnDelete, and a unique index on every bound O2O key; the golden models' `*_velox.go` change only by those unique indexes in their Desc `Indexes`.
  - R5: the examples' generated files change only by those unique indexes. The examples' hand-written DDL is not generated and does not change.
  - Anything else in a diff stops the task.
- [ ] **Step 6: Note the spec.** docs/specs/2026-10-15-schema-design.md:119 gains, at its end: `*(note 2026-10-26, hardening wave 2)* With no OnDelete, a NULL-able key is SET NULL and a NOT NULL one has none (NO ACTION), as ent's deleteAction; and every one-to-one key column is unique, implicit or bound, as ent's fkColumn.`
- [ ] **Step 7: Leftover grep.** `git grep -nP 'OnDelete\(edge\.SetNull\)' -- cmd/velox-gen/testdata`. Expected: nothing. At b02a241 it prints cmd/velox-gen/testdata/ent/schema/user.go:34 and :35.
  - `git grep -nP 'edge\.To\("parent", (Ticket|Sample)\.Type\)\.Unique\(\)\.Field' -- internal/testmodel`. Expected: nothing. At b02a241 it prints internal/testmodel/schema/sample.go:35 and ticket.go:40.
- [ ] **Step 8: Run** `go test ./internal/gen ./cmd/velox-gen`, then `go test -race .`, then `(cd example && go test ./...)`, then `(cd contrib/graphql && go test ./...)`, then run D with `go test -count=1 -run 'TestMigrateBaselineEnt' ./cmd/velox-gen` and `go test -count=1 ./internal/itest`. Expected: PASS. TestMigrateBaselineEnt is the acceptance check: ent's tables adopt with the same goldens.
- [ ] **Step 9: Commit** `velox-gen: ent's foreign-key defaults: a one-to-one key is unique, a NULL-able key is ON DELETE SET NULL; testmodel's parents are trees`.
- [ ] **Step 10: Run G, then the gate.** Expected: G clean, gate rc=0.

### Task 11: A bidirectional self one-to-one edge keeps both rows' keys pointing at each other (G17, O2O half)

This covers the controller's ruling on G17's O2O half (plan ruling 19). velox-gen marks a self-referencing unique edge with no inverse `Bidi`, with its key on the owner's own row (build.go:362-364, `ownFK` at :431). After Task 10 its key column is unique, implicit or bound, as ent's is. This is ent's "spouse" edge. velox2 has no edge-mutation API: the key is a column of the entity, written with `Insert`, `Update` and `Save`. So the core does ent's work in those writes.

**ent's behaviour** (`dialect/sql/sqlgraph/graph.go`; `entc/integration/integration_test.go:1570-1608`):
- `setTableColumns` (:1308-1347, :1902-1906) writes the owner's own key for a Bidi O2O edge, and sets it NULL when the edge is cleared.
- `clearFKEdges` (:1797-1819) clears the other row: `UPDATE t SET fk = NULL WHERE fk IN (ids)`. It applies no privacy rule: it is the edge's integrity.
- `addFKEdges` (:1821-1856) sets the other row: `UPDATE t SET fk = id WHERE id = target AND fk IS NULL`. Fewer rows than targets is a `ConstraintError` ("already connected to a different …"). It refuses linking more than one owner.
- All of this runs in one transaction that sqlgraph opens.
- **C's old partner.** When A is pointed at C and C already has a partner D, the write is refused: C's key is not NULL, so the `addFKEdges` UPDATE matches nothing. D and C are left as they were, and the transaction rolls back.
- **B, A's old partner.** In ent, `SetSpouse(c)` alone does not clear B. The key column's unique index then refuses the write, because B's key still holds A (integration_test.go:1602). `ClearSpouse().SetSpouse(c)` clears B first and succeeds (:1605-1608). velox has one assignment, the key's new value, where ent has a clear and a set. So velox's assignment does what ent's `ClearX().SetX(c)` does.
- A self-link (A as its own partner) is refused by ent: its own row's key is no longer NULL when `addFKEdges` runs.
- ent also links through an upsert (`creator.node`: the INSERT with OnConflict, then `addFKEdges`) and through CreateBulk (`batchCreator.nodes`, per node, in a transaction). velox refuses both, which is **stricter than ent** (plan ruling 19):
  - InsertAll returns no generated keys (C52, deferred), so velox cannot name the rows to link;
  - an upsert's DO NOTHING or DO UPDATE leaves it unknown whether the row that holds the key is the caller's, so the partner to link is ambiguous.
- Delete in ent deletes the row only. The foreign key's ON DELETE acts on the partner, and ent's default for a NULL-able key is SET NULL, which Task 10 gave velox-gen too.

**Files:**
- Modify: `relation.go:66-77` (`EdgeDesc.bidi`), `desc.go` (`EdgeBidi`, and its load checks beside EdgeTo's), `meta.go:21-37` (`EntityInfo.bidiKeys`), `write.go:373-410` (`writeCore`), `insert.go` (the one-row path, `check` and `prepareMany`), and `UpdateQuery.SQL`'s and `SaveQuery.SQL`'s docs.
- Create: `bidi.go`, which holds this task's functions.
- Modify: `internal/gen/build.go:362-364` (refuse `Required()`), `internal/gen/lower.go:758-770`, and `internal/gen/lower_test.go:920` (its want becomes `velox.EdgeBidi[Person]("Spouse", 1, 0)`).
- Modify: `internal/gen/testdata/golden/blog/schema/user.go` (adds `edge.To("spouse", User.Type).Unique()`), and the blog golden by R3.
- Modify, the test fake: `export_test.go` (`FakeExec.AffectedBy`).
- Test: `bidi_test.go`, `desc_test.go`, `internal/gen/build_test.go`, `internal/gen/render_test.go`, `internal/itest/through_test.go`.

**Interfaces:**
- Consumes: Task 9's `bidi_test.go` and itest `person` model, Task 6's insert.go, and Task 10's unique O2O key.
- Produces:

```go
// desc.go
// EdgeBidi is the one-to-one relation of a self-referencing edge that has no
// inverse, which is bidirectional, as in ent: owner.Columns[keyCol] holds
// the other row's T.Columns[idCol], and that row's key holds this row's. For
// generated code. T is the owner entity, idCol is its primary key, and
// keyCol is a nullable column of the key's Go type with a unique index.
//
// velox writes both rows. An Insert that sets the key sets the other row's
// key too. An Update or Save that sets it clears the key of the row that
// pointed here and sets the new partner's; one that sets it to nil clears
// the partner's. They run in a transaction velox opens, a savepoint when the
// caller is in one. A new partner that already has a partner is refused with
// a *ConstraintError of kind ConstraintUnique: clear its key first. Delete
// writes only the deleted rows; the foreign key's ON DELETE acts on their
// partners, and velox-gen's default for this NULL-able key, as ent's, is
// SET NULL.
func EdgeBidi[T any, PT Entity[T]](name string, keyCol, idCol int) EdgeDesc

// relation.go: EdgeDesc gains `bidi bool // EdgeBidi made it`.

// meta.go: EntityInfo gains `bidiKeys []bidiKey`, set at Load from the
// EdgeBidi relations.

// bidi.go
type bidiKey struct {
	col  int    // the key column, an index into Columns
	edge string // the relation's name, for errors
}

// bidiSet returns the bidirectional keys set assigns and their values; a nil
// value clears the key.
func bidiSet(in *EntityInfo, set []Assignment) ([]bidiKey, []any)

// pinnedKey returns v when where holds, at its top level, the condition
// <primary key> = v that Save writes and Where(pk.EQ(v)) adds.
func pinnedKey(in *EntityInfo, where []Predicate) (any, bool)

// bidiUpdate sends an update that sets bidirectional keys of row k, and then
// the statements that re-point the partners (steps 1-3 below), always under
// db.Tx: a transaction, or a savepoint inside the caller's.
func bidiUpdate(ctx context.Context, db *DB, mu *mutation, keys []bidiKey, k any, vs []any) error

// bidiLink sets the key of row v to k, if v has no partner:
// UPDATE … SET key = k WHERE id = v AND key IS NULL, scoped as the caller's
// statement is. When no row matches, it returns a *ConstraintError{Entity,
// Kind: ConstraintUnique, Constraint: the key column's name, Err: …}.
func bidiLink(ctx context.Context, db *DB, in *EntityInfo, bk bidiKey, k, v any) error

// export_test.go: FakeExec gains
//   AffectedBy func(sql string) int64 // when set, the affected rows of each exec, by its text; Affected otherwise
```

- **Load.** It refuses, with a SchemaError naming the entity and the edge, an `EdgeBidi` whose:
  - T is not the owner entity (its `holders[0]` is not `One[E]`'s type);
  - idCol is not the owner's only primary-key column;
  - keyCol is a primary-key column or the tenant column, is not Nullable, has a codec, or has a Go type (a pointer's element) other than the primary key's;
  - keyCol has no one-column unique index in the Desc's `Indexes`. The taken-partner refusal rests on it.
- **Update and Save** that assign a bidirectional key, in `writeCore`, after the existing checks and before anything is sent:
  - The Where must hold a top-level `<pk> = k` (`pinnedKey`). Save always writes one, and GraphQL's UpdateX writes `Where(pk.EQ(key))` (gogen/mutation.go:864). Without it the update is refused: `velox: <E>: an update that sets <column>, the key of the bidirectional edge <Edge>, must name one row with Where(<pk>.EQ(id)), as velox re-points the other row too`. `All` is refused the same way.
  - `v == k` is refused: `velox: <E>: a row cannot be its own <Edge>`.
  - Then, always under `db.Tx`, which is a savepoint inside the caller's transaction. Without the savepoint, a failed step would abort the caller's transaction on PostgreSQL, and on MySQL leave steps 1 and 2 written:
    1. The caller's UPDATE, scoped as today. It reports n rows. When n is 0, nothing else is sent: Save fails with ErrNotFound as before, and Update reports 0. When n is above 1 (a Rewrite hook dropped the key condition), the write fails with `velox: <E>: the update that sets the bidirectional key <column> wrote <n> rows`, and the transaction rolls back.
    2. For each bidirectional key it sets: `UPDATE t SET key = $1 WHERE key = $2`, with `nil, k`. It clears B, the old partner, and v when v already pointed at k, so re-saving the same link works. It runs **scope-free**, under `WithoutPolicy` and `WithAllTenants` as builder ruling 12 defines them, inside the same transaction, and only after step 1 succeeded under the caller's scope (plan ruling 22). It is integrity maintenance, as ent's `clearFKEdges` is. So a partner the caller cannot see is still cleared, and no row is left pointing at k.
    3. For each key set to a non-nil v: `bidiLink(…, k, v)`, scoped as the caller's statement. When it matches no row, the transaction rolls back, and the error depends on whether the caller can see v, which a scoped `Exist` on `<pk> = v` tells:
       - visible: Err is `row <v> already has a <Edge>; clear it first`;
       - hidden by the tenant scope or the Write conditions: Err is `row <v> is already linked`. It reveals nothing more about v.
       - When another row's key already holds v, step 1 is refused first by the column's unique index, which errclass reports as a ConstraintUnique too.
  - The entity's Hooks and Policy.Write run once, around the whole set, because they wrap writeCore. Steps 2 and 3 are sent through `execWrite`, as InsertAll's later batches are, so Rewrite hooks and Around hooks see them.
  - `Mutation.Affected` is step 1's n, the caller's rows.
  - `SQL(ctx)` renders step 1 only. The doc of `UpdateQuery.SQL` and `SaveQuery.SQL` says so in one sentence.
  - A driver that cannot report the rows a step affected (C25's -1) fails the write: `velox: <E>: the driver did not report the rows the link wrote`.
- **Insert** of one row whose bidirectional key is a non-nil v: always under `db.Tx`, the INSERT, then `bidiLink(…, id, v)` with the row's id as the INSERT returned it. A key equal to the row's own id (when the id is set) is refused before sending, with the self-link text. A nil key is one statement, as today.
- **Refused** in `check` and `prepareMany`, before anything is sent. Both are stricter than ent, for the reasons above:
  - an Insert with `OnConflict` whose row sets a bidirectional key: `velox: <E>: an upsert cannot set the key of the bidirectional edge <Edge>; insert the row, then Save the key`;
  - an InsertAll with any row that sets one: `velox: <E>: InsertAll cannot set the key of the bidirectional edge <Edge>; insert the rows, then Save each key`.
- **Delete** is unchanged, as ent's is: the key's ON DELETE acts on the partner. With Task 10's default, SET NULL, the partner's key clears. With an explicit `OnDelete(edge.Restrict)`, deleting a row that has a partner fails with a ConstraintForeignKey error.
- **velox-gen.**
  - At lower.go:769, an edge with `ed.Rel.Kind == "O2O" && ed.Bidi` gets `r.Desc = fmt.Sprintf("velox.EdgeBidi[%s](%q, %d, %d)", t.Name, ed.GoName, ownerCol, targetCol)`.
  - build.go's `case le.Unique && t == n` refuses `le.Required` as a new rule 53: `a bidirectional one-to-one edge cannot be Required: its key is cleared whenever its partner re-points; make it optional, or give it an inverse edge`. A Required key would be NOT NULL, which EdgeBidi's Load refuses at first use.
  - `OutRel`, render.go and `plugin.Edge` are unchanged. contrib/graphql's `OwnsKey` already holds for this edge (sdl/input_test.go:50, `user_best_friend`), so the SDL is unchanged.

**What Wave 3 must add** (the GraphQL part of the O2O half). CreateX sets `<edge>ID` through the core's Insert, and UpdateX sets it through `db.Update[E]().Set(set...).Where(pk.EQ(key))` (gogen/mutation.go:864). So both write the two rows through this task with no generator change. Wave 3 adds:
1. A mutable bidirectional O2O edge to the GraphQL test model, for example `edge.To("bestFriend", User.Type).Unique()`, and an e2e test, on each dialect:
   - `createUser(input: {bestFriendID: b})` makes `b.bestFriend` a;
   - `updateUser(a, {bestFriendID: c})` makes `c.bestFriend` a, and `b.bestFriend` null;
   - `updateUser(a, {clearBestFriend: true})` makes `c.bestFriend` null;
   - `updateUser(a, {bestFriendID: d})`, where d already has a partner, fails with the ConstraintError's message, and changes nothing.
2. velox-graphql's e2e `twin` edge (e2e_test.go:106), which is immutable, is now set on both rows by createCategory. Wave 3 asserts that. The e2e tests generate their code when they run, so no committed GraphQL file holds the `Twin` relation (`git grep -nP 'EdgeTo\[\w+\]\("Twin"'` prints nothing at b02a241).
3. The godoc sentence about self-edges is Task 9's Wave 3 item 2, which covers both halves.

- [ ] **Step 1: Write the failing tests.**
  - `TestBidiOne` (bidi_test.go), with a hand-written `bspouse` (`ID int64`, `SpouseID *int64` with a unique index, table `bspouses`, and `EdgeBidi[bspouse]("Spouse", 1, 0)`), on `OpenFake(Postgres, e)`, with `e.AffectedBy` answering 1 row unless the case says otherwise. Pointers are written `new(int64(3))`: package velox_test has no `ptr`. It asserts each case's statements in order, with their texts and args:
    - `Save(&bspouse{ID: 1, SpouseID: new(int64(3))}, S.SpouseID)` sends, between a BEGIN and a COMMIT: the UPDATE of row 1 setting `spouse_id`, with `3, 1`; `UPDATE "bspouses" SET "spouse_id" = $1 WHERE "spouse_id" = $2`, with `nil, 1`; and `SET "spouse_id" = $1 WHERE "id" = $2 AND "spouse_id" IS NULL`, with `1, 3`;
    - `Save` of the same row with `SpouseID: nil` sends the first two only, the first with `nil, 1`;
    - `Update[bspouse]().Set(S.SpouseID.Set(3)).Where(S.ID.EQ(1))` sends the same three as the first case;
    - the third statement answering 0 rows, with the probe `Exist` answering a row, fails with a `*velox.ConstraintError` of `ConstraintUnique`, `Constraint == "spouse_id"`, its text holding `row 3 already has a Spouse`, and the transaction is rolled back;
    - the same with the probe answering no row fails with `row 3 is already linked`, and the text holds neither `Spouse;` nor anything about row 3 but its key;
    - the first statement answering 0 rows sends nothing more, and Save fails with ErrNotFound;
    - inside `db.Tx(ctx, fn)`, the first case's three statements are sent between a `SAVEPOINT` and its `RELEASE`, and a failing third statement sends `ROLLBACK TO SAVEPOINT`, after which fn can go on;
    - `Save(&bspouse{ID: 1, SpouseID: new(int64(1))}, …)` fails with `a row cannot be its own Spouse`, and nothing is sent;
    - `Update[bspouse]().Set(S.SpouseID.Set(3)).Where(S.ID.In(1, 2))` and `….All()` fail with the `must name one row` text, and nothing is sent;
    - `Insert(&bspouse{ID: 5, SpouseID: new(int64(1))})` sends the INSERT, then `SET "spouse_id" = $1 WHERE "id" = $2 AND "spouse_id" IS NULL` with `5, 1`;
    - `Insert(…).OnConflict(S.ID).DoNothing()` with a non-nil key, and `InsertAll` with one, fail with their texts, and nothing is sent;
    - a hook on bspouse's `Mutation.Affected` reads 1 for the Save.
  - `TestBidiOneDescRefused` (desc_test.go): an EdgeBidi to another entity, one whose key column is not a pointer, one whose key is an `*int32` beside an `int64` id, and one whose key column has no unique index are each refused with a SchemaError naming the entity and the edge.
  - internal/gen/build_test.go: `edge.To("spouse", Person.Type).Unique().Required()` fails with rule 53's text.
  - internal/gen/render_test.go: the blog golden's `user_velox.go` holds `velox.EdgeBidi[User]("Spouse", `, and no `EdgeTo[User]("Spouse"`.
  - `TestBidiOneOnRealDatabases` (internal/itest/through_test.go). Task 9's `person` gains `SpouseID *int64` (column `person_spouse`, UNIQUE, a foreign key to `persons.id` with ON DELETE SET NULL, in the test's own CREATE TABLE) and an `EdgeBidi[person]("Spouse", …)`. On SQLite the test runs `PRAGMA foreign_keys = ON` first: internal/itest opens SQLite as `sql.Open("sqlite", ":memory:")` (itest_test.go:150), which does not enforce foreign keys, and its target has one connection. On each dialect, with persons 1 to 4 (5, 6 and 7 are inserted by the cases):
    - `Insert` of person 5 with `SpouseID: 1` gives person 1 `SpouseID == 5`;
    - `Save` of person 5 with `SpouseID: 2` gives 1 nil, 2 = 5, and 5 = 2;
    - `Save` of person 3 with `SpouseID: 4`, then `Save` of person 5 with `SpouseID: 4`, fails with a ConstraintError, refused by step 1's unique index, and 5 = 2, 2 = 5, 3 = 4 and 4 = 3 are unchanged (C's old partner, as in ent);
    - **the one-way key** (Review Focus 3). Persons 6 and 7 are inserted with no key. With raw SQL, 6 gets `person_spouse = 7` while 7 keeps NULL, so 6's key points one way, and no row's key is 6. `Save` of person 5 with `SpouseID: 6` passes step 1 (no row's key is 6, so the unique index allows it), clears 2 in step 2, and fails in step 3, since 6's key is not NULL. Afterwards 5 = 2 and 2 = 5 again (step 2 rolled back), 6 = 7 and 7 nil;
    - the same `Save` inside a caller's `Tx`, whose fn then reads person 2 and returns nil: the read shows 2 = 5, and the Tx commits;
    - `Save` of person 5 with `SpouseID: nil` gives 5 nil and 2 nil;
    - `Delete` of person 3 leaves 4 nil, through ON DELETE SET NULL;
    - each case reads the rows back with a `Query` ordered by ID.
- [ ] **Step 2: Run** `go test -run 'TestBidiOne|TestBidiOneDescRefused' . && go test -run 'TestGolden|TestLower|TestBuild' ./internal/gen`. Expected: FAIL. `EdgeBidi` is undefined, the golden lacks Spouse, and rule 53 does not exist.
- [ ] **Step 3: Implement** as in Interfaces.
- [ ] **Step 4: Regenerate:** R3, then R2 and R5. Expected:
  - R3 changes the blog golden's user files, plugin.json and tables entry, for the `user_spouse` column, its unique index and its foreign key (ON DELETE SET NULL, by Task 10), and the `Spouse` relation, and changes nothing else;
  - R2 and R5 change nothing: after Task 10 no other model has a self unique edge without an inverse.
- [ ] **Step 5: Leftover grep.** `git grep -nP 'EdgeBidi\[' -- '*.go'`. Expected: desc.go, lower.go, lower_test.go, the blog golden's user_velox.go, bidi_test.go, desc_test.go and internal/itest's model. At b02a241 it prints nothing.
- [ ] **Step 6: Run** `go test -race . ./internal/gen ./cmd/velox-gen`, then `(cd contrib/graphql && go test ./...)`, then run D with `go test -count=1 -run 'TestBidiOnRealDatabases|TestBidiOneOnRealDatabases' ./internal/itest`. Expected: PASS. contrib/graphql's e2e `twin` edge now writes both rows. If an e2e case there created two categories with the same twin, it fails with the ConstraintError. Fix the case's data, not the core, and say so in the commit message.
- [ ] **Step 7: Commit** `velox: a bidirectional self one-to-one edge keeps both rows' keys pointing at each other, as ent's does (EdgeBidi)`.
- [ ] **Step 8: Run G, then the gate.** Expected: G clean, gate rc=0.

### Task 12: Perf: large inserts skip the cache, Set clones once, the scope skips trees without EXISTS, loads pin without a DB copy (C30, C41, C43, C91)

**Files:**
- Modify: `render_cache.go:50-90` (C30), `scope_api.go:340,438-456` and `hook.go:85-99` (C41), `pred.go:35-40,85-117`, `query.go:300-310`, `relation.go:480-485` and `scope.go:455-462` (C43), `load.go:106-215`, `each.go:241`, `query.go:444`, `route.go:448-495`, `db.go:29-31` and `statement.go:203` (C91).
- Test: `render_cache_test.go` (package velox), `hooks_internal_test.go`, a new `scope_internal_test.go` (package velox: it reads `sc.node` and `deep`; scope_fuzz_test.go is velox_test), `budget_test.go`.

**Interfaces:**
- Consumes: Task 7's load.go, and Tasks 9 and 11's insert.go, write.go and bidi.go.
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
    - an InsertAll of 33 rows leaves `CacheSize(db)` unchanged, and an InsertAll of 32 rows grows it by 1 (the file is package velox, so no `velox.` qualifier);
    - both statements' `Fingerprint`, read by an **Around** hook, is 16 hex characters. Rendering runs after the Rewrite hooks (db.go:400-414), so a Rewrite hook would see "";
    - 33 rows twice gives the same fingerprint.
  - `TestMutationSetClonesOnce` (hooks_internal_test.go, package velox):
    - with `st := &Statement{Op: OpUpdate, Set: orig}`, where orig is 2 assignments, and `mu := &mutation{st: st}`, three `mutateSet(mu, Assignment{Column: orig[0].Column, Op: AssignSet, Value: i})` calls leave `orig` unchanged;
    - `testing.AllocsPerRun(100, …)` of a fresh mu doing the three calls is 1.
  - `TestScopeSkipsTreesWithoutExists` (scope_internal_test.go):
    - `And(a, Or(b, c))` of comparisons has `deep == false` at every level, and `And(a, Or(b, rel.Has()))` has `deep == true` on the And and the Or;
    - `sc.node` of a 1000-leaf `And` without EXISTS returns `changed == false` with 0 allocations (`testing.AllocsPerRun`). This is a regression guard only: an unchanged tree allocates nothing at b3348ba too. The `deep` assertions above are what test the change;
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

### Task 13: Re-measure the compile-cost and allocation bounds (builder ruling 20, Wave 2)

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
- [ ] **Step 3: Measure the allocations.** Run `go test -count=1 -run 'Budget' -v . 2>&1 | grep -E 'allocations'` and `(cd contrib/veloxotel && go test -count=1 -run 'Budget|Disabled' -v ./... 2>&1 | grep -E 'allocations|^(ok|FAIL|---)')`. contrib/veloxotel has no Budget test; `Disabled` runs Task 4's `TestRecordSkipsDisabledInstruments`.
  - Every budget holds, or the task stops and reports, as above.
  - Write the numbers in the report.
  - A comment that quotes a measured figure (TestReplicaOneRowBudget's "TestOneRowAllocationBudget's 6") is updated to the measurement.
- [ ] **Step 4: Run** `go test -count=1 -run TestGenericTypesCompileSmall .`. Expected: PASS.
- [ ] **Step 5: Commit** `velox: compile-cost and allocation bounds re-measured after Wave 2`.
- [ ] **Step 6: Run the gate.** Expected: rc=0.

## Rulings this plan makes

Each is a call made while writing this plan. An implementer who finds one wrong stops and reports it instead of changing it.

1. **(call, as the builder check fixed it) G18's measurement and names.** Both forms are called in the same `Use` statement, and both are measured in one scratch copy outside the repo (`git archive`), whose root tests are only `compilecost_test.go`. The path matters: sizes grow about 59 bytes per character of the module's path, so the two numbers must share one. Two runs agree within ±64 bytes, as the test's TempDir name varies. The function form is measured with the methods deleted, because a method costs every package that uses a `Query` whether it is called or not (ruling 21's Sum finding). Δ above 7670 bytes, 1% of the bound, selects functions. The functions are `velox.With` and `velox.WithRel`, as the builder check named them.
2. **(call) C97 is fixed in veloxotel, by delta.** Core's `Route.Fallback` stays sticky, because the Around doc makes "set it to true and the retry goes to the primary" a hook contract. Resetting it per execution would break that contract. Telemetry reads the change during its own span.
3. **(call) C99 pins on any error once the statement reached the driver.** This errs toward the primary, which keeps read-your-writes. A statement refused before sending does not pin.
4. **(call) C49 re-chunks on overflow, and does not merely count the policy's parameters.** Re-chunking holds whatever the policy or a Rewrite hook adds. Counting would still fail when a hook adds more than 64 parameters. The 64-parameter reserve stays, so that the first try usually fits. The overflow is caught before the Around hooks, so no span records it.
5. **(call) C30 leaves inserts above 32 rows uncached, and does not bucket their sizes.** Bucketing would split one InsertAll into more statements, which is more round trips. Not caching costs only the render time that a large insert already spends collecting arguments.
6. **(call) C43 uses a per-node flag, not the triage's per-entity precomputation.** A per-entity flag is unsafe: `Pred[E](Predicate(otherRel.Has()))` converts another entity's EXISTS into E's conditions. Skipping by entity would then drop that target's tenant condition. The node flag is set by the only constructors of And, Or and Not nodes, fits node's padding, and skips exactly the trees that hold no EXISTS. The controller confirmed this approach before execution.
7. **(call) C29 copies deeply**, through pointers, slices, maps, arrays and exported struct fields. A shallow copy would leave a `map[string][]string` Default shared.
8. **(call) C51.**
   - `Statement.Tenant` shows the value converted for the statement's entity, as builder ruling 8's source asked ("show hooks the converted value").
   - `RowScope.Tenant` and `WithShardFor` see the context's raw id: they describe the context, not a statement.
   - The controller refined builder ruling 8: a predeclared source converts into a column of the same kind, whether the column's type is predeclared or named. So `WithTenant(ctx, 1)` converts into a column of a named `OrgID int64`, and a `string` into a column of a named string type. Range and sign are checked on the column type's underlying kind.
   - A named source type is never converted, into any column: a `UserID` given for an `OrgID` column fails with ErrTenantMismatch, even when both are int64.
   - The exact type allocates nothing extra. A conversion allocates at most once per statement per tenant type, and once per InsertAll, through a kind switch to predeclared types and a per-scope memo.
9. **(call) C87 and G53 reuse `Route` as `TxInfo.Route`** (builder ruling 15). Its fields match: Replica is always -1 and Fallback always false. The tx duration point gets the shard and target too, as a statement's does.
10. **(call) C23's bound is 5 s.** database/sql's `Tx.Rollback` takes no context, so on `Open`'s DBs the bound covers ROLLBACK TO only, and Tx's doc says so.
11. **(call) C34: a savepoint's ROLLBACK TO is bounded by the earlier of the rollback timeout and the transaction's deadline.** Past the deadline the transaction's own ROLLBACK undoes everything anyway. The transaction's ROLLBACK is bounded by the timeout alone, since it must run after the deadline.
12. **(call) C15's unknown condition records a rendering error, not a panic.** The statement is refused and the caller gets an error.
13. **(call) C37 keeps the branch, as a backstop.** It is reachable through a Predicate conversion, and the test proves it.
14. **(call) G17's core link path is the join entity's Insert, InsertAll and Delete.** velox2 has no link API.
    - Update, Save and an upsert's Update are refused, because they would break the symmetry.
    - Policy.Insert and Hooks are allowed on the join entity; Read and Write are refused (plan ruling 21).
    - An a–a link is one row.
    - `Mutation.Affected` counts rows, two per link.
    - No generated-code bump: version 6 has not been released.
15. **(call) C55 lands here**, beside the Policy code that Task 8 edits.
16. **(call) `ErrLocked`'s text is "lock not available"**, PostgreSQL's name for 55P03, which a `lock_timeout` raises too.
17. **(call) C35 generalizes `hookError` into `stageError`**, so the hook and rewrite stages share one trimming rule.
18. **(call) C33 joins the timeout first:** `errors.Join(errTimeout(…), err)`, as the transaction's does (tx.go:166).
19. **(controller) G17's O2O half is built in this wave (Task 11).**
    - A self unique edge with no inverse, which velox-gen marks Bidi, keeps both rows' keys pointing at each other, as ent's spouse edge does.
    - velox's one assignment of the key does what ent's `ClearX().SetX(c)` does: the old partner is cleared, and the new one is set.
    - A new partner that already has one is refused with a ConstraintError, and its old partner is untouched, as in ent.
    - An update that sets the key must name one row by primary key, as ent's `addFKEdges` refuses more than one.
    - The steps always run under `db.Tx`, so inside a caller's transaction they are a savepoint, which a failure rolls back alone.
    - Upserts and InsertAll that set the key are refused. This is **stricter than ent**, which links through both: InsertAll returns no generated keys (C52, deferred), and an upsert's DO NOTHING or DO UPDATE leaves the partner ambiguous.
    - Delete leaves the partner to the foreign key's ON DELETE, as ent's does, and Task 10 gives it ent's SET NULL default.
    - velox-gen refuses `Required()` on such an edge (rule 53): its key is cleared whenever its partner re-points.
    - `Mutation.Affected` counts the caller's rows only.
20. **(controller) ent's foreign-key defaults (Task 10).** A one-to-one key column is unique, implicit or bound (ent's `fkColumn`). With no `OnDelete`, a NULL-able key is SET NULL and a NOT NULL one has no clause, which is NO ACTION (ent's `deleteAction`). An explicit `OnDelete` wins. Old velox does the same, and adds a `NonUniqueFK` opt-out that velox2 does not take: a tree is written in ent's form, `To("children").From("parent").Unique()`, which is not O2O. testmodel's two parent edges are rewritten so.
21. **(controller, B12) The join entity of a bidirectional many-to-many edge may have Policy.Insert and Hooks, and not Read or Write.** Insert and the hooks see the caller's rows. A Read or Write condition, ANDed after the swap, would show or delete one row of a link, so Load refuses it, and velox-gen refuses it at file:line when the Policy method's literal shows it. EdgeThrough's doc says so.
22. **(controller, B16) A bidirectional write's mirror is integrity maintenance, as in ent.**
    - O2O: clearing the old partner (`SET key = NULL WHERE key = k`) runs scope-free, under WithoutPolicy and WithAllTenants, inside the same transaction, only after the caller's scoped UPDATE succeeded. The link step stays scoped. A link refused because the partner is hidden says `row <v> is already linked`, and nothing more.
    - M2M: the mirror row `(b, a)` and the swapped half of a Delete are not judged by Policy.Insert or the hooks, which judge the caller's rows.
23. **(call) C25's unknown count is -1, not an error.** The statement ran, so failing it would invite a retry of a write that happened. A write with -1 pins its session (C99).
24. **(call) A policy-conditioned upsert whose count is unknown fails closed** with a PolicyError, since a hidden row cannot be told apart. Only a driver whose `RowsAffected` fails reaches it.

## Decisions for the controller before execution

None. The three open at a66565a (G17's O2O half, C51's integer rule and C43's approach) were ruled by the controller: plan rulings 19, 8 and 6.

## Rulings made during execution

1. **Per-task gates under load.** A gate that failed only in bench/scale on time, with every earlier step passing, was accepted for a task that touched no build surface. The machine is shared with other projects' CI. Task 13 and the final fix wave each passed bench/scale outright.
2. **C27 widened (Task 2).** Go's `errors.Is` itself panics on a typed-nil `*net.OpError` or `*pgconn.ConnectError`, so every driver-error read goes through a nil-skipping walk (`is`, `as`), in core and in contrib. MySQL 8.4's 4025 is ER_INNODB_AUTOEXTEND_SIZE_OUT_OF_RANGE, so 4025 is a CHECK only in MariaDB's message form.
3. **A failed savepoint rollback breaks the transaction (Task 3).** The outer transaction then refuses to commit, later statements fail before sending, and a COMMIT that was sent and failed pins the session.
4. **Callers of an unknown row count (Task 5).**
   - MySQL's insert reads its key when the count is -1.
   - GraphQL update falls back to its locked existence check.
   - GraphQL delete fails with `gqlimpl.CountUnknown` and rolls back.
   - `SetAffected` accepts -1.
5. **Hooks cannot unguard a write (Tasks 6, 11 and the final fix).** `prepare` refuses a Rewrite hook changing InTx. The row count velox sent (`upsertRows`), the bidi marker, the load pin and a bidi key's value are velox's own, snapshotted before the Rewrite hooks and checked after them.
6. **Defaults are copied per row, through interfaces (Task 6).** velox-gen's hoisted `field.Other` Default still shares one value, as ent's does; its godoc says so.
7. **`Statement.Tenant` (Task 8).** On a scoped entity it is the converted id. On an unscoped entity that reads a scoped one, it is the id as WithTenant was given it, which is the earlier behaviour; a nil was tried and reversed.
8. **The M2M bidi mirror is not idempotent (Task 9).** ent's implicit DO NOTHING would also hide a conflict on the caller's own row. `OnConflict(…).DoNothing()` is the idempotent path and repairs a half-link.
9. **ent's FK defaults, completed (Task 10).**
   - `edge.NoAction` exists.
   - A Required self edge's implicit key is NULL-able and SET NULL, as in ent and old velox. Its Required is enforced before sending (`ColumnDesc.RequiredEdge`, `missing required edge "T.e"`), as ent's builder check does.
   - A Required self edge that binds its own non-pointer `.Field()` keeps a NOT NULL key: a known ent-adoption difference.
10. **O2O bidi (Task 11).**
    - The old partner's clear crosses tenants and policy, as the edge's integrity; the scoped API cannot create a cross-tenant pair.
    - A hidden partner gives `row v cannot be linked`.
    - The self-link refusal is a ValidationError.

## Left by this plan

- **Wave 3 (GraphQL).**
  - A nil-safe `velox.IsNotFound(err)` for generated code: gogen's `errors.Is(err, velox.ErrNotFound)` can panic on a typed-nil driver error.
  - `parent: T!` resolves null after ON DELETE SET NULL of a Required self edge's key, as in entgql.
  - `removeChildIDs`/`clearChildren` are offered for a Required self edge and always refused.
  - Bidi e2e tests through create and update.
- **Wave 6 (docs).**
  - doc.go's bidi paragraph.
  - WithShardFor may run more than once per write.
  - The ent-adoption differences above (8, the bound Required self key).
