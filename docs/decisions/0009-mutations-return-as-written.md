# 0009. GraphQL mutations return the row as written; a link needs what it changes

- Status: accepted, 2026-10-29

## Context

An update can move a row out of the caller's Read view, and the generated
resolver must decide what to return (G44). A link to a target must be checked
against the target's policies, and which ones depends on what the link writes
(G52).

## Decision

- A mutation returns the row as written. `Update<T>` reads it back under the
  tenant scope without the Read policy: its UPDATE is scoped by the Read
  policy too, so the caller could read the row before it wrote it. This is a
  contract, and will not change.
- A many-to-many link inserts a join row and changes no target row, so the
  target needs Read. A one-to-many or one-to-one link from the owner sets the
  target's foreign key, so the target needs Read and Write. A bidirectional
  one-to-one key is the exception: its link writes the partner's row too.

## Consequences

A client sees the row it just wrote even when it can no longer query it. A
read-only target can still be linked many-to-many.

## Evidence

- contrib/graphql/doc.go: the paragraphs "A link to a target needs what the
  write changes" and "A mutation returns the row as written".
- `TestUpdateReadBack` and `TestEdgeScope`
  (contrib/graphql/cmd/velox-graphql/mutation_e2e_test.go).
- In the golden contrib/graphql/internal/gogen/testdata/golden/gql_post.go,
  the many-to-many IDs (`relatedIDs`, `addRelatedIDs`) are checked by
  `gqlimpl.CheckExists` on a plain `db.Query`, the Read scope, not
  `.Writable()`.
- Rulings 20–21, docs/specs/2026-10-25-pre-1.0-hardening-design.md:61-62.
