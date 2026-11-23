# 0143. A projection over a to-one join reads the joined row's terms: velox.Joined

- Status: accepted, 2026-11-23. Amends 0031 ("an aggregate … refuse a join")
  for projections; 0031's Not decided listed narrower projections of the
  target.

## Context

`aggregate/expense_claim_item.go:147` replaces FROM with a derived table,
line items joined to their live claims. The report then groups and filters
the lines by claim columns: status, employee, organization, company,
business unit, cost center. velox had no form for this. A projection
(`Select`) refused `Join`, and a `Term[E]` could name only E's columns.
A relation's aggregate (`Rel.Max(x)` of a to-one relation) would give the
parent's value per row, but it cannot be grouped by (0057).

## Decision

- `velox.Joined(r Rel[E, T], s Selectable[T]) Term[E]` is s, a column or Term of
  the target, read from the query's join of r: `SELECT vxj1.status`. It is
  a value of each row, so `Select`, `GroupBy`, `Expr`, an ordering and an
  aggregate of it (`Joined(r, x).Sum()`) take it. It is a function, not
  a method of `Rel`: even a lazy method costs every package that names a
  `Rel` about 0.8 KB (measured on `entityDescUse` and `entityDescHooks`,
  `TestGenericTypesCompileSmall`), and a function costs only its callers.
- A projection may now `Join` or `LeftJoin` a to-one relation. The join
  is written as for an entity read: the target's Read policy, filters and
  interceptors are in the ON clause (`scopeJoins`). So a joined term reads
  nothing that a query on the target would not. An inner join leaves out a
  row whose parent is missing or hidden; a left join keeps it, with NULL
  terms. Every column is qualified. `rel.Is` (conditions) and `rel.By`
  (orderings) work as in 0031. A projection still refuses `With`, so a
  join's `With` fails it.
- Inside `Joined`, s may be a column, a `Calc`, a JSON value or a
  `DateTrunc` of the target. An aggregate (a Term's or one in Calc's text,
  0142), a relation's aggregate, another `Joined`, a `Rollup` and
  `Excluded` are refused: aggregate the joined term instead. A to-many
  relation, or a statement that does not join r, fails the statement.
- A joined column scans into the field of its own name (`velox.Scan`),
  typed as the target's column. A JSON column of the target cannot be a
  result column, as the codec is the target's.
- The engine: `termJoined` holds the relation's index and the inner term.
  It writes the inner term with the writer's entity switched to the
  target, under the join's alias, as `rel.Is` writes conditions. The term's
  shape keys the render cache, and the statement's joins are already keyed.

## Consequences

- The expense-claim report ports as `Join(Items.Claim)`,
  `Where(Items.Claim.Is(claimWhere, C.Status.NotIn(...)))` and
  `Select(velox.Joined(Items.Claim, C.Status), …)` with `GroupBy` of the same
  terms, in one statement with no derived table. Under velox the claim's
  own policy applies in the join, where the veloxe code relied on the
  line's row scope.
- `TestJoinedTermSQL`, `TestJoinedTermRuns` and `TestJoinedTermRefused` in
  `internal/apitest` check the text on every dialect, a run on SQLite, a
  left join's NULL group, a filter in ON and the refusals.
  `TestJoinedTermGroupsByTheParent` in `internal/integration` runs it per
  caller under the ticket policy on every dialect the run has.

## Not done

- A to-many join in a projection, where the fan-out is wanted (0142).
- Terms of a relation two joins away: velox joins only the query's own
  entity's relations (0031).
