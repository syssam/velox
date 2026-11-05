# 0007. Policies decide, hooks query

- Status: accepted, 2026-10-29. Revised by 0039: a hook that must name gen's
  types (to read or write another entity, typed) is a run-time hook,
  `velox.WithHooks`.

## Context

Some rules need other rows: an inserted comment's parent must be one the
caller may read. `Policy.Insert` could take a DB to run that query (C55), but
then every policy signature carries one, and a query from inside a policy runs
outside the hooks' ordering.

## Decision

`Policy.Insert` keeps its signature, `func(ctx, *E) error`. A policy function
decides from ctx and, for Insert, the row; it does not query. A rule that must
see other rows is a hook: it queries through `Mutation.DB`, the write's DB, and
returns the error that refuses the write. Inside `db.Tx` that DB is the
transaction. Outside it, the hook's statements and the write commit separately:
velox opens no transaction for a hook. This is ent's split: privacy rules
decide, hooks query.

## Consequences

Policies stay pure functions of the context and the row, cheap to test. A
cross-row rule is written as a hook. It shares the write's transaction when the
caller is in `db.Tx`; a write that must be atomic with its hook's work runs there.

## Evidence

- `Policy`'s doc, policy.go: "A policy function decides from ctx and, for
  Insert, the row; it does not query."
- `TestMutationDBIsTheTransaction` (lazy_test.go:353): a hook's writes
  through `Mutation.DB` run in the write's transaction, inside `db.Tx`.
- `TestMutationDBAtomicOnlyInTx` (hook_atomic_test.go): outside `db.Tx`, a write
  stays when its hook's statement fails.
- Ruling 13, docs/history/specs/2026-10-25-pre-1.0-hardening-design.md:51.
