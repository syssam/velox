# 112. A relation's aggregates are terms of each row

Status: accepted, 2026-11-22.

## Context

ent orders by a to-many edge's count (`user.ByPostsCount()`) or by an
aggregate of its rows (`user.ByPosts(sql.OrderBySum(post.FieldViews))`),
and entgql exposes the count as an order field (`entgql.OrderField("POSTS_COUNT")`
on the edge). velox could test a relation (`Has`, `HasWith`: an EXISTS,
0031), count it per owner in a second statement (`CountEach`), and project
an entity's own terms (0069, 0074), but could not order by, select or
compare a relation's count. ent's count reads the target's table as it is:
a row that the target's privacy hides is counted, and the order tells a
caller how many hidden rows each owner has.

## Decision

- `Rel[E, T]` has `Count()`, `Sum(s)`, `Avg(s)`, `Min(s)` and `Max(s)`,
  each a `Term[E]`; `s` is a column handle or a `Term` of `T`. They are
  generic methods of the existing `[_ Lazy[T]]` form; nothing is generated.
  The relation's `Where` conditions apply; its `OrderBy`, `With`,
  `LimitEach` and `MaxRows` do not.
- Each renders as a scalar subquery correlated as `HasWith`'s EXISTS is:
  `(SELECT COUNT(*) FROM target AS vxN WHERE vxN.key = outer.key AND …)`.
  Through a join entity or an edge schema it reads the target's rows that a
  join row links to the owner, `WHERE EXISTS (SELECT 1 FROM join …)`, so a
  link held twice counts once, as a load reads it. Over no rows `COUNT` is
  0 and the others NULL.
- The engine holds it as a term (`termRel`) whose relation is the node
  `HasWith` makes. **The scope adds the target's row condition to it as to
  an EXISTS** (`Scoper.term`, through `Scoper.Node`): the `WithFilter`
  filters, the interceptors (a soft delete among them), the Read policy
  (a scope among them) and, on a read, the Query policy. A row a query of
  the target would not return is not counted. ADR 0091's cycle cut applies
  unchanged, since the condition comes from `RowCond`. A target's policy
  that denies fails the statement, as inside `Has`.
- It is usable as an ordering, a result column of `Select`, and in an
  `Expr` of a `Where` (`velox.Expr[User]("? >= ?", Users.Posts.Count(), 3)`),
  directly or inside `Calc`. An `OpExpr` holding one is marked `Deep`, so
  the scope walks it. It is refused in `GroupBy`, beside `GroupBy` or an
  aggregate term in a projection (it is a value of each row), under an
  aggregate (`SUM` of a subquery), in `SetExpr`, and in `Having`, which the
  scope does not walk. Conditions by name are refused in its `Where`; the
  zero `Pred` too.
- **After and cursors page over it.** The After row's value is not in the
  row, so the condition compares the term with the same term **bound to the
  row's key** (`boundRel`): `(SELECT COUNT(*) … WHERE vxN.key = ?) `. Both
  are computed by the statement, under the same row conditions. A count is
  never NULL: `t > b`, `t = b`. `SUM`, `AVG`, `MIN` and `MAX` may be NULL
  and order as a nullable column (NULL last ascending), so the SQL decides
  the NULL cases: past is `(b IS NOT NULL AND (t > b OR t IS NULL))`
  ascending and `((b IS NULL AND t IS NOT NULL) OR t < b)` descending, and
  equal is `(t = b OR (t IS NULL AND b IS NULL))`. The key breaks ties, as
  the ordering must include it. With `Columns`, the relation's owner column
  must be read. An expression over the term (`Calc("? + 1", count)`) is
  still refused, as any expression is.
- A count orders without the `IS NULL` term MySQL and SQLite need for a
  nullable term; the other aggregates keep it.
- The render cache keys the term by the relation's index, its conditions
  after the scope (the term is sealed again there), whether it is bound,
  and its aggregate.
- The untyped API has it as `Table.RelAgg(edge, target, agg, ps…)`.
- **velox-graphql** takes entgql's spelling: `graphql.OrderField(NAME)` on a
  to-many edge (one-to-many, many-to-many, through an edge schema) adds the
  value `NAME` to the owner's order field. The generated `gqlgraph.Column`
  has `Count: true`, the edge and the target's table; the connection orders
  by `Table.RelAgg(edge, target, CountAll())`, so the count is under the
  target's row conditions as above. It joins nothing, and the rows and
  `totalCount` are those without the order. A cursor holds null for the
  count; the next page binds the cursor row's key (`boundRel`). The plugin
  refuses it on a to-one edge (`OrderVia` orders by a target's column), on
  an edge `Skip` leaves out or to an entity with no type, and on an edge
  with an application directive, since the order would reveal how many
  nodes the edge has. The value's SDL description starts
  `Orders by the number of `, which veloxgql's cost model reads: ordering by
  it costs `scanCost`, as a `has<Edge>With` does, from a literal or a
  variable.

## Consequences

- Each package that names `Rel` carries the five methods' declarations:
  about 3 KB, flat in the number of `Rel` instantiations (measured with one
  and five). The repocheck bounds of `entityDescUse`, `entityDescHooks` and
  `entityDescMixin` rose by 3.5 to 5 KB (3.4 to 4.4 KB measured). One
  method cost about 1.3 KB; a single
  `Agg(term)` in place of `Sum`…`Max` would have saved about 1.5 KB a
  package, against ent's spelling.
- The After condition repeats the subquery for each ordering term before
  it; a page of a large table ordered by a count reads the target's index
  per row, as ent's does.
- Evidence: `TestRelAggregateSQL`, `TestRelAggregateHidesRows`,
  `TestRelAggregateSoftDelete`, `TestRelAggregateAfter`,
  `TestRelAggregateRefusals`, `TestRelAggregateCacheKey` and
  `TestRelAggregateUntyped` in `internal/apitest`; `TestRelTermRefusals` in
  `internal/engine`; `TestRelAggregateAfterOnRealDatabases` (ties, NULL
  sums, both directions, pages of 1 to 3), `TestRelAggregateHidesRowsOnRealDatabases`
  (interceptor, policy, soft delete, a scoped many-to-many target) and
  `TestRelAggregateEdgeSchemaOnRealDatabases` in `internal/integration`;
  `TestOrderCount` (contrib/graphql `internal/sdl`), `TestCostCountOrder`,
  `TestConnectionOrderedByCount` (run by `TestConnectionsRun`, including a
  many-to-many edge whose target's scope hides a member) and the example's
  `TestOrderByCount`.

## Not decided here

A count threshold as a condition (`CountGT(n)`): `Expr` says it. An
aggregate of a relation of the target (a count of grandchildren), and an
`UPDATE` that sets a column to a count.
