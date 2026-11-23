# 117. Statement counting finds an N+1 in a test

Status: accepted, 2026-11-22.

## Context

A loop over rows that reads a relation per row sends one statement per
row: an N+1. `With` loads a relation for every row in one statement, and
velox's GraphQL resolvers batch each relation per request, but nothing
told a developer that their own code looped. ent has no such check. The
render cache already keys a statement by its shape, its text without the
arguments and comment (`Statement.Fingerprint`), which the Around hooks
see.

## Decision

- **`velox.WithStatementCounting()`** is an Around hook that counts each
  statement in the `StatementCounter` of its context, by fingerprint (raw
  SQL, which has none, by its text). **`velox.CountStatements(ctx)`**
  returns a context with a new counter. The counter has `Total`, `Shapes`
  (text, entity, op and count of each shape, in first-run order) and
  `Check(StatementBudget{Statements, Repeats})`, which returns a
  `*BudgetError` naming each read, a SELECT or raw query, of one shape run
  more than `Repeats` times, and the count when it is over `Statements`.
  Writes count toward `Statements` only: an insert per row is often meant.
- Off is free by construction: a DB opened without the option has no hook
  and no branch for it. With it, a statement pays the Around machinery
  (6 allocations on a cached one-row read, as any Around hook, measured by
  `TestCountStatementsCost`) and one context lookup; counting a shape
  already seen allocates nothing.
- Counting is per context, not per DB, so a test, or a request in
  development, sees only its own statements, concurrent ones included.
- **`veloxtest.Open`** adds the option, before the caller's Around hooks.
  **`veloxtest.Budget(t, ctx, b)`** returns a counted context and fails
  `t` when it ends if the context went over `b`;
  **`veloxtest.NoNPlusOne(t, ctx)`** is `Budget` with `Repeats: 1`.
- The check is a report, not a refusal: a statement over the budget still
  runs, so the test sees every repeat in one failure rather than the first.
- A repeat is a read of one shape, whatever its arguments; whether each
  is a load by one key is not checked, since a read of a row's children by
  its key repeated per row is an N+1 too.

## Consequences

- The GraphQL guide needs nothing new: its batcher reads each relation
  once per request, so `NoNPlusOne` passes a query through it.
- The root gains `StatementCounter`, `StatementShape`, `StatementBudget`,
  `BudgetError`, `CountStatements` and `WithStatementCounting`, aliases of
  the engine's.

## Evidence

- `TestCountStatementsNPlusOne`, `TestCountStatementsBudget`,
  `TestCountStatementsConcurrent`, `TestCountStatementsCost` and
  `BenchmarkOneRowCounted` (internal/apitest/count_test.go).
  `BenchmarkOneRow` keeps 6 allocations; `BenchmarkOneRowCounted` makes 12.
- `TestNoNPlusOne` (veloxtest) and examples/relations' `TestNoNPlusOne`.
