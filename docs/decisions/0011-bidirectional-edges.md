# 0011. Self edges with no inverse are bidirectional

- Status: accepted, 2026-10-29

## Context

In ent, a self-referencing `edge.To` with no inverse, such as friends or a
spouse, is bidirectional. velox stored one direction only (G17). Building it
later would change what existing data means, so it was built before 1.0.

## Decision

A self-referencing edge with no inverse is bidirectional, as in ent. A link
writes both directions: both join rows of a many-to-many edge, or both rows'
keys of a one-to-one edge. An unlink removes both. This holds in the core link
path and in the generated GraphQL mutations.

## Consequences

Inserting a join row fails when either row is there already;
`OnConflict(...).DoNothing()` is the idempotent insert. A one-to-one write
re-points both rows in a transaction, and a taken partner refuses it. Hooks
cannot drop the bidi marker or a bidi key.

## Evidence

- doc.go, the paragraph "A self-referencing edge with no inverse is
  bidirectional" (commit d8c039d).
- `TestBidiLinks` (bidi_test.go:152) and `TestBidiOne` (bidi_test.go:337).
- `TestBidiPeers` and `TestBidiPartner`
  (contrib/graphql/cmd/velox-graphql/mutation_e2e_test.go).
- Builder ruling 18, docs/history/specs/2026-10-25-pre-1.0-hardening-design.md:173.
