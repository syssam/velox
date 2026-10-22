# velox2 GraphQL — design

Status: draft, builder-checked · 2026-10-19 · Go 1.27+

This is the GraphQL sub-project. The schema design (`2026-10-15-schema-design.md`) made the schema DSL the single source of truth for the Go entities, the database, GraphQL and gRPC; this document is the GraphQL part.

It ports old velox's `contrib/graphql` to velox2, keeping its GraphQL surface (Ent's entgql shape) and dropping what the survey of the old code found tangled:
- the two generation tracks;
- the multi-package split, which existed to avoid import cycles;
- re-parsing generated SDL;
- the registry probe behind `node(id)`;
- dead annotations.

The user opted out of design questions (2026-10-19). Every decision below marked **(call)** is the author's, made so the work could proceed, and is open to the user's correction.

## Intent

An application declares its entities once in the schema package and gets a working Relay-style GraphQL API: queries with pagination, ordering and filtering, mutations, and `node(id)`, with no N+1 queries. Resolvers are one line or none. The API runs on the same velox pipeline as the rest of the application, so tenants, policies, hooks, guards, routing and observability apply to GraphQL requests unchanged.

## Goals

1. **One schema, one command.** `go tool velox-gen -out gen -plugin velox-graphql ./schema` writes the GraphQL SDL and its Go bindings into the same `gen` package as the entities. `-check` and `verify` cover the GraphQL files too.
2. **The Relay surface:**
   - the `Node` interface with `node`/`nodes`;
   - connections with `first`/`after`/`last`/`before`, `orderBy` and `where`;
   - `totalCount` and `pageInfo`.
3. **Mutations:**
   - `createX`, `updateX` and `deleteX`, each opt-in per entity;
   - inputs that set a unique edge by its ID, and add or remove the IDs of a to-many edge;
   - each mutation operation runs in one transaction.
4. **No N+1.** The selection set decides:
   - which columns are read;
   - which edges are loaded, in one batched query per edge per level.

   A nested connection with `first` uses a per-parent limit, never one query per parent.
5. **Safe by default.**
   - Filtering is opt-in per field.
   - The depth of a `where` input is capped, and so is `first`/`last`.
   - IDs carry their type: an ID of the wrong type is refused, not looked up.
   - Every statement goes through the pipeline, with tenant scope and policy.

## Non-goals (v1)

- Federation, subscriptions, interfaces and unions beyond `Node`, typed-JSON custom scalars, `@goModel` remapping (`Mapping`/`Unbind`), schema hooks, custom templates.
- Engines other than gqlgen (`github.com/99designs/gqlgen`).
- Writing the application's `gqlgen.yml` or running gqlgen. The application owns both; the example and the documents show them.

## Anti-goals

- A GraphQL request that bypasses tenant scope, policy, hooks or the guards.
- Unbounded work from a single request: no cap on `first`, unlimited filter nesting, or an edge load without a row cap.
- GraphQL code paid for by programs that do not use it: core velox and velox-gen gain no dependency on gqlgen or gqlparser.

## Constraints

- **The core rules hold.**
  - Go 1.27 generic methods.
  - Nothing runs at program start.
  - No reflect in generic code.
  - Core's allocation budgets are unchanged.
  - An application without GraphQL compiles and links exactly as today.
- **The GraphQL generator is a separate module,** `contrib/graphql`. It may depend on gqlparser, to check the SDL it writes, and on gqlgen, for the runtime pieces gqlgen's interfaces need. The core and `cmd/velox-gen` do not depend on it.
- **Compile cost.** The generated GraphQL Go lives in the `gen` package. bench/scale gains a GraphQL variant; its thresholds are set by the existing rule and recorded.
  - *Note, 2026-10-22 (plan 4):* `go run ./bench/scale -graphql` is that variant, with thresholds under the key `500-graphql`, measured on darwin only (the Linux re-measure is in the follow-ups). The service binary with GraphQL is 11.1 MB, flat in the number of entities.
- **Every dialect.** PostgreSQL (pgx and database/sql), MySQL 8 and SQLite 3.25+. The per-parent limit uses window functions, which all three have.
  - *Note, 2026-10-22 (plan 4):* it is `ROW_NUMBER() OVER (PARTITION BY …)` in a derived table on every dialect, with a golden on all three in `TestRender` and an itest on all three.

## Design

### 1. Plugins in velox-gen (call)

velox-gen gains a repeated flag, `-plugin name`.

**How a plugin runs:**
1. velox-gen loads the schema as today.
2. It runs `go tool <name>` in the application's module, writing the versioned model (`schema/load`) as JSON to the plugin's stdin.
3. The plugin replies on stdout with a JSON list of files, `{path, content}`, relative to `-out`.
4. velox-gen writes them as it writes its own:
   - each file starts with the generated-code header;
   - it writes only what changed;
   - it removes stale files it owns;
   - `-check` and `verify` compare and compile them with the rest.

**Errors.** A plugin's error or non-zero exit fails the run, and nothing is written.

**The protocol** is versioned in the JSON (`"plugin_protocol": 1`; *note 2026-10-21: protocol 2 and model 2 since API plan 2. The request adds `generator` and `options`, the response adds `warnings`, and a response must echo `plugin_protocol`. A plugin is written with `plugin.Main(plugin.Supports{Protocol: 2, Model: 2}, gen)`, or `plugin.Run` to test it without a subprocess, and a plugin of protocol 1 is told to update. `Nillable` leaves the model for `Pointer` and `Nullable`; velox-graphql reads `Nullable`. See the API cleanup spec §3.3 and §6b*). The request also names the generated package: `"out_package"` (its name) and `"out_path"` (its import path).

**What a plugin receives (call).** It gets the resolved model, not the raw `schema/load` model. That is everything velox-gen's own generator works from:
- each entity's Go name, table and primary key;
- each field's Go name, Go type, column and whether it is optional, immutable, sensitive or the tenant column;
- implicit foreign-key fields;
- each relation's kind (one-to-one, one-to-many, many-to-one, many-to-many) and its join entity;
- the ID type;
- the raw annotations.

It is a new public package, `github.com/syssam/velox2/plugin`. It holds only data types with a version number, and velox-gen fills them from its internal graph. A plugin never re-derives names, so it cannot drift from the entities velox-gen writes. gRPC will use the same package.

**Why:** this keeps gqlgen out of velox-gen, gives every generator one way to own files, and is how gRPC will plug in. The plugins are protoc's model, applied to Go tools.

The `velox.yaml` mentioned in the schema design is not needed: flags remain all the configuration there is.

### 2. Annotations

There are two packages in the `contrib/graphql` module (call):
- `github.com/syssam/velox2/contrib/graphql` holds the annotations. It imports only `velox2/schema`, so the schema package and velox-gen's loader compile no gqlgen.
- `github.com/syssam/velox2/contrib/graphql/veloxgql` is the runtime the generated code and the application use. It holds the handler extension, the scalars, cursors, IDs, the where-input interpreter and the limits.

The plugin itself is `contrib/graphql/cmd/velox-graphql`. Annotations reach the plugin through the model's annotation JSON, under the name `"graphql"`.

**Entity:**
- `graphql.Skip(...)`: the type, its inputs, its order, its where input, or its mutations; a bitmask as in old velox.
- `graphql.Type(name)`.
- `graphql.QueryField(name)`: a root `Query` field returning the entity's connection.
- `graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete())`.
- `graphql.MultiOrder()`.
- `graphql.Directives(...)`. *(Note 2026-10-21 (API plan 3): GraphQL's own directives only; an application directive on an entity is refused, since it would guard the objects and not the rows' values in cursors, filters, order and counts. See the API cleanup design, §6c.)*

**Field:**
- `graphql.FieldName(name)`, `graphql.Skip()`.
- `graphql.OrderField(name)`. It is refused on an optional field in v1, because velox's keyset paging needs non-NULL order columns (call).
- `graphql.WhereOps(ops)`: opt-in filtering, with presets `OpsEquality`, `OpsComparison`, `OpsString`, `OpsNullable` and `OpsAll`.
- `graphql.CreateInput(bool)`, `graphql.UpdateInput(bool)`.
- `graphql.EnumValues(map)`.
- `graphql.Directives(...)`.

**Edge:**
- `graphql.Skip()`.
- `graphql.FieldName(name)`.
- `graphql.WhereEdge()`: allows the `has` and `hasWith` filters.
- `graphql.Connection(bool)`: a to-many edge is a connection by default, or a plain list.

**Dropped:** old velox's `Mapping`, `MapsTo`, `Unbind`, `Resolvers`, `InterfaceField`, `UnionMember`, the federation annotations, `Omittable` and validate tags.

### 3. The generated surface

The plugin writes one SDL file, `gen/velox.graphqls` (flat, in the package directory, because velox-gen's stale-file scan reads only that directory), and Go files `gen/gql_*.go`. The Go files carry the generated header and belong to the plugin.

#### Types
- **Type:** each entity not skipped becomes a type implementing `Node`. Its fields are the schema fields not skipped, plus edges.
- **Scalars:**

  | Schema field | GraphQL type |
  |---|---|
  | `string` | `String` |
  | integers up to 64 bits | `Int`, gqlgen's 64-bit Int, as in entgql (call) |
  | `uint64` | `Uint64`, a string on the wire |
  | floats | `Float` |
  | `bool` | `Boolean` |
  | `time` | `Time` (RFC 3339) |
  | UUID | `UUID` |
  | bytes | `Bytes` (base64) |
  | JSON | `JSON` |
  | enum | `<Entity><Field>` enum, values upper-cased from the stored value, `EnumValues` to rename |

  Sensitive fields are never exposed.
- **Keys, foreign keys and tenants (call).**
  - The primary key is the type's `id: ID!`.
  - A foreign-key field bound to an edge is not a scalar field of the type or the inputs. The edge stands for it: the edge field in the type, `<edge>ID` in the inputs, and `<edge>ID`/`<edge>IDIn` (typed IDs) in a `where` input when the foreign-key field has `WhereOps`. So no raw key gets around the ID type check.
  - The tenant column is in no type or input. The tenant scope fills and checks it.
- **Optional fields** are nullable; required fields are non-null. *(Note 2026-10-20: a field is nullable in the schema when it is `Nillable`, or an `Optional` JSON field; an `Optional` value field is non-null and only optional on create. See the API cleanup spec §3.1.)*

#### IDs (call)
Every `ID` is opaque and global. It is base64url, without padding, of `<Type>:<key>`, the key written in its canonical text form.
- `node(id)` decodes the type and runs one query on that entity. There is no probe across tables.
- An ID of the wrong type, given where an ID of another type is expected (`updateUser(id: <a Post ID>)`, an edge ID in an input, a `where` on an ID field), fails with a GraphQL error naming the expected type. It is never looked up.
- Entities whose primary key is not a single column are not `Node`s, and get no `node` lookup or ID-based mutations.

#### Connections
- **Shape:** `XConnection { edges: [XEdge!]!, pageInfo: PageInfo!, totalCount: Int! }` and `XEdge { node: X!, cursor: Cursor! }`.
- **Arguments:** `first`/`after`/`last`/`before`, `orderBy` and `where`.
- **Limits:**
  - `first` and `last` are capped by `veloxgql.MaxPage`, 100 by default (call). More is an error, not a silent truncation.
  - With neither given, the page is the cap.
- **Cursor:** opaque. It holds the order values and the key, encoded as versioned JSON (call) then base64url. A cursor that does not decode, or does not match the query's order, is an error.
- **Paging:** keyset, through velox's `After` (and a `Before` the core gains for `last`/`before`). The order always ends with the primary key as a tiebreaker. The page query reads `n+1` rows to set `hasNextPage`/`hasPreviousPage`.
- **`totalCount`** runs a `Count` only when it is selected; a count-only selection runs no page query.
- *Note, 2026-10-22 (plan 4):* a request is also bounded as a whole, by `Limits.MaxCost` (100000 by default), which multiplies by `first` and `last` down the connections (rulings 10 and 11 of plan 4). A request over it fails with `ErrTooCostly`, code `COST_LIMIT_EXCEEDED`, before any statement.
- *Note, 2026-10-21 (API plan 3):* the cap is the `MaxPage` of `veloxgql.Limits`, read from the request's context (`LimitsFrom`), not a package variable. Every Node entity also gets `<Plural>ConnectionOf(ctx, q, …)`, which composes a connection from the application's own query: it clears the query's Offset, replaces its Limit and refuses one with an `OrderBy` (`velox.Query.Ordered`).

#### Ordering
- `XOrder { direction: OrderDirection! = ASC, field: XOrderField! }`.
- The fields are those with `OrderField`. With `MultiOrder`, `orderBy` takes a list.

#### Filtering
- **Shape:** `XWhereInput` has `not`, `and` and `or`; the predicates of each field with `WhereOps`; and `has<Edge>`/`has<Edge>With` for edges with `WhereEdge`.
- **Default predicates** when `WhereOps` is given without an argument:

  | Field type | Predicates |
  |---|---|
  | ID | `EQ`, `NEQ`, `In`, `NotIn` |
  | Bool | `EQ`, `NEQ` |
  | Enum | `EQ`, `NEQ`, `In`, `NotIn` |
  | String | the above plus `Contains`, `HasPrefix`, `HasSuffix` |
  | Numeric / Time | the above plus `GT`, `GTE`, `LT`, `LTE` |
  | Optional fields | plus `IsNil`, `NotNil` *(note 2026-10-20: `Nillable` fields only; see the API cleanup spec §3.1. Note 2026-10-21: the code offers them on a nullable field that has predicates, which means `Nillable`, as a JSON field has none)* |

- **Depth:** nesting is capped by `veloxgql.MaxFilterDepth`, 5 by default. *(Note 2026-10-21 (API plan 3): the cap is the `MaxFilterDepth` field of `veloxgql.Limits`, read with `LimitsFrom(ctx)`, not a package variable.)*
- **Translation:** a where input becomes a `velox.Pred[X]` through a table each entity's generated code supplies; the runtime in `contrib/graphql` interprets it (the old Track B's table design). No per-field code is written for predicates.

#### Mutations
- **Create:** `createX(input: CreateXInput!): X!`.
  - Every field not marked out of the create input. Fields with a Default or DefaultFunc are optional.
  - `<edge>ID: ID` for a unique edge, and `<edge>IDs: [ID!]` for a to-many edge.
- **Update:** `updateX(id: ID!, input: UpdateXInput!): X!`.
  - Every field optional. Immutable fields are left out.
  - `clear<Field>: Boolean` for an optional field. *(Note 2026-10-20: for a nullable field, since a value handle cannot write NULL: `Nillable`, or an `Optional` JSON field (note 2026-10-21). See the API cleanup spec §3.1 and §6a ruling 8.)*
  - `<edge>ID`/`clear<Edge>` for a unique edge.
  - `add<Edge>IDs`/`remove<Edge>IDs`/`clear<Edge>` for a to-many edge.
- **Delete:** `deleteX(id: ID!): ID!`.
- **Implementation:** each mutation is a generated function in `gen`, for example `gen.CreateUser(ctx, db, input) (*User, error)`. The application's gqlgen resolver calls it in one line.
  - *Note, 2026-10-22 (plan 3):* the signatures take no `db`, which comes from the context (`veloxgql.DBFrom`): `gen.CreateUser(ctx, input) (*User, error)`, `gen.UpdateUser(ctx, id, input) (*User, error)` and `gen.DeleteUser(ctx, id) (string, error)`. Each is atomic: `gqlimpl.Atomic` runs it in the extension's transaction, or in a `db.Tx` of its own where there is none.
- *Note, 2026-10-22 (plan 3), the edge order:* an update applies an edge's `clear`, then its `remove…IDs`, then its `add…IDs`, so `clearTags` with `addTagIDs` replaces the set.
- **Edges:**
  - Unique edges set their foreign key.
  - To-many edges update the children's foreign keys (O2M), or insert and delete join rows (M2M, through the generated join entity).
  - Every write goes through velox's write path, with its hooks, policy, validators and guards.

#### Transactions and the DB
- `veloxgql.Handler(db)` is a gqlgen handler extension (call). It puts the `*velox.DB` in the request context for the generated resolvers.
- For a `mutation` operation it opens one `db.Tx` and puts the transaction's DB in the context instead. It commits when the response has no errors, and rolls back otherwise or on a panic.
- Queries are not wrapped, so they keep routing to replicas.
- *Note, 2026-10-21 (API plan 3):* `veloxgql.Handler(db)` is now `veloxgql.NewExtension(db, opts...)`, a named `*Extension`. Its options are `WithLimits`, `WithTxOptions` (which refuses `velox.TxRetrySerializable`: gqlgen builds the response once) and `WithTxFilter(func(ctx, *ast.OperationDefinition) bool)`; a mutation the filter leaves out runs on the DB under `velox.WithSession`, so it reads its own writes. `WithDB` is gone: `Extension.Context(ctx)` puts the DB and the limits in a context for generated code called outside a server. See the API cleanup design, §6c.

### 4. Field collection and loading

**Edges are methods.** Each entity's edges are generated as methods on the entity. The entity's struct already has a field of the edge's name (`Posts velox.Many[Post]`), so the method is named `<Edge>GQL`, for example `func (u *User) PostsGQL(ctx, first, after, last, before, orderBy, where) (*PostConnection, error)`. The SDL maps the GraphQL field to it with gqlgen's `@goField(name: "PostsGQL")`. gqlgen binds to the methods, so the application writes no edge resolvers.

**Where the eager-loaded page lives (call).** The rows land in the entity's `Many[T]`/`One[T]`, as any `With` load does. What a connection needs beyond the rows goes in a request-scoped table that the collector puts in the context:
- for each loaded edge field, the arguments the load used;
- each parent's grouped `totalCount`.

The edge method uses the loaded rows only when its arguments are the ones loaded. It reads `hasNextPage` from the `first+1` rows. The core's entity structs and `Many[T]` do not change.

**Before a query runs**, the generated collect function for its entity walks gqlgen's selection set (`graphql.CollectFieldsCtx`) and adds to the velox query:
- **Columns:** the selected fields, plus the key and the order columns, and the foreign keys the selected edges need.
- **To-one edges and plain-list edges:** loaded with `With`, recursively.
- **Connection edges:** loaded with `With`, the edge's own `where` and `orderBy`, and a per-parent limit of `first+1`.
  - That is a new core feature, `Rel.LimitEach(n)`. It is rendered with `ROW_NUMBER() OVER (PARTITION BY <owner key> ORDER BY …)`.
  - A nested connection with `after`/`before` cannot be batched, because each parent's cursor differs. It is resolved by the edge method with its own query, one per parent that asks.
  - The edge method returns the eager-loaded page when the loader filled it.
- **Count:** a nested `totalCount` is loaded as one grouped count per edge per level, not one count per parent.

**The same edge twice (call).** When the same edge appears twice with different arguments (`a: posts(first: 5)`, `b: posts(where: …)`), only the first in selection order is batched. The others are resolved by the edge method per parent, at most the page cap of queries per level. Core keeps one load per relation.

**Bounds:** every load keeps the DB's `WithMaxRows` cap, so a selection never reads without bound.

*Note, 2026-10-22 (plan 4):* this section is not how plan 4 built it. Where it differs, the rulings of plan 4 stand:
- **Batching is at the edge** (ruling 1), not in a `With` walk. Each generated edge method reads its edge for every row of its group in one statement, with the typed arguments gqlgen gave it. The selection set is walked for the columns only (ruling 9).
- **The rows live in the request's table of groups,** not in `Many[T]` (ruling 2). The edge methods no longer read `x.<Edge>`; the core's structs are as before.
- **Every alias batches,** each as its own load (ruling 3), and **nested `last`, `after` and `before` batch too** (ruling 4): "only the first is batched" and "cannot be batched" above are superseded.
- **The count** is a grouped `CountEach`, one per edge per level, as written.
- **Bounds** are those of ruling 8: a per-owner limit bounds its own statement, and a list edge reads `MaxList + 1` rows per owner.

### 5. What the application writes

The example (`example/graphql`) shows the whole of it:
1. The schema, with `graphql.*` annotations.
2. `go tool velox-gen -out gen -plugin velox-graphql ./schema`.
3. A `gqlgen.yml` with:
   - `schema: gen/*.graphqls` plus the application's own schema files;
   - `autobind` to the `gen` package;
   - models for `ID`, `Cursor`, `Time`, `UUID`, `Uint64`, `Bytes` and `JSON`, bound to the scalars in `veloxgql`.
4. `go tool gqlgen generate`.
5. Resolvers:
   - the root query fields call `gen.UsersConnection(ctx, db, args…)`;
   - the mutations call `gen.CreateUser(…)`;
   - `node`/`nodes` call `gen.Node(ctx, id)`.
6. `srv.Use(veloxgql.Handler(db))`.

*Note, 2026-10-21 (API plan 3):* step 5's root fields take no `db`: they call `gen.UsersConnection(ctx, args…)`, which reads the DB from the context. Step 6 is `srv.Use(veloxgql.NewExtension(db))` and `srv.SetErrorPresenter(veloxgql.ErrorPresenter(nil))`, which shows a client only coded errors and fixed messages. See the API cleanup design, §6c.

### 6. Testing

- **Golden tests** of the SDL and Go the plugin writes, for a model that covers every annotation. The SDL is checked by gqlparser.
- **Unit tests** in `contrib/graphql`: cursors, ID encoding and type checks, where-input translation (every operation, `and`/`or`/`not`, depth cap), and limits.
- **`example/graphql`**, an end-to-end test with real gqlgen on SQLite. It asserts the SQL statements each request sends, through an Around hook:
  - a three-level nested query is one query per level;
  - a nested connection with `first` is one windowed query;
  - mutations commit or roll back as one transaction.
- **itest:** connections, `LimitEach` and mutations on PostgreSQL and MySQL.
  - *Note, 2026-10-22 (plan 3):* the mutation itest lives in `contrib/graphql`'s e2e app module, not in `internal/itest`: the root module may not depend on `contrib/graphql`. It runs on SQLite always, and on PostgreSQL and MySQL when their variables are set.
  - *Note, 2026-10-22 (plan 4):* the collection itest lives in the same e2e app, through real gqlgen, on SQLite always and on PostgreSQL and MySQL when their variables are set (ruling 16). The core's new SQL has its own itests in `internal/itest`. `example/graphql` asserts the statements of a nested request, and `BenchmarkNestedRequest` with `TestNestedRequestBudget` pins them: 5 statements at 10 and at 50 users, where the edges read one parent at a time sent 221 and 1101.
- **bench/scale:** the GraphQL variant (Constraints).

## What is left to the builder

- The plugin protocol's exact JSON field names, the layout of the `plugin` package's types, and how velox-gen finds and runs the plugin (`go tool`), within §1.
- The table format for where inputs.
- The cursor's JSON layout.
- How `Before` and `LimitEach` are rendered in each dialect.
- The split of the generated Go across files.

## Plans

The work comes in four plans:
1. **Plugins and the SDL:** the `plugin` package, velox-gen plugins, the annotations, and the SDL with its golden tests.
2. **The runtime:** IDs and `node`, cursors and connections, ordering, where inputs, the core's `Before`, and the handler extension.
3. **Mutations:** create, update and delete, with edges, in a transaction.
4. **Collection and loading:** the core's `LimitEach`, collection, grouped counts, the example, itest, and bench/scale.

## Rulings made in plan 1

Plan 1 (plugins and the SDL, `docs/plans/2026-10-19-graphql-1-sdl.md`) settled these points. Where one differs from the text above, the ruling stands; the text above is left as it was written.

**The plugin protocol and model (§1).**
- velox-gen adds the header to a plugin's files (`#` for `.graphqls`, `//` for `.go`), so a plugin cannot forget it. A path with a directory part, `..`, another extension, a name velox-gen or another plugin writes, or an existing file without the header fails the run with nothing written.
- A plugin runs in the application's module (`go tool <name>`, with `go get -tool` to declare it). The model is `plugin.Model`, versioned (`plugin.ModelVersion = 1`; the protocol is versioned apart, `plugin.ProtocolVersion = 1`; *note 2026-10-21: both are 2 since API plan 2*), built from velox-gen's own graph and pinned by a golden JSON test.
- The model carries what the SDL and the Go files need beyond names and types:
  - `Edge.OwnsKey`: the edge's own entity holds the foreign key on its row. The plugin never re-derives which side holds it, and the where predicates, the edge inputs and the refusals below follow it.
  - `Edge.FieldOn`: the entity that holds the key field, which finds the key field.
  - `Field.Nullable`, `Field.Enum` (the Go type's name, for a custom enum type too) and `Field.EnumValues` (the stored values, for every enum field).
  - `Comment` on entities, fields and edges, which become GraphQL descriptions.
  - `Entity.Imports`, the imports the Go files of plan 2 need for custom types.
- The singular rule is `plugin.Singular`, in the stdlib-only `plugin` package. velox-gen's own names and a plugin's use the same function, so `<edge>IDs` and join names cannot drift. It singularizes only a name ending in `s`, so `children` becomes `childrenIDs` (entgql gives `childIDs`).

**Names and bindings (§3).**
- A GraphQL enum is named after velox-gen's Go enum type (`<Schema><Field>`), not after the GraphQL type, so gqlgen's autobind finds it when `graphql.Type` renames the entity. A custom-Go-type enum gets the same GraphQL name; plan 2 binds it with `@goModel` to the type's import path.
- Every Node type's key is `id: ID! @goField(name: "IDGQL")`, the same `<Name>GQL` convention as edges; plan 2 generates `IDGQL()` returning the opaque global ID.
- `graphql.FieldName` renames a field in GraphQL and binds it back with `@goField(name: "<GoName>")`; an application directive named `goField` is refused. `velox.graphqls` also declares `@goField` (with `type`) and `@goModel` as gqlgen documents them (v0.17.86), so the application must not redeclare either.

**Inputs (§3 Mutations).**
- A sensitive field is accepted in create and update inputs (a password is set, never read back) and appears in no type, where input or order. "Never exposed" is about output.
- Edge inputs follow the foreign key's own flags, on whichever entity holds it. An immutable key drops every input that moves it; a required key drops `remove<Edge>IDs` and `clear<Edge>`, for one-to-many and inverse one-to-one alike. A many-to-many edge keeps them.
- Only `Default` or `DefaultFunc` makes a create-input field optional. A field with only `UpdateDefault` that is not nullable is in the create input and required, because nothing fills it on insert; it stays out of the update input, where the `UpdateDefault` fills it. A nullable field with only `UpdateDefault` is in neither input. `CreateInput(true)` and `UpdateInput(true)` bring any such field into that input.

**Refusals.** Generation fails, naming the schema and element, for:
- an annotation on the wrong kind of element (`OrderField` on an edge, `Mutations` on a field);
- `OrderField` on an optional *(note 2026-10-20: a `Nillable` or `Optional` JSON field, the NULL-able ones; API cleanup spec §3.1)*, sensitive, JSON or `Skip(SkipType)` field (keyset paging needs non-NULL columns, and order values are in the cursors);
- `WhereOps` on a sensitive field or the tenant (plan 2 adds `Skip(SkipType)` fields);
- `CreateInput` or `UpdateInput` (either value) on a foreign key or the tenant, and `UpdateInput(true)` on an immutable field;
- `QueryField` or `Mutations` on an entity whose type is skipped, an unknown mutation kind, and an update or delete mutation on an entity whose key is several columns;
- `createX` for an entity whose key the caller supplies (no auto ID, no default): v1 has no raw-key field in the create input (a follow-up);
- a required edge on the entity's own row whose target is skipped or not a Node, since `createX` could never set it;
- an entity whose type would have no fields;
- two schemas whose types, enums, inputs, connections or Query fields would share a name;
- a name that is not a GraphQL name, and an SDL that gqlparser does not accept.

**Types.** `uint` fields map to `Uint64`, and `SkipEnumField` makes an enum field a `String`. Plan 2 needs a uint marshaler under `Uint64`, and the enum's `MarshalGQL` must agree with `SkipEnumField`.

## Rulings made in plan 2

Plan 2 (the runtime, `docs/plans/2026-10-20-graphql-2-runtime.md`) settled these points. As above, where one differs from the text, the ruling stands and the text is left as it was written.

**No core `Before` (§3 Connections), 2026-10-20.** The core does not gain `Before`. For `last`/`before`, `veloxgql`'s paginator reverses every order term's direction, keysets with `After` on the reversed order, reads `n+1` rows, and reverses the page in memory, so the core's API is unchanged. `after` with `before`, `first` with `before` and `last` with `after` are refused with `ErrBadPagination`, since a keyset bounds one side; the opposite-direction flag (`hasPreviousPage` going forward, `hasNextPage` going backward) is false, as Relay allows.

**Where inputs are Go structs (§3 Filtering), 2026-10-20.** gqlgen binds an input object to a Go struct, so each `XWhereInput` is a generated struct, and there is no table for the runtime to interpret. The struct's generated method `Pred(ctx, depth)` returns the `velox.Pred[X]`: one `veloxgql` helper call for each predicate the input sets (`Value`, `List`, `ID`, `IDList`, `Is`, `HasWith`…), with no logic of its own, plus the `and`/`or`/`not` assembly and the limits (`MaxFilterDepth`, `MaxFilterWidth` on every list, `MaxFilterNodes` across one argument) in the helpers. This is the smallest per-field code gqlgen's binding allows.

*Note, 2026-10-21 (API plan 3):* the generated method is `Pred(ctx)`, not `Pred(ctx, depth)`: the depth travels in the context with the node budget, which a context without them starts at depth 0 with a full budget, so two calls on one context do not spend each other's. The helpers are in `contrib/graphql/veloxgql/gqlimpl`, which only generated code calls, and the limits are `veloxgql.Limits`, set with `WithLimits` and read with `LimitsFrom(ctx)`, not the package variables below. The generated code is versioned by `veloxgql.GeneratedCodeIsVersion1`.

**`totalCount` and the page are lazy (§3 Connections, §4), 2026-10-20.** A connection is a value that holds the prepared query. `TotalCount(ctx)` runs the `COUNT` when gqlgen calls it, which is only when `totalCount` is selected, and `Edges(ctx)` and `PageInfo(ctx)` run the page query once, shared through a `sync.Once` inside the connection. So a `totalCount`-only selection runs no page query, and nothing walks the selection set for it. Plan 4's field collection adds the eager `With` loads on top of this; it is not needed for correctness.

**`Node` is bound to `veloxgql.Noder` (§3 Types), 2026-10-20.** The GraphQL interface `Node` binds, in `gqlgen.yml`, to the Go interface `veloxgql.Noder` (`IsNode()` and `IDGQL() string`), and every Node entity gets both methods, so `node` and `nodes` return `veloxgql.Noder` values that gqlgen resolves to their concrete types.

**IDs are `string` in Go (§3 IDs), 2026-10-20.** There is no custom ID scalar: gqlgen's built-in `ID` stays `string`. `IDGQL()` encodes the key, and generated code decodes an ID with `veloxgql.DecodeID[K](id, "<Type>")`, which returns the typed key and fails with `ErrWrongIDType` for an ID of another type and `ErrBadID` for a malformed one, before anything is looked up. The spec's `gqlgen.yml` model for `ID` is therefore not needed.

**Scalars are function marshalers with `<Field>GQL` getters (§3 Types), 2026-10-20.** gqlgen binds a field only to a scalar model of the field's own Go type, so each scalar (`Time`, `UUID`, `Bytes`, `Uint64`, `Int`, `Float`) lists several models in `gqlgen.yml`, among them `veloxgql`'s function marshalers (`MarshalTime`, `MarshalUUID`, `MarshalBytes`, `MarshalUint64`, `MarshalUint`, `MarshalFloat32`), and gqlgen's own for the narrow integers. `Uint64` is a decimal string on the wire both ways. One scalar cannot autobind every Go type, so a field of an application's own Go type, and every JSON field (which binds to `json.RawMessage`), goes through a generated `<Field>GQL` getter that converts to a type of the list.

**The DB comes from the context (§3 Transactions and the DB, §5), 2026-10-20.** Generated functions do not take a `db` argument: `veloxgql.Handler(db)` stores the DB with `veloxgql.WithDB(ctx, db)`, and they read it with `veloxgql.DBFrom(ctx)`, which fails with `ErrNoDB` naming the handler when it is missing. So §5's `gen.UsersConnection(ctx, db, args…)` is `gen.UsersConnection(ctx, args…)`, and the mutations of §3 are `gen.CreateUser(ctx, input)`. For a `mutation` operation the handler stores the transaction's DB instead, runs the operation's resolvers one at a time (a transaction's DB is not safe for concurrent use), and commits when the response has no errors; a rolled-back response has `data` null. `@defer` inside a mutation is unsupported: a deferred fragment resolves after the commit, on a finished transaction, and fails.

*Note, 2026-10-21 (API plan 3):* `veloxgql.Handler(db)` and `WithDB` are `veloxgql.NewExtension(db)` and `(*Extension).Context(ctx)`; the transaction filter and options are on the extension. `DBFrom(ctx)` is unchanged.

**The root connection function takes the GraphQL type's plural (§3 Types, §5), 2026-10-20.** Like every other generated name, the root function is named after the GraphQL type: `gen.UsersConnection` for `User`, and `gen.LabelsConnection` for an entity `Tag` exposed as `Label` (`graphql.Type`), where the entity's own plural, `Tags`, would not match the type.

**Limits bound one argument, not a request (§4 Bounds), 2026-10-20.** Plan 2 adds these limits, all package variables of `veloxgql` with constant initialisers, settable at start-up and refused by `Handler` when not positive:
- `MaxPage` (100): `first`, `last` and the IDs of `nodes`. More fails with `ErrPageTooLarge`.
- `MaxList` (1000): the rows a list edge (`graphql.Connection(false)`) reads, in place of the DB's `WithMaxRows` cap, which is no cap by default. More fails with `velox.ErrTooManyRows`, in an error that names the edge. This replaces "every load keeps the DB's `WithMaxRows` cap" for list edges.
- `MaxFilterDepth` (5), `MaxFilterWidth` (100) for every `and`/`or`/`has…With`/`In`/`NotIn` list, and `MaxFilterNodes` (1000), the budget of inputs, predicates and values across one `where` argument.
- `MaxOrderTerms` (5): the terms of an `orderBy` list, checked before any term is built.

*Note, 2026-10-21 (API plan 3):* they are fields of `veloxgql.Limits`, not package variables: `WithLimits` sets them on the extension, `LimitsFrom(ctx)` reads them (the defaults in a context without them), a zero field means the default, and `Validate` refuses a negative one. Two servers in one process can differ.

Every one of them bounds one argument or one edge. Connections nested in connections multiply, and aliases repeat a field, so a server needs a limit on the cost of the whole request too. *(Note, 2026-10-22 (plan 4): `Limits.MaxCost` is that limit, and `example/graphql` uses it in place of gqlgen's `extension.FixedComplexityLimit`, which counted fields, not rows. See ruling 10 of plan 4.)* `WhereOps` on a field that `Skip(SkipType)` leaves out of the type is refused like `OrderField`: a filter is a value oracle too (`GT`, `LT` and `HasPrefix` bisect the value).

## Rulings made in plan 3

Plan 3 (mutations, `docs/plans/2026-10-21-graphql-3-mutations.md`) settled these points. As above, where one differs from the text, the ruling stands. Those marked *(changed)* were first written otherwise in the plan and changed while it was executed, the final review's fixes included.

**Calls made in the plan.**
1. The Go of the inputs and mutations goes in each entity's `gql_<label>.go`, written by `internal/gogen/mutation.go`: no new generated file kind.
2. Input Go types: a required field is a value, an optional one a pointer (nil means not given, bytes and JSON included), an ID a `string`, an ID list a `[]string`, a `clear…` field a `bool`. Go names follow `goOf`, which gqlgen matches ignoring case.
3. Each mutation is atomic. `gqlimpl.Atomic` runs it in the extension's transaction, or in a `db.Tx` of its own outside a server (`Extension.Context`) and in an operation `WithTxFilter` leaves out, as ent does. No savepoint is made inside the extension's transaction, so a resolver must return a mutation's error: one that swallows it commits the partial writes.
4. Every ID to set or add is checked against what the caller may read before it is written: a unique key and an M2M target by one `COUNT`, an O2M add by the `UPDATE`'s row count. `velox.EdgeThrough` leaves both endpoints to the application.
5. An ID that names no row the caller may see or change is `NOT_FOUND`: missing, another tenant's, or hidden by a policy. A policy's `Insert` refusal stays `FORBIDDEN`.
6. Update: `nil` leaves a field alone. Edges apply clear, then remove, then add. Three inputs are refused with `BAD_USER_INPUT` before any statement: a field with its `clear…`, a unique edge's ID with its `clear…`, and an ID in both `add…IDs` and `remove…IDs`.
7. Repeated IDs count once; adding a link that exists is not an error (`OnConflict(…).DoNothing()`); removing one that is not there is ignored.
8. An ID list is capped by `MaxPage` of `veloxgql.LimitsFrom(ctx)` (`ErrPageTooLarge`), before any statement.
9. An update that sets no column sends no `UPDATE`: it checks the row with a `COUNT`, applies the edges and returns the row.
10. `deleteX` returns the canonical ID. It deletes the row's join rows after a `COUNT` that checks the row is the caller's. Rows of other entities that point at the row are left to the database's foreign keys.
11. The owner's join column is the join entity's `Fields[0]` for an edge declared with `edge.To` and `Fields[1]` for its inverse; a test pins it from both sides.
12. A field's `graphql.Directives` reach its mutation input fields, and an edge's its edge inputs. **Breaking** for an application whose directive is on a field with no where predicates: declare it `on FIELD_DEFINITION | INPUT_FIELD_DEFINITION`. `@deprecated` is not copied onto a required input field, which GraphQL forbids.
13. *(changed)* Validation errors name the input field: `UpdatePostInput.title: invalid value: must not be empty`, `BAD_USER_INPUT`, by `gqlimpl.WriteError`. The plan also meant to name the field in gqlgen's own errors; that was narrowed (below).
14. A mutation returns velox's errors unchanged except validation errors, so `errors.Is` and `errors.As` keep working; `ErrorPresenter` maps them as before.
15. A non-`Nillable` bool or number with a `Default` is refused by velox-graphql for a hand-written model, as velox-gen refuses it.
16. The mutation itest lives in `contrib/graphql`'s e2e app module, on SQLite always and on PostgreSQL and MySQL when set; the `gqlgen@latest` job runs it with `CI` unset.
17. `veloxgql`'s public API does not change; `CodeNotFound`'s doc says what returns it. New names are in `gqlimpl`; `GeneratedCodeIsVersion1` stays 1.
18. Left out: a caller-supplied key in a create input, JSON append, `@goModel` enums, and plan 4's items.

**Rulings that changed while the plan ran.**
- *(changed, re-point)* An O2M/O2O re-point takes only children not linked to another owner, as old velox's `sqlgraph` does: the `UPDATE` adds `fk IS NULL` (on update, `fk IS NULL OR fk = owner`), and a child another owner holds is `BAD_USER_INPUT` "<path>: a <Target> is already linked to another <Owner>" (the caller can read it, so nothing leaks). On update, the children already linked to this owner are counted separately, so `add` succeeds for them and nothing relies on MySQL's rows-affected count.
- *(changed, owner-side edge inputs)* The SDL offers no `<edge>IDs` (create, or add/remove on update) for an O2M/O2O edge whose key on the target is `NOT NULL`: such a child always has an owner, so the input could never succeed. The child is linked from its own side, by `<edge>ID` on its input. The same holds for an immutable key.
- *(changed, write scope)* An edge-only update checks its row under the same scope a row `UPDATE` gets: the tenant, the Read policy and the Write policy. That needed one core addition, `Query.Writable()` (API cleanup design, §6d). The same scope checks `deleteX`'s row before the join rows go.
- *(changed, Optional JSON)* An `Optional` JSON field's column is NULL-able, so a nil binds SQL NULL, not the JSON text `null`: core `ColumnDesc.Nullable`, and `clear<Field>` stores SQL NULL. A NULL-able column must be representable in Go, so velox-gen refuses an `Optional` JSON field of a struct or scalar type unless it is `Nillable` (rule 48). The plugin's `Nullable` is `Nillable || (Optional && a JSON field whose Go type holds nil)`, nil-holding meaning a slice, map, pointer, interface, func or chan, as velox-gen's `nilKinds` has it (one statement of the rule, 2026-10-22; the API cleanup spec §6a ruling 8 and §6d say the same).
- *(changed, read-back)* `UpdateX` reads its row back under the tenant scope but without the Read policy (`velox.SkipPolicy`), so an update that moves the row out of what the caller may read still succeeds, consistent with `CreateX` returning the row as written.
- *(changed, delete's check)* `deleteX`'s row check uses the write scope, not Read: a readable but unwritable row is `NOT_FOUND`, with the text of a missing one, and its join rows are never touched.
- *(changed, ruling 13 narrowed)* velox leaves gqlgen's own validation errors alone. They carry `GRAPHQL_VALIDATION_FAILED` and a `path` (a variable) or `locations` (an inline literal), as GraphQL prescribes; velox names fields only in its own validation errors. gqlgen's coercion of an int to a `String` variable stays gqlgen's.
- *(changed, validate first)* `CreateX` and `UpdateX` run the validators of the fields their input sets before any statement, so an invalid field costs no query, and a bogus ID with it is still the validator's `BAD_USER_INPUT`. The generated `Check` is reachable only through `Desc`, so the core gained two entry points for generated code, `velox.Validate(x, cols...)` and `velox.ValidateSet(set...)` (additive; `GeneratedCodeIsVersion4` stays). `Insert` and `Update` still run the validators too.
- *(changed, M2M unlink scope)* `remove<Edge>IDs` and `clear<Edge>` of a many-to-many edge touch only links whose other end the caller can read: the tenant scope and the target's Read policy, the scope `add`'s `Exist` uses. The join `DELETE` takes only the target keys a scoped query selects (`gqlimpl.Readable`); `clear` reads the row's join rows `MaxPage` at a time (`gqlimpl.UnlinkAll`). A link to another tenant's row, or to one the Read policy hides, stays. A row's own `deleteX` still removes all of its join rows, in every tenant: the row is gone.
- *(changed, Writable guarded)* `prepare` refuses a Rewrite hook that changes `Statement.Writable`, as it refuses one that changes `Op`, `Entity` or the policy: the scope reads it.
- *(changed)* The example's `Post.views` is `Optional` (zero means unset), so `createPost` needs no `views: 0`.

**Limits, documented in `contrib/graphql`'s doc and the generated functions.**
- There is no savepoint inside the extension's transaction (call 3).
- An edge-only update had a time-of-check race between its `COUNT` and its writes; GraphQL plan 4 closes it: the check is a `Writable().ForUpdate().Exists` read (not on SQLite, whose transactions lock the database), and the lock holds for the rest of the mutation's transaction.
- On MySQL the DSN needs `clientFoundRows=true`: `UpdateX` counts matched rows, which MySQL reports only with it.
- Created rows are returned as written.
- Self-edges are written one way, as the core's loads read them (the plugin model has no `Bidi`).
- Clearing or removing children skips those the caller may not change: for an O2M or O2O edge, those the tenant or the child's Write policy hides (the `UPDATE` of the child is scoped); for an M2M edge, the links to targets the tenant or the target's Read policy hides.

## Rulings made in plan 4

Plan 4 (collection and loading, `docs/plans/2026-10-22-graphql-4-collection.md`) settled these points. As above, where one differs from the text, the ruling stands. The first 19 were made in the plan; 20 and after were found while it was executed. The core additions among them are in the API cleanup design, §6g.

**Calls made in the plan.**
1. **Batch at the edge, not a With walk.** §4 had the root's collect function walk the selection set and add `With` loads with each nested connection's `where` and `orderBy`. Generated code cannot decode those arguments: gqlgen decodes them in the application's `graph` package, for the resolver only, and a second decoder for every where input would be large generated code. So each edge method batches on its first call: it gets the typed arguments from gqlgen, and reads the edge for every row of its group in one statement. This gives the same bound, one statement per edge per level, with no second decoder and no lookahead. The selection set is still walked, for the columns only.
2. **The rows live in the request's table, not in the entity.** §4 put the eager-loaded page in `Many[T]`. Batched rows stay in the request's table of groups, and the generated edge methods no longer read `x.<Edge>`: once `Rel.LimitEach` exists, an application's With load may hold a filtered or limited page, which an edge field must not show as the edge. An application's own loads are not needed for batching any more. `gqlimpl.LoadedConnection` stays, for code generated before this plan.
3. **Every alias batches.** §4's call batched only the first of two uses of one edge. With batches keyed by the field, each alias is its own batch: one statement per alias per level, which the cost limit bounds.
4. **Nested `last`, `after` and `before` batch too.** §4 left a nested cursor to one query per parent. A field's cursor is one value for every parent (one field, one set of variables), so it is one more condition of the batch: `q.After(x)` inside the window. `last` reverses the order as the root does.
5. **A window function on every dialect.** `ROW_NUMBER() OVER (PARTITION BY …)` in a derived table, on PostgreSQL, MySQL 8 and SQLite 3.25+. A lateral join was rejected: SQLite has none, and one shape on all three keeps one golden per case. A load keyed by the target's one-column primary key (many-to-one) needs no window. A many-to-many page joins the join table, so the window partitions by the join's owner column in one statement.
6. **`Statement.Each`.** The per-owner shape is a field of the statement, so hooks and adapters see it, and the scope's conditions render inside the window: a row the tenant or a policy hides takes no rank. A Rewrite hook may not change it, as with `Lock` (§6f ruling 7).
7. **The core API.** `Rel.LimitEach` and `Rel.MaxRows` are methods, and `Query.Selects` too: none needs a type parameter beyond the receiver's. `LoadEach` and `CountEach` are functions: they take both `E` and `T` (§6f ruling 21). `Query.With[T]` stays as it is: it is the "Before 1.0" follow-up's, not GraphQL's.
8. **Caps.** A statement with a per-owner limit `n` may return `n` rows per owner in it, in place of the DB's and the owner query's cap: it is bounded by construction. A load keyed by the target's primary key may return one row per key. `Rel.MaxRows` gives any load its own cap. A connection's base query keeps sharing the page's cap of `n+1` with its With loads unless they set one of these; `Paginate`'s doc says so. List edges read `MaxList + 1` rows per owner, and an owner with more fails alone.
9. **The columns collected.** A page reads the selected fields' columns, the key, every foreign key on the row and every order field's column. The foreign keys are cheap and let any edge or an application's With work; the order columns keep every cursor valid. A field velox-graphql did not write (an application's `extend type`) turns narrowing off for that page. Narrowing never adds a column the base query left out (`Selects`), and a base query that drops the key or a requested order column is refused with an internal error, which closes the `ConnectionOf` follow-up. A mutation's returned row is not narrowed.
10. **The cost.** gqlgen's recipe, multiplying by `first`/`last`, as entgql and GitHub do: a field costs 1 plus its children's cost times its multiplier. A connection multiplies by `first` or `last` or `MaxPage`; `nodes` by its IDs; any other list of objects by `MaxList`. Fragments all count, as an upper bound; `@skip` and `@include` are not read; `__schema` and `__type` cost nothing, so introspection works. Named fragments are costed once each, and the sum saturates, so neither a fragment DAG nor huge arguments can make the check itself expensive or wrap. The default `MaxCost` is 100000: two levels of full connection pages pass (about 30000), three do not. A list edge costs `MaxList` per parent, so one full page with a list edge (`posts(first: 100) { … likes { id } }`, 100301) is refused by default; the docs of `MaxCost` and the example's README say so, and how to raise `MaxCost` or lower `MaxList`. The example drops gqlgen's `FixedComplexityLimit`, which counted fields, not rows.
11. **The error.** `ErrTooCostly`, code `COST_LIMIT_EXCEEDED` (a new `CodeTooCostly`), returned by the extension's `MutateOperationContext`, which gqlgen runs before any resolver. A client can tell it from a bad argument.
12. **`veloxgql.Batch`.** Rows an application's own resolver returns join batching through one public function; the generated pages, `node` and `nodes` batch theirs. Rows from anywhere else (a mutation's result) resolve their edges one row at a time, which is one row.
13. **A batch's error and context.** The batch runs once, under the first caller's context, on the DB `DBFrom` reads; its error is every row's. On a DB with replicas a batch may read a replica other than its parents', as the per-parent queries did before.
14. **A many-to-many edge needs no inverse.** The edge methods find nodes with `Rel.Of`, which reads the join table, so velox-graphql no longer refuses a many-to-many edge without an inverse edge. The where input's `has` filters keep using `HasWhere`.
15. **`FOR UPDATE` in `UpdateX` and `DeleteX`.** The check before an edge-only update, and before a delete's join rows, becomes a `Writable().ForUpdate().Exists` read on PostgreSQL and MySQL, which closes the time-of-check race the follow-ups name. SQLite keeps the plain read: its transactions lock the database. `Exists` replaces `Count`, which cannot lock.
16. **The collection itest runs real gqlgen,** in a `contrib/graphql` e2e app, on SQLite always and PostgreSQL and MySQL when their variables are set: batching needs gqlgen's field contexts, which only a server makes. The core's new SQL has its own itests in `internal/itest`.
17. **The bench.** A request-level benchmark in `example/graphql`, whose test pins the target: the statements of a nested request do not grow with its parents, and its allocations grow with its rows only. Its allocation budget holds under the pinned gqlgen only, with 10% for the jitter of gqlgen's concurrent resolvers; the 1.2% rule stays for the core's budgets and compile-cost bounds. `bench/scale` gains the GraphQL variant the spec's Constraints ask for, with thresholds by the existing rule.
18. **Versions.** `GeneratedCodeIsVersion4` stays 4 (velox-gen's output is unchanged) and `veloxgql.GeneratedCodeIsVersion1` stays 1 (every `gqlimpl` name stays, so old generated code compiles; the new code only adds calls).
19. **Left out:** `Bidi` self-edges (a plugin model change, for mutations); the function form of `Query.With`; JSON append; `@goModel` enums; a caller-supplied key in create inputs; the hidden O2O child on re-point; the one-sided M2M's join rows on delete; and gRPC, migrations and the pgx split, which have their own plans.

**Rulings found while executing the plan.**
20. Repeated links in a non-unique join table count once: a many-to-many per-owner page and `CountEach` dedupe (owner, target) before ranking or counting.
21. `vx_rn` and `vx_owner` are reserved aliases of a per-owner page, and a target column of either name is refused.
22. A per-owner load through a join sends no statement for the join entity, so the join entity's hooks and policy do not see it (the target's do).
23. `Query.Count` keeps ignoring `Limit` and `Offset` (`totalCount` relies on it); `CountEach` refuses them, and its doc says "unlike Query.Count". A nil owner fails naming the entity; an owner with a NULL key gets 0.
24. `After` moved out of the generic prepare path: about 26.8 KB less per entity package, and the compile-cost bounds were lowered. Fixed in passing: `With(rel.Where(x.Has()))` no longer panics.
25. The cost's sums and products saturate before they overflow, and a negative cost never passes. Costs are clamped before conversion to `int`, so 32-bit platforms are right (checked as a linux/386 binary). A connection with neither `first` nor `last` costs `MaxPage`.
26. The batch table is per response, so a subscription's table does not grow without bound, and no batch is added without a gqlgen field context. `Selected` and narrowing give ok=false for fields outside the known connection and edge fields, and fragments are matched by the type's real interfaces.
27. The batch key includes the caller's row scope, core `velox.RowScope` (`RowScopeOf(ctx)`: the tenant, `AllTenants`, `SkipPolicy`, the shard), so callers with a different tenant, bypass or shard never share a load. A tenant that is not comparable gets its own batch.
28. Narrowing always reads the tenant column, so a narrowed row handed to an application resolver never has a zero tenant. The docs of `Node`, `Nodes` and `<Plural>ConnectionOf` warn that unselected columns are zero.
29. A generated edge keeps its nil-key check before `EdgeOne`: a NULL foreign key sends no statement.
30. `UpdateX` and `DeleteX` choose their lock (`ForUpdate().Exists`) by the dialect at run time; SQLite keeps the plain read. A lock-wait itest runs on PostgreSQL and MySQL.
31. CI runs `go mod download` before the `contrib/graphql` step, since the e2e app builds offline, in `ci.yml` and `scripts/ci-local.sh` alike.
32. Bench numbers (measured on darwin/arm64, a loaded machine): a nested request is 5 statements at 10 and at 50 users (221 and 1101 before this plan). Allocations are about 44.6 per node, and the budget is the measured figure plus 10%. `bench/scale -graphql` has thresholds under the key `500-graphql`; the service binary with GraphQL is 11.1 MB, flat in the number of entities. The time thresholds were set under load and are in the Linux re-measure follow-up.
33. **A batch is shared only by the same read** (the final review's C1). A call joins a batch only when it is the same edge, called from the edge's own field, with the same arguments. The key gains the edge (its GraphQL field name, the relation's index and the target type) besides the field, the selections and the scope; the generated `EdgeOne`, `EdgeList` and `EdgeConnection` calls name the edge's field (`"Post.likes"`), and a call whose field context is another (an application's resolver calling `x.ChildrenGQL`) reads alone. The batch keeps its first caller's arguments (a connection's `after`, `first`, `before`, `last`, `orderBy` and `where`, passed as one value), and a caller whose arguments differ (`reflect.DeepEqual`, as a forceResolver computing them per row) reads alone. A lone read is correct, one statement for its row. Comparability is checked on the dynamic value (`reflect.Value.Comparable`), so a struct tenant holding a slice reads alone instead of panicking. The nested bench stays at 5 statements; its allocations were remeasured (about 45.0 per node) and its budget moved to the new figure plus 10%.
34. **`veloxgql.WithBatchScope`** (the final review's I1), an opt-in Extension option, is the one public name the fix adds: its function's value joins the batch key, and a value that cannot be compared reads alone. The key also holds the DB `DBFrom` returns and `EdgeList`'s `MaxList`, which are read from each caller's context. A batch runs under its first caller's context; velox's `RowScope` is always in the key, and any other context value a Read policy or a Rewrite or Around hook reads (the viewer, a role) must be the same for every row of a field or be returned by `WithBatchScope`. The docs say so, and that a directive or field middleware varying the context per parent row needs it. With it, the docs now say: one Extension per server (`Validate` stores the schema); a generated many-to-many edge's reads are not seen by the join entity's hooks and policy (ruling 22); unselected columns are zero for a resolver or a directive, and a forceResolver on a generated field is still a generated field for narrowing; a base query with a lock is refused when batched.
