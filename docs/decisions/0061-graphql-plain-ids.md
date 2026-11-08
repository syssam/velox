# 61. A GraphQL ID is the entity's key, and there is no Node

Status: accepted, 2026-11-08. Supersedes 0043's Node type, `GlobalID` and
`node_gql.go`, and the global IDs that 0033, 0035, 0045 and 0046 decode.

## Context

velox-graphql wrote a Relay global ID: base64url of `<Type>:<key>`. Each
type implemented `Node` (`IsNode`, `GlobalID`), `gen.Node` and `gen.Nodes`
served `node` and `nodes`, and every ID in an input or a where input was a
string that `gqlgraph.DecodeID` checked against its type. A resolver took
`id string` and called `veloxgql.DecodeID[int64](id, "Post")` before it
could use the key.

The user chose ent's model: in entgql a resolver gets the key itself. The
decode was boilerplate in every resolver, and applications look rows up
through their own query fields, not `node`. What the global ID bought, a
type check on every ID and a `node` that needs no lookup table, is traded
away (Consequences); access stays the policy's, as it was.

gqlgen binds every ID argument to one Go type: the first model `gqlgen.yml`
lists for `ID` (gqlgen v0.17.86, `codegen/args.go`, `buildArg` calls
`TypeReference(arg.Type, nil)`). A function marshaler is found by name, so
the model `veloxgql.Int64ID` resolves to `MarshalInt64ID` and
`UnmarshalInt64ID`. gqlgen casts only named types whose base is string.

## Decision

- A GraphQL ID is the entity's key, in the key's own Go type. The `id`
  field binds to the key field; an input's `<edge>ID` and `<edge>IDs`, and
  a where input's ID predicates, are keys of the target's Go type.
  gqlgen decodes them. A resolver takes `id int64`.
- There is one key Go type per schema. velox-graphql refuses a schema
  whose GraphQL types have keys of different Go types, naming two of them
  and their key types, and saying that gqlgen binds every ID argument to one
  Go type. A type left out with `graphql.Skip()` does not count.
- veloxgql has `MarshalInt64ID` and `UnmarshalInt64ID`, the model
  `veloxgql.Int64ID`. An ID goes out as a JSON string of the decimal digits,
  and comes in as such a string or as an integer. Anything else is
  BAD_USER_INPUT through the presenter, with a message that does not echo
  the value.
- The ID model per key type: `veloxgql.Int64ID` for int64,
  `veloxgql.UUID` for uuid.UUID, gqlgen's `graphql.ID` for string,
  `graphql.IntID` for int, and the application's own for a named type.
  `contrib/graphql`'s package documentation lists them.
- Node goes: the interface, `implements Node`, the `node` and `nodes`
  queries, `gen.Node`, `gen.Nodes`, `IsNode`, `GlobalID`, `veloxgql.Noder`,
  `EncodeID`, `DecodeID`, `IDType`, `ErrBadID`, `ErrWrongIDType` and
  gqlgraph's node lookups and Decode functions. `gqlgraph.IDs` caps and
  dedupes an input's list of keys.
- With no `QueryField`, `velox.graphql` has no `type Query`, and the
  application declares its own.
- A cursor stays opaque, holds its type's name, and a connection refuses one
  of another type with `ErrBadCursor`.
- `veloxgql.GeneratedCodeIsVersion21`. `velox.GeneratedCodeIsVersion19`
  stays.

## Consequences

- A resolver uses its `id` as it is:
  `db.Update[gen.Post]().Where(gen.Posts.ID.EQ(id)).Set(set...).One(ctx)`.
- An ID holds no type: the ID 7 of a Post and of a User are equal, and an
  ID given for the wrong type names the row of that key, if any. The
  entity's policy decides what a caller may read or change, as for any
  key.
- Renaming a type with `graphql.Type` changes cursors, not IDs.
- A Relay client that needs `node(id:)` gets it from the application, which
  may declare and resolve its own.
- A schema whose entities have keys of different Go types puts each other
  type behind `graphql.Skip()`, or moves them to one key type.
- velox-graphql's e2e apps that had a UUID or a named key in one module now
  have int64 keys. `TestAppUUIDKeys` serves an app of UUID keys of its own;
  a named key type is covered by unit tests of the generated code.

## Evidence

- `TestInt64IDMarshal`, `TestInt64IDUnmarshal` and `ExampleUnmarshalInt64ID`
  in `contrib/graphql/veloxgql`.
- `TestOneKeyType` and `TestKeyTypes` in `contrib/graphql/internal/sdl`: the
  refusal of mixed keys, its message, and the key's Go type.
- `TestVocabulary` in `contrib/graphql/internal/sdl`: the SDL has no Node,
  `node(`, `nodes(`, `implements` or `GlobalID`.
- `TestInputEdgeKinds`, `TestGoTypes`, `TestUUIDKeyIDs` and `TestNoIDs` in
  `contrib/graphql/internal/gogen`: input edge IDs and where IDs are keys of
  the key's Go type, and no `node_gql.go` is written.
- `TestIDsCap` and `TestIDsRepeats` in `contrib/graphql/veloxgql/gqlgraph`.
- `TestMutationSchemaServes` in `contrib/graphql/cmd/velox-graphql`: gqlgen
  serves `folder(id: 3)` and `updateFolder(id: 3, input: {parentID: "2",
  prevID: 2})` on SQLite with resolvers that take `id int64`, and an ID of
  `"c"` is BAD_USER_INPUT. `TestAppEnumRenamed` serves string keys through
  `graphql.ID`; `TestCollectRun` decodes `[ID!]!` to `[]int64`.
- `TestAppUUIDKeys` in `contrib/graphql/cmd/velox-graphql`: with ID bound to
  `veloxgql.UUID`, a resolver takes a `uuid.UUID`, an input's edge ID and a
  where input's ID are UUIDs, and a malformed one is BAD_USER_INPUT.
- `TestByID` and `TestBadIDs` in `examples/graphql`.
