# 102. Distinct, Explain, upsert UpdateAll, privacy combinators and trace

Status: accepted, 2026-11-21.

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
    or `Explain` of such a read. An entity read is by key, so its rows are
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
- `velox.Explain(ctx, q) (string, error)` returns the dialect's plan.
  - The plan comes from PostgreSQL's `EXPLAIN`, MySQL's
    `EXPLAIN FORMAT=TREE` or SQLite's `EXPLAIN QUERY PLAN`. Its rows'
    text is joined by newlines: SQLite's fourth column, the one column of
    the others.
  - MySQL's tree format is one column of text, as PostgreSQL's plan is.
    MySQL's tabular `EXPLAIN` has twelve columns.
  - The statement takes the normal path, the guard, Rewrite hooks, scope
    (policies, interceptors, `WithFilter`), row cap, render cache and
    Around hooks included. An unexported mark puts the prefix on at
    rendering. `WithLog` therefore masks its arguments as it masks any
    statement's: `ShownArgs` writes the same arguments again.
  - The plan's rows do not count against the row cap.
  - Without `ANALYZE`, nothing runs, so it is read-only.
  - It explains the main statement, not `With` loads.
  - It is a function, not a method of `Query` (0022). As a method, it
    added about 900 bytes to every entity's package, measured on
    `entityDescUse`, for a debugging aid. As a function, it costs nothing
    until called.
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
- `privacy.And`, `Or` and `Not` take `QueryMutationRule`s, with veloxe's
  semantics but one, results returned as they are:
  - `And` returns the first result that is not `Allow`, which may be
    `Skip` or nil; with all allowing, or none given, it returns `Allow`.
  - `Or` returns `Allow` at the first that allows. Else it returns the
    first error that is not a decision (not `Allow`, `Deny`, `Skip`, nil
    or one wrapping them), and failing that the last result; with none
    given it returns `Deny`. This departs from veloxe, whose `Or` returned
    the last result: `Or(failing, Skip)` gave `Skip`, and a list that then
    ran out allowed the statement on a rule that could not decide. velox
    fails closed.
  - `And` already returns a failing rule's error as it is: it is not
    `Allow`. An earlier `Skip` or nil ends it, so a later `Deny` does not
    run.
  - `Not` turns `Allow` (wrapped too) into `Deny` and the bare `Deny`
    into `Allow`. It keeps a wrapped `Deny`, `Skip`, nil and any other
    error as they are, failing closed: an error is never inverted.
  - The truth tables are in their godoc and in `TestCombinators`.
- `privacy.WithTrace(ctx, func(privacy.TraceEntry))` reports each rule's
  decision.
  - Each rule of a `Query` or `Mutation` list and each rule a combinator
    runs is reported after it returns, inner rules before the combinator
    or nested list.
  - An entry holds the rule's Go type, its index in its list, the entity
    by the bare name `Filter` and `Mutation.Entity` use (`"Doc"`), the op,
    "allow"/"deny"/"skip" and the raw result. The op is the kind of the
    rule's list: `OpSelect` for a Query rule, even one an update's Read
    policy runs, and the write's op for a Mutation rule.
  - `DecisionContext` replaces the rules, so nothing is reported under it.
  - The entry type is `TraceEntry`, veloxe's name, since `Decision` is
    already the verdict type.
  - The signature takes a function where veloxe had
    `WithTrace(ctx) ctx` and `TraceFrom`. No consumer called them.
  - The rule lists call `engine.EvalQueryRule`/`EvalMutationRule`. Those
    read the context only once a process has called `WithTrace` (an
    `atomic.Bool`), so tracing that is never used costs one atomic load
    per rule.
  - The function is kept in the context. It is neither a filter nor a
    hook: it changes no decision and no statement. The plan's rule,
    nothing about filters or hooks in a context, is not touched.

## Consequences

`Distinct` and `UpdateAll` cost each entity's package about 1.1 KB in all
(`entityDescUse` 771328 to 772452 bytes). The privacy package is not in
the API snapshot, which lists 1.0's packages; its additions are veloxe
compatibility.

## Evidence

- `internal/apitest/parity_test.go`: `TestDistinct`,
  `TestDistinctRefusesEntityReads`, `TestDistinctOrder`,
  `TestDistinctRewriteRefused`,
  `TestExplain` (three dialects, Around hook, `WithLog` masking),
  `TestExplainOfAProjection` and `TestUpsertUpdateAll` (three dialects,
  `SetPtr`, Version, Ignore, order with `Update`/`DoNothing`, InsertAll).
- `internal/integration/parity_test.go`: `TestDistinct`, `TestExplain` and
  `TestUpsertUpdateAll` on SQLite, and on PostgreSQL and MySQL in the gate.
- `privacy/combinator_test.go`: `TestCombinators`, `TestCombinatorInList`,
  `TestWithTrace`; `internal/apitest/privacy_test.go`:
  `TestPrivacyWithTrace`.
- `internal/repocheck/compilecost_test.go`: the measured comments.
