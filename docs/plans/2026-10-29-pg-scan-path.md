# PostgreSQL scan path on database/sql + pgx stdlib — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Remove velox2's PostgreSQL loss on database/sql + pgx stdlib (Query100 2.6×, EagerO2M100 3.5× slower than old velox, `docs/reviews/2026-10-29-perf-comparison.md`, "Where velox2 loses"). Every scan stays bit-identical. SQLite and MySQL do not regress. Then re-measure the runtime with bench/compare, with velox2's `OpenPgx` as a fourth column.

**Architecture:**
- The cause, re-verified in **Evidence**: Go 1.27's database/sql hands each column to pgx's `stdlib.(*Rows).ScanColumn`. That calls `isSQLScanDestination(reflect.TypeOf(dest))` with no cache. The check is a chain of `reflect.Type.Implements` calls, and their cost grows with the number of methods on the destination type. For `*time.Time` it is about 8 µs a call; for `*string` or `*int64` it is about 50 ns. velox2 scans `created_at` straight into a `time.Time` field, so it pays about 8 µs per row per time column. ent and old velox scan into `sql.NullTime`, whose check passes on its first `Implements`.
- The fix is a scan wrapper that sits in one place: the database/sql executor's rows. Every row-scan path reaches `rows.Scan` through the executor: `scanRow`, insert's RETURNING, `readPart`/`ownerRows`, each.go, aggregate.go, raw.go and `kit`. On a `*sql.DB` whose driver is pgx v5's stdlib, `sqlExecutor.query` returns a `*colRows` in place of `sqlRows`. Its `Scan` swaps each destination that pgx would send to `sql.ConvertAssign` for a `*colScanner`. A `colScanner` is an `sql.Scanner` that calls `sql.ConvertAssign(driver.ScanContext{}, dst, src)`. pgx then passes its first check at once and calls `ConvertAssign` on the scanner, which calls `colScanner.Scan(src)` with the same `src`. The conversion is the same `ConvertAssign` call on the same `dst` and `src` as today, so values and error texts are the same by construction. The tests pin that.
- A destination is wrapped only where pgx would have taken the `ConvertAssign` branch anyway, and only if it is not already an `sql.Scanner` (`scanWrapsType`, a stricter mirror of pgx's check). Every other destination is passed through unchanged: a codec column's `*codecScanner`, a `kind.Moment`, a UUID `Scanner`, `*any`, slices and arrays that pgx scans itself, named pointer types, and `sql.RawBytes`. The rule's result is cached per type in a `sync.Map`. `colRows` also remembers each column's last destination type, so a row after the first costs one type comparison per column.
- Where it applies: only a `*sql.DB` whose `Driver()` is `*github.com/jackc/pgx/v5/stdlib.Driver`. This is checked by package path, so velox2 does not import stdlib, which would register the `pgx` driver as a side effect. The check runs in `Open` and `SQLConn`, and `begin` carries it into transactions. SQLite (modernc v1.60.1) and MySQL (go-sql-driver v1.10.1) do not implement `RowsColumnScanner`, so their path stays as it is: the same `sqlRows` value, plus one bool test per query. `OpenPgx` is untouched.
- Cost: on pgx stdlib only, up to 3 allocations per query (the `colRows` and its two slices), and none per row or per column.
- Generated code does not change. `GeneratedCodeIsVersion6` stays.

**Tech Stack:** Go 1.27.1. `github.com/jackc/pgx/v5 v5.11.0` (stdlib, pgtype), `modernc.org/sqlite v1.60.1`, `github.com/go-sql-driver/mysql v1.10.1`. benchstat `golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e`, pinned as in `scripts/bench-compare.sh`. PostgreSQL 18 and MySQL 8.4 from `docker-compose.ci.yml`. bench/compare as built by `docs/plans/2026-10-29-perf-comparison.md`.

**Spec:** There is no spec file. The Rulings below are binding. "Ruling N" means item N of that list.

## Rulings

1. Fix it in velox2's scan path. No fork, vendored patch or replace of pgx. An upstream pgx issue/PR (caching the check) is outward-facing: the plan may draft its text under docs/, but nothing is filed.
2. Results must be bit-identical to today's: every Go field type velox2 supports (ints/uints of all widths, floats, bool, string, []byte, time.Time, named types such as enums, nullable pointer fields, fields whose type implements sql.Scanner, codec/JSON columns, the M2M owner key in ownerRows, aggregates) must scan to the same value and fail with an equivalent error on the same inputs (overflow, NULL into non-nullable, wrong type). Pin this with a differential test against today's behavior / `database/sql` conversion, on SQLite and PostgreSQL, and a fuzz target if it fits the existing fuzz setup (see fuzz_test.go, scripts/ci-local.sh).
3. No regression on SQLite or MySQL in time or allocs: the wrapper (if that is the design) may apply only where it pays, decided by measurement. velox2 prides itself on low allocs/op — no per-column-per-row allocation.
4. Measure: a root-module PostgreSQL benchmark that shows the before/after (benchstat), and re-run bench/compare's runtime step only (not the build steps) for all three ORMs, adding a velox2 `OpenPgx` (native pgx pool) variant as a fourth runtime column. Publish the result as an addendum section in docs/reviews/2026-10-29-perf-comparison.md (or a new dated review that the old one links to — your call, say why), numbers only from the run, plus a CHANGELOG line.
5. Existing coverage floors, lint (golangci-lint; revive.toml), godoc tests (godoc_test.go), convention tests and the gate `bash scripts/ci-local.sh` must stay green. If the generated code changes, follow the CHANGELOG's GeneratedCodeIsVersionN rule — prefer a fix that does not change generated code.

## Decisions made while planning

- **Where the wrapper goes: in the executor's rows, not in `scanDst`.** `scanDst` covers only the entity scans. Raw queries, aggregates, each.go's owner keys and `ownerRows` scan their own destinations, and `ownerRows` builds a fresh slice for every row. All of them call `rows.Scan`, so `colRows.Scan` covers every path, generated code included, with one change. Its per-row cost is one `reflect.TypeOf` and one comparison per column. The probe measured it level with the hand-wrapped scan (Evidence 3).
- **Which destinations: all that pgx would `ConvertAssign`, not only `time.Time`.** Wrapping only the time column won the whole gain in the probe, and wrapping every column was level with that (1.051 ms against 1.049 ms; Evidence 2). The uniform rule is the simpler one to state and to test. Ruling 3's "only where it pays" is applied per driver (pgx stdlib only, where pgx calls `ScanColumn`), and Task 3 re-checks it per column type with `Items100`, a query with no time column. If benchstat shows `Items100` on `postgres-sql` slower after the fix than before (p < 0.05), narrow `scanWrapsType` to types where some type on the pointer chain has `NumMethod() > 0`. Record that under "Rulings made during execution".
- **Detection by package path,** for the reason given under Architecture. A driver wrapped by another package (otelsql, say) is not detected. Its scans keep today's path: no speed-up, and no change.
- **The scanner converts with `driver.ScanContext{}`.** `ConvertAssign` reads its `*sql.Rows` only for `*sql.RawBytes` and `*sql.Rows` destinations (convert.go, Go 1.27.1). `scanWrapsType` never wraps either.
- **Report: a new dated review, linked from the old one** (Ruling 4, the controller's call delegated here). The old review records one run at d59c718. Its appendix is that run's generated tables, and its rule is "every number comes from that run". An addendum would mix two runs, two commits and two loads in one file. A new file keeps one run per file and makes the comparison explicit. The old review gains a one-line pointer in its Summary and under "Where velox2 loses". None of its numbers change.

## Plan-check fixes

The builder check (1 Critical, 8 Important, 10 Minor) and the controller's rulings on it changed the plan as follows:

1. **The velox2pgx `*sql.DB` stays open** (controller ruling). `newApp` runs a probe instead, before any timer: across one `count`, the pool's `AcquireCount` must rise, and `sqlDB.Stats()` must not change in `OpenConnections`, `InUse`, `WaitCount` or `MaxIdleClosed`. The last is made sensitive by `SetMaxIdleConns(0)` during the probe. This changes Task 5 Interfaces, Review Focus 5 and `TestGuardPostgres`.
2. **`ColumnScan` is the outer call,** `sql.ConvertAssign(driver.ScanContext{}, &colScanner{dst}, src)`, which is what pgx does. The fuzz compares it with the plain `ConvertAssign` (Task 2).
3. **`FuzzColumnScan` lives in a new `colscan_fuzz_test.go`** (package `velox_test`), with the parameter `pick`. I took this option over an internal test, because the fuzz types include `kind.Status`, which an internal test cannot import. The file is in Task 2's commit.
4. **Local types in the classification table are given by pointer:** `*status`, `**status`, `*cents`, `*moment`, `*scanned`, `**scanned`.
5. **One type-list convention:** lists name the destination type `T`, always a pointer, and tests make `reflect.New(T.Elem())`. The grid's extra entries were rewritten to match (`*kind.Moment`, `**kind.Moment`, `*bindtype.UUID`, `*sql.NullInt64`, …). The seeds now pin NULL into `*string` as an error, and NULL into `**time.Time` as nil.
6. **An explicit `steps=runtime` config key,** mirroring `COMPARE_STEPS`. `summarizeBuild` tolerates zero build files only under it. Empty `sizes` keeps its meaning today. `TestSummarizeRuntimeOnly` checks both sides.
7. **The variant fixture is its own directory,** `testdata/summary-variant/`, with both rounds. A variant file without `variants` configured is accepted and shown, but not required. The shared fixture is unchanged.
8. **`SQLConn` is tested:** `ConnColumnScanning` in export_test.go, and `TestSQLConnDetects` (Task 3, Review Focus 1).
9. **Coverage is measured as the floors file says:** `-race`, no database variables, twice. This is Task 3 Step 8, separate from Step 5.
10. **A typed-nil destination returns today's error** (controller ruling). I did not take the suggested fix of an error string in `colScanner.Scan`. Instead, `colRows.Scan` does not wrap a typed nil pointer, so the driver rejects it exactly as today: pgx's `destination pointer is nil`, or database/sql's own error. The text cannot drift, and a nil check runs only on wrapped columns. `TestColumnScanRowsKeepCallerSlice` covers `(*time.Time)(nil)` on SQLite and, when the DSN is set, on PostgreSQL.
11. **The PostgreSQL literals carry their types:** `'\x00ff'::bytea` and `'…'::timestamptz`. SQLite keeps `x'00ff'` and the bare text.
12. **Both CONTRIBUTING counts are updated:** eight root fuzz targets become nine, and nine targets in all become ten.
13. **`TestGuardPostgres` gets a fourth run, after velox2's.** Its comment says that the variant reuses and reseeds `cmp_velox2`.
14. **Every run.sh spot is listed:** per-dialect ORM lists, `test_bin`/`test_env`, `rotated`, `rounds_done`, the skip-postgres loop, the guard loop, the round loop, the concatenation and benchstat.
15. **No ruling numbers or plan paths in root comments** (a Global Constraint), because `TestGodocCitesNoSpec` scans the root's tests.
16. **`wrapConnector` is spelled out** (it is itself a `driver.Driver`, with an erroring `Open`), and `stdlib.OpenDBFromPool` is a detected case.
17. **Decision record 0018 pastes the `postgres-sql` benchstat rows** rather than citing the uncommitted file.
18. **`TestColumnScanAllocsPerQuery` is pinned down:**
    - it uses `WithAllTenants` and the DDL of each_test.go:162;
    - its exact per-row equality holds only when `!raceEnabled`, with ±1 under race;
    - Task 5 Step 2 no longer claims that `TestLossesIgnoreVariant` fails first.
19. **Decision record 0018's Consequences say the mirror guards only velox2's own pgx,** because an application's MVS may select a newer one. I left out the optional run-time `debug.ReadBuildInfo` check: it would also refuse harmless pgx upgrades.

## Evidence

The probes ran on 2026-10-29 between 23:43 and 23:49 local time. The machine was the Apple M3 Max, 14 CPUs, Go 1.27.1, pgx v5.11.0, PostgreSQL 18 from `docker-compose.ci.yml` (project `velox2-ci`, brought down afterwards). They ran in a scratch module, not in the repository. The load was high and rose during the run, so the time figures are wide (±10–23%). The ratios are large enough to stand anyway.

1. **The check alone** (a verbatim copy of pgx's `isSQLScanDestination`, 200,000 calls, `-count 3`, 1-minute load 14.4). Per call:
   - `*time.Time` 7.5–10.2 µs and `**time.Time` 7.3–9.0 µs;
   - `*float64` 75–119 ns, `**string` 63–84 ns, `*[]byte` 55–63 ns, `*int64` 48–64 ns, `*string` 44–49 ns, `*bool` 43–48 ns, and a named string enum 44–50 ns;
   - `*sql.NullTime` 28–36 ns, `*sql.NullString` 27–88 ns, and a one-method `Scanner` wrapper 35–134 ns.

   A CPU profile of the `*time.Time` case shows `reflect.implements` with `runtime.resolveNameOff` and `abi.Name.Name` beneath it: 89% of the samples. `Implements` resolves the name of every method of the type, and `time.Time` and `*time.Time` have many.
2. **100 rows × 8 columns** through database/sql (id, name, a named string, n, bool, `time.Time`, nullable `*string`, float). The columns hold one time column, as in velox2's E1. Six samples each, load 26.8 → 39.5:

   | destinations | pgx stdlib sec/op | SQLite (modernc) sec/op | allocs/op (pgx / SQLite) |
   |---|---|---|---|
   | plain field pointers (velox2 today) | 2.025 ms ± 13% | 787 µs ± 14% | 979 / 1,086 |
   | every column wrapped in a `ConvertAssign` scanner | 1.051 ms ± 18% | 787 µs ± 14% | 979 / 1,086 |
   | only the time column wrapped | 1.049 ms ± 20% | 749 µs ± 17% | 979 / 1,086 |
   | `sql.Null*` (ent-style) | 1.035 ms ± 16% | 757 µs ± 13% | 979 / 1,086 |

   On pgx the wrapper halves the time and matches ent's style. On SQLite, which has no `ScanColumn`, no difference can be resolved at this load.
3. **The `colRows` prototype** (per-column type memo, `sync.Map` rule cache, own args slice). Eight samples, load 45.5 → 50.0. Plain took 2.221 ms ± 19%, hand-wrapped 1.008 ms ± 23%, `colRows` 1.040 ms ± 10% and `sql.Null*` 1.006 ms ± 12%. `colRows` made 981 allocations against 979, and used 22.49 KiB against 22.05 KiB: two allocations per query, none per row.
4. **The profile of the plain pgx case** (3 s, load ~37). `stdlib.isSQLScanDestination` took 0.55 s of the 1.64 s of CPU samples (33.5%), nearly all of it in `reflect.implements`. The socket read took 40%. `sql.ConvertAssign` does not appear among the top 40 nodes. The earlier figure, 250 ms of 890 ms (28%), agrees.
5. **The other causes asked about are ruled out.**
   - `ConvertAssign`'s reflection is the same work for all three ORMs: `sql.NullInt64.Scan` calls it too.
   - Allocations are the same with and without the wrapper (point 2).
   - Pointer-to-pointer destinations cost no more than plain ones, except `**time.Time`, which is the time cost again.
   - `grep ScanColumn` finds nothing in go-sql-driver/mysql v1.10.1 or modernc.org/sqlite v1.60.1. Both take database/sql's `Next` path, which this plan does not change.

## Global Constraints

- Go 1.27. Before any go command: `export GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2`.
- The machine is shared, swap-bound and loaded (12–50 while planning).
  - Run one heavy command at a time: never two `go test` runs, two lints, or a lint beside a test.
  - `timeout` is missing. Use `perl -e 'alarm N; exec @ARGV' <cmd>`.
- Databases:
  - Start one stack only, the compose project `velox2-ci`, and only after both checks print nothing: `docker compose -f docker-compose.ci.yml -p velox2-ci ps -q` and `pgrep -f '^bash scripts/ci-local.sh'`.
  - Never touch other containers (postgres, redis, mailpit, pgadmin, redisinsight, buildx_*). Never restart Docker.
  - Bring the stack down explicitly with `docker compose -f docker-compose.ci.yml -p velox2-ci down -v` as the last command of the same Bash call that started it. Do not rely on an EXIT trap: the tool's shell may persist, and during planning a trap did not fire.
- Never print DSNs or passwords. Export them exactly as `scripts/ci-local.sh` lines 27–30 do, with no `set -x` and no echo.
- Generated code must not change: `git diff --exit-code -- internal/testmodel internal/bindmodel example` stays clean. If it would change, stop and report (Ruling 5).
- The per-task check (C). Every task ends green under it:
  - `test -z "$(gofmt -l .)"`;
  - `go vet ./...` and `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` in each module touched (root, `bench/compare`);
  - `go test -race -count=1` of the touched packages: always `.` and `./internal/itest` for root changes, and `-short ./...` in `bench/compare`.
- Test names: before adding one, `git grep -nP 'func <Name>\(' -- '*.go'` must print nothing.
- No ruling numbers, "Review Focus" labels or `docs/plans` paths in root-package comments or strings, tests included: `TestGodocCitesNoSpec` scans the root's test files too (godoc_test.go:19-49). The plan's labels below are for the plan only. Citing `docs/decisions/0018` is allowed.
- Commits:
  - The message is `area: what changed`. It ends with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - Stage by name, never with `-A` or `.`, and never stage `.DS_Store`. Never push.

## Review Focus

1. **A fix that is silently off.** The detection could miss `stdlib.OpenDB`/`GetConnector`, a replica made with `SQLConn`, or a transaction. The tests are in Task 3:
   - `TestPgxStdlibDetected`: `sql.Open("pgx", …)`, `stdlib.OpenDB(cfg)`, `sql.OpenDB(stdlib.GetConnector(cfg))` and `stdlib.OpenDBFromPool(pool)` are detected; `sql.Open("sqlite", …)` and a wrapping connector are not;
   - `TestSQLConnDetects`: `SQLConn` on a pgx `*sql.DB` scans through `colRows`, and on SQLite it does not;
   - `TestBeginKeepsColumnScan`: rows inside a transaction are `*colRows`;
   - `BenchmarkScan`: `postgres-sql` must move before and after.
2. **A destination moved off pgx's own scan path.** If `scanWrapsType` wrapped a type for which pgx's check returns false (a slice, an `ArraySetter`, a named pointer), conversion would switch from pgx's type map to `ConvertAssign` and results would change. Expected: wrap ⇒ pgx's check is true and the type is no `Scanner`. The tests are in Task 2 (`TestScanWrapsMirrorsPgx` and `TestPgxMirrorIsCurrent`, which fails when a pgx upgrade changes the copied function) and Task 3 (`TestColumnScanMatchesPlain` on PostgreSQL).
3. **A cost per row, or on the wrong driver.** Expected: on SQLite and MySQL the rows stay `sqlRows`, and allocations do not change. Under forced wrapping, the extra allocations of a 100-row read equal those of a 1-row read, and are at most 3. The tests are in Task 3 (`TestColumnScanAllocsPerQuery`, `TestSQLiteAndMySQLKeepPlainRows`, and the `sqlite`/`mysql` rows of the Task 3 benchstat).
4. **The caller's slice, or a destination type that changes between rows.** `colRows.Scan` must not write into the caller's `dst` (`ownerRows` and `scanRow` reuse or rebuild theirs). It must re-classify a column whose destination type changes: a raw read into `*string` on one row and `*kind.Status` on the next. A nil destination, untyped or a typed nil pointer, must return the same error as today and never panic. The test is in Task 3: `TestColumnScanRowsKeepCallerSlice`.
5. **The fourth column measures the wrong thing, or counts as a competitor.** The `velox2pgx` variant could fall back to database/sql. Or `losses` could list "velox2pgx beats velox2" as a loss. Expected: the variant's `newApp` runs one op as a probe. Across it, the pgx pool's `Stat().AcquireCount()` must rise, and the `*sql.DB` must show no traffic. The `*sql.DB` stays open, because the harness's setup and guard keep using it. `beaters` ignores the variant, and the report shows it as a column only. The tests are in Task 5: `TestGuardPostgres`'s `velox2pgx` run, and `TestLossesIgnoreVariant`.

---

### Task 1: A PostgreSQL scan benchmark in the root module, and the "before" numbers

**Files:**
- Create: `internal/itest/scan_bench_test.go`
- Modify: `internal/itest/itest_test.go`. `targets`, `sqliteTarget`, `postgresTargets`, `mysqlTarget`, `sqlExec`, `setup` and the field `target.exec` take `testing.TB` in place of `*testing.T`. Callers that pass a `*testing.T` compile unchanged.

**Interfaces:**
- `func BenchmarkScan(b *testing.B)`. For each target of `targets(b, nil)` (`sqlite`, `postgres-pgx`, `postgres-sql`, `mysql`), `setup(b, tg)` runs, then `seedScan(b, tg)`, then three sub-benchmarks with `b.ReportAllocs()` and `for b.Loop()`:
  - `Query100`: `tg.db.Query[testmodel.Invoice]().Where(I.OrgID.EQ(7)).OrderBy(I.ID.Asc()).Limit(100).All(ctx)`. It fails unless it gets 100 rows. Each row has two `time.Time` columns, a `*time.Time`, `[]byte`, float, bool and strings.
  - `EagerO2M100`: the same query with `.With(I.Items)`. It fails unless it gets 100 parents and 500 items.
  - `Items100`: `tg.db.Query[testmodel.Item]().Where(It.InvoiceID.GTE(1000)).OrderBy(It.ID.Asc()).Limit(100).All(ctx)`. It reads only scalar columns, the control for Ruling 3's per-type check.
- `func seedScan(tb testing.TB, tg target)`. It inserts 100 invoices with ids 1000–1099 and org 7, and every third one with a `paid_at`. It inserts 5 items for each, with ids 10000 + 5i + j. All of it goes through `tg.exec`.

- [ ] **Step 1: Write the benchmark.** Run it once on SQLite as a test of its own assertions: `go test -run '^$' -bench '^BenchmarkScan$' -benchtime 1x ./internal/itest`. Expected: `ok`, with `BenchmarkScan/sqlite/Query100`, `…/EagerO2M100` and `…/Items100` reported.
- [ ] **Step 2: Run C** (`go test -race -count=1 ./internal/itest` is unaffected by the `testing.TB` change). **Commit**: `git add internal/itest/scan_bench_test.go internal/itest/itest_test.go`. Message: `itest: BenchmarkScan reads 100 rows, 100 parents with their items, and 100 scalar rows on every database`.
- [ ] **Step 3: The "before" run, at this commit.** Do the database checks of Global Constraints. Then, in one Bash call:
  ```bash
  docker compose -f docker-compose.ci.yml -p velox2-ci up -d --wait
  # export VELOX_TEST_POSTGRES, VELOX_TEST_MYSQL and VELOX_TEST_MYSQL_ADMIN exactly as scripts/ci-local.sh does
  uptime
  perl -e 'alarm 3600; exec @ARGV' go test -run '^$' -bench '^BenchmarkScan$' -benchmem -count 6 ./internal/itest > /private/tmp/claude-501/pgscan-before.txt
  uptime
  docker compose -f docker-compose.ci.yml -p velox2-ci down -v
  ```
  Expected: 72 result lines (4 targets × 3 benchmarks × 6). Note both `uptime` lines for the review. Keep the file; it is not committed.

### Task 2: The scanner and the rule for what it wraps

**Files:**
- Create: `colscan.go`
- Create: `colscan_internal_test.go` (package `velox`), and `pgxmirror_test.go` (package `velox`). The latter holds a verbatim copy of pgx v5.11.0's `isSQLScanDestination`, renamed `pgxIsSQLScanDestination`, under a comment that names its source file, its version and pgx's MIT license.
- Create: `colscan_fuzz_test.go` (package `velox_test`, for `FuzzColumnScan`). fuzz_test.go is `package velox`, and the fuzz needs `internal/testmodel/kind`, which an internal test cannot import.
- Modify: `export_test.go`, `.github/workflows/ci.yml` and `scripts/ci-local.sh` (one fuzz line each, after `FuzzDiff`), and `CONTRIBUTING.md`. In CONTRIBUTING, under Checks, "the eight root fuzz targets" becomes "nine". Under Fuzz targets, "There are nine" becomes "ten", and the new target is listed among the root's.

**Interfaces** (package `velox`, all unexported):
- `type colScanner struct{ dst any }`. `dst` is a pointer to the destination.
- `func (s *colScanner) Scan(src any) error { return sql.ConvertAssign(driver.ScanContext{}, s.dst, src) }`. Its doc says that it converts exactly as database/sql would for `dst`, and why it exists: pgx stdlib checks a destination's type on every column of every row, and a `Scanner` passes that check at once.
- `func scanWrapsType(t reflect.Type) bool`. This is the rule, uncached. It is the algorithm, given here because tests do not determine it:
  1. Return false if `t` is nil, is not a pointer, or is a named type.
  2. Walk `e := t`. At each step, return false if `e` implements `sql.Scanner`, `pgtype.ArraySetter` or `pgtype.BytesScanner`. Stop at the first `e` that is not a pointer. Return false at a named pointer below `t`.
  3. Return false if the base `e` is `sql.RawBytes`.
  4. Return true if the base's kind is bool, any int or uint width, float32, float64 or string; or a slice whose element kind is `uint8`; or a struct `ConvertibleTo(time.Time)`. Return false for anything else, including interfaces, arrays, maps and other slices.
- `var scanWrapCache sync.Map` (`reflect.Type` → `bool`) and `func scanWraps(t reflect.Type) bool`. The latter is the cached `scanWrapsType`. A nil `t` returns false without touching the cache.
- In `export_test.go`:
  - `func ScanWraps(t reflect.Type) bool { return scanWraps(t) }`;
  - `func ColumnScan(dst, src any) error { return sql.ConvertAssign(driver.ScanContext{}, &colScanner{dst: dst}, src) }`. This is the outer call pgx makes (stdlib/sql_go1.27.go:66), so database/sql's typed fast paths (convert.go:249-449) run before the `Scanner` branch, as in production.
- **Type-list convention** (Tasks 2 and 3): every type list in a test names the destination's type, which is always a pointer (`*int8`, `**string`). A test makes a destination of type `T` with `reflect.New(T.Elem())`. So `*string` is a non-nullable field, whose NULL is an error, and `**string` is a nullable one.

- [ ] **Step 1: Write the tests.**
  - `TestScanWrapsClassification` (colscan_internal_test.go), table-driven on `reflect.TypeFor[...]()`.
    - **Wrapped** (each kind velox2 supports): `*int`, `*int8`, `*int16`, `*int32`, `*int64`, `*uint` … `*uint64`, `*float32`, `*float64`, `*bool`, `*string`, `*[]byte`, `*time.Time`, `**string`, `**int64`, `**time.Time`, `**float64`, `**bool`, `*json.RawMessage`, and these local types given by pointer: `*status` and `**status` (`type status string`), `*cents` (`type cents int64`), and `*moment` (`type moment time.Time`, with no methods).
    - **Not wrapped**: `*codecScanner`; `*sql.NullString`; `*scanned` and `**scanned` (a local `type scanned time.Time` whose pointer has `Scan`, as `kind.Moment`); `*[16]byte`; `*[]string`; `*map[string]int`; `*struct{ A int }`; `*any`; `*sql.RawBytes`; `*pgtype.Text`; `*pgtype.FlatArray[int32]`; a named pointer type `type pstr *string` given as `*pstr`; a non-pointer `string`; and nil.
  - `TestScanWrapsMirrorsPgx` (pgxmirror_test.go). For every type in both lists above, plus `***string` and `*time.Location`: if `scanWraps(t)` then `pgxIsSQLScanDestination(t)` and `!t.Implements(reflect.TypeFor[sql.Scanner]())`.
  - `TestPgxMirrorIsCurrent` (pgxmirror_test.go). It runs `go list -m -f '{{.Dir}}' github.com/jackc/pgx/v5`, reads `stdlib/sql_go1.27.go`, and cuts out the text from `func isSQLScanDestination(` to the next line that is exactly `}`. That text, with the name replaced, must equal the copy, read from pgxmirror_test.go the same way. On a mismatch it fails with "pgx's isSQLScanDestination changed: update the copy and re-check scanWrapsType". It skips under `-short`.
  - `FuzzColumnScan` (colscan_fuzz_test.go, package `velox_test`). Arguments: `(pick uint8, i int64, f float64, s string, b []byte, sec int64, which uint8)`. The parameter is not called `kind`, so the `kind` package stays in scope.
    - `pick % 7` picks `src`: nil, `i`, `f`, `i%2 == 0` as a bool, `s`, `b`, or `time.Unix(sec%1e11, 0).UTC()`.
    - `which` picks the destination type from a package-level list `fuzzDests`: the wrapped types of the list above, written with exported or test-file types, plus `*kind.Status`, `**kind.Status` and `*kind.Cents`.
    - Two fresh destinations of that type are made with `reflect.New(T.Elem())`. The test asserts that `velox.ColumnScan(d1, src)` and `sql.ConvertAssign(driver.ScanContext{}, d2, src)` agree. Either both return nil, with `reflect.DeepEqual` on the values, or both return errors with equal `Error()` text. `ColumnScan` is the wrapped path, the outer call into a `*colScanner`. The other is the plain path.
    - The seeds cover each `pick` with each of `*int8`, `*uint8`, `*string` and `**time.Time`. These include overflow (300 into `int8`), a negative into `uint8`, `"abc"` into `int64`, and NULL into `*string` (an error) and into `**time.Time` (nil).
- [ ] **Step 2: Run them to see them fail.** `go test -count=1 -run 'ScanWraps|PgxMirror' .` → FAIL, `undefined: scanWraps`.
- [ ] **Step 3: Implement** `colscan.go`, then the export_test.go helpers.
- [ ] **Step 4: Run the tests.**
  - `go test -race -count=1 -run 'ScanWraps|PgxMirror|FuzzColumnScan' .` → `ok`.
  - `perl -e 'alarm 120; exec @ARGV' go test -run XXX -fuzz '^FuzzColumnScan$' -fuzztime 10s .` → `ok`, with no new file under `testdata/fuzz`.
- [ ] **Step 5: Run C. Commit**: `git add colscan.go colscan_internal_test.go pgxmirror_test.go colscan_fuzz_test.go export_test.go .github/workflows/ci.yml scripts/ci-local.sh CONTRIBUTING.md`. Message: `velox: colScanner converts as database/sql does, and scanWraps says which destinations pgx would convert so`.

### Task 3: `colRows` on pgx stdlib, the differential tests, and the "after" numbers

**Files:**
- Modify: `colscan.go` (`colSlot`, `colRows`, `pgxStdlib`), `executor.go` (`sqlExecutor.cols`; `query` and `begin`), `db.go` (`Open`), `route.go` (`SQLConn`), `export_test.go`
- Create: `colscan_test.go` (package `velox_test`)
- Modify: `colscan_internal_test.go`
- Create: `docs/decisions/0018-pgx-stdlib-scan-wrapper.md`. Modify `docs/decisions/README.md` (one row).
- Modify: `scripts/coverage-floors.txt`, only if the measured root figure moves: the measured value minus 1.0.

**Interfaces:**
- `type colSlot struct { typ reflect.Type; wrap bool; cs colScanner }`
- `type colRows struct { *sql.Rows; slots []colSlot; args []any }`
- `func (r *colRows) Close() { _ = r.Rows.Close() }`
- `func (r *colRows) Scan(dst ...any) error`. When `len(r.slots) != len(dst)`, it makes both slices with `len(dst)`. For each column `i`:
  - `t := reflect.TypeOf(dst[i])`. If `t != slots[i].typ`, set `slots[i].typ, slots[i].wrap = t, scanWraps(t)`.
  - If the column is wrapped and `dst[i]` is not a typed nil pointer (`reflect.ValueOf(dst[i]).IsNil()`, checked only for wrapped columns): `slots[i].cs.dst = dst[i]` and `args[i] = &slots[i].cs`. Otherwise `args[i] = dst[i]`.
  - A typed nil is passed through unwrapped, so the driver rejects it exactly as today: pgx with `destination pointer is nil` (stdlib/sql_go1.27.go:48-51), database/sql with its own error. Wrapped, the inner `ConvertAssign` would dereference it in several cases (convert.go:325-330, 418-445) and panic. This beats a copied error string in `colScanner.Scan`: the text cannot drift from the driver's.

  Then it returns `r.Rows.Scan(r.args...)`. It never writes to `dst`.
- `func pgxStdlib(db *sql.DB) bool`. True when `reflect.TypeOf(db.Driver())` is a pointer to a type named `Driver` in package `github.com/jackc/pgx/v5/stdlib`.
- `sqlExecutor` gains `cols bool`.
  - `func newSQLExecutor(db *sql.DB) sqlExecutor { return sqlExecutor{c: db, db: db, cols: pgxStdlib(db)} }`. `Open` and `SQLConn` call it.
  - `begin` returns `sqlExecutor{c: tx, cols: e.cols}`.
  - `query` returns `&colRows{Rows: r}` when `e.cols`, and `sqlRows{r}` otherwise.
- `raw.go`'s `interface{ Columns() ([]string, error) }` case still matches, through the embedded `*sql.Rows`.
- In `export_test.go`:
  - `func OpenColumnScan(d Dialect, db *sql.DB, wrap bool) *DB { return newDB(d, sqlExecutor{c: db, db: db, cols: wrap}, nil) }`;
  - `func PgxStdlib(db *sql.DB) bool`;
  - `func RowsAreColumnScanning(db *DB) bool`. It runs `SELECT 1` through `db.ex.query`, reports whether the rows are `*colRows`, and closes them. It is for DBs made by `Open`; a DB with replicas has no `ex` (db.go:163-165).
  - `func ConnColumnScanning(c Conn) bool { return c.ex.(sqlExecutor).cols }`.

- [ ] **Step 1: Write the tests.**
  - `TestPgxStdlibDetected` (colscan_test.go). No connection is made.
    - These give true: `sql.Open("pgx", "postgres://u@127.0.0.1:1/x")`, `stdlib.OpenDB(*cfg)`, `sql.OpenDB(stdlib.GetConnector(*cfg))` and `stdlib.OpenDBFromPool(pool)`. `cfg` comes from `pgx.ParseConfig` of the same string. `pool` comes from `pgxpool.New` on it, which does not connect until first use; it is closed in a Cleanup.
    - `sql.Open("sqlite", ":memory:")` gives false.
    - `sql.OpenDB(wrapConnector{stdlib.GetConnector(*cfg)})` gives false. `wrapConnector` is a test type that embeds `driver.Connector`. It is itself a `driver.Driver`: `Open(string) (driver.Conn, error)` returns an error. Its `Driver()` returns the `wrapConnector`.
  - `TestSQLConnDetects` (colscan_test.go). `ConnColumnScanning(velox.SQLConn(pgxDB))` is true and `ConnColumnScanning(velox.SQLConn(sqliteDB))` is false. `pgxDB` is `sql.Open("pgx", …)`, with no connection made. This pins replicas and shards, which all come from `SQLConn` (route.go:31-36, 140, 384).
  - `TestSQLiteAndMySQLKeepPlainRows` (colscan_test.go). `RowsAreColumnScanning(velox.Open(velox.SQLite, sqliteDB))` is false. With `VELOX_TEST_MYSQL` set, the MySQL one is false too.
  - `TestBeginKeepsColumnScan` (colscan_internal_test.go). On SQLite, `sqlExecutor{c: db, db: db, cols: true}.begin(ctx, txOptions{})`, then `query(ctx, "SELECT 1", nil)`, gives a `*colRows`. With `cols: false` it gives a `sqlRows`.
  - `TestColumnScanRowsKeepCallerSlice` (colscan_test.go; Review Focus 4). On SQLite with `OpenColumnScan(SQLite, db, true)`, `RawRows` runs a two-row `SELECT 'a' UNION ALL SELECT 'b'`.
    - Row 1 is scanned into `dst := []any{&s}` (`*string`). Afterwards `dst[0]` is still `&s` and `s == "a"`.
    - Row 2 is scanned into `[]any{&st}` (`*kind.Status`), and `st == "b"`.
    - Two more queries scan into `[]any{nil}` and into `[]any{(*time.Time)(nil)}`, a typed nil of a wrapped type. Each returns an error with the same text that `OpenColumnScan(…, false)` gives, and neither panics.
    - With `VELOX_TEST_POSTGRES` set, the typed-nil case runs on PostgreSQL too: the error contains `destination pointer is nil`, as today.
  - `TestColumnScanAllocsPerQuery` (colscan_test.go).
    - Set-up: SQLite in memory, with the invoices DDL copied from each_test.go:162 and 100 invoices. The queries run under `velox.WithAllTenants(context.Background(), "test")`, because `Invoice` is tenant-scoped (internal/testmodel/schema/invoice.go:26).
    - `testing.AllocsPerRun(50, …)` of `Query[testmodel.Invoice]().Limit(1).All` and `.Limit(100).All` runs on `OpenColumnScan(SQLite, db, true)` and on `…, false)`.
    - Assert `wrapped1 − plain1 <= 3`. Assert `wrapped100 − plain100 == wrapped1 − plain1` (nothing per row) only when `!raceEnabled`. Under race, pooled counts are not exact (race_enabled_test.go), so allow ±1 there.
  - `TestColumnScanMatchesPlain` (colscan_test.go; Ruling 2, Review Focus 2).
    - Targets: SQLite always. PostgreSQL when `VELOX_TEST_POSTGRES` is set, through `sql.Open("pgx", dsn)`. When `VELOX_ITEST_REQUIRE=1` and the DSN is unset, it fails rather than skipping.
    - For each target there are two DBs on the same `*sql.DB`: `OpenColumnScan(d, db, true)` and `OpenColumnScan(d, db, false)`. The second is today's path exactly: on PostgreSQL it still goes through pgx's `ScanColumn`, with plain destinations.
    - The grid: each SQL expression × each destination type of `fuzzDests` (Task 2, the same convention), plus `*kind.Moment`, `**kind.Moment`, `*bindtype.UUID`, `*bindtype.Level`, `*sql.NullInt64` and `*any`.
      - Expressions, the same on both dialects: `127`, `128`, `255`, `256`, `-1`, `32768`, `2147483648`, `9223372036854775807`, `1.5`, `1e300`, `true`, `false`, `'abc'`, `'42'`, `''` and `NULL`.
      - Bytes: on PostgreSQL `'\x00ff'::bytea`; on SQLite `x'00ff'`. PostgreSQL reads a bare `x'00ff'` as a bit string.
      - A time: on PostgreSQL `'2026-10-29 01:02:03.456789+08'::timestamptz`; on SQLite the bare text. On PostgreSQL an untyped literal is text.
    - For each cell, `RawRows` with `SELECT <expr>` on both DBs scans into a fresh `reflect.New(T.Elem())`. The results must agree:
      - both errors nil, `reflect.DeepEqual` on the values, and for a `time.Time` also equal `Location().String()`;
      - or both errors non-nil with equal `Error()` text.
    - The test logs the count of cells compared, and fails if it is under 400 on PostgreSQL.
- [ ] **Step 2: Run them to see them fail.** `go test -count=1 -run 'PgxStdlib|SQLConnDetects|KeepPlainRows|KeepsColumnScan|ColumnScan' .` → FAIL, `undefined: velox.OpenColumnScan`.
- [ ] **Step 3: Implement.** Do not touch `scanDst`, `fieldAddr`, `readPart`, `ownerRows`, each.go or aggregate.go. They are covered through `rows.Scan`.
- [ ] **Step 4: Run the root tests on SQLite.** `go test -race -count=1 .` → `ok`. `TestOneRowAllocationBudget` and the other budget tests use `FakeExec` and do not change.
- [ ] **Step 5: Run the database tests and the "after" run.** In one Bash call, after the database checks of Global Constraints:
  ```bash
  docker compose -f docker-compose.ci.yml -p velox2-ci up -d --wait
  # export the three DSNs and VELOX_ITEST_REQUIRE=1 as scripts/ci-local.sh does
  perl -e 'alarm 3600; exec @ARGV' go test -race -count=1 . ./internal/itest
  uptime
  perl -e 'alarm 3600; exec @ARGV' go test -run '^$' -bench '^BenchmarkScan$' -benchmem -count 6 ./internal/itest > /private/tmp/claude-501/pgscan-after.txt
  uptime
  docker compose -f docker-compose.ci.yml -p velox2-ci down -v
  ```
  Expected: both test packages `ok`. These include every itest that runs on `postgres-sql`, which now reads through `colRows`: `TestOneScansEveryType`, the aggregates, json_test (codec columns), through_test (the M2M owner key via `ownerRows`), raw_test and each's counts. `TestColumnScanMatchesPlain` logs at least 400 PostgreSQL cells.
- [ ] **Step 6: benchstat.** `go run golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e before=/private/tmp/claude-501/pgscan-before.txt after=/private/tmp/claude-501/pgscan-after.txt > /private/tmp/claude-501/pgscan-benchstat.txt`. Expected:
  - `postgres-sql/Query100` and `/EagerO2M100`: sec/op lower with p < 0.05; allocs/op at most +3.
  - `sqlite/*`, `mysql/*` and `postgres-pgx/*`: allocs/op and B/op identical, and sec/op not significantly worse.
  - `postgres-sql/Items100`: not significantly worse (the per-type check of Decisions).

  If a `sqlite` or `mysql` row shows a significant slowdown, rerun before and after alternately once each. Their code path differs only by one bool test per query. Report both runs. If the slowdown persists, stop and report it to the controller (Ruling 3).
- [ ] **Step 7: Decision record.** Write `0018-pgx-stdlib-scan-wrapper.md` in 0017's shape:
  - Context: pgx's per-column check, and the numbers of Step 6.
  - Decision: wrap on pgx stdlib only, by the `scanWrapsType` rule.
  - Consequences:
    - driver wrappers are not detected;
    - up to 3 allocations per query;
    - no generated-code change;
    - `TestPgxMirrorIsCurrent` guards only the pgx that velox2's go.mod selects. An application can select a newer pgx v5 through MVS. If that pgx narrowed `isSQLScanDestination`, velox could wrap a type that pgx no longer converts with `ConvertAssign`. A pgx upgrade in velox2 re-runs the mirror test. Checking the linked pgx version at run time through `debug.ReadBuildInfo` was considered and left out: it would also refuse harmless upgrades.
  - Evidence: the test names, and the `postgres-sql` rows of Step 6's benchstat pasted in a code block with both load ranges. Step 6's file is not committed, so the record does not cite its path.

  Add its README row.
- [ ] **Step 8: Coverage,** measured as the floors file's header says: with `-race`, and with no database variable set.
  - Run `env -u VELOX_TEST_POSTGRES -u VELOX_TEST_MYSQL -u VELOX_TEST_MYSQL_ADMIN -u VELOX_ITEST_REQUIRE go test -race -count=1 -coverprofile=/private/tmp/claude-501/root.out .` in a fresh shell, with the stack down. Run it twice; the figures must agree.
  - `go run ./scripts/covfloor -floors scripts/coverage-floors.txt /private/tmp/claude-501/root.out` must pass for the root package.
  - Raise the root floor only if the measured value minus 1.0 is above it.
- [ ] **Step 9: Run C. Commit**: `git add colscan.go executor.go db.go route.go export_test.go colscan_test.go colscan_internal_test.go docs/decisions/0018-pgx-stdlib-scan-wrapper.md docs/decisions/README.md`, plus `scripts/coverage-floors.txt` if it changed. Message: `velox: on pgx's stdlib, scans go through a Scanner, which skips pgx's per-column type check`. In the body, paste the `postgres-sql` rows of pgscan-benchstat.txt and the two load ranges.

### Task 4: The upstream pgx issue, drafted and not filed

**Files:**
- Create: `docs/upstream/pgx-cache-sql-scan-destination.md`

- [ ] **Step 1: Write the draft.** It is an issue body for jackc/pgx, followed by a PR sketch. It cites only the measured numbers, from Evidence 1 and Task 3 Step 6:
  - The title: "stdlib: cache isSQLScanDestination per reflect.Type".
  - What happens: under Go 1.27, `(*Rows).ScanColumn` calls `isSQLScanDestination(rv.Type())` on every column of every row. For `*time.Time` that is about 8 µs a call on an M3 Max, because `reflect.Type.Implements` resolves every method name.
  - A minimal reproduction: 100 rows with one `timestamptz` column, scanned into `*time.Time` and into `*sql.NullTime`.
  - The proposal: a `sync.Map` from `reflect.Type` to `bool` in front of the function. The result depends only on the type, so it is safe to cache. The named-pointer cycle guard stays inside the uncached function.
  - A note that callers can work around it today by scanning into an `sql.Scanner`, as velox2 now does.

  The first line of the file says: "Draft only; not filed (docs/plans/2026-10-29-pg-scan-path.md, Ruling 1)."
- [ ] **Step 2: Commit**: `git add docs/upstream/pgx-cache-sql-scan-destination.md`. Message: `docs: a draft pgx issue to cache isSQLScanDestination, not filed`.

### Task 5: bench/compare: a runtime-only run, and the `velox2pgx` column

**Files:**
- Modify: `bench/compare/testdata/runtime/common/db_test.go` (`pgDSN`)
- Modify: `bench/compare/testdata/runtime/velox2/ops_test.go` (`newApp`)
- Modify: `bench/compare/run.sh`, `bench/compare/summary.go`, `bench/compare/report.go`, `bench/compare/orm.go` (`runtimeVariants`)
- Modify: `bench/compare/summary_test.go`, `bench/compare/report_test.go`, `bench/compare/runtime_test.go`
- Create: two fixture directories. `testdata/summary/` is shared by every summary and report test and stays unchanged.
  - `bench/compare/testdata/summary-variant/` is a copy of `testdata/summary/` plus `raw/bench-postgres-velox2pgx-1.txt` and `-2.txt`. Both rounds are there because the fixture config is `bench_count: 2`.
  - `bench/compare/testdata/summary-runtime/` is the same with no build JSONs.

**Interfaces:**
- `common/db_test.go`: `func pgDSN(tb testing.TB) string` returns `$VELOX_COMPARE_POSTGRES` plus `search_path=cmp_<ormName>`. It is factored out of `openDB`, which now uses it. The DSN still never reaches a log.
- `velox2/ops_test.go` `newApp`: when `COMPARE_VELOX2_PGX=1` it does the following.
  - It runs the DDL through `sqlDB` as now. It does not close `sqlDB`: the common `setup` and `TestGuard` keep using it for `ANALYZE`, `prevIDIndexed` and `ddlShape` (common/bench_test.go:10-49).
  - Then `pool, err := pgxpool.New(ctx, pgDSN(tb)+"&pool_max_conns=1")`, with `tb.Cleanup(pool.Close)`, and `a := &app{db: velox.OpenPgx(pool)}`.
  - Then a probe, before any timer runs (`setup` calls `newApp` before `b.ResetTimer`). It proves that the ops run on the pool and not on `sqlDB`:
    1. `sqlDB.SetMaxIdleConns(0)`. A database/sql query now opens a connection and closes it on release, which `Stats().MaxIdleClosed` counts.
    2. Take `s0 := sqlDB.Stats()` and `n0 := pool.Stat().AcquireCount()`.
    3. Run `a.count(ctx)`, which works on the empty tables.
    4. Fail with `tb.Fatalf("velox2pgx: …")` unless `pool.Stat().AcquireCount() > n0`. Fail likewise if `sqlDB.Stats()` differs from `s0` in `OpenConnections`, `InUse`, `WaitCount` or `MaxIdleClosed`.
    5. `sqlDB.SetMaxIdleConns(2)`, database/sql's default, for the harness's later calls.

    The probe adds no `*app` method, so `TestRuntimeFilesAgree`'s method set holds.
  - Return `a`.

  Under `COMPARE_DIALECT=sqlite` with the variable set, it calls `tb.Fatal("COMPARE_VELOX2_PGX needs postgres")`. The same app binary serves both columns, so no new codegen is needed.
- `orm.go`: `var runtimeVariants = map[string]string{"velox2pgx": "velox2"}` maps a variant to the ORM whose app it runs. `func runtimeORMByName(name string) (string, error)` accepts the ORM names and the variants. `summarizeRuntime` uses it in place of `ormByName`.
- `summary.go`:
  - A config key `variants`, a list like `sizes`. `missingResults` requires each listed variant's postgres rounds and benchmarks, and nothing for SQLite.
  - A variant round file present without `variants` configured is accepted and shown, but not required. A resumed or hand-made results directory may hold one.
  - A config key `steps`, `all` (the default) or `runtime`, which mirrors `COMPARE_STEPS`.
    - Under `steps=runtime`, `summarizeBuild` accepts zero build JSONs (today it fails with `no build-<orm>-n<N>-r<run>.json`, summary.go:638-640), and `missingResults` expects no build results.
    - Without it, today's behaviour stays, including "sizes from the results" when `sizes` is empty or absent (summary.go:485-487; summary_test.go:193-203).
- `beaters` stays over `orms[1:]`, so a variant is never a loss.
- `report.go`:
  - `display["velox2pgx"] = "velox2 (OpenPgx)"`.
  - A runtime table whose dialect has `velox2pgx` gains the columns `velox2 (OpenPgx)` (after velox2) and `velox2 (OpenPgx) ÷ velox2`.
  - Under `steps=runtime` (recorded in the summary's config), the build tables and the reference-build table are left out, and the report says "Runtime only: the build steps were not run."
- `run.sh`:
  - `COMPARE_STEPS` is `all` (the default) or `runtime`. With `runtime`, the build loop is skipped and logged as `skip build: COMPARE_STEPS=runtime`. summarize gets `-config steps=runtime`, and `-config sizes=` is not passed.
  - `COMPARE_PGX` defaults to `1`. With 1, the PostgreSQL rounds include `velox2pgx`, which runs `$work/rt-velox2.test` under `COMPARE_VELOX2_PGX=1`, and summarize gets `-config variants=velox2pgx`.
  - There is one list per dialect: `sqlite_orms=(velox2 oldvelox ent)`, and `pg_orms`, which is that list plus `velox2pgx` when `COMPARE_PGX=1`. `bench d` and its helpers read the list for `d`. A helper `test_bin orm` maps `velox2pgx` to `rt-velox2.test`, and `test_env orm` adds `COMPARE_VELOX2_PGX=1` for it. Every spot that names the ORMs uses these:
    - `rotated` takes the list and its length, not a fixed 3. SQLite rotates over 3 and PostgreSQL over 4;
    - `rounds_done` (run.sh:121-126), so that a resume does not skip postgres while velox2pgx rounds are missing;
    - the "skip postgres" echo loop (run.sh:156-161);
    - the guard loop in `bench()` (run.sh:132-134), which runs `guard-postgres-velox2pgx` on postgres and never on sqlite;
    - the round loop and its `todo` check in `bench()`;
    - the per-dialect concatenation (run.sh:175-177), which writes `bench-postgres-velox2pgx.txt`;
    - the benchstat call, which gets `velox2pgx=$raw/bench-postgres-velox2pgx.txt` on postgres only.
  - The runtime-app and test-binary loop stays over the three ORMs: the variant has no app of its own.
  - The header comment lists both knobs.

- [ ] **Step 1: Write the tests.**
  - `TestRuntimeORMByName`: `velox2pgx` maps to `velox2`; `nope` gives an error that names the accepted names.
  - `TestSummarizeVariant`, on a copy of `testdata/summary-variant` with `variants: velox2pgx`:
    - the summary gives `Runtime["postgres"]["velox2pgx"]["Query100"]["sec/op"]`, with 2 runs;
    - with `-2.txt` removed, summarize fails with `bench postgres velox2pgx round 2`;
    - with `variants` not configured, it succeeds and still shows the variant.
  - `TestLossesIgnoreVariant` (Review Focus 5): with velox2pgx faster than velox2 on every postgres benchmark, `losses` lists none for velox2pgx. This one passes before Step 3, because `beaters` already iterates `orms[1:]` (summary.go:806). It pins that behaviour.
  - `TestSummarizeRuntimeOnly`: `testdata/summary-runtime` (no build JSONs) with `steps=runtime` summarizes without `-partial`. Without `steps`, it fails as today, with `no build-`.
  - `TestReportRuntimeOnly`: the `testdata/summary-runtime` summary (with `variants`) gives a report with no `## Build cost` heading. The report contains `Runtime only`, and its postgres sec/op table header contains `velox2 (OpenPgx)`.
  - `TestGuardPostgres` (already present with three guard runs, skipped unless `VELOX_COMPARE_POSTGRES` is set) gets a fourth run, after velox2's: velox2 with `COMPARE_VELOX2_PGX=1`. It must pass, so `newApp`'s probe held.
    - The variant reuses the schema `cmp_velox2`, because `ormName` stays velox2. The test's comment that each ORM touches only its own schema becomes: "each ORM touches only its own schema; the velox2pgx run reuses and reseeds velox2's."
    - The final count check stays at 500, because the variant reseeds.
- [ ] **Step 2: Run them to see them fail** (`go test -short -count=1 ./...` in bench/compare → FAIL on every new test except `TestLossesIgnoreVariant`). **Step 3: Implement.** **Step 4:** `-short` → `ok`. `bash -n bench/compare/run.sh` → no output.
- [ ] **Step 5: The guards on PostgreSQL**, in one Bash call after the database checks: `up -d --wait postgres`; export `VELOX_COMPARE_POSTGRES` as run.sh does; `(cd bench/compare && perl -e 'alarm 3600; exec @ARGV' go test -race -count=1 -run '^TestGuardPostgres$' -v .)`; then `down -v`. Expected: `--- PASS: TestGuardPostgres`.
- [ ] **Step 6: A smoke run.** `COMPARE_SMOKE=1 COMPARE_STEPS=runtime perl -e 'alarm 7200; exec @ARGV' bash bench/compare/run.sh`. Expected:
  - exit 0, and no build step in steps.jsonl;
  - 7 round files on sqlite (3) and postgres (4), and `benchstat-postgres.txt` with the columns `velox2 oldvelox ent velox2pgx`;
  - `report-tables.md` with the `velox2 (OpenPgx)` column;
  - `/usr/bin/grep -a -rlF 'velox:velox@' <results>` prints nothing;
  - `docker compose -f docker-compose.ci.yml -p velox2-ci ps -q` is empty afterwards.
- [ ] **Step 7: Run C** in bench/compare and at the root (gofmt covers testdata). **Commit**: `git add` each modified and created file above by name, the fixture files included. Message: `bench/compare: COMPARE_STEPS=runtime runs the runtime only, and velox2's OpenPgx is a fourth PostgreSQL column`.

### Task 6: The runtime run, the review, the CHANGELOG, and the full gate

**Files:**
- Create: `docs/reviews/<run's UTC date, YYYY-MM-DD>-pg-scan-path.md`, called REVIEW below
- Modify: `docs/reviews/2026-10-29-perf-comparison.md`: a one-line pointer at the end of the Summary's second paragraph, and as the last sentence of the first bullet of "Where velox2 loses": "Fixed after this run; see REVIEW." No number in it changes.
- Modify: `CHANGELOG.md`

- [ ] **Step 1: The run.** Check that velox2-ci is down and no gate runs. Leave the working tree clean (`git status --porcelain --untracked-files=no` prints nothing): every app builds velox2 from it. Run `COMPARE_STEPS=runtime bash bench/compare/run.sh` in the background with no outer alarm, and note the results path it prints first. Run nothing heavy beside it. If it is interrupted, resume with `COMPARE_RESULTS=<path>`. Expected: exit 0.
- [ ] **Step 2: Check the run.**
  - Every `exit` in steps.jsonl is 0, and no gate timed out (or the review says which did).
  - There are 6 samples of each of the 7 benchmarks per ORM per dialect: 3 ORMs on sqlite, and 3 ORMs plus velox2pgx on postgres.
- [ ] **Step 3: Write REVIEW** from `summary.json`, `report-tables.md`, the two benchstat files and `/private/tmp/claude-501/pgscan-benchstat.txt` only. Its sections:
  1. **Summary.** Three to five sentences:
     - Query100 and EagerO2M100 on PostgreSQL before (cite the old review's 961 µs and 4.32 ms as that run's figures) and now, with the ratios to old velox and ent;
     - whether SQLite moved;
     - where `OpenPgx` stands.
  2. **What changed.** The cause, in three sentences: per column per row, `time.Time`'s method set. Then the fix in two sentences, naming `colRows`, the decision record 0018 and the commit.
  3. **Machine and load.** The machine, the commit, the steps table's load range, and the gates.
  4. **Method.** It is the old review's runtime method, with these changes: the build steps were not run; `velox2pgx` is velox2's runtime app on `OpenPgx` with `pool_max_conns=1`, rotated with the other three; the root benchmark `BenchmarkScan`.
  5. **Runtime.**
     - The sec/op, B/op and allocs/op tables of both dialects, with p-values where benchstat calls a difference significant.
     - The root benchmark's before/after benchstat for `postgres-sql`, `postgres-pgx`, `sqlite` and `mysql`, as a code block.
  6. **Where velox2 still loses.** The generated list, with a cause where the data shows one and none where it does not.
  7. **Limits.** The shared machine and its load, one machine, MySQL only in the root benchmark, and one connection.
- [ ] **Step 4: CHANGELOG.**
  - Under `### Changed`, add a Runtime line: "On database/sql with pgx's stdlib, velox scans through an `sql.Scanner`, so pgx's per-column type check no longer runs for each column of each row. Query100 on PostgreSQL went from X to Y and EagerO2M100 from X to Y (REVIEW; the commits of Tasks 2 and 3)." X and Y come from REVIEW.
  - Extend the bench/compare line under `### Added`: "`COMPARE_STEPS=runtime`, and an `OpenPgx` column (Task 5's commit)."
- [ ] **Step 5: The full gate,** because ci.yml, ci-local.sh and the root changed: `perl -e 'alarm 10800; exec @ARGV' bash scripts/ci-local.sh`. Expected: exit 0, including `FuzzColumnScan`, the coverage floors, `velox-gen -check`, and a generated-code diff that stays clean. Then `git diff --exit-code -- internal/testmodel internal/bindmodel example` → no output.
- [ ] **Step 6: Commit**: `git add docs/reviews/<REVIEW file> docs/reviews/2026-10-29-perf-comparison.md CHANGELOG.md`. Message: `Review: the PostgreSQL scan path after the fix, with OpenPgx`. Name the results path in the body.

## Left by this plan

- **Filing upstream.** The draft in `docs/upstream/` is for the controller or the user to file, or not (Ruling 1). Once pgx caches the check, `colRows` still costs at most 3 allocations per query. Removing it would then be a measured follow-up, not an automatic one.
- **Wrapped drivers.** A pgx stdlib driver behind another package's wrapper (otelsql, sqlhooks) is not detected, and keeps the slow path. A general probe (the rows of a first query) was judged not worth a round trip. contrib/veloxotel wraps velox's hooks, not the driver, so it is not affected.
- **Other drivers with `ScanColumn`.** Should lib/pq or a future MySQL or SQLite driver implement `RowsColumnScanner`, it gets nothing from this fix until it is measured and added to the detection.
- **migrate's own scans** (`migrate/inspect_*.go`, history.go) run once per command, not per request. They are out of scope.
- **From the old review, not touched here:** EagerO2M100's extra bytes against old velox on both dialects; GetByID's small sec/op gap; `runtime.GC()` before each benchmark timer; subtracting the `-toolexec` wrapper's cost; a re-run on Linux CI hardware.
