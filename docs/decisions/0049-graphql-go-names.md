# 49. velox-graphql's generated names follow Go's

Status: accepted, 2026-11-07. Renames 0046's `Paginate<Plural>` and
0035's runtime package.

## Context

A review against ent and Go's naming found names that read badly: the
connection function `PaginatePosts` and its twin `paginatePosts`, 20 lines
of argument plumbing; `postGQL`, a `gqlimpl.Type`; `gqlimpl.Col{Field,
Col}`, whose field repeats its type; `Type.Read`; the abbreviation
`FKField`; `Checked`, which says nothing of what it does; `nodeType`,
which returns a `NodeKind`; and the receiver `x` on every entity, where Go
names a receiver after its type.

## Decision

- A type's connection is `NewPostConnection(ctx, q, …)`, Go's `NewX`. It
  returns a `*PostConnection` and runs nothing until gqlgen resolves its
  fields, so `New` says what it does; ent's `Paginate` is a method of its
  per-entity query, which velox does not have. Its body is
  `newPostConnection`, which the edge methods call with an Edge.
- `gqlgraph.Args` holds `Order`, and `Where`, made by
  `gqlgraph.WhereOf(where, (*PostWhereInput).predicate)`; `Paginate`
  computes the condition, so newPostConnection has no plumbing left.
- The runtime package is `veloxgql/gqlgraph`, after ent's `sqlgraph`, the
  package ent's generated code calls and applications do not.
- `postGQL` is `postType`; `gqlgraph.Column{Name, Index, Edge}`;
  `Type.AlwaysRead`; `EdgeIDField`; `Validate`; `nodeKind`.
- A receiver is the first letter of its type in lower case, the first two
  where a body declares the letter (v, n, b); velox-gen's `String` and
  `GoString` too.
- veloxgql's generated code is version 18. velox-gen's change is only a
  receiver's name, which no runtime sees, so velox stays at 18.
