# 0017. An upsert's update branch does not fire UpdateDefault

- Status: accepted, 2026-10-29

## Context

An `updated_at` column usually has an `UpdateDefault`. On an upsert that hits
a conflict, the update branch could set it or leave it. veloxe
(`genCreateUpsert`) and ent (entc's upsert.tmpl) do not fire it.

## Decision

An upsert's update branch writes only the columns `Update` names: an
`UpdateDefault`, such as an `updated_at` column's, does not fire, as in old
velox and ent. This is pinned as correct, not as a gap.

## Consequences

An upsert that hits a conflict leaves `updated_at` as it was, unless `Update`
names the column. An application that wants the upsert time names it.

## Evidence

- `InsertQuery.OnConflict`'s doc, insert.go (commit d8c039d).
- `TestUpsertUpdateBranchReadBack` (internal/integration/writes_test.go:283), on
  every database: `updated_at` stays `t0`.
- `ExampleInsertQuery_OnConflict` (example_test.go:192).
- Controller ruling 2, docs/history/plans/2026-10-28-hardening-5-tests.md.
