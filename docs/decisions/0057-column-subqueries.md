# 57. A column's subquery is an EXISTS on two columns

Status: accepted, 2026-11-08.

## Context

A review found that the typed API had no subquery. `HasWith` tests a
relation, but a condition such as "invoices whose id is among the items'
invoice ids where qty >= 100", or one on columns that no edge joins, needed
`Raw`. ent has no typed subquery either; its users write a `sql.Selector`
modifier. SQLAlchemy, jOOQ, Drizzle and GORM all accept a subquery in `IN`.

## Decision

- A column handle's `Select(ps...)` is a `Subquery[V]`: the column's values in
  the rows that meet `ps`. Another handle's `InSelect(s)` and `NotInSelect(s)`
  test its column against it. `V` is the column's type, so an int64 key is
  tested against an int64 column and an enum against the same enum; a JSON
  handle has neither method, as it compares nothing.
- `InSelect` is written as `EXISTS (SELECT 1 FROM target AS vxN WHERE
  vxN.col = outer.col AND ps…)`, the shape `HasWith` writes for a relation. It
  is the same `OpExists` node with an `engine.ColRef` in place of a
  `RelRef`, so everything an EXISTS gets, it gets: the target's Read policy
  and interceptors' conditions inside the subquery (`Scoper.Node`), the
  statement's checks, the render cache, and the swap on a bidirectional join
  entity. Its node's column is -1, and the render cache keys its target and
  both columns, since the outer entity does not fix them.
- `NotInSelect` is `Not(InSelect)`: `NOT EXISTS`. SQL's `NOT IN` is false for
  every row when the subquery yields a NULL; velox's is not. A NULL column is
  in no subquery, so `NotInSelect` is true for it.
- Like `HasWith`, `Select` reads no metadata, so a Policy or a hook may build
  one. A zero `Subquery`, or a bad condition inside `Select`, fails the
  statement.

## Consequences

- `IN (SELECT …)` with a LIMIT, an aggregate (`> (SELECT AVG …)`), a CTE or a
  window function stays in `Raw`.
- On MySQL, an UPDATE or DELETE whose subquery reads the table it changes
  fails with error 1093, as one with `HasWith` on a self-relation does.
- `TestInSelect*` in `internal/apitest` and `TestHas` in `internal/integration`
  (every dialect) cover it; `ExampleNumberCol_InSelect` shows the SQL.
