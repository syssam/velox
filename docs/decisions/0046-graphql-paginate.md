# 46. A connection is Paginate of a query; gqlimpl.Type describes a type

Status: accepted, 2026-11-07. Revises 0035's generated read surface.

## Context

Each GraphQL type had two exported functions for its connection:
`PostsConnection(ctx, args…)`, which read the DB from ctx and paged every
row, and `PostsConnectionOf(ctx, q, args…)`, which paged the
application's query. The first was the second over `db.Query[Post]()`.
entgql has one, `client.Post.Query().Paginate(ctx, args…)`, on a query
the application builds. A where input had `Pred(ctx)` and an exported
`PredicateGQL(ctx)`, its untyped twin, which only gqlimpl called. The
per-type literal the runtime reads was `gqlimpl.Conn`, though it describes
the type, not a connection, and its variable was `postConnGQL`.

## Decision

- One exported function per type, `Paginate<Plural>(ctx, q, after, first,
  before, last, orderBy, where)`, named after the GraphQL type's plural as
  the old one was (`PaginateLabels` for `Label`, entity `Tag`). The query
  resolver gets the DB with `veloxgql.DBFromContext` and passes
  `db.Query[T]()`. An edge method does the same: it reads the DB from ctx,
  as before.
- `PaginateXs` filters `q` by `where.Pred(ctx)` and calls
  `gqlimpl.Paginate`, which checks `q` (unordered, selecting the key and
  the order columns), narrows it to the selection and pages it.
  `gqlimpl.ConnectionBase` and `gqlimpl.NewConnection` are unexported.
- A where input's untyped condition is an unexported method, `predicate`.
  `gqlimpl.Not`, `And`, `Or` and `HasWith` take it as a method expression,
  `(*PostWhereInput).predicate`, of type `gqlimpl.Cond[W]`; an unexported
  method cannot satisfy an interface of another package.
- `gqlimpl.Conn` is `gqlimpl.Type`, its `Type` field `Name`; the variable
  is `postGQL`, and node_gql.go's lookup is `nodeType`. velox-graphql
  refuses a schema where `postGQL` is one of velox-gen's unexported names.
- `veloxgql.GeneratedCodeIsVersion16`.

## Consequences

- A query resolver is four lines, not one; it names the entity's query,
  which is where the application adds its own filters.
- gen exports no `GQL`-suffixed name but the methods gqlgen binds to
  (`MarshalGQL`, `UnmarshalGQL`, the `<Field>GQL` getters).
- Errors about the query name `PaginateXs` (`render_test`'s
  `TestPaginateFilters`, gqlimpl's `TestPaginateBase`).
