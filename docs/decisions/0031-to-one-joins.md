# 31. A query joins a to-one relation, and conditions and orderings reach its columns

Status: accepted, 2026-11-02.

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
- `Count` and `Exists` join too. An aggregate, a group, a per-owner read and a locking
  read refuse a join (PostgreSQL refuses to lock the nullable side of an outer join).
- `After` pages over a joined column: the value comes from the joined entity stored in the
  row's relation field, and the condition is on the joined row (`rel.Is`). A row without
  a joined entity (not read with the join, or a left join that found nothing) has no value
  to page from, and fails. Ties are still broken by the statement's own key, which the
  ordering must include.
- The runtime types stay out of the entity package's compile cost: `Query` holds a pointer
  to a non-generic `joinSet`, and the generic scan closures are made inside `Join`, which
  only a package that joins compiles (`repocheck`'s compile-cost bounds hold).

## Not decided here

Polymorphic targets, an ordering by a given list of ids, subquery projections (counts,
latest per parent), and schema views are separate; the join is a clause of the statement,
not a special case of one edge, so they can build on it. projecting a narrower set of the target's columns, and reading the join in the GraphQL
plugin (`orderBy` and `where` on a related field, which would be an explicit opt-in because
of 0027's cost) follow.
