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

A third review proved that one rule bounded was not the document bounded. The check
counted fields, so 7000 fragment spreads, an alias of 1 MB and a `where` 3700 levels deep
all passed it, and variables were not looked at. Nodes and deletes sent more statements
than they counted. A filter told which row a policy hid, and four inputs stored something
other than what the client gave.

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
counted as 30. `nodes` adds one for each type of its ids, which it can read from each ID
(ids of three types, 30 times, send 90 and cost 9120), and `delete<T>` what it sends, 1
plus one for each table of links (`deletePost` sends 3 and costs 301). An input object
weighs by its type's fields and a string by its bytes (one for 128), at each use.

**`MaxCost` is 150000, 1500 statements.** It was 100000, 1000 statements, and refused
clients' ordinary queries: two levels of full pages with 10 fields each cost 121102. They
allocate 78 MB. Its worst shape, long aliases over rows, allocates about 2 KB a unit, so
286 MB at 150000, measured. Lowering statements' 100 would have kept 1000 statements and
changed every number here; a server open to strangers lowers `MaxCost`.

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
query there. One check holds everything about a document and its variables that a later
stage grows with, linear in its input, so one hook and one place to read:

| What grows | Bound (default) |
| --- | --- |
| tokens | `MaxTokens` (15000), by the parser |
| fields, spreads and inline fragments, each counted | `MaxFields` (2000) |
| fragments | `MaxFragments` (500) |
| nesting of selections, literals, types, and through fragments | `MaxDepth` (64) |
| a name: alias, field, argument, directive, fragment, variable | `MaxNameBytes` (128, or the schema's longest) |
| a literal string or number | `MaxLiteralBytes` (64 KiB) |
| fields under one response name, merged: pairs that differ | 5 x `MaxFields` (10000) |
| the same: pairs that agree | 200 x `MaxFields` (400000) |
| fields merged through fragments | 100 x `MaxFields` |
| variables: nesting, bytes, values | `MaxDepth`, `MaxBodyBytes`, `MaxVariableNodes` (25000) |

An object in a variable weighs 1 plus 1 for each 16 fields of its input type, as gqlgen
makes a map and walks the type for each: `or: [` 8300 objects `]` took 54 MB where the
variable's bytes said nothing. A persisted query's hash has no document to read the type
from, so its objects weigh the schema's widest.

A pair that differs costs about 3.5 KB through gqlgen's OverlappingFieldsCanBeMerged, and
one that agrees is compared and dropped. This does not replace the rule, as a copy of its
algorithm is ours to keep, and it works without any call on the server. It is a second
parse: 54 KB, 744 allocations and from 60 to 170 microseconds on GraphiQL's query, on each
request, and a gqlgen query cache does not spare it. Over the tokens is the parser's
GRAPHQL_PARSE_FAILED, as gqlgen says it; the rest BAD_USER_INPUT, `ErrRequestTooLarge`.
`Limits` holds the numbers so that one call sets them.

The table is `TestRequestBounded`, which asserts each budget on every request, at v0.17.86
and in `scripts/gqlgen-latest.sh`. All through `Setup`, at the defaults, on the example's
schema and SQLite; an attack on the left went to at most 1.6 GB before.

| A request | Bytes | Allocated | Time | Answer |
| --- | --- | --- | --- | --- |
| 7000 distinct spreads | 61923 | 2.1 MB | 8 ms | refused |
| 500 distinct spreads, the most | 20823 | 34 MB | 147 ms | data |
| 500 spreads of nothing | 3923 | 26.7 MB | 36 ms | 101 errors |
| a chain of 490 fragments | 16508 | 31.4 MB | 90 ms | data |
| 200 operations over 400 fragments | 23300 | 36.8 MB | 106 ms | refused by validation |
| a `where` 900 deep | 5435 | 0.4 MB | 2 ms | refused |
| a `where` 3700 deep, by a variable | 61 | 2.5 MB | 4 ms | refused |
| a list 60 deep and 6000 wide | 24164 | 6.4 MB | 6 ms | refused |
| an alias of 1 MB | 1048525 | 7.2 MB | 15 ms | refused |
| 141 conflicts under a long alias | 18785 | 3 MB | 6 ms | 101 errors |
| 800 fields that agree | 4833 | 8 MB | 55 ms | data |
| 1900 unknown fields | 245133 | 4.3 MB | 18 ms | 101 errors |
| a string echoed by errors | 983216 | 22.4 MB | 29 ms | 16 errors, 985 KB |
| `or: [` 95000 `]` by a variable | 61 | 20 MB | 55 ms | refused |
| `or: [` 8300 `]` by a variable | 61 | 54.5 MB | 61 ms | refused by the filter |
| a string of 1 MB at each use | 1393 | 7.3 MB | 11 ms | refused by the cost |
| two levels of pages, 2 fields | 141 | 40.5 MB | 89 ms | data, 913 KB |
| two levels of pages, 10 fields | | 78.2 MB | 165 ms | data, 2.1 MB |
| long aliases at `MaxIntrospectionCost` | 107243 | 232.6 MB | 223 ms | data, 15 MB |
| long aliases at `MaxCost`, on rows | 199705 | 286.3 MB | 289 ms | data, 20 MB |

Those last two are the worst a default server allows one request, and what a server open
to strangers sizes itself for: they are real work, charged to the cost. At v0.17.98 they
are 249 MB and 309 MB, and two levels of pages 62 MB; its parser has a nesting limit of
1000 of its own, which answers GRAPHQL_PARSE_FAILED first.

**`Setup` is the one call a server makes.** The body is read by the transport, before any
extension, so `veloxgql.Setup` returns a handler that refuses one over
`Limits.MaxBodyBytes` (1 MiB) with 413; a variable of 2 M IDs stops there. It uses the Extension and `ErrorPresenter`, sets gqlgen's token
limit, and bounds validation: it stops at `Limits.MaxErrors` (100) errors, where 141
conflicts under one long alias answered with 36 MB, and shows no suggestion of a name,
which takes 25 KB for each unknown field and shows the schema. gqlgen's own option covers
fields only at v0.17.86, so suggestions are off whether or not introspection is on: a
simpler rule than the one asked for, and safer. A server that uses only the Extension
keeps the document's limits and has no body limit, no error cap and gqlgen's suggestions.

**Introspection turned off says so.** gqlgen answers `__schema` with INTERNAL when
`DisableIntrospection` is set, which reads as the server's fault. The Extension answers
GRAPHQL_VALIDATION_FAILED, "introspection is disabled", and no field of the schema.

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

**A null given on update, to a field or an edge with a clear input, is refused**, naming
that input. The input's nil pointer leaves the column, and `{sNil: null}` meant to clear
it, and silently did not. `gqlimpl.NullClears` reads the request as `NotNull` does. A field
with no clear input, a Required one, keeps null as unset: it could not mean clear.

**A foreign key's predicates hold its target's Read policy.** `docs(where: {memberID: X})`
returned rows only if X was the hidden member, which proved who a row linked to, while the
edge read null. Now the key reads as null to each predicate, as the edge does: EQ, NEQ, In
and NotIn are `And(op, edge.Has())`, IsNil is `Not(edge.Has())`, NotNil `edge.Has()`. A
hidden key and a NULL key cannot be told apart, under `not` as well. The policy is found
when the input is read (`func (*T) Policy()`), and a target with no Read policy gets the
column's predicate alone, so no EXISTS.

**A JSON input is stored as given, or refused.** `FromJSON` kept 9007199254740993 in an
`any` as ...992, stored `["a", null]` as `["a", ""]` and dropped a key the type has not. It
now decodes numbers as `json.Number`, refuses unknown keys, and refuses a null where the
type holds none, and all BAD_USER_INPUT. A type with its own `UnmarshalJSON` decides for
itself. A hook on such a field reads a `json.Number`.

**A Uint64 over 2^63-1 is the client's error.** `database/sql` refuses to send one, so
it answered INTERNAL. Output still writes any value.

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

- `GeneratedCodeIsVersion6`: `Create<T>` calls `gqlimpl.NotZero` and `gqlimpl.NotNull`, a
  `Bytes!` binds to a getter, `Update<T>` calls `gqlimpl.NullClears`, and a where input's
  foreign keys call `gqlimpl.FK`, `FKList`, `FKIsNull` and `FKNotNull`.
- `veloxgql.Server` gains `SetValidationRulesFn`, which gqlgen's server has at both versions.
- A foreign key's order is not scoped: `OrderField` on one sorts by the hidden key, and a
  client that pages can learn its order. Scoping covers a Read policy, not a DB's `WithRewrite`.
- A JSON number in a `map[string]any` is stored exact and read back as a float64, by the
  core's scan: 9007199254740993 reads 9007199254740992. A field whose Go type holds an
  `int64` or a `json.Number` does not lose it.
- A Uint64 over 2^63-1 written outside velox's GraphQL reads, and cannot be filtered on.
- Clients of a Required edge see `T` where they saw `T!`.
- GraphiQL's query grows with the where inputs, at about 1400 per rich entity; past 700
  of them, raise `MaxIntrospectionCost`.
- The statements part is an upper bound where a field does not read, and a lower bound
  where an application's resolver reads per row, as it was. `clear<Edge>` repeats its three
  statements for each `MaxPage` links, and is counted once: the links of one row, not the
  client, decide that.
- A query with over 141 fields under one response name that differ, merged, is refused at
  the default. Fields that agree are not, up to 400000 pairs: 190 fragments of 10 common
  fields pass with 8.9 MB. Raise `MaxFields`, which raises what the pairs allocate with it.
- `MaxBodyBytes` does not bound a websocket's messages, whose transport reads them after
  the upgrade: `transport.Websocket` has no limit, so a message is decoded in full before
  the Extension checks its query and variables.
- The error cap and the suggestions are `Setup`'s: a server that calls only the Extension
  has neither.
- `MaxLiteralBytes` and `MaxNameBytes` bound one token, and `MaxBodyBytes` their sum.

## Evidence

- `TestRequestBounded` (examples/graphql/bounded_test.go) asserts the table's budgets;
  `TestIntrospectionOffShowsNothing` there; `TestDocumentShapes`, `TestDocumentPairs`,
  `TestVariablesShapes` (veloxgql/document_test.go); `TestValidationErrorsBounded`,
  `TestIntrospectionOff` (veloxgql/extension_test.go).
- `TestCostIntrospection`, `TestCostStatements`, `TestCostInputs`, `TestCostInputWeighs`,
  `TestCostNodesByType`, `TestCostDeleteStatements`, `TestCostSkipStillReads`,
  `TestCostMutationWrites` (veloxgql/cost_test.go); `TestRequestCost`
  (examples/graphql/main_test.go).
- `TestFromJSONExact`, `TestFKScoped` (veloxgql/gqlimpl/exact_test.go).
- `TestGraphiQLOnARichSchema`, `TestIntrospectionBudgetIsItsOwn` (veloxgql/cost_meta_test.go);
  `TestIntrospectionLimit`, `TestDocumentRefused`, `TestDocumentMergePairs`, `TestDocumentAndAPQ`,
  `TestSetup` (veloxgql/extension_test.go); `TestJSONEmptyLiteral` (veloxgql/scalar_test.go).
- `TestValidationAttacks`, `TestLargeQueriesPass`, `TestBodyLimit`, `TestIntrospectionAtItsLimit`,
  `TestMutationStatementsCounted` (examples/graphql/limits_test.go) print what the table holds.
- `TestLimitsServed` (contrib/graphql/cmd/velox-graphql/limits_e2e_test.go) serves a
  hidden Required edge, a tenant key, an explicit zero value and null, an empty blob and
  `[]`, `or: []` and the names through gqlgen on SQLite, and `TestForeignKeyFilterHidden`,
  `TestUpdateNullRefused`, `TestJSONInputExact` and `TestUint64HighBit`.
- `TestBuildUniqueOptionalRefused`, `TestBuildTwoEdgesOnOneKeyRefused`,
  `TestBuildNonASCIINamesRefused` (internal/codegen/build_refusals_test.go);
  `TestContradictions` (schema/field/field_test.go).
