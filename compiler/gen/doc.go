// Package gen provides code generation for Velox ORM schemas.
//
// This package is responsible for generating Go code from schema definitions,
// producing type-safe database access code, GraphQL schemas/resolvers, and
// gRPC proto definitions.
//
// # Architecture
//
// The code generation pipeline follows this flow:
//
//	Schema Definition (schema/*.go)
//	        ↓
//	   velox.Schema interface + builders
//	        ↓
//	   Graph (internal representation)
//	        ↓
//	   DialectGenerator (database-specific code)
//	        ↓
//	   Generated code (ent/)
//
// # Key Types
//
// The package provides several key types:
//
//   - Graph: Holds all Type definitions with validation
//   - Type: Represents an entity with fields, edges, indexes
//   - Field: Field with type info, constraints, annotations
//   - Edge: Relationship with type (O2O, O2M, M2M), foreign keys
//   - Config: Global configuration for code generation
//
// # Interface Hierarchy
//
// The generator interfaces follow the Interface Segregation Principle:
//
//	MinimalDialect (basic dialect support)
//	├── Name() string
//	├── EntityGenerator (8 methods for per-entity code)
//	│   ├── GenEntity, GenCreate, GenUpdate, GenDelete
//	│   └── GenQuery, GenMutation, GenPredicate, GenPackage
//	└── GraphGenerator (5 methods for graph-level code)
//	    └── GenClient, GenVelox, GenTx, GenRuntime, GenPredicatePackage
//
//	DialectGenerator (full interface, extends MinimalDialect)
//	├── FeatureGenerator (feature detection and generation)
//	└── OptionalFeatureGenerator (optional features like privacy, intercept)
//
// Custom dialects can implement MinimalDialect for basic support,
// or DialectGenerator for full feature support.
//
// # Error Handling
//
// The package uses structured error types for better error handling:
//
//   - SchemaError: Schema definition errors
//   - ConfigError: Configuration errors
//   - EdgeError: Edge/relationship errors
//   - GenerationError: Code generation errors
//   - ValidationError: Validation errors
//
// Example error handling:
//
//	graph, err := gen.NewGraph(config, schemas...)
//	if err != nil {
//	    if gen.IsEdgeError(err) {
//	        // Handle edge-specific error
//	    }
//	    return err
//	}
//
// # Configuration
//
// Configuration is done via the functional options pattern:
//
//	// Recommended: schema/ for definitions, velox/ for generated code
//	config, err := gen.NewConfig(
//	    gen.WithTarget("./velox"),  // Generate to velox/ folder
//	)
//	compiler.Generate("./schema", config)
//
// Additional options available:
//
//	config, err := gen.NewConfig(
//	    gen.WithTarget("./velox"),
//	    gen.WithFeatures(gen.FeaturePrivacy),     // Enable privacy feature
//	    gen.WithHeader("// Custom header"),       // Custom file header
//	)
//
// Package is auto-inferred from go.mod. Override only if needed:
//
//	config, err := gen.NewConfig(
//	    gen.WithTarget("./velox"),
//	    gen.WithPackage("github.com/org/project/velox"),  // Override package
//	)
//
// # Jennifer Generator
//
// Code generation uses the Jennifer library instead of templates for:
//
//   - Auto-tracking imports (no goimports needed)
//   - Streaming writes to disk (lower memory)
//   - Compile-time type safety
//   - Parallel generation with configurable workers
//
// # Usage
//
// The recommended way to generate code is through the sql package:
//
//	import "github.com/syssam/velox/compiler/gen/sql"
//
//	err := sql.Generate(graph)
//
// Or manually configure the generator:
//
//	import (
//	    "github.com/syssam/velox/compiler/gen"
//	    "github.com/syssam/velox/compiler/gen/sql"
//	)
//
//	generator := gen.NewJenniferGenerator(graph, outDir).
//	    WithDialect(sql.NewDialect(generator)).
//	    WithWorkers(4)
//	err := generator.Generate(ctx)
//
// # Code Organization
//
// The package is organized into several files:
//
//   - config.go: Config type methods and grouped configuration
//   - dialect.go: Generator interface definitions (ISP-based)
//   - errors.go: Structured error types
//   - feature.go: Feature flags and definitions
//   - generate.go: JenniferGenerator for code generation
//   - graph.go: Graph type and schema processing
//   - option.go: Functional option pattern for configuration
//   - template.go: Template utilities
//   - type.go: Type definition and methods
//   - type_field.go: Field-related methods
//   - type_edge.go: Edge-related methods
//   - type_helpers.go: Helper functions and utilities
//
// # Generated Output
//
// The generator produces the following structure:
//
//	{output}/
//	├── velox.go            // Base types, errors, interfaces
//	├── client.go           // Client struct with entity clients
//	├── tx.go               // Transaction support
//	├── runtime.go          // Runtime utilities
//	├── predicate/
//	│   └── predicate.go    // Predicate type definitions
//	├── {entity}.go         // Entity struct and client
//	├── {entity}_create.go  // Create builder
//	├── {entity}_update.go  // Update builder
//	├── {entity}_delete.go  // Delete builder
//	├── {entity}_query.go   // Query builder
//	└── {entity}/
//	    ├── {entity}.go     // Package constants (table, columns)
//	    └── where.go        // Predicate functions
//
// # Features
//
// The generator supports optional features that can be enabled:
//
//   - privacy: ORM-level authorization policies
//   - intercept: Query interceptors for middleware
//   - hook: Mutation lifecycle hooks
//   - sql/schemaconfig: Schema configuration
//   - sql/versioned-migration: Migration file generation
//   - sql/globalid: Relay Global ID support
package gen
