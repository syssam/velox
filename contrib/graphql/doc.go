// Package graphql provides GraphQL code generation for Velox schemas.
//
// This package generates GraphQL schema (SDL) and Go code for use with
// the gqlgen GraphQL library. It follows Ent's entgql patterns and is
// designed to work seamlessly with Velox ORM.
//
// # Features
//
// The graphql package generates:
//   - GraphQL types matching your Velox schema entities
//   - WhereInput types for filtering queries
//   - Mutation input types (CreateXXXInput, UpdateXXXInput)
//   - Relay-style cursor pagination (Connection, Edge, PageInfo)
//   - Order types for sorting (XXXOrder, XXXOrderField)
//   - Node interface implementation for Relay
//   - Transaction middleware for mutations
//
// # Usage
//
// Add the GraphQL extension to your generate.go:
//
//	//go:build ignore
//
//	package main
//
//	import (
//	    "log"
//
//	    "github.com/syssam/velox/compiler"
//	    "github.com/syssam/velox/compiler/gen"
//	    "github.com/syssam/velox/contrib/graphql"
//	)
//
//	func main() {
//	    ex, err := graphql.NewExtension(
//	        graphql.WithConfigPath("./gqlgen.yml"),
//	        graphql.WithSchemaPath("./velox/schema.graphql"),
//	    )
//	    if err != nil {
//	        log.Fatalf("creating graphql extension: %v", err)
//	    }
//
//	    cfg, err := gen.NewConfig(
//	        gen.WithTarget("./velox"),
//	    )
//	    if err != nil {
//	        log.Fatalf("creating config: %v", err)
//	    }
//
//	    if err := compiler.Generate("./schema", cfg,
//	        compiler.Extensions(ex),
//	    ); err != nil {
//	        log.Fatalf("running velox codegen: %v", err)
//	    }
//	}
//
// # Annotations
//
// Control GraphQL generation using annotations on your schemas:
//
//	func (User) Annotations() []velox.Annotation {
//	    return []velox.Annotation{
//	        graphql.RelayConnection(),              // Enable Relay connections
//	        graphql.QueryField(),                   // Include in Query type
//	        graphql.Type("Member"),                 // Custom GraphQL type name
//	        graphql.Mutations(                      // Control mutations
//	            graphql.MutationCreate(),
//	            graphql.MutationUpdate(),
//	        ),
//	        graphql.Skip(graphql.SkipWhereInput),   // Skip specific features
//	        graphql.Resolvers(                      // Custom resolver fields
//	            graphql.Map("glAccount", "PublicGlAccount!"),
//	        ),
//	    }
//	}
//
// Field-level annotations:
//
//	func (User) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.String("email").Unique().
//	            Annotations(
//	                graphql.OrderField("EMAIL"),     // Custom order field name
//	                graphql.Skip(graphql.SkipAll),   // Exclude from GraphQL
//	            ),
//	        field.String("memo").Optional().Nillable().
//	            Annotations(graphql.Omittable()),    // PATCH semantics
//	    }
//	}
//
// # Skip Modes
//
// The Skip annotation supports different modes:
//   - SkipType: Skip the entire type from GraphQL schema
//   - SkipEnumField: Skip enum field from GraphQL enum
//   - SkipOrderField: Skip field from ordering options
//   - SkipWhereInput: Skip type from WhereInput generation
//   - SkipMutationCreateInput: Skip field from CreateXXXInput
//   - SkipMutationUpdateInput: Skip field from UpdateXXXInput
//   - SkipAll: Skip from all generated code
//
// # Generated Files
//
// The extension generates the following files:
//   - schema.graphql: GraphQL schema definitions
//   - gql_mutation_input.go: Mutation input types and Mutate methods
//   - gql_where_input.go: WhereInput filter types
//   - gql_pagination.go: Relay pagination types and methods
//   - gql_collection.go: Collection query helpers
//   - gql_node.go: Node interface implementation
//
// # Edge Handling
//
// The package correctly handles edge relationships in mutation inputs:
//   - Owner edges (edge.To): Included in both create and update inputs
//   - Inverse edges with OwnFK (M2O, inverse O2O): Included because the FK is on this table
//   - Inverse edges without OwnFK: Skipped (handled on the other side)
//   - Edges with explicit FK fields: Skipped (already handled as regular fields)
//
// For unique edges:
//   - Required edges: Non-pointer ID type in CreateInput (e.g., AuthorID int64)
//   - Optional edges: Pointer ID type in CreateInput (e.g., CategoryID *int64)
//   - Update inputs: Always pointer type with Clear option
//
// For non-unique edges:
//   - CreateInput: Simple XXXIDs slice (e.g., TagIDs []int64)
//   - UpdateInput: AddXXXIDs and RemoveXXXIDs slices
//
// # WhereInput
//
// WhereInput types provide type-safe filtering:
//
//	type UserWhereInput struct {
//	    Not *UserWhereInput       // Negate a condition
//	    Or  []*UserWhereInput     // Match any condition
//	    And []*UserWhereInput     // Match all conditions
//
//	    // Field predicates
//	    ID          *int64        // Exact match
//	    IDIn        []int64       // Match any in list
//	    NameContains *string      // Substring match
//
//	    // Edge predicates
//	    HasPosts     *bool        // Has any posts
//	    HasPostsWith []*PostWhereInput // Posts matching conditions
//	}
//
// # WhereOps - Fine-Grained Filter Control
//
// WhereOps provides fine-grained control over which filter predicates are
// generated for each field in WhereInput types. By default, the package uses
// smart defaults based on field type, but you can override them using the
// WhereOps annotation.
//
// # Smart Defaults by Field Type
//
// The package automatically selects appropriate predicates based on field type:
//
//	Field Type          Default Predicates
//	─────────────────────────────────────────────────────────────────────────
//	ID / Foreign Key    EQ, NEQ, In, NotIn (4 predicates)
//	Bool                EQ, NEQ (2 predicates)
//	Enum                EQ, NEQ, In, NotIn (4 predicates)
//	String              EQ, NEQ, In, NotIn, Contains (5 predicates)
//	Int/Float/Time      EQ, NEQ, In, NotIn, GT, GTE, LT, LTE (8 predicates)
//	Other types         EQ, NEQ, In, NotIn (4 predicates)
//
// For Nillable() fields, IsNil and NotNil predicates are automatically added.
//
// # Foreign Key Detection
//
// Fields are detected as foreign keys (and get minimal predicates) if:
//   - Field name is "id" (case-insensitive)
//   - Field name ends with "_id" (e.g., "user_id", "customer_id")
//   - Field name ends with "ID" (e.g., "userID", "customerID")
//   - Field type is UUID
//
// # Available WhereOp Constants
//
// Individual operations (can be combined with | operator):
//
//	OpEQ           // Equal: field == value
//	OpNEQ          // Not equal: field != value
//	OpIn           // In list: field IN (values...)
//	OpNotIn        // Not in list: field NOT IN (values...)
//	OpGT           // Greater than: field > value
//	OpGTE          // Greater than or equal: field >= value
//	OpLT           // Less than: field < value
//	OpLTE          // Less than or equal: field <= value
//	OpContains     // String contains: field LIKE '%value%'
//	OpHasPrefix    // String prefix: field LIKE 'value%'
//	OpHasSuffix    // String suffix: field LIKE '%value'
//	OpEqualFold    // Case-insensitive equal: LOWER(field) = LOWER(value)
//	OpContainsFold // Case-insensitive contains
//	OpIsNil        // Is null: field IS NULL
//	OpNotNil       // Is not null: field IS NOT NULL
//
// # Preset Combinations
//
// Common combinations for convenience:
//
//	OpsNone        // No predicates (0)
//	OpsEquality    // OpEQ | OpNEQ | OpIn | OpNotIn
//	OpsNullable    // OpIsNil | OpNotNil
//	OpsComparison  // OpsEquality | OpGT | OpGTE | OpLT | OpLTE
//	OpsSubstring   // OpContains | OpHasPrefix | OpHasSuffix
//	OpsCaseFold    // OpEqualFold | OpContainsFold
//	OpsString      // OpsEquality | OpsSubstring | OpsCaseFold
//	OpsAll         // OpsString | OpsNullable (all predicates)
//
// # Using WhereOps Annotation
//
// Override default predicates using the WhereOps annotation:
//
//	func (Order) Fields() []velox.Field {
//	    return []velox.Field{
//	        // Use preset: only equality predicates
//	        field.String("status").
//	            Annotations(graphql.WhereOps(graphql.OpsEquality)),
//
//	        // Combine presets: comparison + nullable
//	        field.Time("shipped_at").Nillable().
//	            Annotations(graphql.WhereOps(graphql.OpsComparison | graphql.OpsNullable)),
//
//	        // Individual operators: only EQ and In
//	        field.Int64("priority").
//	            Annotations(graphql.WhereOps(graphql.OpEQ | graphql.OpIn)),
//
//	        // Disable all predicates for a field
//	        field.String("internal_notes").
//	            Annotations(graphql.WhereOps(graphql.OpsNone)),
//	    }
//	}
//
// # Custom Go Types
//
// For custom Go types (e.g., decimal.Decimal, custom value objects), the
// package cannot automatically determine appropriate predicates. You must
// explicitly specify them using WhereOps:
//
//	import "github.com/shopspring/decimal"
//
//	func (Product) Fields() []velox.Field {
//	    return []velox.Field{
//	        // Custom decimal type - explicitly enable comparison ops
//	        field.Other("price", decimal.Decimal{}).
//	            Annotations(graphql.WhereOps(graphql.OpsComparison)),
//
//	        // Custom money type with nullable support
//	        field.Other("discount", money.Money{}).Nillable().
//	            Annotations(graphql.WhereOps(graphql.OpsComparison | graphql.OpsNullable)),
//	    }
//	}
//
// Without the WhereOps annotation, custom types default to OpsEquality
// (EQ, NEQ, In, NotIn only).
//
// # Nillable Fields and OpsNullable
//
// For Nillable() fields, IsNil and NotNil predicates are automatically added
// to whatever operations you specify (unless you explicitly set OpsNone):
//
//	// This field gets OpsComparison + OpsNullable automatically
//	field.Time("deleted_at").Nillable()
//
//	// Explicit WhereOps also gets OpsNullable added automatically
//	field.String("nickname").Nillable().
//	    Annotations(graphql.WhereOps(graphql.OpsEquality))
//	// Results in: EQ, NEQ, In, NotIn, IsNil, NotNil
//
//	// Use OpsNone to completely disable predicates (no auto-add)
//	field.String("internal").Nillable().
//	    Annotations(graphql.WhereOps(graphql.OpsNone))
//	// Results in: no predicates at all
//
// # Generated GraphQL Schema Example
//
// Given this schema:
//
//	func (User) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.Int64("id"),                    // FK detection: minimal ops
//	        field.String("name"),                 // String: full string ops
//	        field.String("status").
//	            Annotations(graphql.WhereOps(graphql.OpsEquality)),
//	        field.Time("created_at"),             // Time: comparison ops
//	    }
//	}
//
// The generated GraphQL WhereInput will be:
//
//	input UserWhereInput {
//	  not: UserWhereInput
//	  and: [UserWhereInput!]
//	  or: [UserWhereInput!]
//
//	  # ID field (minimal predicates)
//	  id: ID
//	  idNEQ: ID
//	  idIn: [ID!]
//	  idNotIn: [ID!]
//
//	  # String field (full string predicates)
//	  name: String
//	  nameNEQ: String
//	  nameIn: [String!]
//	  nameNotIn: [String!]
//	  nameGT: String
//	  nameGTE: String
//	  nameLT: String
//	  nameLTE: String
//	  nameContains: String
//	  nameHasPrefix: String
//	  nameHasSuffix: String
//	  nameEqualFold: String
//	  nameContainsFold: String
//
//	  # Status field (equality only via annotation)
//	  status: String
//	  statusNEQ: String
//	  statusIn: [String!]
//	  statusNotIn: [String!]
//
//	  # Time field (comparison predicates)
//	  createdAt: Time
//	  createdAtNEQ: Time
//	  createdAtIn: [Time!]
//	  createdAtNotIn: [Time!]
//	  createdAtGT: Time
//	  createdAtGTE: Time
//	  createdAtLT: Time
//	  createdAtLTE: Time
//	}
//
// # Edge Connections with where: @goField(forceResolver: true)
//
// When a Relay connection edge is opted into `where` filtering via the
// whitelist (edge-level graphql.WhereInput() or entity-level
// graphql.WhereInputEdges(...)), velox emits @goField(forceResolver: true)
// on the SDL field. Example:
//
//	type User {
//	  todos(
//	    first: Int, after: Cursor,
//	    orderBy: TodoOrder,
//	    where: TodoWhereInput,
//	  ): TodoConnection! @goField(forceResolver: true)
//	}
//
// The directive is NOT cosmetic — it prevents a silent-drop bug. Without it,
// gqlgen's autobind treats the velox-generated entity method
//
//	func (m *User) Todos(ctx, after, first, before, last, orderBy) (*TodoConnection, error)
//
// as a successful partial match (all Go params map to SDL args by name),
// even though the Go method has no `where` parameter. gqlgen then emits a
// field resolver that calls the method without `where`, and the `where`
// argument from the client is silently discarded — filter has no effect
// at runtime, with no error or warning.
//
// Historical note: this directive was originally a workaround for a package
// cycle (entity -> filter -> query -> entity). The cycle-break refactor
// (Plan 2, 2026-04-24) eliminated it by splitting heavy per-entity code into
// client/{entity}/ and changing the Filter() signature to return a predicate
// instead of taking a *XxxQuery. Today the forceResolver emission is still
// load-bearing to prevent gqlgen's silent-drop partial-match bug, and the
// resolver-body boilerplate is still a user-authored stub until Plan 3
// generates (c *entity.Category).Todos(ctx, ..., where *filter.TodoWhereInput)
// as a real entity method (now possible post-cycle-break).
//
// # User-written resolver body (required for where-carrying edges)
//
// For every edge-connection field that carries `where:`, you must implement
// the resolver method in your gqlgen package. Standard body:
//
//	func (r *userResolver) Todos(
//	    ctx context.Context, obj *entity.User,
//	    after *gqlrelay.Cursor, first *int, before *gqlrelay.Cursor, last *int,
//	    orderBy *entity.TodoOrder, where *filter.TodoWhereInput,
//	) (*entity.TodoConnection, error) {
//	    q := r.Client.Todo.Query().Where(todo.HasOwnerWith(user.IDField.EQ(obj.ID)))
//	    return q.Paginate(ctx, after, first, before, last,
//	        entity.WithTodoOrder(orderBy),
//	        entity.WithTodoFilter(where.Filter),
//	    )
//	}
//
// If you forget to implement this, gqlgen generates a panic stub that fails
// loudly at first request — not silent. Edges NOT opted into where continue
// to bind to the entity method directly and need no resolver code.
//
// # Fast path: reusing eager-loaded edges
//
// The entity-level edge method (entity/gql_edge_*.go) checks
// obj.Edges.XxxOrErr() before hitting the database. When the edge has been
// eager-loaded by the parent query via .WithXxx(...), the method reuses
// that slice through entity.BuildXxxConnection — zero DB round trips. This
// matches Ent's gql_edge.go pattern and closes a real performance gap: before
// this change, eager loading was effectively dead weight at the GraphQL
// layer because the resolver always re-queried.
//
// The fast path is taken when ALL of the following hold:
//   - obj.Edges.<Edge>OrErr() returns loaded data (parent used .WithXxx())
//   - after == nil AND before == nil (no cursor pagination — in-memory
//     cursor comparison isn't implemented)
//
// Otherwise it falls back to obj.QueryXxx().Paginate(...), which delegates
// connection assembly to the SAME entity.BuildXxxConnection helper. One
// source of truth for cursor/pageInfo/edge construction means the fast and
// slow paths can't drift.
//
// Note: this fast path applies only to the entity method (edges WITHOUT
// `where`). User-written resolvers for edges WITH `where` do not get the
// fast path automatically — add the same NamedXxxOrErr / BuildXxxConnection
// guard in your resolver body if you care about eager-load performance with
// filters.
//
// # Comparison: Ent's entgql
//
// Ent's entgql solves the same end-user problem but with a different
// architecture, because Ent has different package constraints:
//
//	                    Ent (entgql)              Velox
//	Entity struct       package ent               package entity
//	WhereInput struct   package ent (same pkg)    package filter (separate)
//	Query builder       package ent (same pkg)    package query (separate)
//	Paginate option     package ent (same pkg)    package entity (same as struct)
//	Edge resolver       method on *Category       user-written resolver body
//	                    directly in ent/          in gqlgen/schema.resolvers.go
//
// Ent colocates Category, TodoWhereInput, and TodoQuery in the root `ent/`
// package, so it can write a method on *Category that accepts
// *TodoWhereInput — gqlgen autobinds to it directly and there is no
// silent-drop risk. Ent does NOT emit @goField(forceResolver: true) on
// connection edges.
//
// Velox splits the ORM into entity/, query/, filter/, and per-entity
// sub-packages (todo/, user/, etc.) for build performance and memory. The
// per-entity packages currently own client-wrapper code that imports entity,
// which transitively makes entity -> filter -> todo -> entity a cycle.
// The cycle means entity methods can't accept *filter.XxxWhereInput —
// not until a bigger refactor splits per-entity packages into true leaves
// (predicates + enum only, no client wrapper).
//
// Until that refactor, velox gives up on "zero user code" and instead
// guarantees correctness:
//
//  1. @goField(forceResolver: true) on the SDL field — makes gqlgen route
//     through an explicit resolver method so `where` is never silently
//     dropped. Without this directive, gqlgen's autobind partial-matches
//     the entity method (which has no `where` param) and discards the
//     argument at runtime — a data-correctness / tenant-isolation bug.
//
//  2. User writes the resolver body (~5 lines of Paginate delegation) in
//     their gqlgen package. See `examples/fullgql/gqlgen/schema.resolvers.go`
//     for the canonical pattern.
//
// Functional parity with Ent for where-on-edge filtering: yes. Ergonomic
// parity: ~5 lines of user code per edge instead of zero. The fix for that
// gap is the per-entity package refactor (velox team owns this decision;
// not a small job).
//
// Performance parity: the entity-level edge method reuses eager-loaded
// edges via entity.BuildXxxConnection — matching Ent's gql_edge.go pattern
// for edges without `where`. User-written resolvers for where-carrying
// edges can add the same `obj.Edges.<Edge>OrErr` / BuildXxxConnection
// guard in their body if they care about eager-load performance with
// filters.
//
// # Migration from Previous Versions
//
// If you were using the default behavior before WhereOps was introduced,
// your WhereInput types may have had all predicates for all fields. The new
// smart defaults reduce schema size significantly:
//
//   - ID/FK fields: ~15 predicates → 4 predicates (73% reduction)
//   - Bool fields: ~15 predicates → 2 predicates (87% reduction)
//   - Enum fields: ~15 predicates → 4 predicates (73% reduction)
//
// To restore the previous behavior for a specific field, use OpsAll:
//
//	field.Int64("legacy_id").
//	    Annotations(graphql.WhereOps(graphql.OpsAll))
//
// # Pagination
//
// The package generates Relay-style cursor pagination:
//
//	query {
//	    users(first: 10, after: "cursor") {
//	        edges {
//	            node { id name }
//	            cursor
//	        }
//	        pageInfo {
//	            hasNextPage
//	            hasPreviousPage
//	            startCursor
//	            endCursor
//	        }
//	        totalCount
//	    }
//	}
//
// Multi-order support is available for complex sorting:
//
//	query {
//	    users(orderBy: [{field: NAME, direction: ASC}, {field: CREATED_AT, direction: DESC}]) {
//	        ...
//	    }
//	}
//
// # Integration with gqlgen
//
// Configure gqlgen to use the generated code by adding to gqlgen.yml:
//
//	autobind:
//	  - github.com/yourproject/velox
//
//	models:
//	  ID:
//	    model: github.com/yourproject/velox.ID
//	  Node:
//	    model: github.com/yourproject/velox.Noder
//
// # Best Practices
//
//  1. Use RelayConnection annotation for efficient pagination
//  2. Apply SkipWhereInput to types that shouldn't be filterable
//  3. Use custom Type names to improve GraphQL schema readability
//  4. Add validation annotations for input validation
//  5. Use transaction middleware for mutation atomicity
package graphql
