# Performance comparison: velox2, old velox, ent — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Measure velox2 against old velox and ent: build cost (codegen, generated size, cold build, schema-edit rebuild, binary, link memory) and runtime cost (seven operations on SQLite and PostgreSQL). Publish every number, including where velox2 loses, in `docs/reviews/2026-10-29-perf-comparison.md`.

**Architecture:**
- `bench/compare` is a module of its own. It holds a Go harness (`compare`, with subcommands) and `run.sh`, which runs every measurement in order.
- One schema model is written out in all three DSLs. Each app is a temporary module, and every app uses the harness's own `go.mod` requirements, so all three use one version set and the same drivers.
- The build measurement reuses bench/scale's `-toolexec` wrapper and reference build. Task 1 moves them into `internal/buildcost`.
- The runtime benchmarks are one shared benchmark file plus one `ops_test.go` per ORM. These files live under `testdata/` and are copied into a generated n=10 app.
- The raw results stay git-ignored. A JSON summary feeds a `report` subcommand, which prints the tables.

**Tech Stack:** Go 1.27. velox2 at HEAD (`replace => ../..`). `github.com/syssam/velox v0.3.1-0.20261028051754-a7d44fb423b0`. `entgo.io/ent v0.14.6`. `modernc.org/sqlite v1.60.1` and `github.com/jackc/pgx/v5 v5.11.0` (stdlib) for all three. `golang.org/x/mod/modfile`. benchstat `golang.org/x/perf/cmd/benchstat@v0.0.0-20261009192801-be2c69fb417e`, pinned as in `scripts/bench-compare.sh`. PostgreSQL 18 from `docker-compose.ci.yml`.

**Spec:** There is no spec file. The controller's rulings are binding and are copied verbatim into **Rulings** below. "Ruling N" below means item N of that list.

## Rulings

1. **Location.** The harness lives in `bench/compare/`, a module of its own (`github.com/syssam/velox2/bench/compare`), so that ent and old velox never enter velox2's go.mod. It requires velox2 through `replace github.com/syssam/velox2 => ../..` (a relative path) and the other two at the versions above. No committed file has a machine-specific path. The module joins vet, lint and `go test -short` of its own unit tests. The measurements do NOT run in CI or in ci-local.sh.
2. **Schemas.** One generator writes the same logical schema in all three DSLs: n entities, each with the same fields (string, int, bool, time, optional/nillable string, an enum) and the same edges (each entity i>0 has an O2M from entity i-1, plus every 10th entity an M2M to another). Sizes: n = 10 for the runtime benchmarks, and 100 and 300 for the build metrics. Everything the generated code needs to compile is generated too. GraphQL and every optional feature or extension are off for all three (core ORM only; the report says so). Old velox runs with its codegen defaults, and the plan records which flags it passes.
3. **Build metrics, per ORM and size.**
   - Codegen time (wall + CPU).
   - Generated Go lines and bytes.
   - Cold build of the generated package plus s=20 services that each use 5 entities, with an empty GOCACHE (CPU + wall).
   - After adding one field to one schema and regenerating: the rebuild's CPU + wall, and how many compile actions were cached and how many rerun. The count comes from `go build -x` or `-debug-actiongraph`; pick one, measure it, and say why.
   - One service binary's size.
   - Link peak memory.
   - The same reference build as bench/scale, to normalise CPU time across load.
4. **Runtime metrics.**
   - Go benchmarks (`-benchmem`), on SQLite and PostgreSQL, with the n=10 schema. Each ORM does the same work: insert one row; bulk insert 100; get by id; query 100 rows with a filter, an order and a limit, scanned into entities; eager-load one O2M edge for 100 parents; update one row by id; count.
   - All three use the SAME SQLite driver and the SAME PostgreSQL driver (database/sql with pgx stdlib). If one ORM cannot use it, the report says so and that ORM uses the nearest one.
   - PostgreSQL comes from `docker-compose.ci.yml` (project `velox2-ci`, `up -d --wait` … `down -v`). The DSN is read the way ci-local.sh reads it and is never printed.
   - `-count=6`, compared with benchstat.
5. **Runs.**
   - One script, `bench/compare/run.sh` (bash, `set -euo pipefail`), runs everything in order, one heavy step at a time.
   - It records `uptime` before and after each step.
   - Before each build measurement, it waits (bounded, up to 60 min) for the 1-minute load to drop below 12.
   - It takes 3 runs of each build metric and reports the median and the worst.
   - It writes the raw output under a git-ignored results directory, plus a machine-readable JSON summary.
6. **Report.** `docs/reviews/2026-10-29-perf-comparison.md` gives:
   - the machine, the Go version, the load during each step, and the versions/commits of the three;
   - the method;
   - tables per metric with all three side by side, and ratios to velox2;
   - a plain "where velox2 loses" section.
   Its numbers come only from the run: the last task runs it and writes the report from the JSON. CPU-time ratios are the headline for build cost; wall time is shown too.
7. **Correctness guard.** Before timing, each ORM's runtime benchmark code runs once as a test. The test checks that the work was done (row counts, loaded edges), so that no ORM wins by doing less.

## Decisions made while planning (from the spikes)

These came from runs in a scratch directory on 2026-10-29, each with a 3-entity schema in the Ruling 2 shape (velox2 at e44a3b3):

- **Codegen commands.** Each ORM runs its official CLI, built once from the harness module:
  - velox2: `velox-gen -out gen ./schema`. The tables come from `velox-gen migrate sql -dialect {sqlite|postgres} ./schema`.
  - old velox: `velox generate --target ./velox ./schema`, with no `--feature` and no `.velox.yml`. Every `gen.Feature` has `Default: false` (compiler/gen/feature.go), so this is its default feature set. Old velox's generated enums still carry `MarshalGQL`/`UnmarshalGQL` with GraphQL off; the report says so.
  - ent: `ent generate ./ent/schema`, with no features and no extensions.
- **Generated size** at 3 entities: velox2 536 Go lines, old velox 14,021, ent 12,596.
- **x/tools.** ent v0.14.6 alone selects `golang.org/x/tools v0.30.0`, and its codegen then fails on Go 1.27 with `internal error: package "context" without types was imported from "entgo.io/ent"`. Old velox requires v0.52.0, so the harness's single version set resolves to v0.52.0, and ent's codegen works with that. This is one reason every app uses the harness's requirement list.
- **SQLite.** All three run on `modernc.org/sqlite`, driver name `"sqlite"`, DSN `<file>?_pragma=foreign_keys(1)`:
  - velox2: `velox.Open(velox.SQLite, db)`;
  - ent: `ent.NewClient(ent.Driver(entsql.OpenDB(dialect.SQLite, db)))`;
  - old velox: `velox.NewClient(velox.Driver(vsql.OpenDB(dialect.SQLite, db)))`.
  The ops test passed for each. No ORM needs mattn/go-sqlite3.
- **PostgreSQL.** All three take a `*sql.DB` opened with `sql.Open("pgx", dsn)`:
  - velox2: `velox.Open(velox.Postgres, db)`;
  - ent: `entsql.OpenDB(dialect.Postgres, db)`;
  - old velox: `vsql.OpenDB(dialect.Postgres, db)`. Old velox's error classification handles `*pgconn.PgError` (dialect/sql/sqlgraph/errors.go:19).
  This was not run during planning, because no database was started. Task 5's guard test runs it first. Splitting velox2's DDL on `";\n"` was checked on its SQLite output only.
- **Cached vs rerun: `-debug-actiongraph`.** It writes JSON with one object per action (`Mode`, `Package`, `Cmd`, `TimeStart`, `TimeDone`). An action with `Mode == "build"` and a non-empty `Cmd` ran the compiler; one with an empty `Cmd` was served from the cache. Measured: a first build had 217 `build` actions with `Cmd` set; an unchanged rebuild had 217 with none. `-x` would mean parsing shell lines, so the structured file wins.
- **velox2's native pgx path (`velox.OpenPgx`) is not measured.** Ruling 4 fixes database/sql. The report says that this path exists.

## Global Constraints

- Go 1.27. Before any go command: `export GOCACHE=/private/tmp/claude-501/velox2-gocache GOFLAGS=-p=2`. The cold-build measurement uses its own fresh, temporary GOCACHE; the harness sets it.
- The machine is shared, heavily loaded and swap-bound (load ~29 during planning).
  - Run one heavy command at a time: never two `go test` runs, two lints, or a lint beside a test.
  - Never run two DB stacks. Never stop containers that this work did not start. Do not kill other processes.
- `timeout` is missing. Use `perl -e 'alarm N; exec @ARGV' <cmd>`. Use `git grep -P` and `/usr/bin/grep -a`.
- Never print DSNs or passwords: no `set -x`, no echo of a DSN, and no DSN in argv, logs or results. The DSN travels only in the environment variable `VELOX_COMPARE_POSTGRES`.
- No committed file holds a machine-specific path. The harness finds its own directory with `runtime.Caller`, as bench/scale does.
- The per-task check (C). Every task ends green under it:
  - `go vet ./...` and `go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...` in each module the task touches (root, `bench/compare`);
  - `go test -race -short -count=1 ./...` in `bench/compare`, once it exists;
  - for root changes, `go test -race -count=1` of the touched root packages, with the other root tests unaffected (Task 7 runs the full gate);
  - `test -z "$(gofmt -l .)"` at the root, which also covers `bench/compare/testdata`.
- The harness's long tests are skipped under `-short`. A task runs them where it names them, one at a time.
- Commits:
  - The message is `Area: what changed`. It ends with a blank line, then `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
  - Stage by name, never with `-A` or `.`, and never stage `.DS_Store`. Never push.
- Test names: before adding one, `git grep -nP 'func <Name>\(' -- '*.go'` must print nothing.

## Review Focus

1. **A cold build that is not cold.** If a codegen loader or a reused directory wrote into the cold GOCACHE, the cold numbers would be low for that ORM. Expected: `measureBuild` fails when any `build` action of the cold build was served from the cache. The test is in Task 4.
2. **A schema edit that changes nothing.** If an ORM's generator did not pick up the new field, its rebuild would be all-cached and would look free. Expected: `measureBuild` fails unless at least one action of the generated package reran after the edit. The test is in Task 4.
3. **A DSN leak.** The PostgreSQL DSN carries a password. Expected: it never appears in the steps log, the raw results, the summary or the report. The test is in Task 2 (the step log) and Task 6 (the summary).
4. **PostgreSQL state shared across ORMs.** All three name their tables `e0s`…; in one database they would collide or read each other's rows. Expected: each ORM runs in its own schema `cmp_<orm>`, recreated per benchmark. The test is in Task 5 (the guard runs two ORMs' setups back to back).
5. **A load gate that timed out silently.** At load ~29 the gate can expire. Expected: the step log records `timed_out: true` and the waited seconds, and the report shows it beside that step's numbers. The tests are in Task 2 and Task 6.

---

### Task 1: Move bench/scale's measurement tools into `internal/buildcost`

**Files:**
- Create: `internal/buildcost/buildcost.go`, `internal/buildcost/buildcost_test.go`
- Modify: `bench/scale/main.go` (delete `cpuSeconds`, `referenceSrc`, `writeReference`, `buildReference`, `buildWrapper`, `peakMB`; call buildcost)
- Modify: `scripts/coverage-floors.txt` (a line for `internal/buildcost`; re-measure `bench/scale`)

**Interfaces:**
- Produces, in package `buildcost` (`github.com/syssam/velox2/internal/buildcost`). The bodies are moved from bench/scale/main.go unchanged:
  - `const LinkMemEnv = "VELOX_LINKMEM"`
  - `func BuildWrapper(path string) error`. It builds the `-toolexec` program, which appends each link's peak RSS in bytes to `$VELOX_LINKMEM`.
  - `func WriteReference(dir string) error`
  - `func BuildReference(dir, cache, wrapper string) (cpu, wall float64, err error)`
  - `func CPUSeconds(cmd *exec.Cmd) float64`
  - `func PeakMB(file string) (float64, error)`
- The package doc says that bench/scale and bench/compare share the package. bench/compare can import it, because its import path is under `github.com/syssam/velox2`.

- [ ] **Step 1: Write the tests** in `internal/buildcost/buildcost_test.go`:
  - `TestPeakMB`: a file with lines `1048576\n3145728\nx\n` gives `3`; a missing file returns an error that contains `no linker measurements`.
  - `TestReferenceBuild` (skipped under `-short`): `BuildWrapper` into `t.TempDir()`, then `WriteReference`, then `BuildReference` with a fresh cache dir. Assert `cpu > 0 && wall > 0`, and that the file at `LinkMemEnv` (`<ref dir>/linkmem.txt`) gives `PeakMB > 0`.
- [ ] **Step 2: Run them to see them fail.** `go test -race -count=1 ./internal/buildcost` → FAIL, `undefined: PeakMB`.
- [ ] **Step 3: Move the six declarations** into buildcost.go (exported as above). Replace bench/scale's calls with them, and replace bench/scale's literal `"VELOX_LINKMEM="` with `buildcost.LinkMemEnv+"="`.
- [ ] **Step 4: Run the tests.** Run them one at a time:
  - `go test -race -count=1 ./internal/buildcost` → `ok`.
  - `perl -e 'alarm 3600; exec @ARGV' go test -race -count=1 -coverprofile=/private/tmp/claude-501/bc.out ./internal/buildcost ./bench/scale` → `ok` for both. bench/scale's tests are unchanged, and they now exercise buildcost through it.
- [ ] **Step 5: Coverage floors.** `go tool cover -func=/private/tmp/claude-501/bc.out | tail -1` and the per-package lines give the two figures. Add `github.com/syssam/velox2/internal/buildcost <measured − 1.0>` in sorted order. Set `bench/scale`'s floor to its new measured value − 1.0, because code left the package.
- [ ] **Step 6: Run C** for the root (vet, lint, gofmt). Expected: clean.
- [ ] **Step 7: Commit.** `git add internal/buildcost bench/scale/main.go scripts/coverage-floors.txt`, message `bench: the toolexec wrapper and the reference build move to internal/buildcost`.

### Task 2: The `bench/compare` module, its `step` command, and CI wiring

**Files:**
- Create: `bench/compare/go.mod`, `bench/compare/go.sum`, `bench/compare/main.go`, `bench/compare/step.go`, `bench/compare/step_test.go`
- Modify: `.gitignore` (add `/bench/compare/results/`)
- Modify: `.github/workflows/ci.yml` and `scripts/ci-local.sh`. Add a bench/compare block after contrib/graphql's, before the coverage floors.
- Modify: `CONTRIBUTING.md` (Layout: "five Go modules", and a `bench/compare` bullet; Benchmarks: one paragraph), `CHANGELOG.md` (one line)

**Interfaces:**
- `go.mod`:
  - `module github.com/syssam/velox2/bench/compare`, `go 1.27.0`;
  - `replace github.com/syssam/velox2 => ../..`;
  - `tool` directives for `github.com/syssam/velox2/cmd/velox-gen`, `entgo.io/ent/cmd/ent` and `github.com/syssam/velox/cmd/velox`. These keep every codegen dependency in this module's `go.mod`/`go.sum`, so that each app can copy them;
  - direct requirements: velox2 `v0.0.0`, `github.com/syssam/velox v0.3.1-0.20261028051754-a7d44fb423b0`, `entgo.io/ent v0.14.6`, `modernc.org/sqlite v1.60.1`, `github.com/jackc/pgx/v5 v5.11.0`, and `golang.org/x/mod`.
  - Create it with `go mod init`, add the replace, `go get` each pinned version and `go get -tool` each tool, then `go mod tidy`. `main.go` blank-imports `modernc.org/sqlite` and `github.com/jackc/pgx/v5/stdlib`, with the comment "the apps' drivers: required here, so that every app resolves the same version from this go.sum".
- `main.go`: `func main()` dispatches `os.Args[1]` to `step`, `build`, `runtime-app`, `summarize` and `report`. Each subcommand has its own `flag.FlagSet`. An unknown subcommand prints usage and exits 2.
- `step.go` produces:
  - `type load struct{ One, Five, Fifteen float64 }`, with JSON keys `"1m"`, `"5m"`, `"15m"`.
  - `func parseUptime(out string) (load, error)`. It accepts macOS `load averages: 29.60 26.68 25.90` and Linux `load average: 1.00, 2.00, 3.00`.
  - `type gate struct{ Max float64; Wait, Poll time.Duration }`
  - `type gateResult struct{ Max float64 \`json:"max"\`; WaitedS float64 \`json:"waited_s"\`; TimedOut bool \`json:"timed_out"\` }`
  - `func waitLoad(ctx context.Context, g gate, read func() (load, error)) (gateResult, error)`. It returns once `One < Max`, or once `Wait` has passed (`TimedOut`).
  - `type stepRecord struct`, with JSON keys `name`, `argv`, `start` (RFC 3339 UTC), `wall_s`, `exit`, `load_before`, `load_after`, `uptime_before`, `uptime_after` (the raw lines), and `gate` (omitted when there is no gate).
  - `func runStep(ctx context.Context, name string, g *gate, timeout time.Duration, logPath string, argv []string) (stepRecord, error)`. It appends one JSON line to `logPath`. It passes stdout and stderr through. It never records the environment. A timeout kills the command and records exit `-1`.
  - CLI: `compare step -name N -log FILE [-gate 12] [-gate-wait 60m] [-timeout 120m] -- argv…`. It exits with the command's exit code.

- [ ] **Step 1: Write the tests** in `step_test.go`:
  - `TestParseUptime`. The macOS line above gives `{29.60, 26.68, 25.90}`. The Linux line gives `{1, 2, 3}`. `"up 3 days"` returns an error.
  - `TestWaitLoadTimesOut` (Review Focus 5). `read` always returns `One: 20`, with `gate{Max: 12, Wait: 30ms, Poll: 5ms}`. Assert `TimedOut == true` and `WaitedS >= 0.03`.
  - `TestWaitLoadPasses`. `read` returns 20, then 5. Assert `TimedOut == false`.
  - `TestStepLogsNoSecret` (Review Focus 3). `t.Setenv("VELOX_COMPARE_POSTGRES", "postgres://u:hunter2@h/db")`, then `runStep` with `argv = ["sh", "-c", "exit 3"]`. Assert `rec.Exit == 3`, that the log has one line which parses as a `stepRecord`, and that `!bytes.Contains(log, []byte("hunter2"))`.
  - `TestStepTimeout`. `argv = ["sleep", "5"]` with timeout 50ms. Assert `rec.Exit == -1` within 2s.
- [ ] **Step 2: Run them to see them fail.** `cd bench/compare && go test -race -short -count=1 .` → FAIL, undefined names.
- [ ] **Step 3: Implement** go.mod/go.sum (as above), main.go and step.go. `runStep` reads the load with `exec.Command("uptime")` and `parseUptime`.
- [ ] **Step 4: Run them to see them pass.** Same command → `ok`.
- [ ] **Step 5: CI.** In ci.yml, add a step `bench/compare (the harness's checks; the measurements run only from run.sh)` with `working-directory: bench/compare` that runs `go vet ./...`, the golangci-lint line, and `go test -race -short -count=1 ./...`. ci-local.sh gets the same three lines, in a `( cd bench/compare … )` subshell after contrib/graphql's. The root's `gofmt -l .` already covers the module. No coverage profile is added: the module is not in covfloor's profiles, so it needs no floor.
- [ ] **Step 6: Docs.**
  - CONTRIBUTING Layout: change "There are four Go modules" to five, and add the bullet: "`bench/compare`: the harness that compares velox2's build and runtime cost with old velox and ent. CI checks it but does not run it."
  - CONTRIBUTING Benchmarks: add "`bash bench/compare/run.sh` measures velox2, old velox and ent. It takes hours, starts PostgreSQL, and writes `bench/compare/results/`. The last report is `docs/reviews/2026-10-29-perf-comparison.md`."
  - CHANGELOG, under Added › Documentation and tests: "`bench/compare` measures velox2's build and runtime cost against old velox and ent (`docs/reviews/2026-10-29-perf-comparison.md`)."
  - Add the commit ids in Task 8, once they exist.
- [ ] **Step 7: Run C** in the root and in bench/compare. Expected: clean.
- [ ] **Step 8: Commit.** Stage the files above by name. Message: `bench/compare: a module for the comparison harness, its step runner, and its CI checks`.

### Task 3: The schema model, the three DSLs, and the app writer

**Files:**
- Create: `bench/compare/model.go`, `bench/compare/orm.go`, `bench/compare/app.go`, `bench/compare/model_test.go`, `bench/compare/app_test.go`

**Interfaces:**
- `model.go`:
  - `type edgeSpec struct{ Name string; Target int; Inverse bool; Ref string; Unique, Required bool; Field string }`
  - `func edgesOf(i, n int) []edgeSpec`. The rules:
    - for i>0, `From("prev", E<i-1>).Ref("next").Unique().Required().Field("prev_id")`;
    - for i<n-1, `To("next", E<i+1>)`;
    - for i%10==0, `To("links", E<i+5>)`;
    - for i%10==5, `From("linked", E<i-5>).Ref("links")`.
  - `func fieldLines(i int, fk string) []string`, where `fk` is `"Int64"` for velox2 and `"Int"` for the others. It returns, in order: `field.String("name")`, `field.Int("count")`, `field.Bool("active")`, `field.Time("created_at")`, `field.String("note").Optional().Nillable()`, `field.Enum("status").Values("draft", "live", "gone")`, and for i>0 `field.<fk>("prev_id")`.
  - `const editMarker = \`field.Enum("status").Values("draft", "live", "gone"),\``
  - `func editSchema(appDir string, o orm, n int) error`. It inserts `field.Int64("extra"),` after the marker in E<n-1>'s file, and fails if the marker is missing.
  - `func serviceEntities(s, n int) []int`. It returns `10*((s*7+m)%(n/10)) + m` for m in 0..4, as bench/scale does.
- `orm.go`:
  - `type orm struct{ Name, GenDir, SchemaDir string; dsl dsl; Codegen func(tools string) []string; Service func(entities []int) string }`
  - `type dsl struct{ Imports, SchemaType, FieldType, EdgeType, FK string }`. The three DSLs differ only in these; the builder calls are the same. For example, velox2 uses `schema.Schema`/`schema.Field`/`schema.Edge`/`Int64`, ent `ent.Schema`/`ent.Field`/`ent.Edge`/`Int`, old velox `velox.Schema`/`velox.Field`/`velox.Edge`/`Int`.
  - `func (o orm) Schema(n int) map[string]string`. It writes one file per entity, `<SchemaDir>/e<i>.go`.
  - `var orms = []orm{velox2ORM, oldVeloxORM, entORM}` and `func ormByName(string) (orm, error)`.
  - The table, from the spikes:

    | Name | SchemaDir | GenDir | Codegen argv (in the app dir) |
    |---|---|---|---|
    | `velox2` | `schema` | `gen` | `<tools>/velox-gen -out gen ./schema` |
    | `oldvelox` | `schema` | `velox` | `<tools>/velox generate --target ./velox ./schema` |
    | `ent` | `ent/schema` | `ent` | `<tools>/ent generate ./ent/schema` |

  - Each service's `main.go` makes these two calls for each of its five entities, against a client that is never opened. The code must compile and link; it is never run.

    | ORM | Query (`k` is the entity) | Count |
    |---|---|---|
    | velox2 (`db := velox.Open(velox.Postgres, nil)`) | `db.Query[gen.E<k>]().Where(gen.E<k>s.ID.EQ(1)).Limit(1).All(ctx)` | `db.Query[gen.E<k>]().Count(ctx)` |
    | ent (`client := ent.NewClient()`) | `client.E<k>.Query().Where(e<k>.ID(1)).Limit(1).All(ctx)` | `…Query().Count(ctx)` |
    | old velox (`client := velox.NewClient()`) | `client.E<k>.Query().Where(e<k>.IDField.EQ(1)).Limit(1).All(ctx)` | `…Query().Count(ctx)` |

- `app.go`:
  - `func harnessDir() (string, error)`, from `runtime.Caller`.
  - `func appGoMod(harnessMod []byte, root string) ([]byte, error)`. It uses `modfile`: module `example.com/tmp`, every `tool` directive dropped, every require kept with its version, and the velox2 replace rewritten to the absolute `root`. Nothing is tidied, so all three apps share one version set.
  - `func writeApp(dir string, files map[string]string) error`. It writes `appGoMod(...)`, the harness's `go.sum` verbatim, and `files`.
  - `func goEnv(extra ...string) []string`. It returns `os.Environ()` plus `GOFLAGS=<$GOFLAGS> -mod=mod`, `GOPROXY=off` and `GOWORK=off` (as `tempmod.GoFlags`), then `extra`.
  - `func countGenerated(dir, skip string) (lines, bytes int, err error)`. It counts the `*.go` files under `dir`, leaving out the subdirectory `skip` (ent's `ent/schema`; empty for the others).
  - `func codegen(app string, o orm, tools string) (cpu, wall float64, err error)`. It runs `o.Codegen` with `goEnv()` and the ordinary GOCACHE, and takes the CPU time from `buildcost.CPUSeconds`.

- [ ] **Step 1: Write the tests.**
  - `TestEdgesOf`: n=20. `edgesOf(0)` has `next` and `links`→5. `edgesOf(5)` has `prev`←4, `next`→6 and `linked`←0. `edgesOf(19)` has only `prev`.
  - `TestSchemasParse`: for each ORM, n=20. Every file of `o.Schema(20)` parses with `go/parser`. The count of `edge.From(` equals the count of `edge.To(` (19 O2M + 2 M2M = 21 each). E19's file contains `editMarker`.
  - `TestServicesParse`: `serviceEntities(3, 100)` is `[10 21 32 43 54]`. For each ORM, `o.Service` of that list parses with `go/parser` and names `E10`, `E21`, `E32`, `E43` and `E54`.
  - `TestAppGoMod`: the harness's real go.mod, with root `/r`. The output has `module example.com/tmp`, `entgo.io/ent v0.14.6`, `github.com/syssam/velox v0.3.1-0.20261028051754-a7d44fb423b0`, `golang.org/x/tools v0.52.0` and `=> /r`; no `tool` and no `../..`.
  - `TestEditSchema`: the edit adds `field.Int64("extra"),` once, and a second edit of a file that has lost its marker fails.
  - `TestGenerateCompiles` (skipped under `-short`): for each ORM, n=20. Build the tool into a temp dir with `go build -o` from the harness module, then `writeApp`, `codegen` and `go vet ./...` in the app. Expected: no error. `countGenerated` gives lines > 0, and velox2's count is less than ent's.
- [ ] **Step 2: Run them to see them fail.** `go test -race -short -count=1 .` → FAIL.
- [ ] **Step 3: Implement** model.go, orm.go and app.go.
- [ ] **Step 4: Run the tests.** First `go test -race -short -count=1 .` → `ok`. Then, alone: `perl -e 'alarm 3600; exec @ARGV' go test -race -count=1 -run '^TestGenerateCompiles$' .` → `ok`.
- [ ] **Step 5: Run C. Commit** the five files: `bench/compare: one schema model in velox2's, old velox's and ent's DSLs, and the app writer`.

### Task 4: The build measurement (`compare build`)

**Files:**
- Create: `bench/compare/build.go`, `bench/compare/build_test.go`, and `bench/compare/testdata/actiongraph/{cold,edit}.json`, two small hand-written fixtures of four actions each.

**Interfaces:**
- Consumes: Task 1's `buildcost.*`, and Task 3's `orm`, `writeApp`, `codegen`, `editSchema`, `countGenerated` and `serviceEntities`.
- `type actionCounts struct{ Build, BuildRerun, Link, LinkRerun int; AppS, DepS float64 }`
- `func countActions(path, appPrefix string) (actionCounts, error)`. It counts the actions with `Mode == "build"`, and the reruns among them (`len(Cmd) > 0`), and does the same for `Mode == "link"`. `AppS` and `DepS` sum `TimeDone − TimeStart` over the build actions whose `Package` starts with `appPrefix` (`example.com/tmp`) and over the others.
- `type buildConfig struct{ ORM orm; N, Services, Run int; Tools, Raw string }`. `Raw` is the directory for the action graphs.
- `type buildReport struct`, with these JSON keys: `orm`, `n`, `services`, `run`, `codegen_wall_s`, `codegen_cpu_s`, `codegen_x`, `gen_lines`, `gen_bytes`, `cold_wall_s`, `cold_cpu_s`, `cold_x`, `cold_app_action_s`, `cold_dep_action_s`, `regen_wall_s`, `regen_cpu_s`, `rebuild_wall_s`, `rebuild_cpu_s`, `edit_x`, `rebuild_rerun`, `rebuild_cached`, `rebuild_relinks`, `service_binary_mb`, `link_peak_mb`, `ref_cpu_before_s`, `ref_cpu_after_s`, `ref_cpu_s`.
  - `*_x` is the CPU time over `ref_cpu_s`, the mean of the two reference builds.
  - `edit_x` = (regen + rebuild CPU) / ref.
- `func measureBuild(c buildConfig) (buildReport, error)`. It does, in order, in a fresh `os.MkdirTemp` that it removes afterwards:
  1. `writeApp` with the schemas;
  2. the reference build, before;
  3. `codegen`, timed;
  4. `countGenerated`;
  5. the services `s0…s<S-1>/main.go`;
  6. the cold build: `go build -toolexec <wrapper> -debug-actiongraph=<Raw>/ag-<orm>-n<N>-r<run>-cold.json -o bin/ ./...`, with `GOCACHE=<tmp>/cache` (fresh) and `buildcost.LinkMemEnv` → `<tmp>/link-cold.txt`;
  7. `editSchema`, then `codegen` (the regen, timed), then the same build with `…-edit.json` (the rebuild, timed);
  8. the reference build, after;
  9. the size of `bin/s0` and `PeakMB(link-cold.txt)`.
  It returns an error when the cold build's `BuildRerun != Build` (Review Focus 1). It also returns an error when no rebuild action under `example.com/tmp/<GenDir>` reran (Review Focus 2).
- CLI: `compare build -orm NAME -n N -s 20 -run K -tools DIR -raw DIR -out FILE.json`.

- [ ] **Step 1: Write the tests.**
  - `TestCountActions`, on the fixtures. cold.json: 3 build actions, all with Cmd (2 under `example.com/tmp`), and 1 link with Cmd. edit.json: the same IDs, 1 build rerun. Assert `{Build:3, BuildRerun:3, Link:1, LinkRerun:1}` and `{Build:3, BuildRerun:1, …}`, and that `AppS` equals the fixture's summed durations.
  - `TestColdMustBeCold` (Review Focus 1, skipped under `-short`). Run `measureBuild` for velox2 at n=10, s=2, but with a test hook `coldCache` (a package variable, default empty) pointed at a GOCACHE pre-warmed by one build. Expected: an error that contains `cold build reused`.
  - `TestEditMustRebuild` (Review Focus 2, skipped under `-short`). Run with a hook `editLine` that sets the inserted line to a comment, `// extra`, which changes no generated file. Expected: an error that contains `no action of the generated package reran`.
  - `TestMeasureBuildSmall` (skipped under `-short`). For each ORM in turn, n=10, s=2: every field of the report is > 0, except that `rebuild_cached` may be 0. Also assert `rebuild_relinks == 2`, and `cold_x == cold_cpu_s / ref_cpu_s`.
- [ ] **Step 2: Run them to see them fail.** `-short` → FAIL, undefined.
- [ ] **Step 3: Implement** build.go and the `build` subcommand.
- [ ] **Step 4: Run the tests.** First `-short` → `ok`. Then, alone: `perl -e 'alarm 5400; exec @ARGV' go test -race -count=1 -run '^(TestColdMustBeCold|TestEditMustRebuild|TestMeasureBuildSmall)$' .` → `ok`.
- [ ] **Step 5: Run C. Commit**: `bench/compare: the build measurement, with cached and rerun actions from -debug-actiongraph`.

### Task 5: The runtime apps, the guard, and the benchmarks

**Files:**
- Create: `bench/compare/runtime.go` and `bench/compare/runtime_test.go`.
- Create: `bench/compare/testdata/runtime/common/db_test.go` and `bench/compare/testdata/runtime/common/bench_test.go`. These are the same in every app.
- Create: `bench/compare/testdata/runtime/{velox2,oldvelox,ent}/ops_test.go`, one per ORM.

**Interfaces:**
- Each app is the n=10 schema at the app root. The test package is `bench`, and its import path is `example.com/tmp`.
- `common/db_test.go`:
  - `func dialect() string` returns `$COMPARE_DIALECT`, which is `sqlite` (the default) or `postgres`.
  - `func openDB(tb testing.TB) *sql.DB`. For sqlite, it opens `sql.Open("sqlite", <tb.TempDir()>/b.db?_pragma=foreign_keys(1))`. For postgres, it opens `sql.Open("pgx", os.Getenv("VELOX_COMPARE_POSTGRES"))`; if that is unset, `tb.Fatal("VELOX_COMPARE_POSTGRES is unset")`, without the value. It runs `DROP SCHEMA IF EXISTS cmp_<ormName> CASCADE; CREATE SCHEMA cmp_<ormName>`, and returns a second pool on the DSN plus `search_path=cmp_<ormName>` (added with `&` or `?`). Both dialects use `SetMaxOpenConns(1)`.
- `<orm>/ops_test.go` defines `const ormName = "<orm>"` and `type app`, with exactly these methods. The shared file calls them:
  - `func newApp(tb testing.TB, db *sql.DB) *app`. It creates the tables: velox2 runs the embedded `ddl_<dialect>.sql`, split on `";\n"`; ent and old velox run `client.Schema.Create(ctx)`.
  - `(a *app) seed(ctx) error`. It inserts 100 E0 (`name "p<i>"`, status draft); then 5 E1 per E0, 500 in all, with `count = 5*i+j`, `active = count%2 == 0`, and `prev_id` the parent; then one E2 whose prev is the first E1. Any ids it needs later stay in `a`.
  - `(a *app) insertOne(ctx) error`: one E2, prev = the first E1.
  - `(a *app) insertBulk(ctx) error`: 100 E3 in one bulk call, prev = the seeded E2.
  - `(a *app) get(ctx, i int) (id int64, name string, err error)`: E0 `ids[i%100]`, fetched by id.
  - `(a *app) query(ctx) ([]int, error)`: the `count` values of E1 `WHERE count >= 100 ORDER BY count DESC LIMIT 100`.
  - `(a *app) eager(ctx) (parents, children int, err error)`: 100 E0 by id ascending, with `next` loaded. It returns an error if a child's `prev_id` is not its parent's id.
  - `(a *app) update(ctx, i int, name string) error`: sets the name of E0 `ids[i%100]`, by id.
  - `(a *app) count(ctx) (int, error)`: the count of E1 `WHERE active`.
  - `(a *app) rows(ctx) (e1, e2, e3 int, err error)`.
- The API each ORM uses, as confirmed by the spikes:

  | Op | velox2 (`db := velox.Open(D, sqlDB)`) | ent / old velox (`client.E0…`) |
  |---|---|---|
  | insert one | `db.Insert(&x).Exec(ctx)` | `client.E2.Create().Set….Save(ctx)` |
  | bulk 100 | `db.InsertAll(rows).Exec(ctx)` | `client.E3.CreateBulk(bs...).Save(ctx)` |
  | get by id | `db.Query[gen.E0]().Where(gen.E0s.ID.EQ(id)).One(ctx)` | `client.E0.Get(ctx, id)` |
  | query | `db.Query[gen.E1]().Where(gen.E1s.Count.GTE(100)).OrderBy(gen.E1s.Count.Desc()).Limit(100).All(ctx)` | ent: `.Where(e1.CountGTE(100)).Order(ent.Desc(e1.FieldCount)).Limit(100).All(ctx)`; old: `.Where(e1.CountField.GTE(100)).Order(e1.ByCount(vsql.OrderDesc())).Limit(100).All(ctx)` |
  | eager | `db.Query[gen.E0]().OrderBy(gen.E0s.ID.Asc()).With(gen.E0s.Next).Limit(100).All(ctx)`; then `p.Next.Get()` | `client.E0.Query().Order(<asc>).WithNext().Limit(100).All(ctx)`, where `<asc>` is `ent.Asc(e0.FieldID)` for ent and `e0.ByID()` for old velox; then `p.Edges.Next` |
  | update | `db.Update[gen.E0]().Set(gen.E0s.Name.Set(s)).Where(gen.E0s.ID.EQ(id)).Exec(ctx)` | `client.E0.UpdateOneID(id).SetName(s).Exec(ctx)` |
  | count | `db.Query[gen.E1]().Where(gen.E1s.Active.EQ(true)).Count(ctx)` | ent: `.Where(e1.Active(true)).Count(ctx)`; old: `.Where(e1.ActiveField.EQ(true)).Count(ctx)` |

  Old velox imports `e1client "example.com/tmp/velox/client/e1"` for `[]*e1client.E1Create`, and `vsql "github.com/syssam/velox/dialect/sql"`.
- `common/bench_test.go`:
  - `TestGuard` (Ruling 7). It does open → `newApp` → `seed`, then:
    - `insertOne` once, and `rows` gives e2 == 2;
    - `insertBulk` once, and e3 == 100;
    - `get(7)` gives `name == "p7"`;
    - `query` gives 100 values, the first 499, strictly descending;
    - `eager` gives `(100, 500)`;
    - `update(7, "u")` then `get(7)` gives `"u"`;
    - `count` gives 250;
    - finally `rows` gives e1 == 500.
  - `BenchmarkInsertOne`, `BenchmarkInsertBulk100`, `BenchmarkGetByID`, `BenchmarkQuery100`, `BenchmarkEagerO2M100`, `BenchmarkUpdateByID` and `BenchmarkCount`. Each opens a fresh DB, seeds it, calls `b.ReportAllocs()` and `b.ResetTimer()`, then calls one op per iteration and `b.Fatal`s on its error. The names carry no ORM or dialect: the file label does that, as benchstat's column.
- `runtime.go`:
  - `//go:embed testdata/runtime` into `runtimeFS`.
  - `func writeRuntimeApp(dir string, o orm, tools string) error`. It does `writeApp` with `o.Schema(10)` and `codegen`. For velox2 it also writes `ddl_sqlite.sql` and `ddl_postgres.sql` from `velox-gen migrate sql -dialect <d> ./schema`. Then it copies `common/*` and `<o.Name>/*`.
  - CLI: `compare runtime-app -orm NAME -tools DIR -dir DIR`.

- [ ] **Step 1: Write the tests** in `runtime_test.go`:
  - `TestRuntimeFilesAgree` (`-short`). Parse each `<orm>/ops_test.go` with `go/parser`. Each has `const ormName` equal to its directory's name, and the same set of `*app` method names as the list above. `common/bench_test.go` has exactly the seven `Benchmark*` functions listed.
  - `TestGuardSQLite` (skipped under `-short`). For each ORM in turn: `writeRuntimeApp`, then `go test -race -count=1 -run '^TestGuard$' .` in the app, with `COMPARE_DIALECT=sqlite`. Expected: `ok`.
  - `TestGuardPostgres` (Review Focus 4; skipped unless `VELOX_COMPARE_POSTGRES` is set). Run the guard for ent, then for velox2, against the same database. Each passes. Afterwards the database has the schemas `cmp_ent` and `cmp_velox2`, and `cmp_ent.e1s` still has 500 rows, so the second ORM touched only its own schema.
- [ ] **Step 2: Run them to see them fail.** `-short` → FAIL.
- [ ] **Step 3: Implement** runtime.go and the testdata files. gofmt the testdata files: the root's `gofmt -l .` checks them.
- [ ] **Step 4: Run the tests.** `-short` → `ok`. Then, alone: `perl -e 'alarm 5400; exec @ARGV' go test -race -count=1 -run '^TestGuardSQLite$' .` → `ok`.
- [ ] **Step 5: The PostgreSQL run** (the one database stack; first check that `docker compose -f docker-compose.ci.yml -p velox2-ci ps -q` prints nothing, and stop if it does):
  ```bash
  docker compose -f docker-compose.ci.yml -p velox2-ci up -d --wait postgres
  export VELOX_COMPARE_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'
  (cd bench/compare && perl -e 'alarm 3600; exec @ARGV' go test -race -count=1 -run '^TestGuardPostgres$' -v .)
  docker compose -f docker-compose.ci.yml -p velox2-ci down -v
  ```
  Expected: `--- PASS: TestGuardPostgres`. If an ORM fails on pgx stdlib, stop and report. Do not switch its driver in this task; the controller decides (Ruling 4).
- [ ] **Step 6: Run C. Commit** the files: `bench/compare: the seven runtime operations for each ORM, a guard that checks the work, and the benchmarks`.

### Task 6: `summarize` and `report`

**Files:**
- Create: `bench/compare/summary.go`, `bench/compare/report.go`, `bench/compare/summary_test.go`, `bench/compare/report_test.go`, and `bench/compare/testdata/summary/`. The fixtures are a results directory: two build JSONs per ORM, one bench file per ORM and dialect, and a steps.jsonl.

**Interfaces:**
- `type stat struct{ Median float64 \`json:"median"\`; Worst float64 \`json:"worst"\`; Runs []float64 \`json:"runs"\` }`
- `func newStat(runs []float64, lowerIsBetter bool) stat`. The median of an even count is the mean of the middle two. `Worst` is the max for a cost and the min for `rebuild_cached`, the one metric where higher is better (`var higherIsBetter = map[string]bool{"rebuild_cached": true}`).
- `func parseBench(r io.Reader) (map[string]map[string][]float64, error)`. It maps the benchmark name, without its `-N` suffix, to the unit (`sec/op` from ns/op ÷ 1e9, `B/op`, `allocs/op`) and then to the samples.
- `type summary struct`, with these JSON keys:
  - `machine`: `cpu` (`sysctl -n machdep.cpu.brand_string`), `cpus`, `memory_gb` (`sysctl -n hw.memsize`), `os` (`uname -sr`);
  - `go` (`go env GOVERSION`);
  - `versions`: velox2 = `git rev-parse --short HEAD` plus `-dirty` when `git status --porcelain` is non-empty outside `bench/compare/results/`; and `oldvelox`, `ent`, `sqlite`, `pgx` from `debug.ReadBuildInfo()`;
  - `steps`: a `[]stepRecord`;
  - `build`: orm → `"100"|"300"` → metric (the buildReport keys) → `stat`;
  - `runtime`: dialect → orm → benchmark → unit → `stat`;
  - `config`: `runs`, `services`, `bench_count`, `benchtime`, `sizes`.
- `func summarize(results string, cfg map[string]any) (summary, error)`. It reads `<results>/raw/build-*.json`, `<results>/raw/bench-<dialect>-<orm>.txt` and `<results>/steps.jsonl`.
- `type loss struct{ Section, Key, Metric, Unit, Winner string; Velox2, Best, Ratio float64 }`. `Ratio = Best / Velox2`.
- `func losses(s summary) []loss`. It lists every build metric, size, dialect and benchmark unit where another ORM's median beats velox2's, sorted by Section, Key, Metric and Unit. There is no noise threshold; the report says so.
- `func writeReport(w io.Writer, s summary) error`. It writes Markdown:
  - a steps table: name, start, wall, load before/after, and gate `waited_s`/`timed_out`;
  - per build metric, one table with rows `n=100`/`n=300` and columns `velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2`. Each cell is `median (worst)`. The CPU `_x` tables come first;
  - per dialect, one table per unit, with the benchmarks as rows and the same columns;
  - `## Where velox2 loses`, one bullet per `loss`, or the line `velox2 is best on every metric measured.`
- CLI: `compare summarize -results DIR -out summary.json [-config k=v…]` and `compare report -summary summary.json`. The report goes to stdout.

- [ ] **Step 1: Write the tests.**
  - `TestNewStat`: `[3,1,2]` lower-is-better → median 2, worst 3. Higher-is-better → worst 1. `[1,2,3,4]` → median 2.5.
  - `TestParseBench`: the line `BenchmarkCount-14   	    1000	      1234 ns/op	      56 B/op	       3 allocs/op` → `Count` gives `sec/op [1.234e-06]`, `B/op [56]`, `allocs/op [3]`. Header lines (`goos:`, `pkg:`) are skipped.
  - `TestSummarizeFixture`: from `testdata/summary`, `build["ent"]["100"]["cold_cpu_s"].Median` equals the fixture's value, and `steps` has the fixture's 3 records. One of them has `timed_out: true` (Review Focus 5).
  - `TestSummaryHasNoSecret` (Review Focus 3): with `VELOX_COMPARE_POSTGRES=postgres://u:hunter2@h/db` set, the marshalled summary and the report contain no `hunter2`.
  - `TestLosses`: the fixture has ent's `cold_cpu_s` median at 10 and velox2's at 12, and velox2 best everywhere else. Expected: exactly one loss, `{Section:"build", Key:"100", Metric:"cold_cpu_s", Winner:"ent", Ratio:≈0.833}`.
  - `TestReportShowsTimedOutGate`: the report's steps table contains `timed out` on the fixture's timed-out step.
- [ ] **Step 2: Run them to see them fail.** Then **Step 3: implement.** Then **Step 4:** `-short` → `ok`.
- [ ] **Step 5: Run C. Commit**: `bench/compare: the JSON summary and the Markdown tables, with every metric where velox2 loses`.

### Task 7: `run.sh`, a smoke run, and the full local gate

**Files:**
- Create: `bench/compare/run.sh` (mode 0755)

**Interfaces:**
- Consumes the `compare` subcommands of Tasks 2–6.
- The knobs, all optional:

  | Variable | Real run | `COMPARE_SMOKE=1` |
  |---|---|---|
  | `COMPARE_SIZES` | `100 300` | `10` |
  | `COMPARE_RUNS` | `3` | `1` |
  | `COMPARE_SERVICES` | `20` | `2` |
  | `COMPARE_BENCH_COUNT` | `6` | `1` |
  | `COMPARE_BENCHTIME` | `1s` | `10x` |
  | `COMPARE_GATE` | `12` | `0` (off) |
  | `COMPARE_GATE_WAIT` | `60m` | — |

- The script, in order:
  1. Go is 1.27, as ci-local.sh checks it. Docker is present.
  2. `docker compose -f docker-compose.ci.yml -p velox2-ci ps -q` prints nothing; otherwise exit 1 with "velox2-ci is running: run.sh starts and stops its own".
  3. `results=bench/compare/results/$(date -u +%Y%m%dT%H%M%SZ)`, with `raw/`; and `work=$(mktemp -d)`. One `trap cleanup EXIT`: it brings the compose stack down with `down -v` only if this script started it, then `rm -rf "$work"`.
  4. A step `tools` that runs `go -C bench/compare build -o $work/tools/<x>` for `compare`, `velox-gen`, `ent` (`entgo.io/ent/cmd/ent`) and `velox` (`github.com/syssam/velox/cmd/velox`). Every later command is a `$work/tools/compare step -log $results/steps.jsonl …` line.
  5. Build: `for r in 1..RUNS; for n in SIZES; for orm in <rotated by r: velox2 oldvelox ent → oldvelox ent velox2 → ent velox2 oldvelox>`: `step -name build-$orm-n$n-r$r -gate $COMPARE_GATE -gate-wait $COMPARE_GATE_WAIT -timeout 180m -- compare build … -out $results/raw/build-$orm-n$n-r$r.json`. The rotation keeps the load's drift from always landing on the same ORM.
  6. Runtime apps: for each ORM, `compare runtime-app -dir $work/rt-$orm`, then `go test -c -o $work/rt-$orm.test` in it.
  7. SQLite: for each ORM, a guard step: `COMPARE_DIALECT=sqlite $work/rt-$orm.test -test.run '^TestGuard$'`. Then `for i in 1..BENCH_COUNT; for orm (rotated)`: a gated step `bench-sqlite-$orm-$i` that runs `$work/rt-$orm.test -test.run '^$' -test.bench . -test.benchmem -test.count 1 -test.benchtime $COMPARE_BENCHTIME`, appending to `$results/raw/bench-sqlite-$orm.txt`. Through `sh -c`, the step's argv names the file, not the DSN. These are six rounds of `-count 1`, so that each round sees all three ORMs under the same load. That equals Ruling 4's `-count=6` in samples.
  8. PostgreSQL: `up -d --wait postgres` (PostgreSQL only: one stack). `export VELOX_COMPARE_POSTGRES='postgres://velox:velox@127.0.0.1:55432/velox?sslmode=disable'`, as ci-local.sh exports its DSN, never echoed. The guards and the rounds then run as in 7, with `COMPARE_DIALECT=postgres`. Then `down -v`.
  9. `benchstat velox2=… oldvelox=… ent=…` per dialect → `$results/benchstat-<dialect>.txt`, with the pinned benchstat.
  10. `compare summarize -results $results -out $results/summary.json -config …`, then `compare report -summary $results/summary.json > $results/report-tables.md`.
  11. Print the results path.

- [ ] **Step 1: Write `run.sh`.** Check it with `bash -n bench/compare/run.sh` → no output. If `shellcheck` is on PATH, `shellcheck bench/compare/run.sh` → no output.
- [ ] **Step 2: The smoke run.** First check that velox2-ci is not running. Then: `COMPARE_SMOKE=1 perl -e 'alarm 7200; exec @ARGV' bash bench/compare/run.sh`. Expected: exit 0, and a results directory that contains:
  - `steps.jsonl`, with one record per step, every `exit` 0;
  - 3 build JSONs;
  - 6 bench files (3 ORMs × 2 dialects), each with the 7 benchmarks;
  - two benchstat files, with columns `velox2 oldvelox ent`;
  - `summary.json`;
  - `report-tables.md`.
  Also: `/usr/bin/grep -a -rlF 'velox:velox@' <results>` prints nothing (Review Focus 3), and `docker compose -p velox2-ci ps -q` is empty afterwards.
- [ ] **Step 3: The full local gate,** because this plan changed ci-local.sh and ci.yml: `perl -e 'alarm 10800; exec @ARGV' bash scripts/ci-local.sh`. Expected: exit 0, with the new bench/compare block run and the coverage floors met.
- [ ] **Step 4: Commit** `bench/compare/run.sh`: `bench/compare: run.sh runs every measurement, one step at a time, with the load recorded and gated`.

### Task 8: The run, and the report

**Files:**
- Create: `docs/reviews/2026-10-29-perf-comparison.md`
- Modify: `CHANGELOG.md` (the commit ids of the bench/compare line)

- [ ] **Step 1: Run it.** Check that velox2-ci is not running, then run `perl -e 'alarm 86400; exec @ARGV' bash bench/compare/run.sh` in the background. It takes hours, and the load gates can each wait up to 60 min. Do not run anything heavy beside it. Expected: exit 0, and a results path.
- [ ] **Step 2: Check the run.**
  - Every step's `exit` in steps.jsonl is 0.
  - There are 18 build JSONs (3 ORMs × 2 sizes × 3 runs).
  - Each bench file has 6 samples of each of the 7 benchmarks.
  If a build step failed, rerun only that measurement with the same `compare step … build` line, and say so in the report.
- [ ] **Step 3: Write the report** from `summary.json` and `report-tables.md` only. Every number comes from those files. The sections:
  1. **Summary.** Three to six sentences: the headline CPU ratios (cold, edit) at n=300, the runtime picture on PostgreSQL, and the largest loss.
  2. **Machine and load.** The CPU, CPU count, memory, OS and Go version; the steps table, with the gates that timed out named; and the load (~29 when the run was planned), stated as a limit of the numbers.
  3. **Versions.** velox2's commit, old velox's pseudo-version, ent v0.14.6, sqlite and pgx (all from `versions`). One version set for all three apps.
  4. **Method.**
     - Core ORM only, with GraphQL and optional features off. The codegen commands, with old velox's flags (none) and its GraphQL methods on enums.
     - The schema shape, and n and s.
     - What the CPU time counts, and the reference build (from bench/scale).
     - Why `-debug-actiongraph`.
     - Codegen runs with a warm build cache; the cold build with an empty one.
     - Three runs, with the median and the worst; ORM order rotated per run.
     - The seven operations and the guard.
     - The drivers.
     - Six rounds of `-count 1`, rotated.
     - velox2's `OpenPgx` was not measured.
  5. **Build cost.** The tables from report-tables.md, `_x` first.
  6. **Runtime cost.** The SQLite and PostgreSQL tables, plus the benchstat files' p-values for the rows the summary calls out.
  7. **Where velox2 loses.** Every bullet from the generated list, each with one plain sentence of cause where the data shows one, and none where it does not.
  8. **Limits.** The shared machine; one machine only; MySQL not measured; the services link two calls per entity, not a real application.
- [ ] **Step 4: CHANGELOG.** Put the commit ids from Tasks 2–7 on the bench/compare line.
- [ ] **Step 5: Run C** (root gofmt; nothing else changed). **Commit** `docs/reviews/2026-10-29-perf-comparison.md CHANGELOG.md`: `Review: velox2, old velox and ent compared, build and runtime`. The raw results stay git-ignored. Name the results path in the commit message body.
