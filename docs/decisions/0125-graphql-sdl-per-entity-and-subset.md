# 0125. velox-graphql writes the SDL per entity, and for a part of the schema

- Status: accepted, 2026-11-23.

## Context

velox-graphql wrote one SDL file, `velox.graphql`. A port of a 496-entity
application served by graphql-go found two problems with it.

gqlc, graphql-go's generator, makes one Go package of each SDL file. It
names an argument struct `<Type><Field>Args`, and that of a root field
`<Field>Args`. In one file, `Query.salesOrderItems` and `SalesOrder.items`
both name `SalesOrderItemsArgs`. The application's 163,000-line SDL had 89
such argument structs and 107 such method names, and gqlc's package did not
build. veloxe wrote `velox.graphql`, with Query, and one
`velox_<entity>.graphql` per entity, so Query's names and an entity's were
in different packages.

The application serves a slice of its schema over GraphQL. It wrote a
slicer over `velox.graphql`. The slicer had to keep every where input that a
kept where input nests, however far: graphql-go binds an input to its Go
struct and refuses a Go field that the SDL lacks. velox-graphql writes the
where inputs' Go structs for the whole schema.

## Decision

Two options of velox-graphql, also accepted by `graphqlgen.Plugin`.

`sdlfiles=entity` writes `velox.graphql` and one file per entity.
`sdlfiles=one` writes `velox.graphql` alone:

- An entity's file holds its type, connection, order, where input,
  mutation inputs, and each enum that only its elements name. The file is
  `velox_<label>.graphql`, as `<label>_gql.go` is named. Under compat=veloxe
  it is `velox_<name in lower case>.graphql`, veloxe's name. Two entities
  with one file name are refused.
- `velox.graphql` holds the vocabulary (scalars, PageInfo,
  OrderDirection), Node, the root Query, each enum two entities' elements
  name, and gqlgen's introspection binds.
- The default is `one`. Under compat=veloxe it is `entity`, as veloxe wrote
  it. `sdlfiles=one` keeps one file under compat=veloxe.
- `gqlc.yaml`'s `groups: { trimPrefix: velox_ }` then makes package
  `velox` of Query and package `salesorder` of SalesOrder. Every name
  collision is gone, since a type's names are unique within its type.

`entities=A+B` writes the SDL of the entities A and B alone. `+` separates
names because the plugin's options are separated by commas. A name is a
schema's name, not a GraphQL type's. A name the schema package does not
declare, a join entity, and an entity whose type graphql.Skip removes are
refused. The closure rule:

- An entity chosen has its type and every input, and its Query field. No
  other entity has a Query field.
- A field of a written element is left out when it names a type the SDL
  does not declare. That covers an edge to an entity not chosen, and a
  connection to one. A foreign key's ID field stays. A type of
  graphql.Type stays, since the application declares it.
- A written input keeps every field, so its Go struct binds. An input that
  a written input names is written too, however far that goes: in practice
  the where input of an entity not chosen, which `hasXWith` nests. Its
  entity's type, connection and other inputs are not written. Under
  `sdlfiles=entity` it goes in that entity's file.
- An enum is written when a written element names it.
- Node and `node`/`nodes` stay when a chosen entity has an ID. Only the
  chosen entities implement Node.
- The Go velox-graphql writes is the whole schema's. The option changes
  only the SDL, so one generated package serves every slice.

Under `only=enums`, `sdlfiles` and `entities` are refused, as `compat` and
`where` are. velox-graphql writes no SDL then.

## Consequences

- On the 496-entity schema (478 entities with a type), compat=veloxe now
  writes 479 files, veloxe's count. gqlc names no argument struct or method
  twice in any file (89 and 107 before). The 483 packages it writes build:
  62 s on a cold cache, 530 s of CPU.
- `entities=SalesOrder+SalesOrderItem` on that schema writes four files.
  One holds only SalesOrderTeamMember's where input. graphql-go's
  `NewSchema` binds every input. The application's slicer is no longer
  needed.
- `node(id:)` given an ID of an entity not chosen resolves to a Go type the
  SDL does not declare, and graphql-go fails that field.
- An application that ran compat=veloxe and read `velox.graphql` alone reads
  `velox*.graphql` now, or passes `sdlfiles=one`.
- The generated Go does not change, so no version constant moves.
- `TestGqlcPerEntity` builds a model whose `Query.postTags` and `Post.tags`
  both name `PostTagsArgs`. From one file the package does not build. Per
  entity, and with `entities=Post`, it does.
