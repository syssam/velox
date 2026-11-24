# 0152. node= is an option of its own; compat=veloxe presets it

- Status: accepted, 2026-11-24. Amends 0061 and 0088.

## Context

velox's GraphQL API has no Node (0061). Under `compat=veloxe` it has
veloxe's Node: the interface, `implements Node`, `node` and `nodes` first in
the root query, and `noder_gql.go` (0088). An application that wants Node
with velox's own API had to take all of veloxe's surface to get it.

- entgql has one switch, `WithRelaySpec`, on by default. It turns on Relay
  connections and Node together, so Node is on by default as a side
  effect (entgo.io/contrib@v0.7.0, `entgql/extension.go` and
  `entgql/schema.go`, `relaySpec`).
- entgql finds an ID's type in one of two ways. ent's universal IDs
  (`sql/globalid`, experimental, off by default) give each type a range of
  1<<32 IDs, so the type is `id>>32` looked up in `IncrementStarts` (once
  the `ent_types` table). Or the application passes `WithNodeType` or
  `WithFixedNodeType` (`entgql/template/node.tmpl`; ent@v0.14.6,
  `entc/gen/feature.go`, `FeatureGlobalID`). Without either, a numeric ID
  queries a type table that does not exist, and a string or UUID ID fails
  with "cannot resolve noder without its type".
- Relay's Global Object Identification spec asks for `interface Node { id:
  ID! }` and `node(id: ID!): Node`, and for IDs unique across types.
- velox's lookup (0149) finds the types that hold an ID with one
  statement, for any key type, with no ranges. An ID that two types hold is
  reported as ambiguous.

An ID in velox is the entity's key (0061). Auto-increment keys collide
across types, so `node(id:)` over them is ambiguous for most IDs. Keys that
are unique across types, such as snowflakes, UUIDs and prefixed strings,
make Node useful.

## Decision

- `node=on|off`. `off` is the default. `on` declares 0088's Node surface
  outside compat: the interface, bound with `@goModel` to the generated
  `Noder`; `implements Node` on each written type with an ID; `node` and
  `nodes` first in `type Query`, which is then written even with no
  QueryField; and `noder_gql.go`. velox's other defaults hold: an ID is
  the key (0061), names are Go's, the foreign key stays out of the type,
  and filters fail closed.
- `compat=veloxe` presets `node=on`. An explicit option wins, so
  `compat=veloxe,node=off` keeps veloxe's API without Node.
- Under `entities=`, only the chosen types implement Node, as 0125 says,
  with or without compat.
- A type hint, as entgql's. `noder_gql.go` declares
  `type NodeOption = gqlgraph.NodeOption`,
  `WithNodeType(fn func(ctx, id K) (string, error))` and
  `WithFixedNodeType(name string)`. `ResolveNode` and `ResolveNodes` take
  `opts ...NodeOption`. The runtime is in gqlgraph: `gqlgraph.NodeOption`,
  `gqlgraph.WithNodeType[K]` and `gqlgraph.WithFixedNodeType`, and the
  options of `gqlgraph.ResolveNode` and `gqlgraph.ResolveNodes`. Generated
  code only forwards to them.
- With a type given, no lookup statement runs. Only that type is read,
  under its policies: a row that is not there, hidden or denied is
  `ErrNodeNotFound` in `node`, and null in `nodes`. An empty name means the
  type is unknown, and the ID is looked up as before. `nodes` looks up only
  the IDs that no option gives a type. A name that is no type of Node is an
  error that names it. The options are asked for every ID before any
  statement, so that error comes first. An error from the function is
  returned as it is. A later option replaces an earlier one, as in entgql.
- `sdl.Config` takes `Node` as a string. `""` defers to `Veloxe`, and
  `NodeOn()` resolves the preset. Any other value is refused with an
  error that lists the accepted ones. `only=enums` refuses `node=`, as it
  refuses compat. The plugin sets `sdl.Config.Package` from the request
  for `node=`, as for compat, to bind `Noder`.
- `veloxgql.GeneratedCodeIsVersion29`. Generated `noder_gql.go` names
  `gqlgraph.NodeOption`, which the old runtime lacks.

## Rejected

- `node=on` as the default, as entgql has it. Auto-increment keys, velox's
  default, collide across types, so `node(id:)` would be ambiguous for most
  IDs by default.
- One switch for connections and Node, as `WithRelaySpec`. velox always
  writes connections; Node is the part that depends on the keys.
- ID ranges per type, as ent's universal IDs. They need a type table or a
  schema-wide allocation, and velox's lookup already finds the type of any
  key.
- A defined `NodeOption` type in each generated package. It would need a
  conversion of each option before the call into gqlgraph, and buys nothing
  over the alias.

## Also reviewed

- Cursors. velox's cursor is versioned JSON in base64url, bound to its type
  and order. entgql's is msgpack `{i, v}` in base64, unversioned. Relay
  asks only that a cursor is opaque. No change. Under compat a connection
  also reads veloxe's cursors (0108).
- SDL layout. entgql writes one file, as velox does by default. Under
  compat velox writes a file per entity for gqlc (0125). `sdlfiles=` is
  already an option apart. No change.

## Evidence

- `TestNodeOption` in `contrib/graphql/internal/sdl`: Node, `implements
  Node` and a `type Query` of `node` and `nodes` with `node=on` outside
  compat, with Go names; only the chosen type under `entities=`, also with
  `sdlfiles=entity`; none with `compat=veloxe,node=off`, `node=off` or the
  default; `node=yes` refused.
- `TestNodeOption` in `contrib/graphql/internal/gogen`: `noder_gql.go`
  with `WithFixedNodeType` and no `Veloxe` under `node=on`; no
  `noder_gql.go` and `Veloxe` kept under `compat=veloxe,node=off`.
  `TestVeloxeNoder` checks the generated `NodeOption`, `WithNodeType`,
  `WithFixedNodeType` and the `opts` of `ResolveNode` and `ResolveNodes`.
- `TestRun` in `contrib/graphql/internal/gqlplugin`: `node=on`,
  `node=off`, `compat=veloxe,node=off` and `compat=veloxe`; `node=true`
  and an empty `node` refused; `only=enums,node=on` refused.
- `TestNodeType`, `TestNodeTypeErrors` and `TestNodesType` in
  `contrib/graphql/veloxgql/gqlgraph`: statements by table with a type
  given (no lookup), a hidden, denied or missing row as `ErrNodeNotFound`,
  an empty name falling back to the lookup, a later option winning, an
  unknown name failing before any statement, the function's error, an ID
  of another Go type, and `nodes` looking up only the IDs of no known
  type.
- `TestNodeOn` in `contrib/graphql/cmd/velox-graphql`: generates with
  `node=on`, builds with gqlgen, and serves `node` and `nodes` on SQLite
  through the generated resolvers with `WithNodeType`; a memo's ID runs no
  lookup, a customer's runs one, a hidden memo is null in `nodes` and
  `NOT_FOUND` in `node`, and `WithFixedNodeType` of an unknown name fails.
