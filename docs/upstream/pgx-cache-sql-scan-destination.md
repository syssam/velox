Draft only; not filed.

# Issue: stdlib: cache isSQLScanDestination per reflect.Type

## What happens

Under Go 1.27, database/sql hands each column to the driver's
`driver.RowsColumnScanner`. The cost is new with that path: the driver's
`Next` path, which database/sql used before, never ran the check. `stdlib.(*Rows).ScanColumn`
(stdlib/sql_go1.27.go, pgx v5.11.0) calls `isSQLScanDestination(rv.Type())`
on every column of every row, and the result is not cached.

The function calls `reflect.Type.Implements` up to three times per pointer
level. `Implements` resolves the name of every method of the type, so its
cost grows with the method set. For `*time.Time` a call takes about 8 µs on
an Apple M3 Max (7.5–10.2 µs over 200,000 calls, Go 1.27.1); for `*string`
or `*int64` it takes 44–64 ns, and for `*sql.NullTime` 28–36 ns.

In a CPU profile of a database/sql query reading 100 rows of 8 columns, one
of them a `timestamptz` scanned into `*time.Time`, `isSQLScanDestination`
takes 33.5% of the samples, nearly all of it in `reflect.implements`.
Scanning the same column into an `sql.Scanner` halves the query's time
(2.03 ms to 1.05 ms, at a high and varying machine load).

## Reproduction

```go
rows, err := db.QueryContext(ctx,
	`SELECT now() - g * interval '1 minute' FROM generate_series(1, 100) g`)
if err != nil {
	return err
}
defer rows.Close()
for rows.Next() {
	var t time.Time // slow: isSQLScanDestination on every row
	// var t sql.NullTime // fast: passes the Scanner check at once
	if err := rows.Scan(&t); err != nil {
		return err
	}
}
return rows.Err()
```

Benchmark the loop with `db` from `sql.Open("pgx", dsn)`, once with
`*time.Time` and once with `*sql.NullTime`.

## Proposal

Put a cache in front of the function:

```go
var sqlScanDestinationCache sync.Map // reflect.Type -> bool

func isSQLScanDestinationCached(t reflect.Type) bool {
	if v, ok := sqlScanDestinationCache.Load(t); ok {
		return v.(bool)
	}
	b := isSQLScanDestination(t)
	sqlScanDestinationCache.Store(t, b)
	return b
}
```

and call it from `ScanColumn`. The result depends only on the type, so it is
safe to cache, and the set of destination types a program uses is small and
fixed. The named-pointer cycle guard stays inside the uncached function.

## Workaround

Callers can avoid the cost today by scanning into an `sql.Scanner`, which
passes the first check at once. velox2 now does this on pgx's stdlib driver:
it wraps each destination in a `Scanner` that calls `sql.ConvertAssign`, so
values and errors are the same as before.

# PR sketch

- stdlib/sql_go1.27.go: add `isSQLScanDestinationCached` as above and call it
  from `ScanColumn`.
- stdlib/sql_go1.27_test.go: a test that the cached and uncached functions
  agree on the types the existing tests cover, and a benchmark of
  `ScanColumn` into `*time.Time` that shows the call no longer dominates.
