# 0077. graphql: the old velox's spellings

- Status: accepted, 2026-11-18.

## Context

The largest consumer of the old velox writes `graphql.QueryField()` with no name
353 times and `graphql.RelayConnection()` 352 times, and `WhereOps(OpsNone)`
once. Its `Filterable()` helper, `graphql.EnableWhereInputs(true)`, is on 2,837
fields; `WhereInput()` is on 10 edges, `WhereInputEdges` on 5 schemas, and
`NewDirective` is used once. velox2's `QueryField` took a name and had none of
the others, so those schema files did not compile.

## Decision

- `QueryField(name ...string)`: with no name the Query field is the plural of the
  entity's GraphQL type, split into words and joined in lower camel case with
  each later word title-cased, as the old velox named it (`User` gives `users`,
  `PerformanceGoal` gives `performanceGoals`, `GraphQLLog` gives `graphQlLogs`,
  `UserMFA` gives `userMfAs`). The plural is `plugin.Plural`'s, so `Terms` and
  `BusinessHours` keep their form (not the Go handle's `termsSlice`), plus the
  old velox's irregulars (`Person` is `people`). A client's queries therefore do
  not change on a port; a trial against the 365 Query fields of nextapp's
  checked-in SDL matched all but four whose plurals that file has stale. More
  than one name panics. The default is resolved in velox-graphql, so it collides
  with another entity's name as a written one does. `Annotation.QueryFieldDefault` carries it, and
  `Annotation.HasQueryField` is the test the rules use.
- `RelayConnection()` is accepted and says nothing. velox-graphql always
  writes Relay connections (edges, `pageInfo`, `totalCount`, the cursor
  pagination of `Paginate`), so there is nothing to switch on.
- `OpsNone` is `WhereOp(0)`, no predicate. A field without `WhereOps` has no
  where input, so `WhereOps(OpsNone)` is that; on the node key, which filters by
  default, only a non-zero `WhereOps` overrides it.

- `WhereInput()` and `EnableWhereInputs(true)` make a field or an edge
  filterable. On a field they are `WhereOps()`, the defaults for its type; on an
  edge, `WhereEdge()`; on a schema they are refused. The `Annotation.WhereInput`
  flag carries them, and velox-graphql resolves it by placement, so the old
  velox's `Filterable()` helper works unchanged. `EnableWhereInputs(false)` is
  `Skip(SkipWhereInput)`, which wins over a `WhereOps` on the same element. The
  old velox let a true win over that Skip; no nextapp schema sets both.
- `WhereInputEdges(names...)` on a schema is `WhereEdge()` on each named edge; a
  name that is not an edge of the schema is an error.
- `NewDirective(name, args)` and `Deprecated(reason)` build a `Directive`;
  `Deprecated("")` has no `reason` argument.

The old velox's string default was equality and contains only, which a nextapp
hook widened to `OpsString`; decision 0078 makes that velox2's default.

## Not done

`OpsJSONArray` (has, hasSome, hasEvery, isEmpty on a JSON array field, 40 uses
in 33 files) was left out here for want of ORM predicates; decision 0080 adds
them and the annotation. The largest consumer's own hook removes the where input
of every field not marked filterable, and none of the 40 is, so no such filter
was ever in its API. The old velox's `WithWhereInputs` filter-everything switch is not a
spelling: a field is filterable only when it asks.

## Consequences

- A port compiles `QueryField()`, `RelayConnection()`, `OpsNone`, `WhereInput()`,
  `EnableWhereInputs`, `WhereInputEdges`, `NewDirective` and `Deprecated`
  unchanged.
- The annotation's JSON gains optional `query_field_default`, `where_input` and
  `where_input_edges`, which an older plugin never sees.

Enforced by `contrib/graphql/internal/sdl/queryfield_default_test.go` and
`TestPortSpellings`.
