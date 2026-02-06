// Package sql provides SQL dialect code generation for the Jennifer generator.
//
// This package implements the gen.DialectGenerator interface for SQL databases
// including PostgreSQL, MySQL, and SQLite.
//
// Usage:
//
//	import (
//	    "github.com/syssam/velox/compiler/gen"
//	    "github.com/syssam/velox/compiler/gen/sql"
//	)
//
//	generator := gen.NewJenniferGenerator(graph, outDir)
//	dialect := sql.NewDialect(generator)
//	generator.WithDialect(dialect)
//	generator.Generate(ctx)
//
// Generated code structure:
//
//	{output}/
//	├── velox.go            # Base types, errors, interfaces
//	├── client.go           # Client struct with entity clients
//	├── tx.go               # Transaction support
//	├── runtime.go          # Runtime utilities
//	├── predicate/
//	│   └── predicate.go    # Predicate type definitions
//	├── {entity}.go         # Entity struct and client
//	├── {entity}_create.go  # Create builder
//	├── {entity}_update.go  # Update builder
//	├── {entity}_delete.go  # Delete builder
//	├── {entity}_query.go   # Query builder
//	├── {entity}_mutation.go# Mutation type
//	└── {entity}/
//	    ├── {entity}.go     # Package constants (table, columns)
//	    └── where.go        # Predicate functions
package sql

import (
	"context"
	"path/filepath"

	"github.com/dave/jennifer/jen"

	"github.com/syssam/velox/compiler/gen"
)

// Generate is a convenience function to generate SQL-dialect code using the Jennifer generator.
// This is the recommended entry point for code generation.
//
// This function properly applies hooks registered in g.Config.Hooks, which allows extensions
// like GraphQL to generate additional code after the main ORM generation.
//
// Example:
//
//	import "github.com/syssam/velox/compiler/gen/sql"
//	err := sql.Generate(graph)
func Generate(g *gen.Graph) error {
	if g.Config == nil || g.Config.Target == "" {
		return gen.NewConfigError("Target", nil, "missing target directory in config")
	}

	// Create the base SQL generator
	baseGenerator := gen.GenerateFunc(func(g *gen.Graph) error {
		generator := gen.NewJenniferGenerator(g, g.Config.Target)
		if g.Config.Package != "" {
			generator.WithPackage(filepath.Base(g.Config.Package))
		}
		dialect := NewDialect(generator)
		generator.WithDialect(dialect)
		return generator.Generate(context.Background())
	})

	// Apply hooks in reverse order (like graph.Gen() does)
	// This allows extensions like GraphQL to run after SQL generation
	var generator gen.Generator = baseGenerator
	for i := len(g.Config.Hooks) - 1; i >= 0; i-- {
		generator = g.Config.Hooks[i](generator)
	}

	return generator.Generate(g)
}

// Dialect implements gen.DialectGenerator for SQL databases.
// It generates SQL-specific code for PostgreSQL, MySQL, SQLite, etc.
//
// Supported SQL features:
//   - Schema migrations (CREATE TABLE, ALTER TABLE, DROP TABLE)
//   - CRUD operations with parameterized queries
//   - Upsert (INSERT ON CONFLICT / ON DUPLICATE KEY)
//   - Row-level locking (FOR UPDATE, FOR SHARE)
//   - Query interceptors and mutation hooks
//   - Privacy layer policies
type Dialect struct {
	helper gen.GeneratorHelper
}

// NewDialect creates a new SQL dialect generator.
// The helper parameter should be a *gen.JenniferGenerator.
func NewDialect(helper gen.GeneratorHelper) *Dialect {
	return &Dialect{helper: helper}
}

// Name returns the dialect name.
func (d *Dialect) Name() string {
	return "sql"
}

// =============================================================================
// Per-entity generation methods
// =============================================================================

// GenEntity generates the entity struct file ({entity}.go).
// Includes: entity struct, edges struct, enum types, entity client.
func (d *Dialect) GenEntity(t *gen.Type) *jen.File {
	return genEntity(d.helper, t)
}

// GenCreate generates the create builder file ({entity}_create.go).
// Includes: Create builder, CreateBulk builder, field setters.
func (d *Dialect) GenCreate(t *gen.Type) *jen.File {
	return genCreate(d.helper, t)
}

// GenUpdate generates the update builder file ({entity}_update.go).
// Includes: Update builder, UpdateOne builder, field setters.
func (d *Dialect) GenUpdate(t *gen.Type) *jen.File {
	return genUpdate(d.helper, t)
}

// GenDelete generates the delete builder file ({entity}_delete.go).
// Includes: Delete builder, DeleteOne builder.
func (d *Dialect) GenDelete(t *gen.Type) *jen.File {
	return genDelete(d.helper, t)
}

// GenQuery generates the query builder file ({entity}_query.go).
// Includes: Query builder with Where, Order, Limit, Offset, eager loading.
func (d *Dialect) GenQuery(t *gen.Type) *jen.File {
	return genQuery(d.helper, t)
}

// GenMutation generates the mutation type file ({entity}_mutation.go).
// Includes: Mutation type with field getters/setters, edge operations.
func (d *Dialect) GenMutation(t *gen.Type) *jen.File {
	return genMutation(d.helper, t)
}

// GenPredicate generates the where predicates file ({entity}/where.go).
// Includes: Predicate functions for all fields (EQ, NEQ, GT, LT, etc.).
func (d *Dialect) GenPredicate(t *gen.Type) *jen.File {
	return genPredicate(d.helper, t)
}

// GenPackage generates the entity package constants file ({entity}/{entity}.go).
// Includes: Table name, column names, field descriptors, edge descriptors.
func (d *Dialect) GenPackage(t *gen.Type) *jen.File {
	return genPackage(d.helper, t)
}

// GenFilter generates the filter file ({entity}_filter.go) for privacy filtering.
// This is only generated when the privacy feature is enabled.
// Implements gen.PrivacyFilterGenerator interface.
func (d *Dialect) GenFilter(t *gen.Type) *jen.File {
	return genFilter(d.helper, t)
}

// =============================================================================
// Graph-level generation methods
// =============================================================================

// GenClient generates the client file (client.go).
// Includes: Client struct, entity clients, hooks/interceptors registration.
func (d *Dialect) GenClient() *jen.File {
	return genClient(d.helper)
}

// GenVelox generates velox.go (base types).
// Includes: Common types, errors, interfaces, Op enum, Value interface.
func (d *Dialect) GenVelox() *jen.File {
	return genVelox(d.helper)
}

// GenTx generates transaction file (tx.go).
// Includes: Tx struct, Commit, Rollback, Client method.
func (d *Dialect) GenTx() *jen.File {
	return genTx(d.helper)
}

// GenRuntime generates runtime file (runtime.go).
// Includes: Runtime utilities, schema descriptors, hook helpers.
func (d *Dialect) GenRuntime() *jen.File {
	return genRuntime(d.helper)
}

// GenPredicatePackage generates the predicate package file (predicate/predicate.go).
// Includes: Predicate type alias for each entity.
func (d *Dialect) GenPredicatePackage() *jen.File {
	return genPredicatePackage(d.helper)
}

// =============================================================================
// Feature support methods
// =============================================================================

// SupportsFeature reports if the feature is supported by SQL dialect.
func (d *Dialect) SupportsFeature(feature string) bool {
	switch feature {
	case "migrate", "upsert", "lock", "modifier", "intercept", "privacy", "hook":
		return true
	default:
		return false
	}
}

// GenFeature generates feature-specific code.
func (d *Dialect) GenFeature(feature string) *jen.File {
	switch feature {
	case "hook":
		// TODO: Generate hook.go
		return nil
	case "intercept":
		// TODO: Generate intercept.go
		return nil
	case "privacy":
		// TODO: Generate privacy.go
		return nil
	default:
		return nil
	}
}

// GenMigrate generates the migrate package files.
// Returns schema.go and migrate.go for the migrate package.
func (d *Dialect) GenMigrate() []*jen.File {
	return genMigrate(d.helper)
}

// GenSchemaConfig generates the internal/schemaconfig.go file.
// This is called when the sql/schemaconfig feature is enabled.
func (d *Dialect) GenSchemaConfig() *jen.File {
	return genSchemaConfig(d.helper, d.helper.Graph())
}

// GenIntercept generates the intercept/intercept.go file.
// This is called when the intercept feature is enabled.
func (d *Dialect) GenIntercept() *jen.File {
	return genIntercept(d.helper)
}

// GenPrivacy generates the privacy/privacy.go file.
// This is called when the privacy feature is enabled.
func (d *Dialect) GenPrivacy() *jen.File {
	return genPrivacy(d.helper)
}

// GenSnapshot generates the internal/schema.go file.
// This is called when the schema/snapshot feature is enabled.
func (d *Dialect) GenSnapshot() *jen.File {
	return genSnapshot(d.helper)
}

// GenVersionedMigration generates the migrate/migrate.go file.
// This is called when the sql/versioned-migration feature is enabled.
func (d *Dialect) GenVersionedMigration() *jen.File {
	return genVersionedMigration(d.helper)
}

// GenGlobalID generates the internal/globalid.go file.
// This is called when the sql/globalid feature is enabled.
func (d *Dialect) GenGlobalID() *jen.File {
	return genGlobalID(d.helper)
}

// GenEntQL generates the querylanguage.go file.
// This is called when the entql feature is enabled.
func (d *Dialect) GenEntQL() *jen.File {
	return genEntQL(d.helper)
}

// Verify Dialect implements gen.DialectGenerator at compile time.
var _ gen.DialectGenerator = (*Dialect)(nil)
