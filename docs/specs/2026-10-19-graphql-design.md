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
- **Every dialect.** PostgreSQL (pgx and database/sql), MySQL 8 and SQLite 3.25+. The per-parent limit uses window functions, which all three have.

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

**The protocol** is versioned in the JSON (`"plugin_protocol": 1`). The request also names the generated package: `"out_package"` (its name) and `"out_path"` (its import path).

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
- `graphql.Directives(...)`.

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
  | Optional fields | plus `IsNil`, `NotNil` *(note 2026-10-20: `Nillable` fields only; see the API cleanup spec §3.1)* |

- **Depth:** nesting is capped by `veloxgql.MaxFilterDepth`, 5 by default.
- **Translation:** a where input becomes a `velox.Pred[X]` through a table each entity's generated code supplies; the runtime in `contrib/graphql` interprets it (the old Track B's table design). No per-field code is written for predicates.

#### Mutations
- **Create:** `createX(input: CreateXInput!): X!`.
  - Every field not marked out of the create input. Fields with a Default or DefaultFunc are optional.
  - `<edge>ID: ID` for a unique edge, and `<edge>IDs: [ID!]` for a to-many edge.
- **Update:** `updateX(id: ID!, input: UpdateXInput!): X!`.
  - Every field optional. Immutable fields are left out.
  - `clear<Field>: Boolean` for an optional field. *(Note 2026-10-20: for a `Nillable` field only, since a value handle cannot write NULL; see the API cleanup spec §3.1.)*
  - `<edge>ID`/`clear<Edge>` for a unique edge.
  - `add<Edge>IDs`/`remove<Edge>IDs`/`clear<Edge>` for a to-many edge.
- **Delete:** `deleteX(id: ID!): ID!`.
- **Implementation:** each mutation is a generated function in `gen`, for example `gen.CreateUser(ctx, db, input) (*User, error)`. The application's gqlgen resolver calls it in one line.
- **Edges:**
  - Unique edges set their foreign key.
  - To-many edges update the children's foreign keys (O2M), or insert and delete join rows (M2M, through the generated join entity).
  - Every write goes through velox's write path, with its hooks, policy, validators and guards.

#### Transactions and the DB
- `veloxgql.Handler(db)` is a gqlgen handler extension (call). It puts the `*velox.DB` in the request context for the generated resolvers.
- For a `mutation` operation it opens one `db.Tx` and puts the transaction's DB in the context instead. It commits when the response has no errors, and rolls back otherwise or on a panic.
- Queries are not wrapped, so they keep routing to replicas.

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

### 6. Testing

- **Golden tests** of the SDL and Go the plugin writes, for a model that covers every annotation. The SDL is checked by gqlparser.
- **Unit tests** in `contrib/graphql`: cursors, ID encoding and type checks, where-input translation (every operation, `and`/`or`/`not`, depth cap), and limits.
- **`example/graphql`**, an end-to-end test with real gqlgen on SQLite. It asserts the SQL statements each request sends, through an Around hook:
  - a three-level nested query is one query per level;
  - a nested connection with `first` is one windowed query;
  - mutations commit or roll back as one transaction.
- **itest:** connections, `LimitEach` and mutations on PostgreSQL and MySQL.
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
- A plugin runs in the application's module (`go tool <name>`, with `go get -tool` to declare it). The model is `plugin.Model`, versioned (`plugin.ModelVersion = 1`; the protocol is versioned apart, `plugin.ProtocolVersion = 1`), built from velox-gen's own graph and pinned by a golden JSON test.
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

**`totalCount` and the page are lazy (§3 Connections, §4), 2026-10-20.** A connection is a value that holds the prepared query. `TotalCount(ctx)` runs the `COUNT` when gqlgen calls it, which is only when `totalCount` is selected, and `Edges(ctx)` and `PageInfo(ctx)` run the page query once, shared through a `sync.Once` inside the connection. So a `totalCount`-only selection runs no page query, and nothing walks the selection set for it. Plan 4's field collection adds the eager `With` loads on top of this; it is not needed for correctness.

**`Node` is bound to `veloxgql.Noder` (§3 Types), 2026-10-20.** The GraphQL interface `Node` binds, in `gqlgen.yml`, to the Go interface `veloxgql.Noder` (`IsNode()` and `IDGQL() string`), and every Node entity gets both methods, so `node` and `nodes` return `veloxgql.Noder` values that gqlgen resolves to their concrete types.

**IDs are `string` in Go (§3 IDs), 2026-10-20.** There is no custom ID scalar: gqlgen's built-in `ID` stays `string`. `IDGQL()` encodes the key, and generated code decodes an ID with `veloxgql.DecodeID[K](id, "<Type>")`, which returns the typed key and fails with `ErrWrongIDType` for an ID of another type and `ErrBadID` for a malformed one, before anything is looked up. The spec's `gqlgen.yml` model for `ID` is therefore not needed.

**Scalars are function marshalers with `<Field>GQL` getters (§3 Types), 2026-10-20.** gqlgen binds a field only to a scalar model of the field's own Go type, so each scalar (`Time`, `UUID`, `Bytes`, `Uint64`, `Int`, `Float`) lists several models in `gqlgen.yml`, among them `veloxgql`'s function marshalers (`MarshalTime`, `MarshalUUID`, `MarshalBytes`, `MarshalUint64`, `MarshalUint`, `MarshalFloat32`), and gqlgen's own for the narrow integers. `Uint64` is a decimal string on the wire both ways. One scalar cannot autobind every Go type, so a field of an application's own Go type, and every JSON field (which binds to `json.RawMessage`), goes through a generated `<Field>GQL` getter that converts to a type of the list.

**The DB comes from the context (§3 Transactions and the DB, §5), 2026-10-20.** Generated functions do not take a `db` argument: `veloxgql.Handler(db)` stores the DB with `veloxgql.WithDB(ctx, db)`, and they read it with `veloxgql.DBFrom(ctx)`, which fails with `ErrNoDB` naming the handler when it is missing. So §5's `gen.UsersConnection(ctx, db, args…)` is `gen.UsersConnection(ctx, args…)`, and the mutations of §3 are `gen.CreateUser(ctx, input)`. For a `mutation` operation the handler stores the transaction's DB instead, runs the operation's resolvers one at a time (a transaction's DB is not safe for concurrent use), and commits when the response has no errors; a rolled-back response has `data` null. `@defer` inside a mutation is unsupported: a deferred fragment resolves after the commit, on a finished transaction, and fails.

**The root connection function takes the GraphQL type's plural (§3 Types, §5), 2026-10-20.** Like every other generated name, the root function is named after the GraphQL type: `gen.UsersConnection` for `User`, and `gen.LabelsConnection` for an entity `Tag` exposed as `Label` (`graphql.Type`), where the entity's own plural, `Tags`, would not match the type.

**Limits bound one argument, not a request (§4 Bounds), 2026-10-20.** Plan 2 adds these limits, all package variables of `veloxgql` with constant initialisers, settable at start-up and refused by `Handler` when not positive:
- `MaxPage` (100): `first`, `last` and the IDs of `nodes`. More fails with `ErrPageTooLarge`.
- `MaxList` (1000): the rows a list edge (`graphql.Connection(false)`) reads, in place of the DB's `WithMaxRows` cap, which is no cap by default. More fails with `velox.ErrTooManyRows`, in an error that names the edge. This replaces "every load keeps the DB's `WithMaxRows` cap" for list edges.
- `MaxFilterDepth` (5), `MaxFilterWidth` (100) for every `and`/`or`/`has…With`/`In`/`NotIn` list, and `MaxFilterNodes` (1000), the budget of inputs, predicates and values across one `where` argument.
- `MaxOrderTerms` (5): the terms of an `orderBy` list, checked before any term is built.

Every one of them bounds one argument or one edge. Connections nested in connections multiply, and aliases repeat a field, so a server needs a limit on the cost of the whole request too: `example/graphql` sets gqlgen's `extension.FixedComplexityLimit`. A cost limit that multiplies by `first` and `last` across connection fields is for plan 4. `WhereOps` on a field that `Skip(SkipType)` leaves out of the type is refused like `OrderField`: a filter is a value oracle too (`GT`, `LT` and `HasPrefix` bisect the value).
