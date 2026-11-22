# 102. Distinct, upsert UpdateAll (Explain, privacy combinators and trace removed)

Status: accepted, 2026-11-21.

Amended 2026-11-22: `velox.Explain`, `privacy.And`, `Or`, `Not`,
`privacy.WithTrace` and `privacy.TraceEntry` removed; no caller, and ent has
none. To read a plan, take `q.SQL(ctx)` and run `EXPLAIN` through `db.Rows`
(0060). `Distinct` and `UpdateAll` stand. Their Decision bullets and tests
are gone; the Context is as it was.

## Context

The veloxe gap report (§2.2) lists ent and veloxe patterns nextapp does not
use but other applications coming from ent do: `Distinct` (#65), `Explain`
(#77), an upsert that updates every column (#83, ent's `UpdateNewValues`),
and veloxe's `privacy.And`, `Or`, `Not` and rule trace (#104, #106).

## Decision

- `Query.Distinct()` sets `Statement.Distinct`, rendered `SELECT DISTINCT`.
  - It applies to a projection (`Select`, `Calc`, `SelectRaw`, read with
    `Rows`) and to `Values` and `IDs`, which read one column
    (`engine.ColumnRead` marks the statement).
  - On anything else the guard fails the statement before anything is
    sent: a read of entities, `Count`, `Exists`, an aggregate, and `SQL`
    of such a read. An entity read is by key, so its rows are
    distinct already.
  - `Count` with `Distinct` is refused, not rendered `COUNT(DISTINCT …)`.
    `Count` refuses a projection already, and a distinct count is a
    handle's `CountDistinct` in a `Select`.
  - Its `ORDER BY` names only what it reads: a projection's terms
    (compared whole, so an aggregate must be the same aggregate), or the
    one column of `Values`. Anything else fails in the guard on every
    dialect, as PostgreSQL and MySQL refuse it; SQLite would order by an
    arbitrary row of each group.
  - A Rewrite hook may not change it, and it is part of the render
    cache's shape key.
- `InsertQuery.UpdateAll()` is ent's `UpdateNewValues`.
  - `engine.CheckInsertQuery` resolves it into `ConflictClause.Update`
    before any hook sees the clause. Every column is written, except the
    primary key's, `OnConflict`'s columns, the `Immutable` columns and the
    `Version` column, which every upsert bumps. `Setters.Upsert` then
    drops a column whose `SetPtr` was skipped.
  - With no column left, the branch is `Ignore`'s, which keeps the row.
  - 0017's rule is kept: an `UpdateDefault` does not fire. Its column
    takes the inserted row's value, which is its `Default` unless the row
    set it, as in ent.
  - A column `UpdateAll` writes is sent, never left to a database default
    (lean inserts, 0093), as any `Update` column is.
  - `Update` and `DoNothing` after it replace it, and it replaces them.
  - The flag is a field of `InsertQuery`, not of `ConflictClause`, whose
    fields are all exported so that a hook can rebuild it.
## Consequences

`Distinct` and `UpdateAll` cost each entity's package about 1.1 KB in all
(`entityDescUse` 771328 to 772452 bytes).

## Evidence

- `internal/apitest/parity_test.go`: `TestDistinct`,
  `TestDistinctRefusesEntityReads`, `TestDistinctOrder`,
  `TestDistinctRewriteRefused` and `TestUpsertUpdateAll` (three dialects,
  `SetPtr`, Version, Ignore, order with `Update`/`DoNothing`, InsertAll).
- `internal/integration/parity_test.go`: `TestDistinct` and
  `TestUpsertUpdateAll` on SQLite, and on PostgreSQL and MySQL in the gate.
- `internal/repocheck/compilecost_test.go`: the measured comments.
