# 0077. graphql: QueryField(), RelayConnection and OpsNone

- Status: accepted, 2026-11-18.

## Context

The largest consumer of the old velox writes `graphql.QueryField()` with no name
353 times and `graphql.RelayConnection()` 352 times, and `WhereOps(OpsNone)`
once. velox2's `QueryField` took a name and had no `RelayConnection`, so those
schema files did not compile.

## Decision

- `QueryField(name ...string)`: with no name the Query field is the plural of the
  entity's GraphQL type, split into words and joined in lower camel case with
  each later word title-cased, as the old velox named it (`User` gives `users`,
  `PerformanceGoal` gives `performanceGoals`, `GraphQLLog` gives `graphQlLogs`,
  `UserMFA` gives `userMfAs`). The plural is `plugin.Plural`'s, so `Terms` and
  `BusinessHours` keep their form (not the Go handle's `termsSlice`), plus the
  old velox's irregulars (`Person` is `people`). A client's queries therefore do
  not change on a port; a trial against the 365 Query fields of nextapp's
  checked-in SDL matched all but four whose plurals that file has stale. More than one name panics. The default is
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
