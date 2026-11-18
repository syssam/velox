# 0071. A schema may add resolver fields to its GraphQL type

- Status: accepted, 2026-11-18. Amends the 2026-10-19 GraphQL design, which
  dropped old velox's `Resolvers`.

## Context

The 2026-10-19 design dropped old velox's `Resolvers`, `Map`, `Mapping`,
`MapsTo`, `Unbind`, `InterfaceField`, `UnionMember`, the federation
annotations and `Omittable`, leaving an application to write `extend type X
{ f: T @goField(forceResolver: true) }` in its own SDL.

An application ported from the old velox shows what that left out. nextapp
calls `graphql.Resolvers(graphql.Map(...))` 296 times, and uses no other of
the dropped annotations more than five times. The calls are almost all one
shape: an entity holds a key (`organization_id`, `approver_id`) of a type that
is not an entity of its schema, such as `PublicOrganization` or
`PublicEmployee`, which the application's own SDL declares and a data loader
fills; and `Map("organization", "PublicOrganization")` is the field that
returns it. A package of twenty helpers (`MapOrganization()`,
`MapCompany()`...) holds the repeated ones with their descriptions. The same
list in an `extend type` is 296 lines in a second file, kept in step by hand
with the entity's name and its helpers' descriptions.

## Decision

`graphql.Resolvers(mappings...)` on a schema, and `graphql.Map(field, type)`
with `.WithComment(text)`, as the old velox spelled them, so a port changes an
import and nothing else.

- A mapping is a field of the entity's type, after the generated ones. Its
  name may carry arguments in SDL, `priceListItem(priceListId: ID!)`; its type
  is the SDL's, nullability included. The plugin writes
  `@goField(forceResolver: true)` on it, and gqlgen asks for a resolver method.
- The type may be one the application's SDL declares. `Render` checks the SDL
  it wrote on its own, and leaves the mapped fields out of that check, since it
  cannot see the application's types; gqlgen, which reads both, refuses an
  undeclared one.
- What the plugin does check is that a mapping is one field and one type and
  nothing more, since it writes the text: a name and arguments that parse, a
  type that parses, no directive riding in the type, and a name no other field
  of the type has.
- Two `Resolvers` merge as a list, a field both name once, the later one's, so
  a mixin's and a schema's own compose.
- It applies to a schema whose type is shown; a field, an edge, a skipped
  schema and a join entity are refused.
- A selection that holds a mapped field reads every column of the entity. The
  generated `gqlgraph.Type` lists the columns each generated field reads, and
  a field it does not list turns the narrowing off, as one of an extend type's
  does. The old velox's `Loads` and `Reads`, which declared what a resolver
  reads so that narrowing could stay on, are not taken: nextapp uses neither.

## Consequences

- An entity with a mapped field reads whole rows when the field is selected.
  A query that does not select it narrows as before.
- A resolver that reads a column the query did not select gets it, which a
  resolver of an extend type already did.
- The generated code's shape does not change, so `GeneratedCodeIsVersionN`
  stays. The annotation's JSON gains an optional `resolvers`.
- The rest of the dropped list stays dropped.

Enforced by `contrib/graphql/internal/sdl/resolvers_test.go` and
`TestAppResolvers` (`cmd/velox-graphql`), which builds an application, runs
gqlgen over it with its own SDL for the mapped type, and serves the fields
through resolvers that read columns the query did not select.
