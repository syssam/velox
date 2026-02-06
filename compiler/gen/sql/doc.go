// Package sql implements the SQL dialect code generator for Velox ORM.
//
// This package generates type-safe Go code for database operations using the
// Jennifer code generation library. It implements the full DialectGenerator
// interface hierarchy (EntityGenerator, GraphGenerator, FeatureGenerator,
// OptionalFeatureGenerator) and produces entity structs, query builders,
// mutation builders, and predicates.
//
// # Interface Implementation
//
// The SQL dialect implements all generator interfaces:
//
//	┌─────────────────────────────────────────────────────────────┐
//	│                      SQLDialect                             │
//	│  Implements: DialectGenerator (full interface)              │
//	└─────────────────────────────────────────────────────────────┘
//	                              │
//	          ┌───────────────────┼───────────────────┐
//	          ▼                   ▼                   ▼
//	┌─────────────────┐ ┌─────────────────┐ ┌─────────────────┐
//	│ EntityGenerator │ │ GraphGenerator  │ │FeatureGenerator │
//	│  (8 methods)    │ │  (5 methods)    │ │  (2 methods)    │
//	└─────────────────┘ └─────────────────┘ └─────────────────┘
//
// # Generated Code Structure
//
// For each entity defined in the schema, this package generates:
//
//   - Entity struct with fields and edge accessors (entity.go)
//   - Create/CreateBulk builders for INSERT operations (create.go)
//   - Update/UpdateOne builders for UPDATE operations (update.go)
//   - Delete/DeleteOne builders for DELETE operations (delete.go)
//   - Query builder for SELECT operations with filtering (query.go)
//   - Mutation type for tracking field changes (mutation.go)
//   - Predicate functions for type-safe WHERE clauses (predicate.go)
//   - Package constants for column names and field descriptors (package.go)
//
// # Generated Output Structure
//
// The generator produces the following files in the output directory:
//
//	{output}/
//	├── velox.go              # Base types, errors, Op enum, Value interface
//	├── client.go             # Client struct, entity clients, hooks registration
//	├── tx.go                 # Transaction (Tx, Commit, Rollback)
//	├── runtime.go            # Runtime utilities, schema descriptors
//	├── predicate/
//	│   └── predicate.go      # Predicate type definitions
//	├── intercept/            # (if FeatureIntercept enabled)
//	│   └── intercept.go      # Interceptor helpers
//	├── privacy/              # (if FeaturePrivacy enabled)
//	│   └── privacy.go        # Privacy policy helpers
//	├── internal/             # (if schema features enabled)
//	│   ├── schema.go         # Schema snapshot
//	│   └── schemaconfig.go   # Multi-schema config
//	│
//	├── {entity}.go           # Entity struct + Edges + Client
//	├── {entity}_create.go    # Create/CreateBulk builders
//	├── {entity}_update.go    # Update/UpdateOne builders
//	├── {entity}_delete.go    # Delete/DeleteOne builders
//	├── {entity}_query.go     # Query builder
//	│
//	└── {entity}/
//	    ├── {entity}.go       # Table name, columns, field descriptors
//	    └── where.go          # WHERE predicate functions
//
// # Supported Features
//
// The SQL dialect supports all Velox features:
//
//   - FeaturePrivacy: ORM-level authorization policies
//   - FeatureIntercept: Query interceptors for middleware
//   - FeatureEntQL: Runtime query language
//   - FeatureNamedEdges: Named edge loading
//   - FeatureBidiEdgeRefs: Bidirectional edge references
//   - FeatureSnapshot: Schema snapshot for migrations
//   - FeatureSchemaConfig: Multi-schema support
//   - FeatureLock: SQL row-level locking (FOR UPDATE/FOR SHARE)
//   - FeatureModifier: Query modifiers
//   - FeatureExecQuery: Raw SQL execution
//   - FeatureUpsert: ON CONFLICT support
//   - FeatureVersionedMigration: Versioned migrations
//   - FeatureGlobalID: Relay Global ID
//
// # Code Generation Patterns
//
// The generator uses Jennifer (github.com/dave/jennifer/jen) for type-safe
// code generation. Key patterns include:
//
//   - Fluent builder APIs for all operations
//   - Type-safe predicate functions with compile-time checking
//   - Proper handling of optional/nillable fields with pointer types
//   - Support for eager loading relationships via With* methods
//   - Transaction-aware operations with proper error handling
//   - Hook and interceptor support for cross-cutting concerns
//
// # Usage
//
// This package is typically invoked through the gen.JenniferGenerator:
//
//	import (
//	    "github.com/syssam/velox/compiler/gen"
//	    "github.com/syssam/velox/compiler/gen/sql"
//	)
//
//	// Create graph from schemas
//	graph, err := gen.NewGraph(config, schemas...)
//	if err != nil {
//	    return err
//	}
//
//	// Generate using SQL dialect
//	err = sql.Generate(graph)
//
// Or with explicit generator configuration:
//
//	generator := gen.NewJenniferGenerator(graph, outDir).
//	    WithDialect(sql.NewDialect(generator)).
//	    WithWorkers(4)
//	err := generator.Generate(ctx)
//
// # Database Support
//
// The SQL dialect supports multiple databases via driver abstraction:
//
//   - PostgreSQL (primary, full feature support)
//   - MySQL (full support)
//   - SQLite (limited feature support)
//
// Database-specific SQL is handled by the dialect/sql package at runtime.
//
// # Error Handling
//
// The dialect uses structured error types from the gen package:
//
//   - gen.SchemaError: Schema definition errors
//   - gen.EdgeError: Relationship definition errors
//   - gen.GenerationError: Code generation failures
//
// Example error handling:
//
//	err := sql.Generate(graph)
//	if err != nil {
//	    if gen.IsEdgeError(err) {
//	        // Handle edge-specific error
//	    }
//	    return err
//	}
package sql
