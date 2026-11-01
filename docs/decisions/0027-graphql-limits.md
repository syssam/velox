# 0027. What the GraphQL layer limits, and what the generator refuses

- Status: accepted, 2026-11-01

## Context

A review proved ten defects. Introspection and `first: 0` were free under
`Limits.MaxCost`: a 4 KB request took 1.6 GB, and 2000 aliases ran 2000 COUNTs. A
Required edge rendered `T!`, so one hidden target nulled a page and revealed the row.
The generator accepted schemas that could only fail later.

## Decision

**The cost has two parts.** Rows, as before: a connection's `edges` multiply by its
page, and its `totalCount` and `pageInfo` no longer do. Statements: 100 for a root
field, a connection, its `totalCount` and any other field of objects, with no
multiplier, since a batch reads every parent in one statement. Multiplying it would
refuse two levels of pages with a to-one edge, which send three statements. An input
object adds its size at each use, read from the variables. The default allows 1000
statements.

**Introspection costs its response.** A walk over the schema counts one for each field
gqlgen would write, exactly: a multiplier per list would refuse GraphiQL's own query,
whose `ofType` chain is mostly null. Each (selection, value) is costed once, and the
walk stops at `MaxCost`, so its work is bounded. One unit allocates about 600 bytes on
the example's schema.

**Parsing has no limit from the Extension.** A handler extension cannot reach gqlgen's
executor, so the documentation and `examples/graphql` call
`srv.SetParserTokenLimit(15000)`.

**A Required unique edge is nullable, always.** The plugin's model has no policy, and
adding it would not be enough: a DB's `WithRewrite`, a replica's lag and a concurrent
delete hide a target as well, and none is in any model. `Create<T>Input` still requires
the `<edge>ID`.

**Names of introspection types work.** gqlgen's autobind binds `__Type` to a Go type
`Type` before it applies its builtins. velox.graphqls writes `extend type __Type
@goModel(...)` when the generated package has such a name, so an entity may be named
`Type`, `Field`, `Schema`, `Directive`, `EnumValue` or `InputValue`, and an enum
`TypeKind` or `DirectiveLocation`.

**`Unique().Optional()` without `Nillable` is refused** (rule 54), as is a unique index
of that field alone, unless a `DefaultFunc` or `SchemaDefault` may give each row its own
value.

**An empty string with a Default stays unset in the core.** Rule 36 refuses a Default on
a non-Nillable bool or number because the field could never store false or 0, which
are ordinary values. The same is true of `""`, and the rule was not extended: a Default
is the normal way to declare a string column, refusal would make each of them a NULL-able
pointer, and `""` on a defaulted string rarely means anything else. `field.Default`
documents it. GraphQL can tell the cases apart, as its input is a pointer, so
`Create<T>` refuses a given zero value (`""`, the zero time, the nil UUID) with
BAD_USER_INPUT rather than store another value silently. An update stores it.

**A Default is checked against its own built-in checks**, in the schema package. A
custom validator is not called: it is the application's code, and `Descriptor` also
runs when the program starts.

**A tenant column with an edge is no input**, and `ErrTenantMismatch` and
`ErrTenantImmutable` present as FORBIDDEN.

**`or: []` matches no row**, as `titleIn: []` does and as the input documents. A name that
is not ASCII is refused (rule 56): `plugin.Snake` works on bytes, and its output may not
change. Two edges bound to one foreign key are refused (rule 55).

## Consequences

- `GeneratedCodeIsVersion4`: `Create<T>` calls `gqlimpl.NotZero`.
- Clients of a Required edge see `T` where they saw `T!`.
- A large schema's GraphiQL query can pass the default `MaxCost`: it grows with the where
  inputs, at about 1100 per entity on the example. Raise `MaxCost` on the server that
  offers introspection.
- The statements part is an upper bound where a field does not read, and a lower bound
  where an application's resolver reads per row, as it was.

## Evidence

- `TestCostIntrospection`, `TestCostStatements`, `TestCostInputs` (veloxgql/cost_test.go);
  `TestRequestCost` (examples/graphql/main_test.go).
- `TestLimitsServed` (contrib/graphql/cmd/velox-graphql/limits_e2e_test.go) serves a
  hidden Required edge, a tenant key, an explicit zero value, `or: []` and the names
  through gqlgen on SQLite.
- `TestBuildUniqueOptionalRefused`, `TestBuildTwoEdgesOnOneKeyRefused`,
  `TestBuildNonASCIINamesRefused` (internal/codegen/build_refusals_test.go);
  `TestContradictions` (schema/field/field_test.go).
