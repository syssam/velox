# Review: the memory of a to-many load

2026-10-30, branch `eager-mem` (4e4e85a, 7cefa0f, 9fbf19e, b19178f).

After the PostgreSQL scan-path fix (`2026-10-29-pg-scan-path.md`), the one
runtime metric where velox2 was well behind was EagerO2M100's memory: 100
invoices with their 500 items, through `With`, at 1.8 times old velox's bytes
on both databases. It is now under old velox's and half of ent's.

## What changed

- **The scan's rows never move** (4e4e85a). A relation's rows are scanned into
  arrays of the row type, 4 rows and then twice the one before up to 1,024,
  that are never grown or copied (`blocks`, load.go). A to-many load's owners
  got their rows in one new array between them, not one slice each.
- **An owner whose rows were read in a run takes them in place** (9fbf19e).
  When an owner's rows lie one after another in one of those arrays, its slice
  is that run of the array (`inPlace`); only the other owners' rows are copied.
  A run never spans two arrays, so a slice keeps alive the one array it is in.
  No row is in place for two owners: an owner whose rows another owner has in
  place gets a copy.
- **Owner keys group their rows in one array** (9fbf19e). Each key's rows, as
  indexes into the targets, come from one counting pass into one buffer
  (`keyRows.fill`), in place of a slice grown per key. The key map is kept out
  of that struct so that escape analysis leaves it on the stack.

Every owner's slice is capped at its length, so an append to it copies first.
What a row kept from a to-many load keeps alive grew: the arrays the rows were
read into, up to 1,024 rows each, and the one the rest were copied to. The
`LoadEach` documentation and the CHANGELOG say so; copying a row keeps it alone.

## Results

The three-ORM runtime comparison (`COMPARE_STEPS=runtime bench/compare/run.sh`,
results `20261030T090109Z`, at b19178f), against the run of the PG fix
(`20261029T173316Z`, at 7a55762). Bytes and allocations are exact; the times
are not compared, as below.

B/op, EagerO2M100:

| | velox2 | velox2 (OpenPgx) | old velox | ent |
|---|---|---|---|---|
| sqlite, before | 462,070 | | 259,513 | 402,122 |
| sqlite, after | 209,188 | | 259,516 | 402,124 |
| postgres, before | 454,924 | 409,584 | 251,709 | 394,703 |
| postgres, after | 202,040 | 156,699 | 251,707 | 394,701 |

allocs/op, EagerO2M100:

| | velox2 | velox2 (OpenPgx) | old velox | ent |
|---|---|---|---|---|
| sqlite, before | 5,800 | | 5,279 | 10,662 |
| sqlite, after | 5,305 | | 5,279 | 10,662 |
| postgres, before | 5,203 | 2,470 | 4,676 | 10,160 |
| postgres, after | 4,708 | 1,975 | 4,676 | 10,160 |

velox2's bytes fell 55% on both databases, 62% with `OpenPgx`; old velox now
uses 1.24 to 1.25 times velox2's bytes, ent 1.92 to 1.95 times. Allocations
are within 0.7% of old velox's (32 more on PostgreSQL, 26 on SQLite), and with
`OpenPgx` 42% of them.

`BenchmarkScan/sqlite/EagerO2M100` (internal/itest), on the way:

| | B/op | allocs/op |
|---|---|---|
| main (f41fa5d) | 261,168 | 4,699 |
| 4e4e85a, the arrays that never move | 190,839 | 4,606 |
| 9fbf19e, rows in place, keys grouped | 151,605 | 4,204 |

Query100 (49,029 B/op, 1,439 allocs) and Items100 (11,512, 425) did not move.

## What it cost

- **Compile cost.** `entityDesc` measures 132,904 bytes (130,104 before
  9fbf19e, 132,386 on main), `entityDescUse` 749,204 and `entityDescHooks`
  1,038,290, all under their bounds. Taking the scratch flags of `inPlace` out
  of the generic `pick` saved 750 of the 2,800 bytes and cost an allocation on
  every to-many load; they stay in `pick`, on its stack for a small load.
- **Allocation budgets.** They fell: an All with one With load 41 → 40,
  LoadEach 33 → 30; the test holds them at those numbers.

## Limits

- The gain of the rows in place depends on the order the rows are read in.
  The benchmarks, like most applications, insert an owner's children together,
  and the load orders them by primary key, so they arrive in runs. Rows read
  out of turn, by an `OrderBy` on another column or ids not grouped by owner,
  are copied as before: still in one array, still under 4e4e85a's numbers.
  `TestWithManyOwnersAcrossBlocks` covers both orders.
- The times were measured at a load of 34 to 48 on a shared machine, with
  spreads up to three times the median; Query100 on SQLite, whose path did not
  change, moved by 15% between the two runs. They are not a result in either
  direction. Bytes and allocations do not depend on the load.

## Verification

- `scripts/ci-local.sh` passed at b19178f: rc 0 in 21 minutes, generated code
  clean. At 7cefa0f the GraphQL scale step went over a threshold twice under
  load (34.57× edit-and-rebuild, then 33.20× cold), each figure under its
  threshold in the other run; the run at b19178f passed it.
- `go test -race . ./internal/...` and `golangci-lint` (0 issues) at b19178f.
- A review of 9fbf19e found nothing Critical or Important; its fixes are
  b19178f: `inPlace` checks the run without assuming the indexes rise, the
  documentation says what an owner in place keeps, and a many-to-many test
  writes through one owner's rows and reads the other's.
