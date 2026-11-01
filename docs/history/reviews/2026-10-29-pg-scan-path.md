# The PostgreSQL scan path after the fix, with OpenPgx

Run on 2026-10-29, from 17:33 to 17:45 UTC, with
`COMPARE_STEPS=runtime bench/compare/run.sh` at velox2 commit 7a55762.
Every number of this run comes from its `summary.json`, `report-tables.md`
and benchstat files. The root benchmark's numbers come from its own
benchstat file. The figures before the fix come from
[the earlier review](2026-10-29-perf-comparison.md). The raw results are not
committed.

## Summary

On PostgreSQL through database/sql and pgx's stdlib, velox2's Query100 went
from 961 µs to 415 µs, and its EagerO2M100 from 4.32 ms to 1.42 ms. In the
earlier run old velox took 0.378× and 0.288× of velox2's time, and now it
takes 0.913× and 0.874×. ent took 0.424× and 0.317×, and now it takes 0.957×
and 0.958×. benchstat calls none of these four new differences significant,
because this run's PostgreSQL samples are noisy (see Machine and load). SQLite
did not move: Query100 is 483 µs and EagerO2M100 985 µs, against 489 µs and
984 µs before. velox2 on `OpenPgx` has the lowest median of the four on
Query100 (306 µs, 0.738× of velox2 on database/sql, not significant) and on
EagerO2M100 (958 µs, 0.676×, p=0.041), and it makes a quarter of the
allocations on Query100.

## What changed

In Go 1.27, database/sql hands every column of every row to pgx's
`stdlib.(*Rows).ScanColumn`, which first runs `isSQLScanDestination`, an
uncached `reflect.Type.Implements` check on the destination's type. The check
resolves the name of every method of the type, and `time.Time`'s method set
is large, so a `*time.Time` destination costs microseconds per column per
row. ent and old velox scan into `sql.Scanner` types, which pass the check at
its first step, while velox2 scanned into plain field pointers.

Now, on a `*sql.DB` whose driver is pgx's stdlib, velox2 reads rows through
`colRows`, whose `Scan` hands pgx a `colScanner` (an `sql.Scanner` that calls
`sql.ConvertAssign` on the field) in place of each destination pgx would
convert that way. The decision is record
[0018](../../decisions/0018-pgx-stdlib-scan-wrapper.md), and the change is
commit f023d4b, with the scanner from 399d7fe.

## Machine and load

- Apple M3 Max, 14 CPUs, 36 GB, Darwin 27.0.0, Go 1.27.1, `GOFLAGS=-p=2`.
- velox2 7a55762; old velox v0.3.1-0.20261028051754-a7d44fb423b0; ent
  v0.14.6; modernc.org/sqlite v1.60.1; pgx v5.11.0, stdlib and pgxpool.
- A shared machine, not an idle one. The 1-minute load before or after a step
  was between 4.05 and 25.31. During the benchmark steps it was between 5.46
  and 11.69 on SQLite and between 4.05 and 22.26 on PostgreSQL.
- Each round waited at a load gate of 12. None timed out. gate-sqlite-1
  waited 50 seconds and gate-postgres-3 70 seconds; the others passed at
  once.
- PostgreSQL's second round passed its gate at 11.22, and the load rose to
  22.26 while it ran. benchstat's spread on PostgreSQL sec/op is ±25% to
  ±1712%. On SQLite it is ±1% to ±47%, except ent's InsertOne (±270%) and
  InsertBulk100 (±472%). The medians stand, but on PostgreSQL only two sec/op
  differences are significant.

## Method

The earlier review's runtime method (its Method section, Runtime to Rounds),
with these changes:

- **Runtime only.** The build steps were not run (`COMPARE_STEPS=runtime`).
- **A fourth PostgreSQL column, `velox2 (OpenPgx)`.** It is velox2's runtime
  app on `velox.OpenPgx`, on a pgx pool with `pool_max_conns=1`, against the
  same database and schema as velox2. It is rotated with the other three in
  each of the six rounds. A probe before any timer checks that its ops reach
  the pool and not database/sql. It is velox2 too, so it is never counted as
  a loss.
- **The root benchmark.** `BenchmarkScan` (internal/itest) reads 100 rows
  (Query100), 100 parents with their items (EagerO2M100) and 100 rows with no
  time column (Items100) on each database: SQLite, PostgreSQL through
  database/sql and pgx's stdlib (`postgres-sql`), PostgreSQL on `OpenPgx`
  (`postgres-pgx`) and MySQL. Six runs each, before and after the fix.

## Runtime

sec/op, the median of six samples (ratio = other ÷ velox2; p-values from
`benchstat-<dialect>.txt` where benchstat calls the difference significant,
p < 0.05):

| benchmark | dialect | velox2 | velox2 (OpenPgx) | old velox | ent | OpenPgx ÷ velox2 | old velox ÷ velox2 | ent ÷ velox2 |
|---|---|---|---|---|---|---|---|---|
| InsertOne | sqlite | 430 µs | | 445 µs | 454 µs | | 1.04 | 1.05 |
| InsertBulk100 ¹ | sqlite | 1.06 ms | | 1.12 ms | 1.13 ms | | 1.06 | 1.07 |
| GetByID | sqlite | 23.5 µs | | 22.0 µs | 22.2 µs | | 0.935 | 0.947 (p=0.002) |
| Query100 | sqlite | 483 µs | | 490 µs | 501 µs | | 1.01 | 1.04 (p=0.004) |
| EagerO2M100 | sqlite | 985 µs | | 923 µs | 1.01 ms | | 0.937 (p=0.002) | 1.02 |
| UpdateByID | sqlite | 14.9 µs | | 16.1 µs | 16.3 µs | | 1.08 | 1.10 |
| Count | sqlite | 47.4 µs | | 48.6 µs | 53.4 µs | | 1.03 (p=0.041) | 1.13 (p=0.002) |
| InsertOne | postgres | 165 µs | 160 µs | 164 µs | 167 µs | 0.971 | 0.989 | 1.01 |
| InsertBulk100 ¹ | postgres | 971 µs | 940 µs | 1.05 ms | 1.13 ms | 0.968 | 1.08 | 1.17 (p=0.041) |
| GetByID | postgres | 140 µs | 153 µs | 161 µs | 156 µs | 1.09 | 1.15 | 1.12 |
| Query100 | postgres | 415 µs | 306 µs | 378 µs | 397 µs | 0.738 | 0.913 | 0.957 |
| EagerO2M100 | postgres | 1.42 ms | 958 µs | 1.24 ms | 1.36 ms | 0.676 (p=0.041) | 0.874 | 0.958 |
| UpdateByID | postgres | 158 µs | 141 µs | 156 µs | 161 µs | 0.891 | 0.984 | 1.02 |
| Count | postgres | 166 µs | 176 µs | 161 µs | 178 µs | 1.06 | 0.975 | 1.07 |

¹ ent and old velox read 100 ids back with `RETURNING`, and velox2 reads
none, so part of velox2's lead on this row is doing less.

B/op (every difference here is significant, p=0.002):

| benchmark | sqlite: velox2 / old velox / ent | postgres: velox2 / OpenPgx / old velox / ent |
|---|---|---|
| InsertOne | 3,592 / 5,041 / 5,049 | 2,519 / 1,708 / 3,839 / 3,847 |
| InsertBulk100 ¹ | 136,530 / 296,620 / 306,220 | 246,984 / 205,998 / 396,795 / 409,122 |
| GetByID | 2,048 / 4,544 / 5,016 | 2,360 / 1,284 / 4,392 / 4,856 |
| Query100 | 31,064 / 43,656 / 67,632 | 28,240 / 19,940 / 40,312 / 64,288 |
| EagerO2M100 | 462,070 / 259,513 / 402,122 | 454,924 / 409,584 / 251,709 / 394,703 |
| UpdateByID | 1,457 / 3,017 / 3,041 | 1,088 / 946 / 2,728 / 2,664 |
| Count | 1,736 / 3,272 / 3,368 | 1,704 / 1,235 / 3,096 / 3,176 |

allocs/op (every difference here is significant, p=0.002):

| benchmark | sqlite: velox2 / old velox / ent | postgres: velox2 / OpenPgx / old velox / ent |
|---|---|---|
| InsertOne | 54 / 80 / 103 | 38 / 27 / 62 / 90 |
| InsertBulk100 ¹ | 1,557 / 2,883 / 4,490 | 2,062 / 2,058 / 2,884 / 5,492 |
| GetByID | 42 / 87 / 127 | 38 / 14 / 80 / 121 |
| Query100 | 941 / 1,002 / 1,945 | 838 / 215 / 896 / 1,840 |
| EagerO2M100 | 5,800 / 5,279 / 10,662 | 5,203 / 2,470 / 4,676 / 10,160 |
| UpdateByID | 16 / 49 / 56 | 12 / 8 / 45 / 53 |
| Count | 20 / 54 / 70 | 19 / 8 / 50 / 66 |

On PostgreSQL through database/sql, velox2 makes 3 more allocations per query
than in the earlier run (35 → 38 on GetByID and InsertOne, 835 → 838 on
Query100, 16 → 19 on Count) and 6 more on EagerO2M100, which sends two queries
(5,197 → 5,203). That is `colRows` and its two slices, as record 0018 says.
UpdateByID reads no rows and is unchanged at 12. velox2's B/op rose too
(1,896 → 2,360 on GetByID). SQLite's allocs/op are the earlier run's, and its
B/op within 4 bytes of them.

The root benchmark, `BenchmarkScan`, before and after the fix. The before run
was at a 1-minute load of 12 to 30, the after run at 5 to 9, so every
difference is partly load; the paths the fix does not touch (`sqlite`,
`postgres-pgx`, `mysql`) moved by 1.5% to 41%, most likely from the load, and
their B/op and allocs/op did not change:

```
                                 │    before     │                after                │
                                 │    sec/op     │    sec/op     vs base               │
Scan/sqlite/Query100-14             282.5µ ±  3%   278.3µ ±  1%   -1.50% (p=0.009 n=6)
Scan/sqlite/EagerO2M100-14          757.9µ ±  3%   740.5µ ±  5%   -2.29% (p=0.041 n=6)
Scan/sqlite/Items100-14             66.42µ ±  3%   65.24µ ±  1%   -1.79% (p=0.009 n=6)
Scan/postgres-pgx/Query100-14       249.3µ ± 11%   216.7µ ±  3%  -13.10% (p=0.002 n=6)
Scan/postgres-pgx/EagerO2M100-14    814.3µ ± 13%   758.4µ ± 13%   -6.87% (p=0.041 n=6)
Scan/postgres-pgx/Items100-14       195.1µ ± 20%   182.8µ ± 14%        ~ (p=0.394 n=6)
Scan/postgres-sql/Query100-14      1710.2µ ±  8%   325.9µ ±  4%  -80.94% (p=0.002 n=6)
Scan/postgres-sql/EagerO2M100-14    3.560m ± 31%   1.058m ± 10%  -70.29% (p=0.002 n=6)
Scan/postgres-sql/Items100-14       318.3µ ± 73%   227.2µ ± 11%  -28.62% (p=0.002 n=6)
Scan/mysql/Query100-14              578.8µ ± 23%   410.3µ ±  3%  -29.11% (p=0.002 n=6)
Scan/mysql/EagerO2M100-14           1.436m ± 32%   1.126m ±  9%  -21.57% (p=0.002 n=6)
Scan/mysql/Items100-14              526.5µ ± 44%   313.1µ ± 12%  -40.54% (p=0.002 n=6)
geomean                             528.8µ         359.0µ        -32.11%

                                 │    before    │                after                 │
                                 │     B/op     │     B/op      vs base                │
Scan/sqlite/Query100-14            47.87Ki ± 0%   47.87Ki ± 0%       ~ (p=0.924 n=6)
Scan/sqlite/EagerO2M100-14         255.0Ki ± 0%   255.0Ki ± 0%       ~ (p=0.177 n=6)
Scan/sqlite/Items100-14            11.24Ki ± 0%   11.24Ki ± 0%       ~ (p=1.000 n=6) ¹
Scan/postgres-pgx/Query100-14      25.39Ki ± 0%   25.39Ki ± 0%       ~ (p=0.892 n=6)
Scan/postgres-pgx/EagerO2M100-14   220.8Ki ± 0%   220.8Ki ± 0%       ~ (p=0.673 n=6)
Scan/postgres-pgx/Items100-14      7.636Ki ± 0%   7.637Ki ± 0%       ~ (p=1.000 n=6)
Scan/postgres-sql/Query100-14      40.95Ki ± 0%   41.58Ki ± 0%  +1.53% (p=0.002 n=6)
Scan/postgres-sql/EagerO2M100-14   259.5Ki ± 0%   260.4Ki ± 0%  +0.35% (p=0.002 n=6)
Scan/postgres-sql/Items100-14      11.52Ki ± 0%   11.80Ki ± 0%  +2.44% (p=0.002 n=6)
Scan/mysql/Query100-14             40.18Ki ± 0%   40.18Ki ± 0%       ~ (p=1.000 n=6)
Scan/mysql/EagerO2M100-14          258.9Ki ± 0%   258.9Ki ± 0%       ~ (p=0.848 n=6)
Scan/mysql/Items100-14             12.30Ki ± 0%   12.30Ki ± 0%       ~ (p=1.000 n=6) ¹
geomean                            46.09Ki        46.26Ki       +0.36%
¹ all samples are equal

                                 │   before    │                after                │
                                 │  allocs/op  │  allocs/op   vs base                │
Scan/sqlite/Query100-14            1.439k ± 0%   1.439k ± 0%       ~ (p=1.000 n=6) ¹
Scan/sqlite/EagerO2M100-14         4.699k ± 0%   4.699k ± 0%       ~ (p=1.000 n=6) ¹
Scan/sqlite/Items100-14             425.0 ± 0%    425.0 ± 0%       ~ (p=1.000 n=6) ¹
Scan/postgres-pgx/Query100-14       350.0 ± 0%    350.0 ± 0%       ~ (p=1.000 n=6) ¹
Scan/postgres-pgx/EagerO2M100-14   2.103k ± 0%   2.103k ± 0%       ~ (p=1.000 n=6) ¹
Scan/postgres-pgx/Items100-14       109.0 ± 0%    109.0 ± 0%       ~ (p=1.000 n=6) ¹
Scan/postgres-sql/Query100-14      1.207k ± 0%   1.210k ± 0%  +0.25% (p=0.002 n=6)
Scan/postgres-sql/EagerO2M100-14   4.475k ± 0%   4.481k ± 0%  +0.13% (p=0.002 n=6)
Scan/postgres-sql/Items100-14       424.0 ± 0%    427.0 ± 0%  +0.71% (p=0.002 n=6)
Scan/mysql/Query100-14             1.101k ± 0%   1.101k ± 0%       ~ (p=1.000 n=6) ¹
Scan/mysql/EagerO2M100-14          4.365k ± 0%   4.365k ± 0%       ~ (p=1.000 n=6) ¹
Scan/mysql/Items100-14              427.0 ± 0%    427.0 ± 0%       ~ (p=1.000 n=6) ¹
geomean                            1.007k        1.008k       +0.09%
¹ all samples are equal
```

## Where velox2 still loses

Each line is a metric where another ORM's median beats velox2's, with no
noise threshold, from the generated list in `report-tables.md`. It is grouped
by cause where the data shows one.

- **PostgreSQL, Query100 and EagerO2M100, sec/op.** Old velox takes 0.913× and
  0.874× of velox2's time, and ent 0.957× and 0.958×. None of the four is
  significant (p=0.394, 0.240, 0.589, 0.394). The earlier run's 0.378× to
  0.424× on Query100 and 0.288× to 0.317× on EagerO2M100 are no longer seen:
  what remains is within this run's noise. velox2 still makes fewer
  allocations than old velox on Query100 (838 to 896), so allocation does not
  explain a remaining gap, and the data shows no other cause.
- **EagerO2M100, B/op and allocs/op, both dialects.** velox2 allocates 1.8×
  old velox's bytes (454,924 to 251,709 on PostgreSQL, 462,070 to 259,513 on
  SQLite) and 1.15× ent's; old velox also makes about 10% fewer allocations
  (4,676 to 5,203, and 5,279 to 5,800). This is the earlier run's loss,
  unchanged, and this run does not show its cause either.
- **EagerO2M100 on SQLite, sec/op.** Old velox takes 0.937× of velox2's time
  (p=0.002), as before. ent is level (1.02).
- **GetByID on SQLite, sec/op.** Old velox takes 0.935× of velox2's time (not
  significant, p=0.093) and ent 0.947× (p=0.002). velox2 allocates less than
  half of either; the data shows no cause. On PostgreSQL velox2 is now the
  fastest of the three on GetByID, though not significantly.
- **PostgreSQL Count, UpdateByID and InsertOne, sec/op, against old velox.**
  Ratios of 0.975, 0.984 and 0.989, none significant.

## Limits

- **One shared machine.** A shared macOS laptop, not an idle server, at a
  1-minute load of 4.05 to 25.31 during the run. The rotated rounds share the
  load between the ORMs, but PostgreSQL's second round ran at a load up to
  22.26, and its spreads are wide (up to ±1712%). A rerun on a quiet machine or
  on Linux CI hardware would resolve the remaining PostgreSQL differences
  that this run cannot.
- **MySQL** is only in the root benchmark, which shows no change in its B/op
  or allocs/op. bench/compare does not run it.
- **One connection.** Every benchmark runs on one connection
  (`SetMaxOpenConns(1)`, and `pool_max_conns=1` for `OpenPgx`). Concurrency
  is not measured.
- **Before and after are two runs.** The headline 961 µs → 415 µs and 4.32 ms
  → 1.42 ms compare the earlier run with this one, at different loads; the
  same-run ratios against old velox and ent are the fair comparison.
- **The root benchmark's before and after runs had different loads** (12 to
  30, then 5 to 9), so its percentages overstate the fix somewhat. This run's
  same-run ratios against old velox and ent are the fair comparison.
