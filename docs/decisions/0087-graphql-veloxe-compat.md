# 0087. velox-graphql can make veloxe's API

- Status: accepted, 2026-11-19. The user chose this over the application
  writing its own SDL, and over changing the API.
  Amended 2026-11-24 by 0150: names from the schema name are the option
  names=schema, which compat=veloxe presets and names=go overrides.

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
- In compat mode `WhereOps()` on a string or integer field named as a key
  (`id`, `*_id`, `*ID`) gives equality alone (EQ, NEQ, In, NotIn, and IsNil
  and NotNil when nullable), as veloxe's defaults did.
- An Other field's handle is `velox.OrderedCol` (since 0096 `OtherCol`, or
  `NumberCol` in a decimal column) (GT, GTE, LT, LTE, Between,
  Min, Max), as veloxe's predicates had them: its SQL type, a decimal most
  often, compares in the database. velox-graphql's WhereOps on it gives
  equality and comparison, typed with its `graphql.Type` scalar.
- In compat mode a foreign key whose edge GraphQL leaves out stays in the
  type as an `ID`, and its WhereOps give field-named predicates on the key's
  own handle, as veloxe had them. Outside compat mode this is still refused.
- In compat mode explicit WhereOps on a nullable field add IsNil and NotNil,
  as veloxe did.
- In compat mode a create or update input takes an edge's bound key as
  veloxe's create input did, by the key field's name (`customerId`).
- The Go names stay velox's (`CustomerID`): a GraphQL name that matches a Go
  name but for case binds to it.

### The rest of veloxe's surface (2026-11-19)

An audit of the consumer's SDL against veloxe's found more rows. Each is
veloxe's rule, read in its generator (contrib/graphql/graphqlgen), and is
made in compat mode only:

- The order enum lists every field veloxe's did: the key first only when its
  `OrderField` names it, then in schema order each field that is not
  sensitive or skipped from the type or the order and is a string (not
  `field.Text`), time, int, int64, bool, enum or float; a declared foreign
  key too. A value is the `OrderField`, or the name in upper snake case.
  `OrderField` on any other field says nothing, as in veloxe. A type with
  `Skip(SkipOrderField)` or without a connection has no order. velox's guard
  against ordering by a field with an application directive does not fire:
  the consumer's `@sensitive` audits reads, and veloxe ordered by such
  fields.
- An Optional field is nullable in the type (veloxe's `!Optional &&
  !Nillable`), though its column is NOT NULL; gqlgen writes the Go value as
  non-null. The inputs keep velox's rules.
- A Required unique edge is non-null. velox makes it nullable because a
  policy, a hook, replica lag or a delete may leave the target unread; in
  compat mode such a read nulls the parent, as it would have in veloxe.
- Every type has a root Query field, named by `QueryField` or by default.
  A schema with `QueryField` and without `RelayConnection` has no
  connection, as veloxe's `hasRelayConnection` said: its Query field is
  `xs: [X!]!` with no arguments, its to-many edges are lists, and it has no
  connection, edge or order types. `graphql.RelayConnection()` therefore
  records itself; outside compat mode it still says nothing. The
  application writes the Query resolvers, as it did.
- A connection's `edges` is `[XEdge!]` and an edge's `node` is `X`.
- velox.graphql declares only `Cursor` and `Time`, and `Bytes` when a field
  is bytes, beside `PageInfo` and `OrderDirection`. The application declares
  `JSON`, `UUID` and `Uint64` where it uses them, as it did (the consumer's
  own `JSON` would otherwise clash); velox validates its SDL with a scalar
  standing for each.
- Where inputs: the key's and an Other field's default predicates are
  equality alone; an explicit `WhereOps` replaces `WhereInput`'s defaults
  instead of adding to them; no directive is on a where-input or mutation
  input field (veloxe put directives on the type's fields alone).
- Silently ignored, as veloxe ignored them: `Resolvers`, `QueryField` and
  `Mutations` on a skipped schema; `WhereOps` and `OrderField` on a
  sensitive field or one skipped from the type; `WhereOps` on a json field
  veloxe gave no predicates (any but a slice of scalars with a JSON array
  op); a directive on a foreign key the type shows, which is rendered there.
- A json field of a named Go type is typed by the name, as veloxe's
  `getTypedJSONScalarName` and `inferGraphQLSliceType` derived it: a named
  type or a pointer to one is its name, a slice of one is `[Name!]`. The
  application declares the type, as it did (veloxe's SDL only named it);
  velox binds the field's own Go type, as for `graphql.Type`, and an input
  takes `JSON` for it.

A separate plugin option, `where=whitelist`, leaves out the predicates of
every field but the key that lacks `WhereInput` (the old velox's
`EnableWhereInputs(true)`), whatever its `WhereOps`. It is the consumer's
codegen hook under veloxe, not veloxe's rule, so it is not part of
`compat=veloxe`.

## Consequences

- Without the option, velox-graphql's output is unchanged.
- Other differences between the consumer's API and this one are separate
  rows: the Node interface and node/nodes, and hand-shaped types such as
  `PublicCustomer`.
- velox's cost model charges a list of objects `MaxList` rows for each
  parent: a root list and a json list of a named type are such lists, so an
  application may need to lower `MaxList` or raise `MaxCost`.
- veloxe's order by an edge's count (`OrderField` on a to-many edge) is not
  made.
- The end-to-end test runs through gqlgen. The consumer builds with gqlc
  (graphql-go), which still needs its own check.
