# velox2, old velox and ent compared: build and runtime cost

Run on 2026-10-29, from 12:14 to 12:58 UTC, with `bench/compare/run.sh` at
velox2 commit d59c718. Every number here comes from that run's
`summary.json`, `report-tables.md` and benchstat files; the full generated
tables are the appendix. The raw results are not committed.

## Summary

At 300 entities, velox2's cold build costs 2.41 reference builds of CPU,
against 8.48 for ent (3.5×) and 12.8 for old velox (5.3×). Adding one field
and regenerating costs 1.22 reference builds, against 12.6 for ent (10.3×)
and 9.53 for old velox (7.8×). velox2 generates 49,550 lines where ent
generates 1,141,879 and old velox 1,296,808. Its service binary is 8.39 MB,
against 21.9 MB and 52.8 MB, and its link peaks at 238 MB, against 786 MB
and 1,415 MB.

At runtime velox2 allocates least on every benchmark except EagerO2M100. On
SQLite it is level with both: the geomean of sec/op is within 0.1% of old
velox and 3.9% better than ent. On PostgreSQL, through database/sql and pgx,
it loses: 2.6× slower than old velox on Query100 and 3.5× slower on
EagerO2M100, the two benchmarks that scan many rows. The cause is in the
scan path (see Where velox2 loses). That is the largest loss in this report,
and it is a velox2 fix to make before 1.0. Fixed after this run; see
[the PostgreSQL scan path review](2026-10-29-pg-scan-path.md).

## Machine and load

- Apple M3 Max, 14 CPUs, 36 GB, Darwin 27.0.0, Go 1.27.1.
- A shared machine, not an idle one. The 1-minute load before each step was
  between 4.4 and 11.6. Two steps ended at a load of about 29, with other
  work on the machine: build-velox2-n100-r2 and build-velox2-n300-r3. Their
  numbers are in the medians, and the worst-of-three columns show them.
- Every build step waited at a load gate of 12. None timed out; three waited
  10 or 90 seconds.
- The reference build (a cold build of a module that imports `net/http`)
  took a median of 27.9 to 30.7 CPU seconds across ORMs and sizes. The `_x`
  metrics divide by it, which absorbs most of the load's effect on CPU time.
- `GOFLAGS=-p=2` for codegen and the app builds. The reference build runs
  at the default `-p`. CPU time hardly depends on `-p`.

## Versions

| component | version |
|---|---|
| velox2 | d59c718 |
| old velox | github.com/syssam/velox v0.3.1-0.20261028051754-a7d44fb423b0 |
| ent | entgo.io/ent v0.14.6 |
| SQLite driver | modernc.org/sqlite v1.60.1 |
| PostgreSQL driver | github.com/jackc/pgx/v5 v5.11.0, stdlib |
| golang.org/x/tools | v0.52.0 |

All three apps resolve one version set, the harness's go.mod. ent alone
would select x/tools v0.30.0, whose codegen fails on Go 1.27.

## Method

- **Scope.** The core ORM only. GraphQL and every optional feature are off.
  Each ORM runs its own CLI: `velox-gen -out gen ./schema`, `velox generate
  --target ./velox ./schema` with no feature flags (old velox's defaults),
  and `ent generate ./ent/schema`. Old velox's generated enums still carry
  `MarshalGQL` methods with GraphQL off.
- **Schema.** One generator writes the same schema in all three DSLs, which
  a test checks file by file. There are n entities, each with name, count,
  active, created_at, a nillable note, an enum status, and for i>0 a
  required O2M edge from entity i-1 bound to `prev_id`. Every tenth entity
  has an M2M edge to the fifth one after it. Each `prev_id` is indexed in all
  three; ent and old velox get the index from `index.Fields("prev_id")`. The
  runtime guard checks that every table has the same indexes and foreign
  keys in all three. Two differences remain:
  - velox2 stores status as a PostgreSQL ENUM type, the others as varchar;
  - velox2 also indexes the second key column of an M2M join table. No
    benchmark touches an M2M table.
- **Build cost.** One measurement per ORM, size and run, at n=100 and n=300,
  with s=20 services that each call Query and Count on five entities. In
  order:
  - a discarded warm-up codegen in a throwaway app, so the ordinary build
    cache is warm for that ORM's dependencies;
  - the timed codegen;
  - a cold `go build -o bin/ ./...` with an empty GOCACHE;
  - adding one field to the last entity, a timed regen, and a timed rebuild;
  - the reference build, before and after.

  `./...` compiles every package of the app, including the schema packages
  and generated packages no service imports (ent's `hook`, `enttest` and
  `runtime`, old velox's `hook`). `gen_lines` counts the generated Go files,
  without ent's hand-written `ent/schema`. The binary size is a service's
  after the edit.
- **CPU time.** User plus system time of each command and the children it
  reaped, the compiler and linker included, as in `bench/scale`. Every
  compile and link runs under a `-toolexec` wrapper that records the
  linker's peak memory. The wrapper's start-up cost is counted once per
  action, and ent and old velox have about one generated package per entity
  where velox2 has one. That biases the build CPU slightly in velox2's
  favour: milliseconds per action, against cold builds of 70–355 CPU
  seconds.
- **Cached and rerun actions** come from `go build -debug-actiongraph`. An
  action with a command ran; one without came from the cache.
  `cold_app_action_s` and `cold_dep_action_s` are the CPU seconds of the
  compile actions of the app's packages and of everything else.
  `rebuild_rerun` counts every rerun compile action, the services included.
- **Runs.** Three runs of each build measurement, with the ORM order rotated
  per run. A cell is the median, with the worst run in brackets.
- **Runtime.** Seven Go benchmarks per ORM on an n=10 schema, seeded with 100
  E0 and 500 E1:
  - InsertOne;
  - InsertBulk100;
  - GetByID;
  - Query100 (`count < 450 ORDER BY count DESC LIMIT 100`, scanned into
    entities);
  - EagerO2M100 (100 parents with their 500 children);
  - UpdateByID;
  - Count.

  Before any timing, a guard test runs each op once per ORM and checks that
  it did the work: row counts, the filter, the order and the ids. A
  statement log showed every ORM sending the same statements for each op,
  with no transaction:
  - one statement for each op except EagerO2M100, which sends two;
  - UpdateByID is `UPDATE e0s SET name=? WHERE id=?` in all three. ent and
    old velox use `Update().Where(id)`, not `UpdateOneID`, which adds a
    transaction and a SELECT.

  Small differences remain:
  - InsertBulk100 is not the same work. velox2's `InsertAll` reads nothing
    back, while ent and old velox send `RETURNING id` and build 100 entities.
  - velox2's INSERTs list the NULL `note` column.
  - velox2 binds the LIMIT as a parameter where the others inline it.
  - ent counts `COUNT(id)`, the others `COUNT(*)`.
- **Benchmark setup.** Each ramp step of a benchmark gets a fresh database,
  seeded and then ANALYZEd outside the timer, on one connection
  (`SetMaxOpenConns(1)`). The seed's garbage can be collected inside the
  timed loop, which charges the ORM that allocates more in setup. InsertOne
  and InsertBulk100 grow their table with b.N, so a faster ORM inserts into a
  slightly larger table.
- **Databases.** PostgreSQL 18 from `docker-compose.ci.yml`, in Docker on
  tmpfs over loopback, one schema per ORM. SQLite is a temporary file in the
  default rollback-journal mode with synchronous FULL, so SQLite's write
  benchmarks are bound by fsync, which narrows the ratios between ORMs. All
  three use the same drivers through database/sql. velox2's native pgx path
  (`velox.OpenPgx`) exists but was not measured.
- **Rounds.** Six rounds of `-count 1 -benchtime 1s`, each preceded by one
  load gate and running the three ORMs in rotated order, so that each round
  sees all three under the same load. benchstat compares the six samples.

## Build cost

CPU over the reference build (lower is better; ratio = other ÷ velox2):

| metric | size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|---|
| codegen_x | n=100 | 0.0546 | 0.151 | 1.52 | 2.76 | 27.9 |
| codegen_x | n=300 | 0.088 | 0.377 | 5.75 | 4.28 | 65.3 |
| cold_x | n=100 | 1.82 | 4.57 | 3.81 | 2.51 | 2.09 |
| cold_x | n=300 | 2.41 | 12.8 | 8.48 | 5.28 | 3.51 |
| edit_x | n=100 | 0.679 | 2.84 | 3.06 | 4.18 | 4.51 |
| edit_x | n=300 | 1.22 | 9.53 | 12.6 | 7.79 | 10.3 |

Size and memory:

| metric | size | velox2 | old velox | ent |
|---|---|---|---|---|
| gen_lines | n=100 | 16,510 | 432,468 | 380,968 |
| gen_lines | n=300 | 49,550 | 1,296,808 | 1,141,879 |
| service_binary_mb | n=100 | 8.39 | 23.8 | 13.7 |
| service_binary_mb | n=300 | 8.39 | 52.8 | 21.9 |
| link_peak_mb | n=100 | 203 | 594 | 400 |
| link_peak_mb | n=300 | 238 | 1,415 | 786 |

velox2's cost grows little with n: a service's binary holds only the
entities it uses, as velox2's design intends, and its cold build at n=300
costs 1.3× its cost at n=100, where ent's costs 2.2× and old velox's 2.6×.
The full tables, with wall time, the worst runs and the action counts, are in
the appendix.

## Runtime cost

sec/op, the median of six samples (ratio = other ÷ velox2; benchstat p-values
from `benchstat-<dialect>.txt`):

| benchmark | dialect | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|---|
| InsertOne | sqlite | 410 µs | 408 µs | 406 µs | 0.997 | 0.992 |
| InsertBulk100 ¹ | sqlite | 993 µs | 1.01 ms | 1.02 ms | 1.02 | 1.02 |
| GetByID | sqlite | 23.1 µs | 21.3 µs | 22.1 µs | 0.923 (p=0.002) | 0.954 |
| Query100 | sqlite | 489 µs | 491 µs | 515 µs | 1.01 | 1.05 (p=0.009) |
| EagerO2M100 | sqlite | 984 µs | 924 µs | 1.02 ms | 0.94 (p=0.002) | 1.04 (p=0.004) |
| UpdateByID | sqlite | 14.9 µs | 16.3 µs | 16.4 µs | 1.09 (p=0.026) | 1.10 (p=0.015) |
| Count | sqlite | 47.7 µs | 49.0 µs | 53.5 µs | 1.03 (p=0.015) | 1.12 (p=0.002) |
| InsertOne | postgres | 159 µs | 160 µs | 162 µs | 1.00 | 1.02 |
| InsertBulk100 ¹ | postgres | 1.06 ms | 1.18 ms | 1.21 ms | 1.11 (p=0.009) | 1.14 (p=0.009) |
| GetByID | postgres | 155 µs | 148 µs | 144 µs | 0.954 | 0.927 |
| Query100 | postgres | 961 µs | 363 µs | 407 µs | 0.378 (p=0.002) | 0.424 (p=0.002) |
| EagerO2M100 | postgres | 4.32 ms | 1.25 ms | 1.37 ms | 0.288 (p=0.002) | 0.317 (p=0.002) |
| UpdateByID | postgres | 155 µs | 150 µs | 155 µs | 0.967 | 0.996 |
| Count | postgres | 171 µs | 165 µs | 175 µs | 0.965 | 1.02 |

A p-value is shown where benchstat calls the difference significant
(p < 0.05); elsewhere it reports no significant difference.

¹ ent and old velox read 100 ids back with `RETURNING`, and velox2 reads
none, so part of velox2's lead on this row is doing less.

allocs/op (B/op is in the appendix):

| benchmark | sqlite: velox2 / old velox / ent | postgres: velox2 / old velox / ent |
|---|---|---|
| InsertOne | 54 / 80 / 103 | 35 / 62 / 90 |
| InsertBulk100 ¹ | 1,557 / 2,883 / 4,490 | 2,062 / 2,884 / 5,492 |
| GetByID | 42 / 87 / 127 | 35 / 80 / 121 |
| Query100 | 941 / 1,002 / 1,945 | 835 / 896 / 1,840 |
| EagerO2M100 | 5,800 / 5,279 / 10,662 | 5,197 / 4,676 / 10,160 |
| UpdateByID | 16 / 49 / 56 | 12 / 45 / 53 |
| Count | 20 / 54 / 70 | 16 / 50 / 66 |

## Where velox2 loses

Each line is a metric where another ORM's median beats velox2's, with no
noise threshold, from the generated list in the appendix. It is grouped by
cause where the data shows one.

- **PostgreSQL, Query100 and EagerO2M100, sec/op.** Old velox is 0.378× and
  0.288× of velox2's time; ent is 0.424× and 0.317×. This is the one large
  loss. velox2 allocates less than old velox on Query100, so it is not
  allocation. The cause was found after the run, by profiling Query100 on
  PostgreSQL, and is not part of the measured data:
  - In Go 1.27, database/sql hands every column of every row to the driver's
    `ScanColumn`. pgx v5.11's `stdlib.(*Rows).ScanColumn` first runs
    `isSQLScanDestination`, a `reflect.Type.Implements` check, on the
    destination's type, and the result is not cached.
  - ent and old velox scan into `sql.Scanner` types, which pass the first
    check at once.
  - velox2 scans into plain Go pointers (`*string`, `*int64`, …), which pay
    the full set of checks. In the profile, `isSQLScanDestination` is 250 ms
    of the 890 ms of CPU.
  - SQLite's driver does not implement `ScanColumn`, and there velox2 is
    level.
  - The PostgreSQL plans are the same for all three. A bound or an inlined
    LIMIT plans the same, and turning generic plans off changes nothing.

  This is velox2's to fix: its scan destinations on database/sql should not
  fall through pgx's slow path. velox2's native pgx path (`OpenPgx`) skips
  `ScanColumn` but was not measured here. Fixed after this run; see
  [the PostgreSQL scan path review](2026-10-29-pg-scan-path.md).
- **EagerO2M100, B/op and allocs/op, both dialects.** velox2 allocates 1.8×
  old velox's bytes and 1.15× ent's; old velox also makes 10% fewer
  allocations. This run does not show why: the scan path above is
  PostgreSQL-only, and SQLite shows the same bytes.
- **EagerO2M100 on SQLite, sec/op.** Old velox takes 0.94× of velox2's time
  (p=0.002). ent is slower than velox2 here.
- **GetByID, sec/op.** On SQLite old velox takes 0.923× of velox2's time
  (p=0.002) and ent 0.954× (not significant). On PostgreSQL old velox takes
  0.954× and ent 0.927×, neither significant. velox2 allocates less than
  half of either, so the difference is not allocation, and the data shows
  no other cause.
- **Count, UpdateByID (PostgreSQL), InsertOne (SQLite), sec/op.** These are
  ratios of 0.965 to 0.997, which benchstat does not call significant.
- **rebuild_rerun_share, ent.** The share of compile actions an edit reran
  is 0.0714 for ent against 0.089 for velox2 at n=100, and 0.0441 against
  0.089 at n=300. ent reruns 23 actions to velox2's 21, but out of more
  packages, so its share is smaller; its rebuild costs 4.9× velox2's CPU at
  n=300. The share falls as the package count grows, so this line does not
  show that ent rebuilds faster.

## Limits

- **One shared machine.** This is one shared macOS laptop, not an idle
  server, and the load moved during the run. The reference build absorbs
  part of that for build CPU, and the rotated rounds share it for runtime;
  the wall times carry it in full.
- **Not measured:** MySQL, velox2's native pgx path, concurrency (every
  benchmark runs on one connection), and GraphQL.
- **The services are not real applications:** they make two calls on five
  entities each.
- **Parked for the next run:**
  - collect the garbage before each benchmark's timer starts;
  - subtract the `-toolexec` wrapper's per-action cost.

## Appendix: the generated tables

The tables below are `report-tables.md` from the run, as `compare report`
wrote it.

How to read the tables: a cell is `median (worst)` of the runs, where worst is the largest run, or the smallest for a metric where higher is better (the tables say which). The ratio columns are the other ORM ÷ velox2, from the medians: for a lower-is-better metric, a ratio above 1 means velox2 is better; for a higher-is-better one, a ratio below 1 does.

### Machine and load

- CPU: Apple M3 Max (14 CPUs)
- Memory: 36 GB
- OS: Darwin 27.0.0
- Go: go1.27.1
- Runs: 3, services: 20, benchmark rounds: 6, benchtime: 1s

#### Versions

| component | version |
|---|---|
| velox2 | d59c718 |
| oldvelox | v0.3.1-0.20261028051754-a7d44fb423b0 |
| ent | v0.14.6 |
| sqlite | v1.60.1 |
| pgx | v5.11.0 |
| x/tools | v0.52.0 |

#### Reference build

`ref_cpu_s`: the CPU seconds of a fixed reference build, taken around each measurement. It measures the machine's load, not an ORM; the `_x` metrics divide by it. Each cell is `median (worst)`.

| size | velox2 | old velox | ent |
|---|---|---|---|
| n=100 | 30.7 (32) | 30.2 (30.5) | 30.2 (31.6) |
| n=300 | 30.7 (32) | 27.9 (29.4) | 29.3 (30) |

#### Steps

Load is the 1m, 5m and 15m load average. A step that waited at its load gate shows how long, and whether the wait timed out: it then ran under the load shown. After a resume the table lists every attempt of a step; the notes about timed-out gates and failed steps use only the last attempt.

| step | start | wall (s) | exit | load before | load after | gate waited (s) | gate |
|---|---|---|---|---|---|---|---|
| tools | 2026-10-29T12:14:13Z | 3.78 | 0 | 7.45 / 11.12 / 11.78 | 7.57 / 11.09 / 11.77 | - | - |
| build-velox2-n100-r1 | 2026-10-29T12:14:18Z | 34.4 | 0 | 7.57 / 11.09 / 11.77 | 8.80 / 11.01 / 11.71 | 0.00689 | passed (max 12) |
| build-oldvelox-n100-r1 | 2026-10-29T12:14:52Z | 73.9 | 0 | 8.80 / 11.01 / 11.71 | 10.24 / 10.59 / 11.47 | 0.00726 | passed (max 12) |
| build-ent-n100-r1 | 2026-10-29T12:16:06Z | 80.8 | 0 | 10.24 / 10.59 / 11.47 | 9.10 / 10.44 / 11.35 | 0.00549 | passed (max 12) |
| build-velox2-n300-r1 | 2026-10-29T12:17:27Z | 43.4 | 0 | 9.10 / 10.44 / 11.35 | 8.28 / 10.14 / 11.20 | 0.00573 | passed (max 12) |
| build-oldvelox-n300-r1 | 2026-10-29T12:18:10Z | 191 | 0 | 8.28 / 10.14 / 11.20 | 12.61 / 11.15 / 11.42 | 0.00491 | passed (max 12) |
| build-ent-n300-r1 | 2026-10-29T12:21:31Z | 254 | 0 | 10.90 / 10.83 / 11.30 | 9.89 / 11.95 / 11.80 | 10 | passed (max 12) |
| build-oldvelox-n100-r2 | 2026-10-29T12:25:45Z | 70.8 | 0 | 9.89 / 11.95 / 11.80 | 9.26 / 11.35 / 11.59 | 0.00576 | passed (max 12) |
| build-ent-n100-r2 | 2026-10-29T12:26:55Z | 89.5 | 0 | 9.26 / 11.35 / 11.59 | 9.37 / 10.84 / 11.36 | 0.00555 | passed (max 12) |
| build-velox2-n100-r2 | 2026-10-29T12:28:25Z | 52.6 | 0 | 9.37 / 10.84 / 11.36 | 29.34 / 15.89 / 13.19 | 0.00894 | passed (max 12) |
| build-oldvelox-n300-r2 | 2026-10-29T12:30:48Z | 192 | 0 | 10.55 / 13.08 / 12.37 | 10.02 / 11.22 / 11.69 | 90.1 | passed (max 12) |
| build-ent-n300-r2 | 2026-10-29T12:34:00Z | 206 | 0 | 10.02 / 11.22 / 11.69 | 8.95 / 10.48 / 11.31 | 0.00482 | passed (max 12) |
| build-velox2-n300-r2 | 2026-10-29T12:37:26Z | 44.6 | 0 | 8.95 / 10.48 / 11.31 | 7.76 / 10.04 / 11.11 | 0.00615 | passed (max 12) |
| build-ent-n100-r3 | 2026-10-29T12:38:11Z | 81.2 | 0 | 7.76 / 10.04 / 11.11 | 7.89 / 9.48 / 10.79 | 0.00579 | passed (max 12) |
| build-velox2-n100-r3 | 2026-10-29T12:39:32Z | 33.8 | 0 | 7.89 / 9.48 / 10.79 | 9.78 / 9.71 / 10.82 | 0.0054 | passed (max 12) |
| build-oldvelox-n100-r3 | 2026-10-29T12:40:06Z | 72 | 0 | 9.78 / 9.71 / 10.82 | 8.28 / 9.25 / 10.56 | 0.00688 | passed (max 12) |
| build-ent-n300-r3 | 2026-10-29T12:41:18Z | 235 | 0 | 8.28 / 9.25 / 10.56 | 9.95 / 12.07 / 11.67 | 0.00579 | passed (max 12) |
| build-velox2-n300-r3 | 2026-10-29T12:45:13Z | 71.7 | 0 | 9.95 / 12.07 / 11.67 | 29.26 / 17.04 / 13.56 | 0.00755 | passed (max 12) |
| build-oldvelox-n300-r3 | 2026-10-29T12:47:54Z | 187 | 0 | 11.59 / 14.41 / 12.90 | 9.00 / 11.74 / 12.03 | 90.1 | passed (max 12) |
| runtime-app-velox2 | 2026-10-29T12:51:02Z | 2.55 | 0 | 9.00 / 11.74 / 12.03 | 8.76 / 11.64 / 11.99 | - | - |
| test-binary-velox2 | 2026-10-29T12:51:04Z | 1.43 | 0 | 8.76 / 11.64 / 11.99 | 8.76 / 11.64 / 11.99 | - | - |
| runtime-app-oldvelox | 2026-10-29T12:51:06Z | 0.974 | 0 | 8.76 / 11.64 / 11.99 | 8.76 / 11.64 / 11.99 | - | - |
| test-binary-oldvelox | 2026-10-29T12:51:07Z | 2.56 | 0 | 8.76 / 11.64 / 11.99 | 8.30 / 11.50 / 11.94 | - | - |
| runtime-app-ent | 2026-10-29T12:51:09Z | 1.47 | 0 | 8.30 / 11.50 / 11.94 | 8.30 / 11.50 / 11.94 | - | - |
| test-binary-ent | 2026-10-29T12:51:11Z | 2.22 | 0 | 8.30 / 11.50 / 11.94 | 8.30 / 11.50 / 11.94 | - | - |
| guard-sqlite-velox2 | 2026-10-29T12:51:13Z | 0.52 | 0 | 8.30 / 11.50 / 11.94 | 8.30 / 11.50 / 11.94 | - | - |
| guard-sqlite-oldvelox | 2026-10-29T12:51:14Z | 0.57 | 0 | 8.30 / 11.50 / 11.94 | 8.30 / 11.50 / 11.94 | - | - |
| guard-sqlite-ent | 2026-10-29T12:51:14Z | 0.567 | 0 | 8.12 / 11.41 / 11.90 | 8.12 / 11.41 / 11.90 | - | - |
| gate-sqlite-1 | 2026-10-29T12:51:15Z | 0.00415 | 0 | 8.12 / 11.41 / 11.90 | 8.12 / 11.41 / 11.90 | 0.00519 | passed (max 12) |
| bench-sqlite-velox2-1 | 2026-10-29T12:51:15Z | 10.8 | 0 | 8.12 / 11.41 / 11.90 | 7.70 / 11.21 / 11.83 | - | - |
| bench-sqlite-oldvelox-1 | 2026-10-29T12:51:26Z | 10.6 | 0 | 7.70 / 11.21 / 11.83 | 7.49 / 11.06 / 11.77 | - | - |
| bench-sqlite-ent-1 | 2026-10-29T12:51:36Z | 10.9 | 0 | 7.49 / 11.06 / 11.77 | 6.86 / 10.81 / 11.67 | - | - |
| gate-sqlite-2 | 2026-10-29T12:51:47Z | 0.00313 | 0 | 6.86 / 10.81 / 11.67 | 6.86 / 10.81 / 11.67 | 0.00616 | passed (max 12) |
| bench-sqlite-oldvelox-2 | 2026-10-29T12:51:47Z | 10.7 | 0 | 6.86 / 10.81 / 11.67 | 6.27 / 10.55 / 11.57 | - | - |
| bench-sqlite-ent-2 | 2026-10-29T12:51:58Z | 11 | 0 | 6.27 / 10.55 / 11.57 | 6.42 / 10.45 / 11.52 | - | - |
| bench-sqlite-velox2-2 | 2026-10-29T12:52:09Z | 11 | 0 | 6.42 / 10.45 / 11.52 | 5.97 / 10.15 / 11.39 | - | - |
| gate-sqlite-3 | 2026-10-29T12:52:20Z | 0.00323 | 0 | 5.97 / 10.15 / 11.39 | 5.97 / 10.15 / 11.39 | 0.00622 | passed (max 12) |
| bench-sqlite-ent-3 | 2026-10-29T12:52:20Z | 10.8 | 0 | 5.97 / 10.15 / 11.39 | 5.52 / 9.91 / 11.29 | - | - |
| bench-sqlite-velox2-3 | 2026-10-29T12:52:31Z | 10.9 | 0 | 5.52 / 9.91 / 11.29 | 5.75 / 9.81 / 11.24 | - | - |
| bench-sqlite-oldvelox-3 | 2026-10-29T12:52:42Z | 10.7 | 0 | 5.75 / 9.81 / 11.24 | 5.24 / 9.57 / 11.14 | - | - |
| gate-sqlite-4 | 2026-10-29T12:52:52Z | 0.00283 | 0 | 5.24 / 9.57 / 11.14 | 5.24 / 9.57 / 11.14 | 0.00564 | passed (max 12) |
| bench-sqlite-velox2-4 | 2026-10-29T12:52:52Z | 10.7 | 0 | 5.24 / 9.57 / 11.14 | 5.53 / 9.49 / 11.09 | - | - |
| bench-sqlite-oldvelox-4 | 2026-10-29T12:53:03Z | 10.7 | 0 | 5.53 / 9.49 / 11.09 | 5.62 / 9.38 / 11.03 | - | - |
| bench-sqlite-ent-4 | 2026-10-29T12:53:14Z | 10.9 | 0 | 5.62 / 9.38 / 11.03 | 5.48 / 9.16 / 10.93 | - | - |
| gate-sqlite-5 | 2026-10-29T12:53:25Z | 0.003 | 0 | 5.48 / 9.16 / 10.93 | 5.48 / 9.16 / 10.93 | 0.00527 | passed (max 12) |
| bench-sqlite-oldvelox-5 | 2026-10-29T12:53:25Z | 10.6 | 0 | 5.48 / 9.16 / 10.93 | 5.65 / 9.07 / 10.87 | - | - |
| bench-sqlite-ent-5 | 2026-10-29T12:53:36Z | 10.9 | 0 | 5.65 / 9.07 / 10.87 | 5.32 / 8.89 / 10.79 | - | - |
| bench-sqlite-velox2-5 | 2026-10-29T12:53:46Z | 10.8 | 0 | 5.32 / 8.89 / 10.79 | 4.88 / 8.68 / 10.69 | - | - |
| gate-sqlite-6 | 2026-10-29T12:53:57Z | 0.00259 | 0 | 4.88 / 8.68 / 10.69 | 4.88 / 8.68 / 10.69 | 0.00573 | passed (max 12) |
| bench-sqlite-ent-6 | 2026-10-29T12:53:57Z | 10.7 | 0 | 4.88 / 8.68 / 10.69 | 4.80 / 8.54 / 10.62 | - | - |
| bench-sqlite-velox2-6 | 2026-10-29T12:54:08Z | 10.8 | 0 | 4.80 / 8.54 / 10.62 | 5.39 / 8.54 / 10.59 | - | - |
| bench-sqlite-oldvelox-6 | 2026-10-29T12:54:19Z | 10.6 | 0 | 5.39 / 8.54 / 10.59 | 6.56 / 8.70 / 10.62 | - | - |
| guard-postgres-velox2 | 2026-10-29T12:54:32Z | 0.14 | 0 | 6.43 / 8.63 / 10.59 | 6.43 / 8.63 / 10.59 | - | - |
| guard-postgres-oldvelox | 2026-10-29T12:54:32Z | 0.122 | 0 | 6.43 / 8.63 / 10.59 | 6.43 / 8.63 / 10.59 | - | - |
| guard-postgres-ent | 2026-10-29T12:54:32Z | 0.126 | 0 | 6.43 / 8.63 / 10.59 | 6.43 / 8.63 / 10.59 | - | - |
| gate-postgres-1 | 2026-10-29T12:54:33Z | 0.0032 | 0 | 6.43 / 8.63 / 10.59 | 6.43 / 8.63 / 10.59 | 0.00634 | passed (max 12) |
| bench-postgres-velox2-1 | 2026-10-29T12:54:33Z | 14.2 | 0 | 6.43 / 8.63 / 10.59 | 5.51 / 8.32 / 10.45 | - | - |
| bench-postgres-oldvelox-1 | 2026-10-29T12:54:47Z | 13.7 | 0 | 5.51 / 8.32 / 10.45 | 6.50 / 8.41 / 10.44 | - | - |
| bench-postgres-ent-1 | 2026-10-29T12:55:00Z | 13.6 | 0 | 6.50 / 8.41 / 10.44 | 5.81 / 8.19 / 10.34 | - | - |
| gate-postgres-2 | 2026-10-29T12:55:14Z | 0.00308 | 0 | 5.81 / 8.19 / 10.34 | 5.81 / 8.19 / 10.34 | 0.00565 | passed (max 12) |
| bench-postgres-oldvelox-2 | 2026-10-29T12:55:14Z | 14.5 | 0 | 5.81 / 8.19 / 10.34 | 5.82 / 8.07 / 10.26 | - | - |
| bench-postgres-ent-2 | 2026-10-29T12:55:29Z | 13.9 | 0 | 5.82 / 8.07 / 10.26 | 5.19 / 7.82 / 10.13 | - | - |
| bench-postgres-velox2-2 | 2026-10-29T12:55:43Z | 14.1 | 0 | 5.19 / 7.82 / 10.13 | 5.70 / 7.80 / 10.08 | - | - |
| gate-postgres-3 | 2026-10-29T12:55:57Z | 0.00291 | 0 | 5.70 / 7.80 / 10.08 | 5.70 / 7.80 / 10.08 | 0.00548 | passed (max 12) |
| bench-postgres-ent-3 | 2026-10-29T12:55:57Z | 14.7 | 0 | 5.70 / 7.80 / 10.08 | 5.21 / 7.58 / 9.96 | - | - |
| bench-postgres-velox2-3 | 2026-10-29T12:56:11Z | 16.8 | 0 | 5.21 / 7.58 / 9.96 | 7.19 / 7.90 / 10.03 | - | - |
| bench-postgres-oldvelox-3 | 2026-10-29T12:56:28Z | 14.4 | 0 | 7.19 / 7.90 / 10.03 | 6.44 / 7.69 / 9.92 | - | - |
| gate-postgres-4 | 2026-10-29T12:56:43Z | 0.00239 | 0 | 6.44 / 7.69 / 9.92 | 6.44 / 7.69 / 9.92 | 0.00492 | passed (max 12) |
| bench-postgres-velox2-4 | 2026-10-29T12:56:43Z | 13.4 | 0 | 6.44 / 7.69 / 9.92 | 5.52 / 7.43 / 9.78 | - | - |
| bench-postgres-oldvelox-4 | 2026-10-29T12:56:56Z | 13.6 | 0 | 5.52 / 7.43 / 9.78 | 5.58 / 7.35 / 9.72 | - | - |
| bench-postgres-ent-4 | 2026-10-29T12:57:10Z | 14.4 | 0 | 5.58 / 7.35 / 9.72 | 5.09 / 7.19 / 9.63 | - | - |
| gate-postgres-5 | 2026-10-29T12:57:24Z | 0.00317 | 0 | 5.09 / 7.19 / 9.63 | 5.09 / 7.19 / 9.63 | 0.00577 | passed (max 12) |
| bench-postgres-oldvelox-5 | 2026-10-29T12:57:24Z | 12.4 | 0 | 5.09 / 7.19 / 9.63 | 4.70 / 7.00 / 9.52 | - | - |
| bench-postgres-ent-5 | 2026-10-29T12:57:37Z | 13.6 | 0 | 4.70 / 7.00 / 9.52 | 4.43 / 6.82 / 9.41 | - | - |
| bench-postgres-velox2-5 | 2026-10-29T12:57:50Z | 13.5 | 0 | 4.43 / 6.82 / 9.41 | 4.85 / 6.83 / 9.38 | - | - |
| gate-postgres-6 | 2026-10-29T12:58:04Z | 0.00297 | 0 | 4.85 / 6.83 / 9.38 | 4.85 / 6.83 / 9.38 | 0.00572 | passed (max 12) |
| bench-postgres-ent-6 | 2026-10-29T12:58:04Z | 13.5 | 0 | 4.85 / 6.83 / 9.38 | 5.01 / 6.77 / 9.32 | - | - |
| bench-postgres-velox2-6 | 2026-10-29T12:58:18Z | 13.4 | 0 | 5.01 / 6.77 / 9.32 | 5.57 / 6.80 / 9.28 | - | - |
| bench-postgres-oldvelox-6 | 2026-10-29T12:58:31Z | 13.4 | 0 | 5.57 / 6.80 / 9.28 | 5.68 / 6.79 / 9.25 | - | - |
| benchstat-sqlite | 2026-10-29T12:58:46Z | 2.7 | 0 | 5.71 / 6.77 / 9.23 | 5.71 / 6.77 / 9.23 | - | - |
| benchstat-postgres | 2026-10-29T12:58:48Z | 1.98 | 0 | 5.71 / 6.77 / 9.23 | 5.65 / 6.75 / 9.21 | - | - |
| summarize | 2026-10-29T12:58:50Z | 0.0447 | 0 | 5.65 / 6.75 / 9.21 | 5.65 / 6.75 / 9.21 | - | - |
| report | 2026-10-29T12:58:50Z | 0.0249 | 0 | 5.65 / 6.75 / 9.21 | 5.65 / 6.75 / 9.21 | - | - |

### Build cost

The `_x` metrics are CPU seconds over the reference build's CPU seconds.

#### codegen_x

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 0.0546 (0.0602) | 0.151 (0.157) | 1.52 (1.58) | 2.76 | 27.9 |
| n=300 | 0.088 (0.117) | 0.377 (0.377) | 5.75 (6.32) | 4.28 | 65.3 |

#### cold_x

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 1.82 (2.04) | 4.57 (4.59) | 3.81 (3.89) | 2.51 | 2.09 |
| n=300 | 2.41 (2.43) | 12.8 (12.9) | 8.48 (8.98) | 5.28 | 3.51 |

#### edit_x

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 0.679 (0.735) | 2.84 (2.85) | 3.06 (3.84) | 4.18 | 4.51 |
| n=300 | 1.22 (1.37) | 9.53 (9.88) | 12.6 (13.4) | 7.79 | 10.3 |

#### codegen_cpu_s

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 1.75 (1.79) | 4.55 (4.74) | 46 (47.5) | 2.6 | 26.3 |
| n=300 | 2.75 (3.44) | 10.5 (11.1) | 172 (185) | 3.82 | 62.6 |

#### codegen_wall_s

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 1.07 (1.38) | 1.36 (1.4) | 7.08 (7.17) | 1.27 | 6.61 |
| n=300 | 1.57 (1.73) | 2.39 (2.41) | 34 (35.5) | 1.52 | 21.6 |

#### cold_cpu_s

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 55 (65.2) | 138 (139) | 116 (117) | 2.51 | 2.1 |
| n=300 | 70.9 (77.8) | 355 (361) | 254 (261) | 5.01 | 3.58 |

#### cold_wall_s

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 13.3 (23.3) | 32.1 (32.9) | 30.1 (31.7) | 2.42 | 2.26 |
| n=300 | 17.9 (26.1) | 92 (93.6) | 70.9 (85.6) | 5.13 | 3.95 |

#### cold_app_action_s

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 10.6 (11.9) | 78.2 (78.2) | 65.2 (66.5) | 7.38 | 6.16 |
| n=300 | 25.3 (27.5) | 267 (269) | 189 (194) | 10.5 | 7.45 |

#### cold_dep_action_s

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 34.4 (41.6) | 36.7 (36.9) | 35.1 (35.5) | 1.07 | 1.02 |
| n=300 | 34.3 (36.8) | 37.5 (37.6) | 39.3 (40.4) | 1.09 | 1.14 |

#### regen_cpu_s

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 1.84 (1.94) | 4.55 (4.69) | 29.3 (62) | 2.48 | 15.9 |
| n=300 | 3.3 (3.49) | 9.63 (10) | 209 (228) | 2.92 | 63.3 |

#### regen_wall_s

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 1.13 (1.84) | 1.88 (2.08) | 5.83 (8.86) | 1.67 | 5.17 |
| n=300 | 1.71 (3.99) | 3 (3.16) | 29.9 (30.4) | 1.75 | 17.5 |

#### rebuild_cpu_s

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 18.4 (21.7) | 81.4 (81.4) | 62.3 (63) | 4.42 | 3.38 |
| n=300 | 34.2 (40.4) | 257 (266) | 168 (178) | 7.52 | 4.92 |

#### rebuild_wall_s

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 5.42 (8.4) | 22.8 (23.1) | 19.5 (19.8) | 4.21 | 3.59 |
| n=300 | 9.81 (22.5) | 77.7 (78.2) | 53.5 (69.1) | 7.92 | 5.46 |

#### rebuild_rerun

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 21 (21) | 125 (125) | 23 (23) | 5.95 | 1.1 |
| n=300 | 21 (21) | 325 (325) | 23 (23) | 15.5 | 1.1 |

#### rebuild_rerun_share

lower is better.

rebuild_rerun ÷ (rebuild_rerun + rebuild_cached): the fraction of the build's actions that the edit reran. The count of cached actions is not a metric, because it grows with the number of packages, not with a faster rebuild.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 0.089 (0.089) | 0.287 (0.287) | 0.0714 (0.0714) | 3.23 | 0.803 |
| n=300 | 0.089 (0.089) | 0.389 (0.389) | 0.0441 (0.0441) | 4.37 | 0.495 |

#### rebuild_relinks

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 20 (20) | 20 (20) | 20 (20) | 1 | 1 |
| n=300 | 20 (20) | 20 (20) | 20 (20) | 1 | 1 |

#### gen_lines

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 16510 (16510) | 432468 (432468) | 380968 (380968) | 26.2 | 23.1 |
| n=300 | 49550 (49550) | 1296808 (1296808) | 1141879 (1141879) | 26.2 | 23 |

#### gen_bytes

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 436509 (436509) | 11986293 (11986293) | 10799014 (10799014) | 27.5 | 24.7 |
| n=300 | 1328453 (1328453) | 36186809 (36186809) | 32625009 (32625009) | 27.2 | 24.6 |

#### service_binary_mb

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 8.39 (8.39) | 23.8 (23.8) | 13.7 (13.7) | 2.84 | 1.64 |
| n=300 | 8.39 (8.39) | 52.8 (52.8) | 21.9 (21.9) | 6.29 | 2.61 |

#### link_peak_mb

lower is better.

| size | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| n=100 | 203 (203) | 594 (596) | 400 (401) | 2.92 | 1.97 |
| n=300 | 238 (238) | 1415 (1424) | 786 (798) | 5.95 | 3.31 |

### Runtime: postgres

#### sec/op (postgres)

Lower is better.

| benchmark | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| InsertOne | 0.000159 (0.000166) | 0.00016 (0.000168) | 0.000162 (0.000187) | 1 | 1.02 |
| InsertBulk100 ¹ | 0.00106 (0.00112) | 0.00118 (0.00125) | 0.00121 (0.00127) | 1.11 | 1.14 |
| GetByID | 0.000155 (0.000161) | 0.000148 (0.000157) | 0.000144 (0.000153) | 0.954 | 0.927 |
| Query100 | 0.000961 (0.00102) | 0.000363 (0.000435) | 0.000407 (0.000422) | 0.378 | 0.424 |
| EagerO2M100 | 0.00432 (0.00454) | 0.00125 (0.00132) | 0.00137 (0.0014) | 0.288 | 0.317 |
| UpdateByID | 0.000155 (0.000163) | 0.00015 (0.000163) | 0.000155 (0.000159) | 0.967 | 0.996 |
| Count | 0.000171 (0.000176) | 0.000165 (0.000172) | 0.000175 (0.00018) | 0.965 | 1.02 |

¹ InsertBulk100 is not the same work in every ORM: velox2's `InsertAll` reads no keys back, while ent and old velox send `INSERT ... RETURNING id`, scan the 100 ids and return 100 entities. The difference is by design and cannot be removed: ent has no bulk insert without the read-back. A velox2 advantage in this row is partly the advantage of doing less.

#### B/op (postgres)

Lower is better.

| benchmark | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| InsertOne | 2391 (2391) | 3839 (3839) | 3847 (3847) | 1.61 | 1.61 |
| InsertBulk100 ¹ | 246984 (246988) | 396794 (396795) | 409123 (409126) | 1.61 | 1.66 |
| GetByID | 1896 (1896) | 4392 (4392) | 4856 (4856) | 2.32 | 2.56 |
| Query100 | 27729 (27729) | 40312 (40312) | 64288 (64288) | 1.45 | 2.32 |
| EagerO2M100 | 453957 (453958) | 251709 (251709) | 394702 (394703) | 0.554 | 0.869 |
| UpdateByID | 1088 (1088) | 2728 (2728) | 2664 (2664) | 2.51 | 2.45 |
| Count | 1576 (1576) | 3096 (3096) | 3176 (3176) | 1.96 | 2.02 |

¹ InsertBulk100 is not the same work in every ORM: velox2's `InsertAll` reads no keys back, while ent and old velox send `INSERT ... RETURNING id`, scan the 100 ids and return 100 entities. The difference is by design and cannot be removed: ent has no bulk insert without the read-back. A velox2 advantage in this row is partly the advantage of doing less.

#### allocs/op (postgres)

Lower is better.

| benchmark | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| InsertOne | 35 (35) | 62 (62) | 90 (90) | 1.77 | 2.57 |
| InsertBulk100 ¹ | 2062 (2062) | 2884 (2884) | 5492 (5492) | 1.4 | 2.66 |
| GetByID | 35 (35) | 80 (80) | 121 (121) | 2.29 | 3.46 |
| Query100 | 835 (835) | 896 (896) | 1840 (1840) | 1.07 | 2.2 |
| EagerO2M100 | 5197 (5197) | 4676 (4676) | 10160 (10160) | 0.9 | 1.95 |
| UpdateByID | 12 (12) | 45 (45) | 53 (53) | 3.75 | 4.42 |
| Count | 16 (16) | 50 (50) | 66 (66) | 3.12 | 4.12 |

¹ InsertBulk100 is not the same work in every ORM: velox2's `InsertAll` reads no keys back, while ent and old velox send `INSERT ... RETURNING id`, scan the 100 ids and return 100 entities. The difference is by design and cannot be removed: ent has no bulk insert without the read-back. A velox2 advantage in this row is partly the advantage of doing less.

### Runtime: sqlite

#### sec/op (sqlite)

Lower is better.

| benchmark | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| InsertOne | 0.00041 (0.000411) | 0.000408 (0.000424) | 0.000406 (0.000468) | 0.997 | 0.992 |
| InsertBulk100 ¹ | 0.000993 (0.00103) | 0.00101 (0.00105) | 0.00102 (0.00116) | 1.02 | 1.02 |
| GetByID | 2.31e-05 (2.33e-05) | 2.13e-05 (2.15e-05) | 2.21e-05 (2.35e-05) | 0.923 | 0.954 |
| Query100 | 0.000489 (0.000504) | 0.000491 (0.000506) | 0.000515 (0.000543) | 1.01 | 1.05 |
| EagerO2M100 | 0.000984 (0.00101) | 0.000924 (0.000961) | 0.00102 (0.00109) | 0.94 | 1.04 |
| UpdateByID | 1.49e-05 (1.64e-05) | 1.63e-05 (1.65e-05) | 1.64e-05 (1.74e-05) | 1.09 | 1.1 |
| Count | 4.77e-05 (4.87e-05) | 4.9e-05 (4.97e-05) | 5.35e-05 (5.74e-05) | 1.03 | 1.12 |

¹ InsertBulk100 is not the same work in every ORM: velox2's `InsertAll` reads no keys back, while ent and old velox send `INSERT ... RETURNING id`, scan the 100 ids and return 100 entities. The difference is by design and cannot be removed: ent has no bulk insert without the read-back. A velox2 advantage in this row is partly the advantage of doing less.

#### B/op (sqlite)

Lower is better.

| benchmark | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| InsertOne | 3593 (3594) | 5040 (5042) | 5050 (5052) | 1.4 | 1.41 |
| InsertBulk100 ¹ | 136530 (136530) | 296616 (296620) | 306220 (306223) | 2.17 | 2.24 |
| GetByID | 2048 (2048) | 4544 (4544) | 5016 (5016) | 2.22 | 2.45 |
| Query100 | 31064 (31066) | 43656 (43658) | 67632 (67634) | 1.41 | 2.18 |
| EagerO2M100 | 462074 (462079) | 259513 (259518) | 402122 (402122) | 0.562 | 0.87 |
| UpdateByID | 1457 (1457) | 3017 (3017) | 3041 (3041) | 2.07 | 2.09 |
| Count | 1736 (1736) | 3272 (3272) | 3368 (3368) | 1.88 | 1.94 |

¹ InsertBulk100 is not the same work in every ORM: velox2's `InsertAll` reads no keys back, while ent and old velox send `INSERT ... RETURNING id`, scan the 100 ids and return 100 entities. The difference is by design and cannot be removed: ent has no bulk insert without the read-back. A velox2 advantage in this row is partly the advantage of doing less.

#### allocs/op (sqlite)

Lower is better.

| benchmark | velox2 | old velox | ent | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|
| InsertOne | 54 (54) | 80 (80) | 103 (103) | 1.48 | 1.91 |
| InsertBulk100 ¹ | 1557 (1557) | 2883 (2883) | 4490 (4490) | 1.85 | 2.88 |
| GetByID | 42 (42) | 87 (87) | 127 (127) | 2.07 | 3.02 |
| Query100 | 941 (941) | 1002 (1002) | 1945 (1945) | 1.06 | 2.07 |
| EagerO2M100 | 5800 (5800) | 5279 (5279) | 10662 (10662) | 0.91 | 1.84 |
| UpdateByID | 16 (16) | 49 (49) | 56 (56) | 3.06 | 3.5 |
| Count | 20 (20) | 54 (54) | 70 (70) | 2.7 | 3.5 |

¹ InsertBulk100 is not the same work in every ORM: velox2's `InsertAll` reads no keys back, while ent and old velox send `INSERT ... RETURNING id`, scan the 100 ids and return 100 entities. The difference is by design and cannot be removed: ent has no bulk insert without the read-back. A velox2 advantage in this row is partly the advantage of doing less.

### Where velox2 loses

Every metric in which another ORM's median beats velox2's. There is no noise threshold: a difference of any size is listed.

- build, n=100, rebuild_rerun_share: ent 0.0714 vs velox2 0.089 (ent ÷ velox2 = 0.803, lower is better)
- build, n=300, rebuild_rerun_share: ent 0.0441 vs velox2 0.089 (ent ÷ velox2 = 0.495, lower is better)
- runtime, postgres, Count, sec/op: old velox 0.000165 vs velox2 0.000171 (old velox ÷ velox2 = 0.965, lower is better)
- runtime, postgres, EagerO2M100, B/op: old velox 251709 vs velox2 453957 (old velox ÷ velox2 = 0.554, lower is better)
- runtime, postgres, EagerO2M100, B/op: ent 394702 vs velox2 453957 (ent ÷ velox2 = 0.869, lower is better)
- runtime, postgres, EagerO2M100, allocs/op: old velox 4676 vs velox2 5197 (old velox ÷ velox2 = 0.9, lower is better)
- runtime, postgres, EagerO2M100, sec/op: old velox 0.00125 vs velox2 0.00432 (old velox ÷ velox2 = 0.288, lower is better)
- runtime, postgres, EagerO2M100, sec/op: ent 0.00137 vs velox2 0.00432 (ent ÷ velox2 = 0.317, lower is better)
- runtime, postgres, GetByID, sec/op: old velox 0.000148 vs velox2 0.000155 (old velox ÷ velox2 = 0.954, lower is better)
- runtime, postgres, GetByID, sec/op: ent 0.000144 vs velox2 0.000155 (ent ÷ velox2 = 0.927, lower is better)
- runtime, postgres, Query100, sec/op: old velox 0.000363 vs velox2 0.000961 (old velox ÷ velox2 = 0.378, lower is better)
- runtime, postgres, Query100, sec/op: ent 0.000407 vs velox2 0.000961 (ent ÷ velox2 = 0.424, lower is better)
- runtime, postgres, UpdateByID, sec/op: old velox 0.00015 vs velox2 0.000155 (old velox ÷ velox2 = 0.967, lower is better)
- runtime, postgres, UpdateByID, sec/op: ent 0.0001548 vs velox2 0.0001555 (ent ÷ velox2 = 0.996, lower is better)
- runtime, sqlite, EagerO2M100, B/op: old velox 259513 vs velox2 462074 (old velox ÷ velox2 = 0.562, lower is better)
- runtime, sqlite, EagerO2M100, B/op: ent 402122 vs velox2 462074 (ent ÷ velox2 = 0.87, lower is better)
- runtime, sqlite, EagerO2M100, allocs/op: old velox 5279 vs velox2 5800 (old velox ÷ velox2 = 0.91, lower is better)
- runtime, sqlite, EagerO2M100, sec/op: old velox 0.000924 vs velox2 0.000984 (old velox ÷ velox2 = 0.94, lower is better)
- runtime, sqlite, GetByID, sec/op: old velox 2.13e-05 vs velox2 2.31e-05 (old velox ÷ velox2 = 0.923, lower is better)
- runtime, sqlite, GetByID, sec/op: ent 2.21e-05 vs velox2 2.31e-05 (ent ÷ velox2 = 0.954, lower is better)
- runtime, sqlite, InsertOne, sec/op: old velox 0.000408 vs velox2 0.00041 (old velox ÷ velox2 = 0.997, lower is better)
- runtime, sqlite, InsertOne, sec/op: ent 0.000406 vs velox2 0.00041 (ent ÷ velox2 = 0.992, lower is better)
