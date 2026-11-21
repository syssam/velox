# 103. Foreign-key predicates and run-time row filters

Status: accepted, 2026-11-22.

## Context

A GraphQL filter on a foreign key reads a key whose target the reader
cannot see as null, as the edge does: `xIDIsNil: true` matches a row whose
target is hidden, `xID: 1` does not match it, and both agree with `hasX`.
gqlgraph made that choice when the predicate was built: it added the
edge's `Has` when `Table.HasRowFilter` was true, an interceptor or a Read
policy on the target. `HasRowFilter` reads the entity's metadata. A
`velox.WithFilter` filter (0098) is the DB's, not the entity's, so it did
not count. With a filter hiding a row's target, `xIDIsNil: false` matched
the row and `hasX: true` did not. A predicate is also built before the DB
that runs it is known (`Predicate(ctx)` outside a server), so no build-time
check can be right.

## Decision

- `Table.ScopedKey(p, edge, target)` is `p`, a condition on the foreign
  key of relation `edge`, read as the relation lets the statement see its
  target. With a row condition on the target, `p IS NULL` is the edge's
  `Has` negated, `p IS NOT NULL` is the `Has`, and any other `p` is `p AND`
  the `Has`. With none it is `p` alone, with no EXISTS, so a schema with
  no filters keeps its SQL.
- The scope decides, when the statement runs: the target has a row
  condition when `Scoper.target` gives it one, from the DB's `WithFilter`
  filters, its interceptors, or its Read (and, for a read, Query) policy
  unless `WithoutPolicy`. A filter that returns `NoFilter()` for the
  target, an interceptor that returns `Allow()`, or a lifted policy adds
  no `Has`, as the target then hides nothing.
- It is a method of `Table`, not the spec's `untyped.ScopedKey(p, edge,
  target)`: the `Has` needs the owner's table, as `Table.Has` does.
- The node is an And (an Or for `IS NULL`) whose `V` marks it, with `p`
  and the `Has` (negated) as kids. Every walker treats it as the And or Or
  it is. `Scoper.Node` replaces it with the decided form, so the render
  cache keys the SQL that is sent: a statement with the extra `Has` and one
  without never share an entry. Where no scope runs (velox's own reads by
  key), the node renders as it is, which means `p` on a target that hides
  nothing.
- `edge` must be a to-one relation whose foreign key is the owner's
  column, keyed by the target's primary key, and `p` a comparison on that
  column. Anything else (another column, an And or Or, a to-many or
  many-to-many relation, a key on the target, a foreign key in the
  owner's primary key) is an invalid condition,
  which fails the statement: the rewrite to the `Has` is right only for a
  key the relation reads.
- gqlgraph's `EdgeIDField` uses it for its IDs predicates and its IsNil
  and NotNil. `Table.HasRowFilter` stays, and still reads the metadata
  alone.

## Consequences

The relation of a foreign-key predicate must resolve on every DB now, not
only where its target is filtered: a predicate on an edge the entity lacks
fails the statement. gqlgraph's `TestFKScoped`, which used a relation that
did not exist to tell the two forms apart, is replaced. A target whose
policy or interceptor returns `Allow()` for the context gets no EXISTS,
where before it got one. Where foreign keys are not enforced, a key with
no target row then reads as the key it holds, not as null: the same as an
unfiltered target always did. A target whose Read policy denies the
statement, or fails, fails it, as any EXISTS on the target does. Generated
code does not change.

## Evidence

- `internal/apitest/scopedkey_test.go`: `TestScopedKey`, a WithFilter
  filter, an interceptor and a Read policy, `WithoutPolicy`, IS NULL, IS
  NOT NULL, EQ, In and NotIn, each run on one DB under contexts that
  differ, twice in turn, on the plain path and the hooked one;
  `TestScopedKeyDenied`, a target's Read policy that denies or fails;
  `TestScopedKeyChecks`, the conditions and relations it refuses;
  `TestScopedKeyRelationShapes`, a to-one relation keyed on the target
  and one whose foreign key is in the owner's primary key.
- `contrib/graphql/veloxgql/gqlgraph/scopedkey_test.go`:
  `TestForeignKeyRunTimeFilter`, `ownerIDIsNil`, `ownerIDNotNil`,
  `ownerID`, `ownerIDNEQ`, `ownerIDIn` and `ownerIDNotIn` agree with
  `hasOwner` under a filter, and have no EXISTS without one.
- `contrib/graphql/cmd/velox-graphql`: `TestForeignKeyFilterHidden` (a Read
  policy) and `TestForeignKeyFilterOtherScope` (a scope's interceptor).
