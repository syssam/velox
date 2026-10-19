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

**The protocol** is versioned in the JSON (`"plugin_protocol": 1`).

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

The plugin writes one SDL file, `gen/graphql/velox.graphqls`, and Go files `gen/gql_*.go`. The Go files carry the generated header and belong to the plugin.

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
- **Optional fields** are nullable; required fields are non-null.

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
  | Optional fields | plus `IsNil`, `NotNil` |

- **Depth:** nesting is capped by `veloxgql.MaxFilterDepth`, 5 by default.
- **Translation:** a where input becomes a `velox.Pred[X]` through a table each entity's generated code supplies; the runtime in `contrib/graphql` interprets it (the old Track B's table design). No per-field code is written for predicates.

#### Mutations
- **Create:** `createX(input: CreateXInput!): X!`.
  - Every field not marked out of the create input. Fields with a default are optional.
  - `<edge>ID: ID` for a unique edge, and `<edge>IDs: [ID!]` for a to-many edge.
- **Update:** `updateX(id: ID!, input: UpdateXInput!): X!`.
  - Every field optional. Immutable fields are left out.
  - `clear<Field>: Boolean` for an optional field.
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
   - `schema: gen/graphql/*.graphqls` plus the application's own schema files;
   - `autobind` to the `gen` package;
   - models for `ID`, `Cursor`, `Time`, `UUID`, `Uint64`, `Bytes` and `JSON`, bound to the scalars in `veloxgql`.
4. `go run github.com/99designs/gqlgen generate`.
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
