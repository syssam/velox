# 59. A connection is a query's Paginate, as in ent

Status: accepted, 2026-11-08. Revises 0049's `NewPostConnection`.

## Context

0049 named a type's connection `gen.NewPostConnection(ctx, q, after,
first, before, last, orderBy, where)`. It held that ent's `Paginate` needs a
per-entity query type to hang on, which velox does not have (0020). The
user found `db.Query[gen.Post]().Paginate(...)` clearer: the query comes
first, then what is done with it, as in ent.

Go 1.27 has generic methods, so a method of `velox.Query[E]` can have type
parameters of its own. Go infers a type parameter constrained by `*E` from
the core type, and the others from the methods of that constraint.

## Decision

- The root has `velox.Paged[E, K, O, C]`, a constraint: `*E` with
  `PaginateQuery(ctx, q, after *K, first *int, before *K, last *int,
  opts []O) (C, error)`.
- `Query[E].Paginate[P Paged[E, K, O, C], K, O, C any](ctx, after, first,
  before, last, opts ...O) (C, error)` calls `P(nil).PaginateQuery`. The
  root names no GraphQL type: K is the cursor, O the option, C the
  connection, all inferred from E.
- velox-graphql writes, for each type with a connection, `(*Post)
  PaginateQuery`, `PostPaginateOption`, `WithPostOrder(orderBy)` when the
  type has an order and `WithPostFilter(where)` when it has a where input,
  ent's names. Of two options that set the same thing, the later wins; a
  nil one sets nothing. `NewPostConnection` is gone.
- Errors about the query say "Paginate of Post".
- `veloxgql.GeneratedCodeIsVersion20`.

## Consequences

- A resolver reads as ent's:
  `db.Query[gen.Post]().Paginate(ctx, after, first, before, last,
  gen.WithPostOrder(orderBy), gen.WithPostFilter(where))`.
- `Paginate` does not compile for an entity with no connection: the
  constraint names the missing method.
- Every entity with a connection has an exported `PaginateQuery` method,
  which applications do not call.
- `Paginate` is compiled only where it is called, as every method with a
  type parameter is (0055); the root's compile cost does not change.
