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
//   - gql_transaction.go: Transaction middleware
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
//	String              EQ, NEQ, In, NotIn, GT, GTE, LT, LTE,
//	                    Contains, HasPrefix, HasSuffix,
//	                    EqualFold, ContainsFold (13 predicates)
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
//	OpsString      // OpsComparison | OpsSubstring | OpsCaseFold
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
