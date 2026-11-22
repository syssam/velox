# GraphQL: order and filter a connection by a to-one edge's columns

Status: built, 2026-11-03 (see ADR 0031, "In the GraphQL plugin"). The design below is as written; the connection of an edge also orders by it, which needed a join inside the per-owner window. Builds on ADR 0031 (to-one joins) and 0027
(GraphQL limits).

## Intent

A feed is `post_feed` rows with their `post`: a client orders the feed by the post's
`createdAt` and filters it by the post's `kind`. In the core that is
`Join(PostFeeds.Post).Where(PostFeeds.Post.Is(...)).OrderBy(PostFeeds.Post.By(...))`.
GraphQL today orders a connection only by the node's own `OrderField` columns, and its
`where` reaches an edge only by `hasX` / `hasXWith`, which is EXISTS. This design lets
the schema expose a to-one edge's columns to `orderBy`, and uses the join.

## Goals

- `orderBy: {field: AUTHOR_NAME}` on a connection of `Post` orders by the author's name;
  keyset paging (`after`) continues over it.
- The schema opts in, per edge and per column, so the cost limits of 0027 hold: nothing
  becomes orderable by default.
- The target's Read policy decides what the join shows (ADR 0031): a post whose author
  is hidden is left out of an ordered connection, and nothing about the author leaks.

## Non-goals

- A to-many edge (refused by the core).
- Filtering by a joined column. `hasAuthorWith` already filters by the edge's columns,
  and EXISTS does not repeat or reorder rows; a joined `where` adds nothing but a second
  way. Revisit only if a measured case needs it.
- Ordering by more than one hop (`author.company.name`).

## Design

**Schema.** An annotation on the edge names the columns of its target that the source
may order by, each with its enum value:

    edge.To("author", User.Type).Unique().
        Annotations(graphql.OrderVia(graphql.OrderBy("name", "AUTHOR_NAME")))

The target field must itself be orderable under the same rules as `OrderField`
(`OrderUnguarded` included). velox-graphql refuses, with the entity and edge: a
to-many edge, a field the target does not have, an enum value that clashes, and a
target whose field has an application directive without `OrderUnguarded`.

**SDL.** The enum `PostOrderField` gains `AUTHOR_NAME`. Nothing else in the schema
changes: `PostOrder` already holds `field` and `direction`.

**Generated code.** `<Post>OrderTerm` gains a case for `AUTHOR_NAME` that returns a
joined term, and the connection function joins the edge when an `orderBy` names one.
It is an inner `q.Join(gen.Posts.Author)`, not a LEFT JOIN, because `AddAfter` has to
read the ordered value from the row's joined entity: it fails for a row with none
(`internal/engine/query.go`, `joinedSource`), and a page's last row is the next page's
cursor. So an `orderBy` on a joined column drops the rows whose target is missing or
hidden by the target's Read policy, from the page and from `totalCount` alike (the
count carries the same join). That is a visible rule and is documented on the
annotation. The target column must not be Nillable either: `AddAfter` refuses a
nullable column, so velox-graphql refuses the annotation for one, with the entity,
edge and field. This settles the NULL-ordering question: no NULL reaches an order.

**Runtime (`gqlimpl`).** A new constructor `JoinedTerm[E, T, V]` beside `Term`: its
`asc` and `desc` are `rel.By(...)`; `get` reads the column from the row's `One[T]`
holder; `set` fills a holder for the cursor's row. The core needs for that one small
addition: `One[T].Set(*T)` or the like, so a decoded cursor can build the row `After`
reads (the engine's `AddAfter` already reads a joined term's value from the holder).
The cursor's term records the enum value, as now, so a cursor taken under `AUTHOR_NAME`
is refused under another order.

**Selection.** The columns of the target are loaded by the join; `colsFunc` must keep
the join's `Columns` narrowing out of it (a joined term is not a column of the root).

**Cost (0027).** The join is one statement, in the same page query: no new statement
and no new multiplier.

## Constraints

- Every file under `contrib/graphql` that this touches (`annotation.go`, `internal/sdl`,
  `internal/gogen`, `veloxgql/gqlimpl`, their golden files and e2e tests) has
  uncommitted changes of the GQL codegen slimdown. Build this on top of that commit,
  not beside it.
- Compile cost: the joined term is generic over three types, instantiated per
  (entity, edge, column) in generated code. Measure it against `compilecost_test.go`
  before widening, as ADR 0031 did.

## Left to the builder

- The annotation's exact spelling.
- Whether `JoinedTerm` takes the edge handle or two closures.
- The e2e: a feed connection ordered by a joined column, paged forward and backward,
  with a policy hiding some targets, on SQLite and PostgreSQL.
