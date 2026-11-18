# 0077. graphql: QueryField(), RelayConnection and OpsNone

- Status: accepted, 2026-11-18.

## Context

The largest consumer of the old velox writes `graphql.QueryField()` with no name
353 times and `graphql.RelayConnection()` 352 times, and `WhereOps(OpsNone)`
once. velox2's `QueryField` took a name and had no `RelayConnection`, so those
schema files did not compile.

## Decision

- `QueryField(name ...string)`: with no name the Query field is the entity's
  plural in lower camel case, as entgql names it (`User` gives `users`,
  `PerformanceGoal` gives `performanceGoals`), through the same plural velox
  uses for table and handle names. More than one name panics. The default is
  resolved in velox-graphql, so it collides with another entity's name as a
  written one does. `Annotation.QueryFieldDefault` carries it, and
  `Annotation.HasQueryField` is the test the rules use.
- `RelayConnection()` is accepted and says nothing. velox-graphql always
  writes Relay connections (edges, `pageInfo`, `totalCount`, the cursor
  pagination of `Paginate`), so there is nothing to switch on.
- `OpsNone` is `WhereOp(0)`, no predicate. A field without `WhereOps` has no
  where input, so `WhereOps(OpsNone)` is that; on the node key, which filters by
  default, only a non-zero `WhereOps` overrides it.

## Not done

`OpsJSONArray` (has, hasSome, hasEvery, isEmpty on a JSON array field, 26 uses)
needs predicates on JSON array handles that the ORM does not have; a JSON field
has no where predicates today. `EnableWhereInputs` and `WithWhereInputs`
(a where input for every field) are the opposite of velox2's opt-in `WhereOps`.

## Consequences

- A port compiles `QueryField()`, `RelayConnection()` and `OpsNone` unchanged.
- The annotation's JSON gains an optional `query_field_default`, which an older
  plugin never sees.

Enforced by `contrib/graphql/internal/sdl/queryfield_default_test.go` and
`TestPortSpellings`.
