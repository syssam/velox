// Package graph provides the internal schema representation for Velox ORM code generation.
//
// This package is responsible for building and validating the graph structure
// that represents entity schemas, their fields, edges, and indexes. It serves
// as the intermediate representation between schema definitions and code generation.
//
// # Graph Structure
//
// The Graph type holds all entity definitions (Types) and provides validation:
//
//	type Graph struct {
//	    Types []*Type  // All entity types in the schema
//	}
//
// # Type Representation
//
// Each Type represents an entity in the schema:
//
//	type Type struct {
//	    Name    string    // Entity name (e.g., "User")
//	    Fields  []*Field  // Entity fields
//	    Edges   []*Edge   // Entity relationships
//	    Indexes []*Index  // Database indexes
//	}
//
// # Field Representation
//
// Fields represent entity properties with type information and constraints:
//
//	type Field struct {
//	    Name       string      // Field name
//	    Type       Type        // Field type (string, int, etc.)
//	    Optional   bool        // Whether field is optional
//	    Unique     bool        // Whether field has unique constraint
//	    Default    any         // Default value
//	    Validators []Validator // Field validators
//	}
//
// # Edge (Relationship) Types
//
// Edges represent relationships between entities:
//
//   - O2O (One-to-One): User has one Profile
//   - O2M (One-to-Many): User has many Posts
//   - M2O (Many-to-One): Post belongs to User
//   - M2M (Many-to-Many): User has many Groups, Group has many Users
//
// # Builder
//
// The Builder constructs a Graph from velox.Schema definitions:
//
//	builder := graph.NewBuilder(config)
//	for _, schema := range schemas {
//	    builder.AddSchema(schema)
//	}
//	g, err := builder.Build()
//
// # Validation
//
// The Validate method checks the graph for consistency:
//
//	if err := g.Validate(); err != nil {
//	    log.Fatal(err)
//	}
//
// Validation includes:
//   - Edge linking (ensuring bidirectional edges match)
//   - Field uniqueness within types
//   - Index column validation
//   - Foreign key constraints
//
// # Naming Conventions
//
// The package provides utilities for name conversion:
//
//	graph.Snake("UserProfile")  // "user_profile"
//	graph.Pascal("user_name")   // "UserName"
//
// # Usage in Code Generation
//
// The graph package is primarily used internally by the gen package:
//
//	import (
//	    "github.com/syssam/velox/graph"
//	    "github.com/syssam/velox/compiler/gen"
//	)
//
//	// Build graph from schemas
//	g, err := graph.NewGraph(schemas...)
//	if err != nil {
//	    log.Fatal(err)
//	}
//
//	// Use in code generation
//	generator := gen.NewJenniferGenerator(g, outDir)
package graph
