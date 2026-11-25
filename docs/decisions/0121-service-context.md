# 121. A service asks for reads outside a request

Status: accepted, 2026-11-23. Amends 0105. Amended by 0171: an edge of a
row a statement read reads on that row's DB, with or without `Service`;
only a connection of a query still needs it. Amended by 0173: no read
needs `Service`; it stays for `veloxgql.DB(ctx)` and the limits.
Superseded by 0175: `Service`, `ServiceWithLimits` and `veloxgql.DB(ctx)`
are gone; the limits are the DB's (`veloxgql.WithLimits`).

## Context

0105 made every gqlgraph read fail without a selection source, with
`ErrNoSelectionSource` joined with `ErrNoDB`. Its reason holds: a read
that silently took every column and batched nothing hid N+1 bugs.

Services page lists without a GraphQL request too. Porting nextapp's
sales order service found its `List` and `ListFor*` methods, its
service-to-service calls and its unit tests calling the generated
`Paginate` (root `Query.Paginate`, then `PaginateQuery`) with no request
in the context. Each failed with `ErrNoSelectionSource`.

The one way round was `gqlgenx.NewExtension(db).NewContext(ctx)`. It ties
a service to gqlgen, which a graphql-go application does not import, and
to an extension it does not serve.

## Decision

- `veloxgql.Service(ctx, db)` returns ctx with db, `DefaultLimits()` and a
  selection source that selects nothing: its `Field` is never a field and
  its `Wait` does nothing. It is engine-free and lives in `veloxgql`.
- `veloxgql.ServiceWithLimits(ctx, db, l)` takes the limits, each zero
  field from `DefaultLimits()`, as `gqlgenx.WithLimits` does. A service
  that pages by more than `MaxPage` rows, or reads a list edge of more
  than `MaxList`, raises them there. A negative limit panics, naming it.
  A nil db panics, as `graphqlgo.With` does.
- The limits default to `DefaultLimits()` and not to none: a service
  passing a client's `first` through keeps the same bound as the request.
- Under it, `Paginate`, an edge's connection, `EdgeOne` and `EdgeList`
  read every column, and an edge reads alone for each row. That is
  0105's "outside a field" rule, which now has an explicit way in.
- Without it, or an adapter, a read still fails, before any statement.
  The error now names `veloxgql.Service` beside the two adapters.
- It replaces any state in ctx. A resolver does not call it: under the
  adapter's context, the same service code reads only what the field
  selects, and batches. It is not made a no-op inside a request, since
  then the db argument would be ignored there, unseen.
- `gqlgenx.Extension.NewContext` stays, for a gqlgen application that
  wants its extension's limits.

## Consequences

- Fail closed is kept: the full-column, unbatched read happens only where
  the caller wrote `veloxgql.Service`.
- `TestPaginateInAService` (examples/graphql) pages generated code by a
  filter, an order and a cursor, and reads an edge, under `Service`, and
  fails with `ErrNoSelectionSource` without it. `TestNoSelectionSource`
  reads each of the four under `Service`, and `TestService` checks the
  limits and the panics.
- The public API gains `veloxgql.Service` and `ServiceWithLimits`.
