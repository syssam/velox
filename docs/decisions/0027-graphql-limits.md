# 0027. What the GraphQL layer limits, and what the generator refuses

- Status: accepted, 2026-11-01. The tenant parts (the `FK` helpers' tenant flag, the
  tenant column with an edge, `ErrTenantMismatch`) were removed by
  [0029](0029-scope-replaces-tenant.md): a scope is the target's Read policy now, and
  `scope.ErrNoScope` presents as UNAUTHENTICATED, `scope.ErrMismatch` as FORBIDDEN.

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

A fourth proved the claim that closed the third false: cost ignores a value's size. One
string of 900 KB under 300 aliases, a query of 22 KB, took 3.7 GB and answered with 270 MB.
A field a directive refuses made 97000 errors and 516 MB from 1.4 KB; 700 LIKE scans
over 90000 rows ran 13.6 s; a websocket's `connection_init` of 64 MB took 512 MB. It also
proved that large colocated fragments, a 13000-point GeoJSON and every nested list edge
were refused where they should pass.

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

**A scan costs 20 statements.** A fifth review proved that statements are not rows read:
700 aliases of `items(first: 1, where: {nameContains: "x"})` over 90000 rows cost 74200,
passed every limit and took 12.5 s, bounded only by `Timeout`. In a `<T>WhereInput`, each
`<field>Contains`, `<field>HasPrefix` and `<field>HasSuffix` given now adds 2000 (`scanCost`),
as a LIKE reads the whole table on every dialect (a prefix is served by an index only with
a C collation or pattern ops, which velox does not know of), and so does each non-empty
`has<Edge>With`, whose subquery runs for each row read. The flood costs 1474200 and is
refused; the default admits about 70 such filters, 1.3 s at 18 ms a scan, and a page of 100
users with 10 posts each, filtered by two LIKEs and a `has<Edge>With` holding a third, costs 10610. Null and `[]` add nothing, as
they add no predicate. 20 is from the measure, a scan of 90000 rows being about 20 indexed
statements; a larger table is slower still, and its server lowers `MaxCost`. Cost cannot see
indexes, so an equality on an unindexed column stays a statement: the plugin model's
indexes would tell, and a scan's cost then depends on the schema, which no client can read
from the SDL.

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
| fields, spreads and inline fragments, each counted | `MaxFields` (5000) |
| fragments | `MaxFragments` (500) |
| nesting of selections, literals, types, and through fragments | `MaxDepth` (64) |
| a name: alias, field, argument, directive, fragment, variable | `MaxNameBytes` (128, or the schema's longest) |
| a literal string or number | `MaxLiteralBytes` (64 KiB) |
| fields under one response name, merged: pairs that differ | 2 x `MaxFields` (10000) |
| the same: pairs that agree | 200 x `MaxFields` (1000000) |
| fields merged through fragments | 40 x `MaxFields` (200000) |
| variables: nesting, bytes, values | `MaxDepth`, `MaxBodyBytes`, `MaxVariableNodes` (100000) |

`MaxFields` was 2000, and refused 150 colocated fragments of 14 fields. At 5000 the pairs
that differ and the steps of a merge, which are what validation's cost follows, stay where
they were, 10000 and 200000; 340 such fragments take 32 MB. An object of an input type in a
variable weighs 4 for each of 1 plus 1 for each 16 fields of its type, as gqlgen
makes a map and walks the type for each: `or: [` 8300 objects `]` took 54 MB, and is where
it was, as the limit went up by the same 4; a plain value and a JSON scalar's object weigh 1,
and 95000 strings take 32 MB, so a GeoJSON of 13000 points, which is 39001 values, passes. A
persisted query's hash has no document to read the type from, so its objects weigh the
schema's widest, by the same 4.

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
| 1400 fields that agree | 8433 | 23.5 MB | 76 ms | data |
| 340 fragments of 14 common fields | 47095 | 32.3 MB | 114 ms | data |
| 4900 unknown fields | 632133 | 11.3 MB | 10 ms | 101 errors |
| a string echoed by errors | 983216 | 22.4 MB | 29 ms | 16 errors, 985 KB |
| `or: [` 110000 `]` by a variable | 61 | 21.3 MB | 24 ms | refused |
| 95000 strings by a variable | 68 | 31.6 MB | 33 ms | refused by the filter |
| `or: [` 8300 `]` by a variable | 61 | 54.5 MB | 61 ms | refused by the filter |
| a string of 1 MB at each use | 1393 | 7.3 MB | 11 ms | refused by the cost |
| two levels of pages, 2 fields | 141 | 40.5 MB | 89 ms | data, 913 KB |
| two levels of pages, 10 fields | | 78.2 MB | 165 ms | data, 2.1 MB |
| long aliases at `MaxIntrospectionCost` | 107243 | 232.6 MB | 223 ms | data, 15 MB |
| long aliases at `MaxCost`, on rows | 199705 | 286.3 MB | 107 ms | data, 20 MB |
| 36 aliases of one title of 900 KB | | 428.6 MB | 119 ms | data, 33 MB |
| long aliases at `MaxCost` on titles, just under `MaxResponseBytes` | | 583.1 MB | 181 ms | data, 47 MB |
| both, with introspection: 1400 long aliases of titles and 800 of `inputFields` | | 844 MB | | data, 59 MB |
| 300 aliases of one title of 900 KB (was 3.7 GB) | 25094 | 38.8 MB | 55 ms | `ErrResponseTooLarge` |
| 100 `@admin` fields on 100 rows | 1135 | 0.7 MB | 3 ms | 100 errors and `ErrTooManyErrors` |

A review found the row "both": `MaxCost` and `MaxIntrospectionCost` are separate budgets,
so one request spends both, and 844 MB, not 583 MB, is the worst measured with
introspection on. A server open to strangers turns introspection off, which leaves 583 MB.
The rows above it are the worst a default server was measured to allow one request, and what a
server open to strangers sizes itself for: real work, under the cost and the bytes. They
are `TestResponseAtItsLimit`, whose budgets of 800 MB and 1000 MB leave room for `-race`. The earlier claim, that
286 MB was the most, was false, as it was only what cost sees. Rows being read when the limit
is passed are read before they are counted, so a server with many connections reads more
of them than SQLite's one at a time: at most one for each aliased connection, 270 MB in
the attack above. The budgets hold at v0.17.98 as well (`scripts/gqlgen-latest.sh`); its parser has a nesting
limit of 1000 of its own, which answers GRAPHQL_PARSE_FAILED first.

**A response is held to `MaxResponseBytes` (32 MiB) as it builds, by the Extension.** Each
field's value is counted in the field's interceptor, a string as gqlgen will escape it; past
the limit, a field not yet resolved returns nil without running and the context is
cancelled, and the response is replaced with `ErrResponseTooLarge` and data null. It adds
one type switch to a field, and a loop over a string's bytes: `BenchmarkNestedRequest` at
50 users went from 156.6 to 156.7 thousand allocations and stayed at 9.9 MB and 19 ms, of
3550 nodes. 32 MiB is the design's number, kept as it answers the measured worst with
under a sixth of the attack's allocation; a smaller one would refuse a page of 100 rows
of 300 KB. It counts what a field returns of a string, bytes or JSON kind, and a
`[]string`: a custom scalar's struct, and a list of another element, are not.

**A response's errors are held to `MaxErrors`, by the Extension.** `Setup` already cut off
validation's. Every field's error counts where it returns; and `ErrorPresenter`, which
`Setup` gives, returns nil, which gqlgen drops at both versions, past the limit, so one no
resolver returned does too. The request then stops as it does for bytes. A presenter of
the application's keeps the count of what resolvers return, and only pays to make the
errors of the fields that stop, which are dropped at the end.

**A query or a mutation has `Limits.Timeout` (30 s).** A subscription, which lives as long as
it is listened to, has none, and a request's own deadline is not this one: a client gone or
a deadline of the application's answers as before. The Extension adds it to the context of a
first response, before the transaction, and modernc's SQLite interrupts a statement on it:
600 scans over 22500 rows stop at 114 ms with a Timeout of 100 ms. `ErrTimeout`, code
TIMEOUT, data null; a mutation rolls back. A resolver that ignores its context holds the
response until it returns.

**A websocket's message is held to `MaxBodyBytes`, by `Setup`'s handler.** gqlgen's
transport has no limit at v0.17.86 and `PayloadReadLimit` of a different library at v0.17.98,
and no field compiles at both, and no extension runs before a message is decoded. So the
handler wraps the hijacked connection, reads each client frame's header as it passes, adds
up the message's payload across its fragments and closes the connection before an over
one is read, whichever gqlgen. It removes `Sec-WebSocket-Extensions` from the upgrade, so
that a compressed message cannot be small on the wire. A client that sends frames
before the upgrade's answer, which RFC 6455 forbids, is refused. A POST that names an
upgrade keeps the body's limit. How many connections and messages a client sends stays outside the
bound: a rate limit in front has that.

**`Setup` is the one call a server makes.** The body is read by the transport, before any
extension, so `veloxgql.Setup` returns a handler that refuses one over
`Limits.MaxBodyBytes` (1 MiB) with 413; a variable of 2 M IDs stops there. It uses the Extension and `ErrorPresenter`, sets gqlgen's token
limit, and bounds validation: it stops at `Limits.MaxErrors` (100) errors, where 141
conflicts under one long alias answered with 36 MB, and shows no suggestion of a name,
which takes 25 KB for each unknown field and shows the schema. gqlgen's own option covers
fields only at v0.17.86, so suggestions are off whether or not introspection is on: a
simpler rule than the one asked for, and safer. A server that uses only the Extension
keeps the document's and the response's limits, and has no body or websocket limit, no cap
on validation's errors and gqlgen's suggestions.

**Introspection turned off says so.** gqlgen answers `__schema` with INTERNAL when
`DisableIntrospection` is set, which reads as the server's fault. The Extension answers
GRAPHQL_VALIDATION_FAILED, "introspection is disabled", and no field of the schema.

**A Required unique edge is nullable, always.** The plugin's model has no policy, and
adding it would not be enough: a DB's `WithRewrite`, a replica's lag and a concurrent
delete hide a target as well, and none is in any model. `Create<T>Input` still requires
the `<edge>ID`.

**Names of introspection types work.** gqlgen's autobind binds `__Type` to a Go type
`Type` before it applies its builtins. velox.graphql writes `extend type __Type
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

**A null given on update to a field with a clear input clears it**, as a client writing
`{sNil: null}` means; `clear<Field>` stays. It was refused, then silently ignored before
that. `gqlimpl.NullGiven` reads the request as `NotNull` does. A field with no clear input, a
Required one, keeps null as unset: it could not mean clear. A null on `<edge>ID` stays refused
by `gqlimpl.NullEdge`, naming `clear<Edge>`: the safer of the two, as a mistaken null on a
link drops it, and a client has the one way to say so.

**A list edge has its own bound, `graphql.MaxList(n)`.** Every nested list edge cost
`MaxList` for each parent, and was refused under a full page. The annotation lowers what
it reads and what it costs on one edge, refused on a connection, on a unique edge, and
under 1; it reaches gqlgen as `@goField(maxList: n)`. Over it, the edge fails with
`velox.ErrTooManyRows` as over `MaxList`: dropping rows silently is not on offer. A
refusal for a list edge's cost names the edge, `graphql.MaxList` and a connection.

**A foreign key's predicates hold its target's Read policy.** `docs(where: {memberID: X})`
returned rows only if X was the hidden member, which proved who a row linked to, while the
edge read null. Now the key reads as null to each predicate, as the edge does: EQ, NEQ, In
and NotIn are `And(op, edge.Has())`, IsNil is `Not(edge.Has())`, NotNil `edge.Has()`. A
hidden key and a NULL key cannot be told apart, under `not` as well. The policy is found
when the input is read (`func (*T) Policy()`), and a target with no Read policy and no
tenant gets the column's predicate alone, so no EXISTS. A tenant target counts: a key to
another tenant's row proved its existence too, through a table with the same scope or
none, so the generated call gives `FK` whether its target holds a tenant.

**A JSON input is stored as given, or refused.** `FromJSON` kept 9007199254740993 in an
`any` as ...992, stored `["a", null]` as `["a", ""]` and dropped a key the type has not. It
keeps a number as the `float64` a hook and encoding/json expect when that is the same
number, and as a `json.Number` only when it would lose digits; refuses unknown keys, a null
where the type holds none and an array of the wrong length, which it would pad or cut, all
BAD_USER_INPUT. A type with its own `UnmarshalJSON` decides for itself.

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

- `GeneratedCodeIsVersion7`: `Create<T>` calls `gqlimpl.NotZero` and `gqlimpl.NotNull`, a
  `Bytes!` binds to a getter, `Update<T>` calls `gqlimpl.NullGiven` and `gqlimpl.NullEdge`, a
  where input's foreign keys call `gqlimpl.FK`, `FKList`, `FKIsNull` and `FKNotNull` with the
  target's tenant, and `gqlimpl.EdgeList` takes the edge's `maxList`.
- `veloxgql.Server` gains `SetValidationRulesFn`, which gqlgen's server has at both versions.
- A foreign key's order is not scoped: `OrderField` on one sorts by the hidden key, and a
  client that pages can learn its order. Scoping covers a Read policy, not a DB's `WithRewrite`.
- A JSON number in a `map[string]any` is stored exact and read back as a float64, by the
  core's scan: 9007199254740993 reads 9007199254740992. A field whose Go type holds an
  `int64` or a `json.Number` does not lose it.
- A Uint64 over 2^63-1 written outside velox's GraphQL reads, and cannot be filtered on.
- Clients of a Required edge see `T` where they saw `T!`.
- GraphiQL's query grows with the where inputs, at about 1400 per rich entity, and about 2500
  with `graphql.WhereOps` on all 18 fields: the default admits 150 of the first, about
  100 of the second (236748 at 95, 261558 at 105). Past them, raise `MaxIntrospectionCost`.
- The statements part is an upper bound where a field does not read, and a lower bound
  where an application's resolver reads per row, as it was. `clear<Edge>` repeats its three
  statements for each `MaxPage` links, and is counted once: the links of one row, not the
  client, decide that.
- A query with over 141 fields under one response name that differ, merged, is refused at
  the default. Fields that agree are not, up to 1000000 pairs: 340 fragments of 14 common
  fields pass with 32 MB. Raise `MaxFields`, which raises what the pairs allocate with it.
- The websocket limit, validation's error cap and the suggestions are `Setup`'s: a server
  that calls only the Extension has none of them, and a message is decoded in full.
- `MaxResponseBytes` counts values, not aliases or numbers: 47 MB of response is the most
  measured under it. `Timeout` and `MaxResponseBytes` are per request, and many requests
  are for what is in front.
- `MaxLiteralBytes` and `MaxNameBytes` bound one token, and `MaxBodyBytes` their sum.

## Evidence

- `TestRequestBounded` (examples/graphql/bounded_test.go) asserts the table's budgets, and `TestScanFloodRefused` the scans' cost;
  `TestIntrospectionOffShowsNothing` there; `TestDocumentShapes`, `TestDocumentPairs`,
  `TestVariablesShapes` (veloxgql/document_test.go); `TestValidationErrorsBounded`,
  `TestIntrospectionOff` (veloxgql/extension_test.go).
- `TestCostIntrospection`, `TestCostStatements`, `TestCostInputs`, `TestCostInputWeighs`,
  `TestCostNodesByType`, `TestCostDeleteStatements`, `TestCostSkipStillReads`,
  `TestCostMutationWrites` (veloxgql/cost_test.go), `TestCostScans` (veloxgql/cost_scan_test.go); `TestRequestCost`
  (examples/graphql/main_test.go).
- `TestFromJSONExact`, `TestFromJSONNumbers`, `TestFromJSONArrayLength`, `TestFKScoped`
  (veloxgql/gqlimpl/exact_test.go).
- `TestResponseBytesBounded`, `TestResponseBytesStop`, `TestResolverErrorsBounded`,
  `TestTimeout`, `TestSubscriptionHasNoTimeout` (veloxgql/budget_test.go);
  `TestWebsocketMessageBounded` (veloxgql/websocket_test.go); `TestLargeValuesBounded`,
  `TestResponseAtItsLimit`, `TestFieldErrorsBounded`, `TestTimeoutCancelsStatements`
  (examples/graphql/bounded_test.go); `TestGraphiQLWithWhereOps`
  (veloxgql/cost_meta_test.go); `TestVariablesWeighWhatTheyTake` (veloxgql/limits_test.go);
  `TestCostListMaxList`, `TestListCostNamesTheFix`, `TestEdgeMaxList`.
- `TestGraphiQLOnARichSchema`, `TestIntrospectionBudgetIsItsOwn` (veloxgql/cost_meta_test.go);
  `TestIntrospectionLimit`, `TestDocumentRefused`, `TestDocumentMergePairs`, `TestDocumentAndAPQ`,
  `TestSetup` (veloxgql/extension_test.go); `TestJSONEmptyLiteral` (veloxgql/scalar_test.go).
- `TestValidationAttacks`, `TestLargeQueriesPass`, `TestBodyLimit`, `TestIntrospectionAtItsLimit`,
  `TestMutationStatementsCounted` (examples/graphql/limits_test.go) print what the table holds.
- `TestLimitsServed` (contrib/graphql/cmd/velox-graphql/limits_e2e_test.go) serves a
  hidden Required edge, a tenant key, an explicit zero value and null, an empty blob and
  `[]`, `or: []` and the names through gqlgen on SQLite, and `TestForeignKeyFilterHidden`,
  `TestForeignKeyFilterOtherTenant`, `TestListEdgeMaxList`, `TestUpdateNullClears`,
  `TestUpdateNullEdgeRefused`, `TestNullBytesGoType`, `TestJSONInputExact` and
  `TestUint64HighBit`.
- `TestBuildUniqueOptionalRefused`, `TestBuildTwoEdgesOnOneKeyRefused`,
  `TestBuildNonASCIINamesRefused` (internal/codegen/build_refusals_test.go);
  `TestContradictions` (schema/field/field_test.go).
