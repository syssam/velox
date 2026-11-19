# 0087. velox-graphql can make veloxe's API

- Status: accepted, 2026-11-19. The user chose this over the application
  writing its own SDL, and over changing the API.

## Context

The largest consumer's clients are built against the GraphQL API veloxe's
generator made. velox-graphql's differs from it in three ways:

- veloxe camel-cased a field from its schema name, with no initialisms:
  `customer_id` became `customerId`, `image_url` became `imageUrl`. velox names
  a field from its Go name, so these become `customerID` and `imageURL`. The
  consumer's API has about 3,600 such names.
- veloxe kept a foreign key in the type, as an `ID`, beside its edge: about
  6,300 fields. velox leaves it out, since the edge stands for it.
- veloxe named its where predicates from the field (`customerId`,
  `customerIdNEQ`, `customerIdIn`, `customerIdNotIn`). velox names them from
  the edge (`customerID`, `customerIDIn`).

The consumer's schemas also put `graphql.Type` on about 230 fields, which
velox refused, since `graphql.Type` named a schema's type. veloxe took it as
the field's GraphQL type: `ID` (also on string fields), the application's
scalars (`Map`, `Decimal`), lists (`[ID!]`, `[ProductTranslate!]`), and on
an enum field the enum's name, shared by every field of that name.

## Decision

- The plugin option `compat=veloxe` (`sdl.Config.Veloxe`) makes veloxe's API.
  It names fields and edges with veloxe's camel of the schema name, keeps a
  bound foreign key in the type as an `ID`, and names its predicates from
  the field, keeping IsNil and NotNil for an optional key. Edges keep `hasX`
  and `hasXWith`, as veloxe had them. It is an option of the plugin, not of
  the schema, because only `generate` runs plugins.
- `graphql.Type` on a field is the field's type, as veloxe took it, in
  either mode:
  - on an enum field it names the enum, declared once for the fields of one
    Go type that give the name. A velox-gen enum type renamed this way gets
    a Go alias of the name, which gqlgen's autobind finds;
  - on any other field it is a name or a list of one, which the application
    declares and binds to the field's Go type. velox validates its SDL with
    a scalar standing for each such type it does not declare. `ID` gives
    equality predicates;
  - an input takes the type when veloxe judged it could (a known scalar, a
    name ending in Input, an enum, an all-upper-case name, or a list of
    one). Otherwise a json field is JSON in the inputs, and any other field
    is refused there;
  - on the key it takes only `ID`.
- In compat mode a create or update input takes an edge's bound key as
  veloxe's create input did, by the key field's name (`customerId`).
- The Go names stay velox's (`CustomerID`): a GraphQL name that matches a Go
  name but for case binds to it.

## Consequences

- Without the option, velox-graphql's output is unchanged.
- Other differences between the consumer's API and this one are separate
  rows. Examples are the where predicates veloxe gave every field, and
  hand-shaped types such as `PublicCustomer`.
- The end-to-end test runs through gqlgen. The consumer builds with gqlc
  (graphql-go), which still needs its own check.
