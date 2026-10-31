# 0010. A savepoint per GraphQL mutation

- Status: accepted, 2026-10-29

## Context

The GraphQL extension runs an operation in one transaction. A resolver that
swallowed one mutation's error kept that mutation's partial writes, since they
shared the operation's transaction (G45).

## Decision

`gqlimpl.Atomic` runs each generated mutation with `db.Tx`, which nests as a
savepoint inside the extension's transaction, or opens a transaction of its
own where there is none. A mutation's error rolls back its own writes at once,
and the operation's when the resolver returns it.

## Consequences

A resolver that swallows a mutation's error keeps the operation's other writes,
and the operation commits. Each mutation pays a SAVEPOINT and a RELEASE round
trip.

## Evidence

- contrib/graphql/doc.go: "each is atomic: gqlimpl.Atomic runs it in a
  savepoint".
- `TestAtomicSavepoint` (contrib/graphql/veloxgql/gqlimpl/mutation_test.go:410).
- Ruling 18 and builder ruling 9,
  docs/history/specs/2026-10-25-pre-1.0-hardening-design.md:56, :164.
