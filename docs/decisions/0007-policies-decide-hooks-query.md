# 0007. Policies decide, hooks query

- Status: accepted, 2026-10-29

## Context

Some rules need other rows: an inserted comment's parent must be one the
caller may read. `Policy.Insert` could take a DB to run that query (C55), but
then every policy signature carries one, and a query from inside a policy runs
outside the hooks' ordering.

## Decision

`Policy.Insert` keeps its signature, `func(ctx, *E) error`. A policy function
decides from ctx and, for Insert, the row; it does not query. A rule that must
see other rows is a hook: it queries through `Mutation.DB`, in the write's
transaction, and returns the error that refuses the write. This is ent's
split: privacy rules decide, hooks query.

## Consequences

Policies stay pure functions of the context and the row, cheap to test. A
cross-row rule is written as a hook, and runs inside the write's transaction.

## Evidence

- `Policy`'s doc, scope_api.go: "A policy function decides from ctx and, for
  Insert, the row; it does not query."
- `TestMutationDBIsTheTransaction` (lazy_test.go:353): a hook's writes
  through `Mutation.DB` run in the write's transaction.
- Ruling 13, docs/specs/2026-10-25-pre-1.0-hardening-design.md:51.
