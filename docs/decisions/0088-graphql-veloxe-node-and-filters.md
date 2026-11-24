# 0088. Under compat=veloxe, Node and where inputs are veloxe's

Status: accepted, 2026-11-19. Extends 0087. Amends 0061 for compat=veloxe
only: velox's own API still has no Node. Amended 2026-11-22 for
`ResolveNodes` and the node budget, and 2026-11-24 for what the filter
caps are under compat and for the type lookup (0149): see the notes at the
end.
Amended 2026-11-24 by 0150: veloxe's where semantics are the option
filters=veloxe and `gqlgraph.Type.VeloxeFilters`, which compat=veloxe
presets and filters=strict overrides; `Type.Veloxe` no longer gives them.
Amended 2026-11-24 by 0152: Node is the option node=on, which compat=veloxe
presets and node=off overrides, and which works without compat;
`ResolveNode` and `ResolveNodes` take `WithNodeType` and
`WithFixedNodeType`, which give an ID's type and skip the lookup.
Its GraphQL parts are superseded by [0156](0156-graphql-options-not-compat.md),
2026-11-24: compat is gone, and each part is a neutral option or a default.

## Context

veloxe's generator declared Relay's `interface Node { id: ID! }`, made every
type with a single ID implement it, and put `node(id: ID!): Node` and
`nodes(ids: [ID!]!): [Node]!` first in the root query. gqlgen bound Node to
the generated `Noder` interface (`@goModel`), which every entity satisfied
with `func (*X) IsNode() {}`; gqlgen refuses an implementing type whose Go
type does not satisfy the bound interface (`codegen/interface.go`).
`Client.Noder` found an ID's type by asking every registered type, sorted by
table, one query each, and refused an ID that more than one had
(`ErrAmbiguousNodeID`); an ID no type had was `ErrNodeNotFound`, and in
`Client.Noders` a nil entry. The consumer's clients use `nodes(ids:)` with
`... on SalesOrder` fragments; its resolvers call its own scan over the
registry, which turns a denying policy into "missing" and gives the
NOT_FOUND code `ENTITY_NOT_FOUND`. Its IDs are snowflakes, unique across
tables.

veloxe's where inputs also meant other things than velox's. Its generated
`P()` appended a predicate only for a non-empty In or NotIn
(`gqlrelay.AppendSlice`), for a true IsNil, NotNil or IsEmpty
(`AppendBool`), and for a non-empty or; it skipped null entries; a nested
input that set nothing was `ErrEmpty<T>WhereInput`, which an and skipped and
a not, an or or a has<Edge>With returned as an error; only the top level
swallowed it. Its only limit was depth 5. velox reads an empty In as false,
an empty NotIn as true, a false IsNil as IS NOT NULL, an empty or as false
and `not: {}` as matching nothing, and caps lists at MaxFilterWidth (100) and
a where argument at MaxFilterNodes (1000). The consumer's clients send
possibly empty and over-100 `idIn` lists, and rely on the old meaning.

## Decision

- Under `compat=veloxe` velox-graphql declares Node as veloxe did, bound by
  `@goModel` to `<generated package>.Noder` (`sdl.Config.Package`, which the
  plugin takes from the request), with `implements Node` on every type with
  an ID and `node` and `nodes` first in `type Query`, which is then written
  even with no QueryField.
- It writes `noder_gql.go`: `type Noder interface{ IsNode() }`, `IsNode` on
  each such entity, and `ResolveNode(ctx, db, id)` and
  `ResolveNodes(ctx, db, ids)` for the application's resolvers, and
  `NodeTypes()` for one that scans its own way. The resolvers stay the
  application's, as they were in the consumer: gqlgen asks for them.
- `gqlgraph.ResolveNode` probes every type of Node, in GraphQL name order,
  with `gqlgraph.LoadNode`: an untyped query of the type's table by its key,
  through `db`, so the Read policy, scope and interceptors apply. A row that
  is not there, one the policy hides, a policy that denies the read and a
  key the column cannot hold are all "none". No match is
  `gqlgraph.ErrNodeNotFound` (veloxe's text, code NOT_FOUND); two are
  `ErrAmbiguousNodeID`, naming the types; any other error stops it.
  `ResolveNodes` gives nil for a not-found ID and has no cap, as veloxe.
- `gqlgraph.Type.Veloxe`, which the generated type literals set under
  compat, gives a where input veloxe's semantics, in the runtime helpers:
  empty In, NotIn, HasSome and HasEvery, and false IsNil, NotNil and IsEmpty
  add nothing; an or of no inputs adds nothing; null entries are skipped; a
  nested input that sets nothing is BAD_USER_INPUT "empty predicate
  <T>WhereInput" in a not, an or or a has<Edge>With, and skipped in an and
  (only when it is itself empty, as veloxe compared with `==`).
  MaxFilterWidth and MaxFilterNodes do not apply; MaxFilterDepth does.
  (Superseded on 2026-11-22: MaxFilterNodes applies, as a budget of 10
  times it, never over 30000; see the amendments.)

## Why

- A flag in the `gqlgraph.Type` literal, not a second set of helpers or a
  per-schema runtime setting: the mode is the generated code's, fixed at
  generation as the API is, one line per type; the helpers stay compiled
  once for every input; and two schemas in one process may differ.
- A denial counts as none, where veloxe's `Client.Noder` aborted on it: the
  consumer replaced that behaviour because one ungranted type broke `node`
  for every other, and a polymorphic lookup must not tell a caller that a
  row it may not read exists. Not-found stays veloxe's error and code.
- Probing every type keeps veloxe's ambiguity check; an application that
  wants first-match order or a probe budget, as the consumer has, builds it
  on `NodeTypes` and `LoadNode`.
- No width or node cap under compat, rather than a cap with a compat
  default: veloxe had none, and `Limits` cannot tell an explicit 100 from the
  default. MaxCost still bounds a request, and depth stays at 5.

## Consequences

- An application under compat=veloxe declares `extend type Query`, not
  `type Query`, as it did under veloxe.
- A nodes(ids:) of many IDs costs one query per type per ID; the
  application caps it in its resolver if it needs to.
- velox's default SDL and Go are unchanged: no Node, no `noder_gql.go`, no
  `Veloxe` in a type literal, and velox's filter semantics and caps.

## Evidence

- `TestVeloxeNode` in `contrib/graphql/internal/sdl`: Node, `@goModel`,
  `implements Node`, node and nodes first; none of them by default.
- `TestVeloxeNoder` in `contrib/graphql/internal/gogen`: noder_gql.go and
  `Veloxe: true` under compat, neither by default.
- `TestVeloxeWhere`, `TestVeloxeDepth`, `TestVeloxeNullTests`,
  `TestResolveNode` and `TestResolveNodes` in
  `contrib/graphql/veloxgql/gqlgraph`: each case with velox's own result
  beside veloxe's; a policy that hides a row and one that denies; an
  ambiguous ID.
- `TestVeloxeCompat` in `contrib/graphql/cmd/velox-graphql` serves, through
  gqlgen, `node` with a fragment, `nodes` with a missing and a hidden ID
  (null entries), `node` of a missing or hidden ID (NOT_FOUND),
  `organizationIdIn: []`, `referrerIdIsNil: false`, `or: []`, an
  `organizationIdIn` of 151 IDs, and `not: {}` and `or: [{}]` refused.

## Amendment, 2026-11-22: nodes(ids:) is bounded

A review proved `nodes(ids:)` unbounded: `ResolveNodes` ran one query per
ID per type, serially, and the cost model charged 50000 IDs what it charged
one. `ResolveNodes` now works as entgql's `Noders`: it reads each type with
one `IN` query of the IDs its key column can hold, so T statements, not
N×T; each type's rows are one batch, so a nested edge is one statement;
and more IDs than `Limits.MaxPage` (100) is `ErrPageTooLarge`,
BAD_USER_INPUT. MaxPage, not a new limit: `nodes` returns a page of rows,
and a page is what MaxPage bounds. The cost model charges `node` and
`nodes` 100 for each type of Node, and the rows of `nodes` by the length of
its list. `ErrAmbiguousNodeID` is BAD_USER_INPUT, not an internal error.
`ResolveNode` still probes each type with `LoadNode`. Evidence:
`TestResolveNodesBatched` and `TestResolveNodesCap` in
`contrib/graphql/veloxgql/gqlgraph`, `TestCostNodesAndLists` in
`contrib/graphql/veloxgql/internal/cost`.

## Amendment, 2026-11-22: a node budget under compat

The decision above left a where argument under compat with only its depth
bounded, and graphqlgo applies no velox cost (0106): a request of one huge
`idIn` reached the database, and past the dialect's limit on bind
parameters it failed as an internal error. What was rejected above was the
width cap's default, which the consumer's over-100 `idIn` lists exceed, not
a budget. So under compat a where argument has a budget of 10 times
`MaxFilterNodes`, 10000 by default, an order above what the consumer sends;
a multiple, not a new limit, so `Limits` stays as it is and a lower
`MaxFilterNodes` lowers it too. Under compat or not, the budget is held to
30000: each predicate and each In value binds one parameter, so a where
argument stays under SQLite's 32766, and a larger one is `ErrFilterTooLarge`,
BAD_USER_INPUT, before any statement. The width cap still does not apply.
Evidence: `TestVeloxeWhere` (an `idIn` of 9000 passes and one of 10000 is
refused) and `TestVeloxeBindLimit` in `contrib/graphql/veloxgql/gqlgraph`.

## Amendment, 2026-11-24: the filter caps as built, and the type lookup

The decision's bullet and its "No width or node cap" reason still said
that MaxFilterNodes does not apply under compat. The code applies it, as
the amendment above says: `newBudget` in
`contrib/graphql/veloxgql/gqlgraph/where.go` gives a where argument of a
`Type.Veloxe` type a budget of 10 times `MaxFilterNodes` (10000 by
default), held to 30000 under compat or not, and a larger argument is
`ErrFilterTooLarge`, BAD_USER_INPUT, before any statement. What does not
apply under compat is MaxFilterWidth (`Filter.width` returns early), so an
`idIn` of over 100 IDs passes; MaxFilterDepth applies. Evidence:
`TestVeloxeWhere` and `TestVeloxeBindLimit`.

`ResolveNode` no longer probes every type, and `ResolveNodes` no longer
reads every type: one statement finds which types hold the IDs, and only
those are read, under their policies, with the same results (0149).
