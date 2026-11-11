# 0068. velox-graphql:only=enums writes the enum marshalers alone

- Status: accepted, 2026-11-11.

## Context

A stored enum value is lower case (`open`); a GraphQL enum value is upper
case (`OPEN`). Something converts between them on every request.

Old velox did it in the core generator: every enum type had a `MarshalGQL`
that wrote `strings.ToUpper(e.String())` and an `UnmarshalGQL` that read it
back, whether or not the application used GraphQL. velox2's velox-gen writes
plain string types; `velox-graphql` writes the marshalers, in `*_gql.go`,
next to the SDL it generates.

An application that writes its own SDL and binds it with gqlgen's autobind
runs velox-gen without the plugin, so its enums have no marshalers. A port of
such an application from old velox found both failures: a task went out as
`"open"`, and the filter `{status: ACTIVE}` queried `status = 'ACTIVE'` and
matched nothing. It mended them with hand-written gqlgen marshalers and a
`models:` entry for each enum.

## Decision

`velox-graphql` takes one option, `only=enums`:

    go tool velox-gen -out veloxgen -plugin velox-graphql:only=enums ./veloxschema

It writes `<entity>_gql.go` with only the `MarshalGQL` and `UnmarshalGQL` of
each enum type velox-gen declares, the same code the full output writes, and
no `velox.graphql`. The values come from `sdl.Enums`, which uses the full
output's rule: the stored value upper-cased, or as `graphql.EnumValues` names
it. It skips the checks of names, keys and types the full output makes, since
the SDL is the application's.

The marshalers stay out of velox-gen:

- One generator owns an enum's GraphQL values. A rename by
  `graphql.EnumValues` is a GraphQL annotation, which the core does not read;
  a core method could only upper-case.
- `velox-graphql` already writes `MarshalGQL` on velox-gen's types; both
  writing it would declare it twice.
- ent's enum marshalers come from entgql too, not from ent.

## Consequences

- An application with its own SDL gets old velox's wire format by adding
  `:only=enums` to the plugin, with no `models:` entries and no hand-written
  marshalers.
- Input is stricter than old velox's: `UnmarshalGQL` takes only the declared
  GraphQL values, not the stored ones.
- An enum of the application's own Go type still brings its own marshalers,
  as in the full output.
- `TestAppOwnSDLEnums` runs gqlgen over a hand-written SDL; without the option
  it fails with `"open"` out and no rows for `IN_PROGRESS`.
