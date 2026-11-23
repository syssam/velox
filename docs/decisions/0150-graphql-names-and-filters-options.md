# 148. names= and filters= are options of their own; compat=veloxe presets them

- Status: accepted, 2026-11-24. Amends 0087 and 0088.

## Context

`compat=veloxe` turns on many behaviours with one switch (0087, 0088,
0108). Two of them are choices an application may want apart from the
rest.

- Names. veloxe camel-cased the schema name, with each word title-cased:
  `imageUrl`, `customerId`. GitHub's and Shopify's APIs use this style.
  velox camel-cases the Go name, as entgql does: `imageURL`, `customerID`.
  An application whose clients use the first style had to take all of
  veloxe's surface to get it.
- Where semantics. veloxe read an empty `idIn`, an empty `or` and a false
  `isNil` as adding nothing (0088). velox fails closed: an empty `idIn`
  matches no node. nextapp keeps access-policy rules as where documents
  (0146). A rule of `{"idIn": []}` that adds nothing lets every row
  through. Such an application wants veloxe's API with fail-closed filters.

## Decision

- `names=go|schema`. `go` is the default: `camel` of the Go name.
  `schema` is `veloxeCamel` of the schema name. Every name made from a
  field's or an edge's name follows it: the type's fields, predicates
  (`imageUrlNEQ`), create and update inputs, `has<Edge>`, the edge
  inputs, and the names `DecodeWhere` reads (`WhereField.Name`). An edge's
  foreign-key predicates and input outside compat are the edge's name
  plus `ID`, or `Id` under `schema`: `customerId`, `customerIdIn`.
  Edge-list inputs keep `IDs` (`addTagIDs`), as veloxe's did. Go names do
  not change, and gqlgen binds `imageUrl` to `ImageURL` ignoring case, as
  under compat. Order values are `graphql.OrderField`'s under either
  option.
- `filters=strict|veloxe`. `strict` is the default: velox's fail-closed
  semantics. `veloxe` gives 0088's semantics. The runtime flag is split.
  `gqlgraph.Type.VeloxeFilters` carries the where semantics, including the
  raised node budget. `Type.Veloxe` keeps the connections and plain
  foreign-key predicates (0108). Generated code writes each flag from its
  option.
- `compat=veloxe` presets `names=schema` and `filters=veloxe`. An explicit
  option wins. So `compat=veloxe,filters=strict` keeps veloxe's names and
  surface, with fail-closed filters. Under `compat=veloxe,names=go` a
  foreign key in the type is `customerID`, and its predicates follow.
- Outside compat, `names=schema` keeps the rest of velox's design: no
  foreign key in the type, scoped foreign-key predicates, no Node.
- `sdl.Config` takes `Names` and `Filters` as strings. `""` defers to
  `Veloxe`, and `SchemaNames()` and `VeloxeFilters()` resolve the preset.
  Any other value is refused with an error that lists the accepted ones.
  `only=enums` refuses both options, as it refuses compat.
- `veloxgql.GeneratedCodeIsVersion28`. Code generated under compat before
  this change sets only `Veloxe`. Against this runtime its filters would
  silently turn strict, so the version moves and such code stops
  compiling.

## Rejected

- A `names=entgql` value. `go` is entgql's style already, and naming the
  option after the source of a name says less than naming the style.
- One bool for both options. The point is to choose filters apart from
  names.
- Leaving the version at 27, as for `gqlgraph.Column.Count`. That field
  added a behaviour. This change takes one away from code that relies on
  it.
