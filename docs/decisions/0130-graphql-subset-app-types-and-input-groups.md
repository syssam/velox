# 0130. A subset's SDL drops unlisted application types, and nests inputs in the shared file

- Status: accepted, 2026-11-23. Amends 0125.

## Context

ADR 0125's `entities=` writes the SDL of a part of the schema. A field
whose type graphql.Type or graphql.Map names stays, since the application
declares that type. The port of a 496-entity application ran
`entities=SalesOrder+SalesOrderItem` with gqlc and hit two problems.

The slice names eleven of the application's object types, such as
PublicEmployee and CustomFieldValue, through graphql.Map resolver fields.
The slice serves none of them. The application declared eleven stub types
holding only `id`, and wrote 21 resolvers that return null.

Under `sdlfiles=entity`, one file held only SalesOrderTeamMember's where
input, which a written where input nests. gqlc made a package of it whose
`Bindings()` takes no Resolver. gqlc's `NewSchema` registers such a package
itself, and says so in its comment. The application passed that package's
`Bindings()` to `NewSchema` as it passed the others. graphql-go then bound
the input twice and refused it: `Input "SalesOrderTeamMemberWhereInput":
already bound`. Neither velox's generated Go nor `veloxgql/graphqlgo`
registers a binding; the second registration was the application's call.

graphql-go binds an object type's fields to Go methods and fields by name.
A Go type may have more of them than the SDL type, as the edges to entities
not chosen already show. An input's Go struct must have every field of the
SDL input, and no field the SDL lacks.

## Decision

`apptypes=A+B` names the types the application declares that the SDL of
`entities=` may name. It is an option of velox-graphql and
`graphqlgen.Plugin`.

- A field of a written type that names a type neither velox-graphql's SDL
  declares nor `apptypes=` lists is left out. Its arguments' types count.
- A written input whose field names such a type is refused, naming the
  input, the field and the type. graphql-go binds every field of an input.
  The application then lists the type, as a scalar it declares.
- A name velox-graphql declares, and one that is not a GraphQL name, are
  refused. `apptypes=` without `entities=` is refused.
- Without `apptypes=`, every type graphql.Type and graphql.Map name stays,
  as 0125 has it. A list was chosen over `apptypes=drop`: velox-graphql
  cannot tell an application's scalar, such as Decimal, which inputs need,
  from an object type, which only resolver fields name. The list states the
  application's contract, and a missing scalar fails generation, not
  `NewSchema`.

Under `sdlfiles=entity` with `entities=`, the inputs written of an entity
whose type is not go in `velox.graphql`, with their enums, and not in that
entity's file (0125 put them there). Every file but `velox.graphql` then
holds a type, so no gqlc group is made of inputs alone. The `velox` group
holds Query, so its `Bindings` takes the Resolver and the inputs register
once.

gqlc's rule is unchanged and is gqlc's: a group whose `Bindings()` takes no
Resolver is registered by `NewSchema`. An application passes only the
groups whose `Bindings` takes a Resolver. velox does not change gqlc.

## Consequences

- On the 496-entity application, `entities=SalesOrder+SalesOrderItem,
  apptypes=Decimal+Map` writes three files, not four, and gqlc four groups,
  not five. The application's SDL declares no stub type (eleven before) and
  its resolver writes one method that returns null, for
  `SalesOrder.relatedOrders`, a field of a chosen entity (22 before).
- `TestGqlcPerEntity` runs gqlc over `sdlfiles=entity,entities=Post,
  apptypes=PublicEditor`. Two files are written, no gqlc package's
  `Bindings` takes no Resolver, and the field of the unlisted type is gone.
- `TestEntitiesAppTypes` has the golden `entities_apptypes.graphql`.
- The generated Go does not change, so no version constant moves.
