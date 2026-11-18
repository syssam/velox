# 0078. Text predicates that ignore case, and the string where defaults

- Status: accepted, 2026-11-19.

## Context

The largest consumer of the old velox widens every filterable string field to
`OpsString` with a generator hook, and its generated SDL has 1,017 `EqualFold`
and 1,017 `ContainsFold` predicates, beside `Contains`, `HasPrefix` and
`HasSuffix`. velox2 had `EqualFold` on a handle and none of these in
velox-graphql, so those filters would have left the API on a port. The same
names also meant less: the old velox's `OpsString` is equality, substring and
the two folds, its `OpsComparison` includes equality, while velox2's were the
substring predicates alone and the ordering ones alone, so a ported
`WhereOps(OpsString)` gave a narrower where input without an error.

## Decision

- A string handle has `ContainsFold`: `LOWER(column) LIKE LOWER(pattern) ESCAPE
  '!'`, the pattern holding the value literally, as `Contains` does. The same
  condition by name is `velox.FieldContainsFold`, and `untyped.Table.Pred` takes
  `PredEqualFold` and `PredContainsFold`. Both fold predicates compare LOWER of
  the column, so they ignore case on every dialect, where `Contains` follows the
  database. SQLite's LOWER folds ASCII letters only.
- velox-graphql has `OpEqualFold` and `OpContainsFold`, the fields
  `<field>EqualFold` and `<field>ContainsFold`. The bits follow the existing
  ones, so a stored annotation keeps its value.
- The sets mean what the old velox's did: `OpsCaseFold` is the two folds,
  `OpsSubstring` contains, has prefix and has suffix, `OpsString` is
  `OpsEquality`, `OpsSubstring` and `OpsCaseFold`, and `OpsComparison` is
  `OpsEquality` and the four orderings. `OpsAll` is still every predicate.
- `WhereOps()` on a string field offers `OpsString`, which is what the largest
  consumer's hook made of it. A nullable one adds `OpsNullable`, as before.
- The cost model charges `<field>EqualFold` and `<field>ContainsFold` as scans,
  as it does `Contains`: a plain index serves neither, and a name that does not
  end in `Contains` would otherwise have cost nothing.
- The generated code sets the two new fields of `gqlgraph.Ops`, so
  `veloxgql.GeneratedCodeIsVersion23` replaces Version22.

## Not done

`OpsStringBasic` (equality and contains) and a switch back to it are not
spellings: an app that wants fewer predicates names them in `WhereOps`.

## Consequences

- A string field with `WhereOps()` has two more predicates than before, and its
  generated Go code two more fields.
- A functional index on `LOWER(column)` is what makes a fold predicate cheap;
  the cost model does not know of one.

Enforced by `TestWhere` and `TestCostScans`, the golden files, the integration
test `TestTextMatchingFollowsTheDatabase` on PostgreSQL, MySQL and SQLite, and
`TestPluginThroughVeloxGen`.
