# 0142. The nextapp port's projections: recipes, an aggregate in Calc's text, and dated non-edge joins

- Status: accepted, 2026-11-23. The aggregate in Calc's text is superseded
  by [0151](0151-explicit-aggregates-filter-entity-policy-db.md):
  `velox.Agg` marks one, and velox does not read the text.

## Context

The nextapp port triaged 240 `Modify`/`Select`/`GroupBy` sites
(`nv7-sites.md`). Most map to `Select`, `GroupBy` and `velox.Scan`. It asked
three questions about existing API and listed 10 statements as needing new
API: a dated join to `store_group_member` that no edge describes (8 rows in
`businessreport`), a computed sort key beside entity rows
(`customobject/sort.go:797`), a derived table so that a parent's columns
group a child's aggregate (`aggregate/expense_claim_item.go:147`, ADR 0143)
and an array inside a JSON object (ADR 0141).

Every answer keeps velox's rule: each table a statement reads is read
under its own entity's policy, filters and interceptors. That is why
`Expr` refuses a subquery and `FieldInSelect` exists (0057, 0079).

## Decision

Answers, each with a test in `internal/apitest`:

- **An aggregate of a column known by name** is
  `t, ok := velox.ColumnByName[E](col); t.Sum()`. It writes exactly what
  the handle's `Sum` writes, and on SQLite it rounds a decimal column as
  the handle does (0126). `Calc[E]("SUM(?)", t)` also works in `Select`
  and in `Having` beside `GroupBy`, but `Calc` knows nothing of decimals,
  so prefer `t.Sum()` (`TestAggregateOfAColumnByName`).
- **Fixed: an aggregate in Calc's text is an aggregate.** velox did not see
  that `Calc("SUM(?)", x)` is one. On MySQL a grouped projection ordered by
  it wrote `ANY_VALUE((SUM(x)))`. That form exists to lift
  ONLY_FULL_GROUP_BY for a key; around an aggregate it nests one aggregate
  in another, and MySQL refuses that. Now an expression whose text calls an
  aggregate function counts as an aggregate (`exprData.agg`). The function
  must be one of SUM, AVG, MIN, MAX, COUNT, TOTAL, ARRAY_AGG, STRING_AGG,
  GROUP_CONCAT, JSON_AGG, JSONB_AGG, JSON_GROUP_ARRAY, JSON_GROUP_OBJECT,
  JSON_ARRAYAGG, JSON_OBJECTAGG, BOOL_AND, BOOL_OR, EVERY, BIT_AND, BIT_OR,
  BIT_XOR, STDDEV, STDDEV_POP, STDDEV_SAMP, VARIANCE, VAR_POP or VAR_SAMP,
  in any case, followed by `(`. A name inside a string literal does not
  count, and neither does a text with OVER. So MySQL orders by the text as
  written, and `SetExpr` refuses it as it refuses an aggregate Term
  (`TestCalcAggregateText`). Nothing else was refused: `Expr`'s guard reads
  words that could name a table, and SUM is not among them.
- **COUNT(DISTINCT CASE WHEN c <> 0 THEN c END)** is
  `velox.Calc[E]("CASE WHEN ? <> 0 THEN ? END", c, c).CountDistinct()`. It
  writes `COUNT(DISTINCT (CASE … END))` on every dialect, and the CASE's
  NULL is not counted (`TestCountDistinctOfACase`).
- **ent's `GroupBy(keys...).Scan` with no aggregate** is
  `Select(keys...).Distinct()` (SELECT DISTINCT) or
  `Select(keys...).GroupBy(keys...)`, read with `velox.Scan`. Both read the
  same rows (`TestDistinctKeys`).
- **A computed column beside entity rows, with a keyset over it**
  (`AppendSelectExprAs` plus `Value(alias)`) needs no new API. Select
  every column with `ColumnByName` over
  `untyped.TableOf[E]().Info().Columns`, then the Calc with
  `.As("sort_key_0")`. Scan into a struct that embeds the entity and has
  a field tagged `velox:"sort_key_0"`. Order by the Term, then the key, and
  page with an `Expr` over the same Term:
  `Expr("? > ? OR (? = ? AND ? > ?)", key, k, key, k, ID, id)`
  (`TestEntityRowsWithAComputedKey`). `Paginate` and `After` refuse an
  ordering by an expression (0059), so the keyset is the caller's, as it
  was in veloxe. A query that also loads relations reads them in a second
  query by `ID.In(ids...)`.
- **A dated join to a membership table that no edge describes**
  (`businessreport`, 7 of the 8 rows). The schema says a store has at most
  one membership per hierarchy at an instant. So the group of a document
  is a function of the document's own store and date, given the
  memberships. Read the memberships first with
  `db.Query[StoreGroupMember]()` under their own policy (hierarchy, groups
  in the subtree, overlapping the period). Then write the group as a CASE
  Term over the document's columns, with the memberships' values bound:
  `CASE WHEN store_id = ? AND date >= ? AND date < ? THEN <group> … END`.
  Select it, group by it and keep `Expr("? IS NOT NULL", g)` for the
  join's inner semantics (`TestGroupByADatedMembership`). Write the group
  id as an integer literal: a bound THEN has no type on PostgreSQL, which
  would read the CASE as text. Each membership costs 5 parameters, so a
  hierarchy of a few thousand memberships stays under every dialect's
  limit. `membershipNarrow` (867/869, 978/980) is the same term in Where,
  with `Columns` for the entity read. A document matches at most one
  membership, so nothing repeats.
- **The elimination predicate (585)** composes too. The customer is
  `Invoices.Customer.HasWith(...)`, or `FieldInSelect("customer_id",
  "Customer", "id", ...)` for `credit_note`, which has no edge. The
  customer's link is
  `C.LinkedOrganizationID.InSelect(S.OrganizationID.Select(S.ID.In(stores...)))`.
  The only correlation left is the document's date against the
  membership's interval, so it is an `Or` of one such `HasWith` per
  membership, with that membership's stores and its interval on the
  document's date as bound values. The customer's Read policy then applies
  inside, which the veloxe code skipped on purpose ("whatever the caller's
  customer grants"). A port that keeps that rule runs the statement on
  `db.WithoutPolicy(reason)`, or opens the Customer policy to the report.

Written up, not built:

- **An outer reference.** Every shape of the dated join, and 585 without
  reading the memberships first, needs one thing velox does not have: a
  subquery condition that compares the target's column with the enclosing
  row's column, `outer.date >= m.effective_from`. A proposed form is
  `velox.Outer[T](E's handle or Term) Term[T]`. It would be an argument
  of `Expr` or `Calc` inside a `HasWith`, `InSelect` or `FieldInSelect`,
  written as the enclosing statement's alias. It keeps the rule: the
  subquery still reads T under T's policy, and the outer value is a column
  of a row the outer policy already let through. It was not built here.
  The engine would need an alias stack across subquery depths, and rules
  for an outer reference inside a policy's own condition, a hook's
  `Mutation.Where` and a Rewrite. The port does not need it, given the
  recipe above. Build it when a statement needs a correlation that cannot
  be read first, such as an unbounded membership table.
- **A correlated scalar lookup Term** (`(SELECT m.store_group_id FROM …
  WHERE … outer …)`) selected and grouped. It needs the outer reference.
  It is only right when the subquery is single-valued: PostgreSQL and
  MySQL fail at run time otherwise, and SQLite picks a row. And
  `GROUP BY (subquery)` behaves differently on each dialect, which is why
  0057's relation aggregates refuse `GroupBy`. Not proposed.
- **A general `JoinOn` to a to-many table.** It would let one row of E
  read as several, which 0031 refuses for entity reads. In a grouped
  projection the fan-out would be the point, but the ON clause needs the
  outer reference too. Not proposed until that exists.

## Consequences

- One engine change: `hasAgg` sees an aggregate in Calc's text. A
  `SetExpr` whose text calls an aggregate fails before anything is sent,
  where the database refused it before. MySQL's ordering by such a Calc in
  a grouped projection loses its `ANY_VALUE`.
- Until velox has an outer reference, raw SQL (`db.Raw`) stays the only
  form for a correlation that cannot be read first. Raw states every
  table's policy by hand, as the veloxe code already did.
