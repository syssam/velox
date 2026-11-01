# 0027. What the GraphQL layer limits, and what the generator refuses

- Status: accepted, 2026-11-01

## Context

A review proved ten defects. Introspection and `first: 0` were free under
`Limits.MaxCost`: a 4 KB request took 1.6 GB, and 2000 aliases ran 2000 COUNTs. A
Required edge rendered `T!`, so one hidden target nulled a page and revealed the row.
The generator accepted schemas that could only fail later.

A second review proved that all of it ran after validation, which did the damage
first: 12 KB of `a: id a: name`, under the token limit, took 3.49 GB and 2 s in
gqlparser's OverlappingFieldsCanBeMerged, and answered with 250 MB of errors.

## Decision

**The cost has two parts.** Rows, as before: a connection's `edges` multiply by its
page, and its `totalCount` and `pageInfo` no longer do. Statements: 100 for a root
field, a connection, its `totalCount` and any other field of objects, with no
multiplier, since a batch reads every parent in one statement. Multiplying it would
refuse two levels of pages with a to-one edge, which send three statements. An input
object adds its size at each use, read from the variables. On a mutation it adds what
it sends: the row read back, two for a non-empty `<edge>IDs`, `add<Edge>IDs` or
`remove<Edge>IDs`, one for an `<edge>ID`, three for `clear<Edge>: true`. 30 `updatePost`
with `title`, `clearTags` and `addTagIDs` send 208 statements and cost 21180, where they
counted as 30. The default allows 1000 statements.

**A directive does not lower what it does not stop.** `@skip` on a leaf leaves the value
above it read, so an object whose every selection is left out costs what one selecting
`__typename` does: `tags(first: 100) { edges { cursor @skip(if: true) } }` under two
levels of pages costs 1030602, not 30602, and is refused. The same holds of each value of
introspection: 1800 `a: __schema { types { __typename @skip(if: true) } }` cost 93600,
not 3600. A field skipped itself is not resolved, and costs nothing.

**Introspection costs its response, under its own limit.** A walk over the schema counts
one for each field gqlgen would write, exactly: a multiplier per list would refuse
GraphiQL's own query, whose `ofType` chain is mostly null. Each (selection, value) is
costed once, and the walk stops at `Limits.MaxIntrospectionCost`, so its work is bounded.
It is its own limit because GraphiQL's query cost 42266, 110966, 207146 and 688046 on
velox-graphql's schema of 30, 80, 150 and 500 rich entities (`richSchema`; a review
measured 45536, 119686 and 223496), and raising `MaxCost` to admit it loosened the data
budget. The default, 250000, admits it on 150 and leaves 20% over. A bound of 1000000 was
measured at 640 MB and 490 ms through gqlgen, answering with 17 MB: 285 aliases of
GraphiQL's selection on the example's schema, about 640 bytes a unit. That is too much
to allow by default for the sake of schemas few have, so 250000 is about 160 MB at that
rate: a larger schema raises it, and a server open to strangers lowers it, or has no
`extension.Introspection`.

**A document is refused before gqlgen parses it.** gqlgen's `graphql.OperationParameterMutator`
runs first, on every transport, at v0.17.86 and at v0.17.98. The Extension parses the
query there, with
`Limits.MaxTokens` (15000), counts the fields (`Limits.MaxFields`, 2000), and merges the
selection sets as validation would, each fragment where it is spread. The quadratic rule
compares the pairs of fields under one response name, so at most 5 times `MaxFields` pairs,
10000, reach it. A pair that conflicts costs about 3.5 KB through gqlgen; a fragment DAG
stops at 100 times `MaxFields` fields. This does not replace the rule (`SetValidationRulesFn`
is also there at both versions), as a copy of its algorithm is ours to keep, and it works
without any call on the server. It is a second parse: 46 microseconds and 36 KB on
GraphiQL's query, on each request, and a gqlgen query cache does not spare it. `Limits`
holds the three numbers so that one call sets them. Over the tokens is the parser's
GRAPHQL_PARSE_FAILED, as gqlgen says it; the rest BAD_USER_INPUT, `ErrRequestTooLarge`.

| A request | Bytes | Allocated | Time |
| --- | --- | --- | --- |
| `a: id a: name` x 1000, no budget | 12033 | 3884 MB | 2.1 s |
| the same | 12033 | 0.7 MB | 1.5 ms |
| 4990 `a: id`, near the token limit | 24983 | 1.5 MB | 1.8 ms |
| over the token limit | 48033 | 4.6 MB | 3.1 ms |
| `a: id a: name` x 70, at the pairs, validated | 878 | 18.6 MB | 12 ms |
| the same in 141 fragments | 5452 | 20.7 MB | 15 ms |
| 99 `a: posts(first: n)`, each pair conflicting | 3389 | 17.8 MB | 10 ms |
| 1990 unknown fields, with Setup | 10863 | 6.6 MB | 5 ms |
| a DAG of 40 fragments | 2797 | 27.6 MB | 27 ms |

**`Setup` is the one call a server makes.** The body is read by the transport, before any
extension, so `veloxgql.Setup` returns a handler that refuses one over
`Limits.MaxBodyBytes` (1 MiB) with 413; a variable of 2 M IDs stops there. It uses the Extension and `ErrorPresenter`, sets gqlgen's token
limit, and turns off the suggestions of a field's name, which take 25 KB for each unknown
field and show the schema. A server that uses only the Extension keeps the document's
limits and has no body limit.

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
value. Its message names `DefaultFunc`: velox-gen cannot see a hook that fills the field.

**An empty string with a Default stays unset in the core.** Rule 36 refuses a Default on
a non-Nillable bool or number because the field could never store false or 0, which
are ordinary values. The same is true of `""`, and the rule was not extended: a Default
is the normal way to declare a string column, refusal would make each of them a NULL-able
pointer, and `""` on a defaulted string rarely means anything else. `field.Default`
documents it. GraphQL can tell the cases apart, as its input is a pointer, so
`Create<T>` refuses a given zero value (`""`, the zero time, the nil UUID) with
BAD_USER_INPUT rather than store another value silently. An update stores it.

**A null given to a Nillable field with a Default is refused on create.** The insert
would store the Default, as for a field left out, and it has no way to store NULL there:
`gqlimpl.NotNull` reads the request, since the input's nil pointer cannot say. Clear it
with an update. Called from a field of the application's whose argument is not `input`, it
finds no null and refuses nothing.

**An empty blob reads as empty in a `Bytes!`.** modernc.org/sqlite gives nil for an empty
blob (its `columnBlob`), and gqlgen refuses nil in a `T!`, so one such row nulled every page
holding it. A generated getter reads nil as empty: the fix is the GraphQL layer's, since
the core's scan cannot tell it, nil being NULL in a Nillable `[]byte`. On SQLite a
Nillable blob that is empty still reads null.

**A literal `[]` stays `[]`.** gqlparser gives it as a nil slice, which `json.Marshal`
wrote as null. `{}` and a variable's were right.

**A Default is checked against its own built-in checks**, in the schema package. A
custom validator is not called: it is the application's code, and `Descriptor` also
runs when the program starts.

**A tenant column with an edge is no input**, and `ErrTenantMismatch` and
`ErrTenantImmutable` present as FORBIDDEN.

**`or: []` matches no row**, as `titleIn: []` does and as the input documents. A name that
is not ASCII is refused (rule 56): `plugin.Snake` works on bytes, and its output may not
change. Two edges bound to one foreign key are refused (rule 55).

## Consequences

- `GeneratedCodeIsVersion6`: `Create<T>` calls `gqlimpl.NotZero` and `gqlimpl.NotNull`, and
  a `Bytes!` binds to a getter.
- Clients of a Required edge see `T` where they saw `T!`.
- GraphiQL's query grows with the where inputs, at about 1400 per rich entity; past 700
  of them, raise `MaxIntrospectionCost`.
- The statements part is an upper bound where a field does not read, and a lower bound
  where an application's resolver reads per row, as it was. `clear<Edge>` repeats its three
  statements for each `MaxPage` links, and is counted once: the links of one row, not the
  client, decide that.
- A query with over 141 fields under one response name, merged, is refused at
  the default, as 200 `... on T { id }` of a union are. Raise `MaxFields`, which raises what
  the pairs allocate with it.
- `MaxBodyBytes` does not bound a websocket's messages, whose transport reads them after
  the upgrade: `transport.Websocket` has no limit, and its variables are not bounded
  by a veloxgql limit. Its query is, by the Extension.

## Evidence

- `TestCostIntrospection`, `TestCostStatements`, `TestCostInputs`, `TestCostSkipStillReads`,
  `TestCostMutationWrites` (veloxgql/cost_test.go); `TestRequestCost`
  (examples/graphql/main_test.go).
- `TestGraphiQLOnARichSchema`, `TestIntrospectionBudgetIsItsOwn` (veloxgql/cost_meta_test.go);
  `TestIntrospectionLimit`, `TestDocumentRefused`, `TestDocumentMergePairs`, `TestDocumentAndAPQ`,
  `TestSetup` (veloxgql/extension_test.go); `TestJSONEmptyLiteral` (veloxgql/scalar_test.go).
- `TestValidationAttacks`, `TestLargeQueriesPass`, `TestBodyLimit`, `TestIntrospectionAtItsLimit`,
  `TestMutationStatementsCounted` (examples/graphql/limits_test.go) print what the table holds.
- `TestLimitsServed` (contrib/graphql/cmd/velox-graphql/limits_e2e_test.go) serves a
  hidden Required edge, a tenant key, an explicit zero value and null, an empty blob and
  `[]`, `or: []` and the names through gqlgen on SQLite.
- `TestBuildUniqueOptionalRefused`, `TestBuildTwoEdgesOnOneKeyRefused`,
  `TestBuildNonASCIINamesRefused` (internal/codegen/build_refusals_test.go);
  `TestContradictions` (schema/field/field_test.go).
