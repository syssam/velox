# 31. A query joins a to-one relation, and conditions and orderings reach its columns

Status: accepted, 2026-11-02. Its `After` of a left join, the GraphQL
plugin's inner join and a denied target are amended by the reviews of
2026-11-22 and 2026-11-24, at the end.
Amended by [0143](0143-joined-terms-in-projections.md).

## Context

A relation could be tested (`Has`, `HasWhere`: an EXISTS) or loaded (`With`: a second
query, `IN (…)`). Neither serves a feed: `post_feed` rows, filtered by `post`'s columns
(not deleted, a kind), ordered by `post_feed`'s rank and `post`'s time, each with its post,
in one statement and a page of the size asked for. EXISTS cannot order by the related
column or fill the field. `With` costs a round trip, and a filter applied to its rows
afterwards shortens the page.

A view (ent's schema views) was considered first. It is flat, so the entity loses its
type, its Read policy and its relations, and a scope bound to `post` does not reach it.

## Decision

- `Query.Join(rel)` and `Query.LeftJoin(rel)` read a **to-one** relation in the query's own
  statement, and fill the relation's field (`One`) from the same row. A relation that
  holds several rows (`Many`, or through a join entity) is refused, with the error naming
  `With` and `HasWhere`: a join would read an owner once for each, and `Limit`, `Count`
  and `After` would count joined rows.
- `rel.Is(preds…)` is a condition on the joined row, and `rel.By(order)` an ordering by
  its column; both return the root's `Pred[E]` and `Order[E]`, so they compose with `And`,
  `Or` and `Not`. A statement that has one without the join fails at render. Nothing is
  generated for this: the join, `Is` and `By` are generic methods of the `Rel[E, T]` the
  generated code already holds, and a query joins a relation once.
- The target's **Read policy, a scope included, is in the join's ON clause**, as `With`
  reads the target. An inner join then drops a row whose target the policy hides; a left
  join keeps it, loaded and empty. A condition made by `Is` is in the WHERE, so on a left
  join it leaves out a row that has no target, as in SQL.
- The statement carries the joins (`Statement.Joins`, a `JoinClause` each); a Rewrite hook
  may not change them, as the rows are scanned by them. The render cache keys on them.
  The target's table is `vxj<n>`, so a relation to the statement's own table is told apart.
- `Count` and `Exists` join too. An aggregate, a group, a per-owner read through a join table and a locking
  read refuse a join (PostgreSQL refuses to lock the nullable side of an outer join).
- `After` pages over a joined column: the value comes from the joined entity stored in the
  row's relation field, and the condition is on the joined row (`rel.Is`). A row without
  a joined entity (not read with the join, or a left join that found nothing) has no value
  to page from, and fails. Ties are still broken by the statement's own key, which the
  ordering must include.
- The runtime types stay out of the entity package's compile cost: `Query` holds a pointer
  to a non-generic `joinSet`, and the generic scan closures are made inside `Join`, which
  only a package that joins compiles (`repocheck`'s compile-cost bounds hold).

## In the GraphQL plugin

`graphql.OrderVia(field, value)` on a to-one edge adds an `orderBy` value that orders by
the target's column (`AUTHOR_NAME`), with the cost limits of 0027 unchanged: the schema
opts in for each column, and the join is one statement in the page query and in the count.
The connection joins the edge with an **inner** JOIN: `After` reads the cursor's value from
the row's joined entity, which a left join may not have, so a post whose author is missing
or hidden by the author's Read policy is not in an ordered connection nor in its
`totalCount`. The plugin refuses the annotation on a to-many edge, for a nullable (lifted: see the Review), sensitive,
json or bytes field, a field or an edge with an application directive (which guards neither
the cursors nor the order), and a field or edge the type leaves out. `gqlimpl.JoinedTerm`
is the runtime's term, and `Rel.Fill` and `Rel.Filled` set and read a joined entity, so a
decoded cursor can build the row `After` pages from.

The connection of an edge (`User.posts`) is read for every parent in one statement with a
ranking window, and that window reads joins too: the joined columns are named inside the
derived table (`vxj1_4`, which MySQL requires, as it refuses duplicate column names there) and
read by the outer query, and the engine attaches each row's joined entities after its kit has
made the rows. The join is before the rank, so a row it drops takes none: `LimitEach(n)` is the
first n rows that have a joined row. `CountEach` counts the joined rows. A per-owner read
through a join table (many-to-many) still joins no relation.

## Not decided here

Polymorphic targets, an ordering by a given list of ids, subquery projections (counts,
latest per parent), and schema views are separate; the join is a clause of the statement,
not a special case of one edge, so they can build on it. Projecting a narrower set of the
target's columns follows. `where` on a related field stays `hasXWith`.

## Review

2026-11-05, a nullable joined column: an ordering by a joined column put NULL
first ascending on MySQL and SQLite, where `After` counts on it last, so a
page over a Nillable target field skipped or repeated rows there. The
ordering now adds `IS NULL` before the column when it is nullable or the
join is a left join, as an ordering by the row's own column does (0032). The
plugin no longer refuses `OrderVia` of a nullable field: the cursor holds
NULL as null, as for an `OrderField`. Evidence: `TestJoinOrderByANullableColumn`,
`TestJoinAfterANullableColumn`, `TestJoinAfterNullsExact` (SQLite,
PostgreSQL through pgx and database/sql, MySQL), `TestPaginateNullableVia`,
`TestNullableViaServed`.

2026-11-22, OrderVia's inner join: ordering a connection by a related field
dropped each row whose target was missing or hidden, from the page and from
`totalCount`, so the order told a caller how many rows a policy hid. The
connection now reads the edge with a left join. `After` of a left join pages
from a row that has no joined entity as from NULL, and treats the joined
column as nullable, as the ordering already did; of an inner join such a row
is still refused, as it was not read with the join. The cursor holds the
missing target's term as null. Evidence: `TestJoinAfterARowWithoutAJoinedRow`
in `internal/apitest`; `TestJoinedTermPages` and `TestOrderViaKeepsRows` (a
missing and a hidden target: the same rows and `totalCount` with and without
the order, paged both ways) in `contrib/graphql/veloxgql/gqlgraph`;
`TestConnectionOrderedByAnEdge`, served by `TestConnectionsRun`.

2026-11-24, a denied target: a left join to an entity whose Read policy or
filter denies outright failed the statement, so an `OrderVia` connection and
its `totalCount` failed though the docs say a hidden target sorts as null.
A left join to a denied target now joins on a condition that matches
nothing (`1 = 0`): every row stays, loaded and empty, and nothing of the
target is read. An inner join to it still fails with `ErrDenied`, and the
error names the join, not an EXISTS. Evidence: `TestJoinToADeniedTarget` in
`internal/apitest`; `TestOrderViaDeniedTarget` in
`contrib/graphql/veloxgql/gqlgraph`.
