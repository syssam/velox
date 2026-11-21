# 107. engine=graphql-go output for gqlc

Status: accepted, 2026-11-22.

## Context

nextapp serves GraphQL with github.com/syssam/graphql-go, and binds its SDL
to Go with graphql-go's generator gqlc, by autobind (spec 0.2). The graphqlgo
adapter (0106) runs velox-graphql's generated code under graphql-go, but the
output was gqlgen's: run through gqlc, it put every edge with arguments,
every connection's pageInfo and cursor, and every time field on a Resolver
stub, and generated models of its own for velox's inputs, enums and scalars,
because gqlc found no Go type of theirs to bind to (14 Resolver methods
beside the 3 root fields on examples/graphql's schema).

## Decision

- velox-graphql and `graphqlgen.Plugin(opts)` take `engine=gqlgen` (the
  default) and `engine=graphql-go` (`sdl.Config.GraphQLGo`). Under gqlgen
  the output is unchanged, byte for byte. `only=enums` takes either and
  writes the same Go. graphql-go stays out of the generator's imports
  (`TestGeneratorImportsNoEngine`).
- Under graphql-go the SDL binds every type it declares but Query with
  `@goModel`: an entity to its own Go type (`Account` to `gen.User`, not
  the alias `gen.Account`, which gqlc does not see through: bound to the
  alias it left `Label.posts` and `LabelEdge.node` on Resolver stubs and
  wrote a conversion that did not compile), a velox-gen enum to its type,
  an application's enum to its own, connections, edges, orders, order
  fields, where and mutation inputs to theirs, PageInfo and OrderDirection
  to veloxgql's, Node to the Noder under compat. Each scalar velox declares
  names the Go type entities hold: Time `time.Time`, UUID
  `github.com/google/uuid.UUID`, Bytes `[]byte`, Uint64 `uint64`, Cursor
  and JSON veloxgql's. Explicit models make the binding independent of
  autobind's order: nextapp autobinds a second package.
- Under graphql-go there are no introspection binds, which only gqlgen's
  autobind needs.
- The Go is gqlgen's, with no graphql-go import, and two differences. An
  input's Go field whose GraphQL name graphql-go would not derive (its
  rule: UUID, GUID, ID and URL as words, first letter lower; `AuthorID` is
  `authorId`) has `graphql:"authorID"`; gogen carries a copy of the rule,
  tested against graphql-go's own. An optional list value in a mutation
  input is `[]T`, nil when not given, and not `*[]T`, which graphql-go
  cannot bind; a list given empty is an empty slice.
- gqlc does not honour `@goField(name:)` yet (spec GA1, deferred), and an
  element bound by it would fall to a Resolver stub. So under graphql-go
  velox-gen refuses, naming the schema, the field or edge and the reason
  "gqlc does not yet honour @goField(name:)", and writes nothing: a field
  or edge `graphql.FieldName` renames, a field read through a
  `<Field>GQL` getter (a JSON field, a non-null bytes field, a field of
  the application's own Go type), and a key whose Go name is not ID (Q2).
- `graphqlgo.Scalars()` also binds Int to int8, int16, uint8, uint16 and
  uint32, and Uint64 to uint, as gqlgen.yml lists models for them: a
  generated input's field of such a type does not bind otherwise. A value
  outside the Go type's range, or Int's 32 bits, is the client's error.
- `veloxgql.GeneratedCodeIsVersion27` stays. It changes when code generated
  before no longer fits the runtime; gqlgen's output is unchanged, and the
  graphql-go output names nothing the runtime lacks.

## Consequences

- With the keys doc.go lists (`autoBind`, `models: {ID: int64}`,
  `modelDirective`, `fieldDirective`, `zeroForNullInputs`,
  `inputListPointers`, `inlineAccessors`), gqlc puts only the root fields
  on Resolvers and generates no model. Each root field is one line over
  the generated code, as under gqlgen.
- graphql-go writes a nil `[]byte`, a NULL bytes field, as `""` where
  gqlgen writes null, as nextapp's veloxe binding does today.
- Under compat=veloxe the application declares JSON, UUID, Uint64 (and
  Bytes when no field is bytes) and binds them in gqlc.yaml's models, as
  veloxe left them to it. graphql-go logs that it walks `[]Noder` (nodes)
  by reflection.
- The getters' fields are refused until gqlc reads `@goField(name:)`;
  nextapp's SDL has 26 of them, which need `graphql.Type` or a schema
  change first.

## Evidence

- `contrib/graphql/cmd/velox-graphql`: `TestGqlcBinds` generates a schema
  with every scalar but JSON, an enum, an int8, an optional list, unique,
  connection and many-to-many edges, a multi-field order and mutation
  inputs with `engine=graphql-go`, runs gqlc of graphql-go 3e0050b
  (nextapp's pin) with doc.go's keys, finds the root fields alone on the
  Resolvers and no `graph/model`, passes `gqlc -check`, and serves two
  queries and two mutations through `graphqlgo.With` on SQLite.
  `TestGraphQLGoRefusesThroughVeloxGen`: velox-gen fails naming the field
  and writes no `gen`.
- `contrib/graphql/internal/sdl`: `TestGraphQLGoGolden`
  (`testdata/graphqlgo.graphql`, `graphqlgo_veloxe.graphql`),
  `TestGraphQLGoBindsEveryType`, `TestGraphQLGoRefusesNameBindings`,
  `TestGraphQLGoNeedsPackage`; `TestRenderGolden` unchanged.
- `contrib/graphql/internal/gogen`: `TestGraphQLGoGolden`
  (`testdata/graphqlgo`), `TestGraphQLGoInputs`, `TestGraphQLName`;
  `TestGoGolden` unchanged.
- `contrib/graphql/internal/gqlplugin`: `TestRun` (engine values, the
  refusal, only=enums). `contrib/graphql/graphqlgen`: `TestPluginAnswers`.
- `contrib/graphql/veloxgql/graphqlgo`: `TestScalarsIntegers`.
- examples/graphql regenerated with gqlgen generate: no difference.
