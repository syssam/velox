# 0018. On pgx's stdlib driver, scans go through a sql.Scanner

- Status: accepted, 2026-10-30

## Context

On Go 1.27, pgx v5.11.0's stdlib driver implements
`driver.RowsColumnScanner`. Its `ScanColumn` runs `isSQLScanDestination`, an
uncached reflection walk over the destination's type, on every column of
every row, to choose between database/sql's conversion and pgx's own scan
plans. A destination that is a `sql.Scanner` passes that check at its first
step. On `postgres-sql` (database/sql over pgx's stdlib) this made velox2's
100-row query about 7 times slower than on `postgres-pgx`.

## Decision

When a `*sql.DB`'s driver is pgx's stdlib `*Driver` (however the DB was
opened from it: `sql.Open("pgx", …)`, `stdlib.OpenDB`, `stdlib.GetConnector`
or `stdlib.OpenDBFromPool`), `Open` and `SQLConn` read its rows through
`colRows`. Its `Scan` hands the driver a `colScanner` in place of each
destination that `scanWrapsType` wraps: a pointer to a bool, an integer, a
float, a string, a byte slice or a struct that converts to `time.Time`, or a
pointer to one of them, at any depth. `colScanner.Scan` calls
`sql.ConvertAssign` on the field itself, which is what pgx would have done for
it. Every other destination, and every other driver, is scanned as before.

The wrapper sits in the rows the executor reads, not in `scanDst` or in the
generated code, so raw, aggregate and owner-key scans get it too and the
generated code does not change.

## Consequences

- A driver that wraps pgx's (a tracing or metrics wrapper with its own
  `driver.Driver`) is not detected. Its rows are read as before: they are
  slower only if the wrapper's rows forward `driver.RowsColumnScanner` to
  pgx's, and they stay correct.
- A query on pgx's stdlib makes up to 3 more allocations (the rows value and
  its two slices), none per row.
- No generated code changes: the generated scan destinations are unchanged,
  and only the rows they are scanned from differ.
- `TestPgxMirrorIsCurrent` guards only the pgx that velox2's go.mod selects.
  An application can select a newer pgx v5 through MVS. If that pgx narrowed
  `isSQLScanDestination`, velox could wrap a type that pgx no longer converts
  with `ConvertAssign`. A pgx upgrade in velox2 re-runs the mirror test.
  Checking the linked pgx version at run time through `debug.ReadBuildInfo`
  was considered and left out: it would also refuse harmless upgrades.

## Evidence

- `TestPgxStdlibDetected`, `TestSQLConnDetects`,
  `TestSQLiteAndMySQLKeepPlainRows` and `TestBeginKeepsColumnScan`: where
  `colRows` is used, and where not.
- `TestColumnScanMatchesPlain`: 594 cells per database (18 expressions × 33
  destination types) give the same value or the same error, wrapped and
  plain, on SQLite and PostgreSQL.
- `TestColumnScanRowsKeepCallerSlice`, `TestColumnScanAllocsPerQuery`,
  `FuzzColumnScan`, `TestScanWrapsClassification` and
  `TestPgxMirrorIsCurrent`.
- `BenchmarkScan` (internal/integration), 6 runs each on an Apple M3 Max, before at
  load 12–30 and after at load 5–9; benchstat's `postgres-sql` rows:

```
                                 │    before     │                after                │
                                 │    sec/op     │    sec/op     vs base               │
Scan/postgres-sql/Query100-14      1710.2µ ±  8%   325.9µ ±  4%  -80.94% (p=0.002 n=6)
Scan/postgres-sql/EagerO2M100-14    3.560m ± 31%   1.058m ± 10%  -70.29% (p=0.002 n=6)
Scan/postgres-sql/Items100-14       318.3µ ± 73%   227.2µ ± 11%  -28.62% (p=0.002 n=6)

                                 │    before    │                after                 │
                                 │     B/op     │     B/op      vs base                │
Scan/postgres-sql/Query100-14      40.95Ki ± 0%   41.58Ki ± 0%  +1.53% (p=0.002 n=6)
Scan/postgres-sql/EagerO2M100-14   259.5Ki ± 0%   260.4Ki ± 0%  +0.35% (p=0.002 n=6)
Scan/postgres-sql/Items100-14      11.52Ki ± 0%   11.80Ki ± 0%  +2.44% (p=0.002 n=6)

                                 │   before    │                after                │
                                 │  allocs/op  │  allocs/op   vs base                │
Scan/postgres-sql/Query100-14      1.207k ± 0%   1.210k ± 0%  +0.25% (p=0.002 n=6)
Scan/postgres-sql/EagerO2M100-14   4.475k ± 0%   4.481k ± 0%  +0.13% (p=0.002 n=6)
Scan/postgres-sql/Items100-14       424.0 ± 0%    427.0 ± 0%  +0.71% (p=0.002 n=6)
```

  On the unchanged paths (sqlite, mysql, postgres-pgx) B/op and allocs/op
  are identical and no sec/op is worse; their gains are most likely the
  lower load of the after run.
