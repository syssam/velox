package gen

import "github.com/dave/jennifer/jen"

// =============================================================================
// Interface Segregation: Split DialectGenerator into smaller, focused interfaces
// =============================================================================

// EntityGenerator generates per-entity code.
// Each method is called once per entity type in the schema.
type EntityGenerator interface {
	// GenEntity generates the entity struct ({entity}.go)
	GenEntity(t *Type) *jen.File
	// GenCreate generates the create builder ({entity}_create.go)
	GenCreate(t *Type) *jen.File
	// GenUpdate generates the update builder ({entity}_update.go)
	GenUpdate(t *Type) *jen.File
	// GenDelete generates the delete builder ({entity}_delete.go)
	GenDelete(t *Type) *jen.File
	// GenQuery generates the query builder ({entity}_query.go)
	GenQuery(t *Type) *jen.File
	// GenMutation generates the mutation type ({entity}_mutation.go)
	GenMutation(t *Type) *jen.File
	// GenPredicate generates where predicates ({entity}/where.go)
	GenPredicate(t *Type) *jen.File
	// GenPackage generates entity package constants ({entity}/{entity}.go)
	GenPackage(t *Type) *jen.File
}

// GraphGenerator generates graph-level code.
// Each method is called once per generation run.
type GraphGenerator interface {
	// GenClient generates the client (client.go)
	GenClient() *jen.File
	// GenVelox generates base types, errors, interfaces (velox.go)
	GenVelox() *jen.File
	// GenTx generates transaction support (tx.go)
	GenTx() *jen.File
	// GenRuntime generates runtime utilities (runtime.go)
	GenRuntime() *jen.File
	// GenPredicatePackage generates the predicate package (predicate/predicate.go)
	GenPredicatePackage() *jen.File
}

// FeatureGenerator generates feature-specific code.
type FeatureGenerator interface {
	// SupportsFeature checks if the dialect supports a feature
	SupportsFeature(feature string) bool
	// GenFeature generates feature-specific code
	GenFeature(feature string) *jen.File
}

// OptionalFeatureGenerator provides optional feature generation.
// Dialects may implement some or all of these methods.
type OptionalFeatureGenerator interface {
	// GenSchemaConfig generates internal/schemaconfig.go
	GenSchemaConfig() *jen.File
	// GenIntercept generates intercept/intercept.go
	GenIntercept() *jen.File
	// GenPrivacy generates privacy/privacy.go
	GenPrivacy() *jen.File
	// GenSnapshot generates internal/schema.go
	GenSnapshot() *jen.File
	// GenVersionedMigration generates migrate/migrate.go
	GenVersionedMigration() *jen.File
	// GenGlobalID generates internal/globalid.go
	GenGlobalID() *jen.File
	// GenEntQL generates querylanguage.go
	GenEntQL() *jen.File
}

// MigrateGenerator generates the migrate package for auto-migration support.
// This is optional - dialects that support it will generate migrate/schema.go
// and migrate/migrate.go files.
type MigrateGenerator interface {
	// GenMigrate generates the migrate package files.
	// Returns [schema.go, migrate.go] for the migrate package.
	GenMigrate() []*jen.File
}

// PrivacyFilterGenerator generates per-entity filter files for privacy feature.
// This is optional - dialects that support privacy filtering implement this interface.
type PrivacyFilterGenerator interface {
	// GenFilter generates the filter file ({entity}_filter.go) for privacy filtering.
	// Returns nil if the dialect doesn't support filter generation.
	GenFilter(t *Type) *jen.File
}

// MinimalDialect requires only entity and graph generation.
// This is the minimum interface a dialect must implement.
type MinimalDialect interface {
	// Name returns the dialect name (e.g., "sql", "gremlin")
	Name() string
	EntityGenerator
	GraphGenerator
}

// =============================================================================
// Full DialectGenerator interface (backward compatible)
// =============================================================================

// DialectGenerator defines the interface for dialect-specific code generation.
// Each database dialect (SQL, Gremlin, etc.) implements this interface to generate
// dialect-specific code for CRUD operations.
//
// Architecture:
//
//	┌─────────────────────────────────────────────────────────────┐
//	│                    JenniferGenerator                        │
//	│  (Orchestration: parallel execution, file writing)          │
//	└─────────────────────────┬───────────────────────────────────┘
//	                          │ uses
//	                          ▼
//	┌─────────────────────────────────────────────────────────────┐
//	│                   DialectGenerator                          │
//	│  (Interface: defines what each dialect must implement)      │
//	└─────────────────────────┬───────────────────────────────────┘
//	                          │ implemented by
//	          ┌───────────────┼───────────────┐
//	          ▼               ▼               ▼
//	   ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
//	   │ SQLDialect  │ │GremlinDialect│ │ CustomDialect│
//	   │ (gen/sql)   │ │  (future)   │ │ (user-defined)│
//	   └─────────────┘ └─────────────┘ └─────────────┘
//
// Methods return *jen.File containing the generated code. The main generator
// orchestrates calling these methods and writing the files to disk.
//
// Usage:
//
//	import "github.com/syssam/velox/compiler/gen/sql"
//
//	generator := gen.NewJenniferGenerator(graph, outDir).
//	    WithDialect(sql.NewDialect(generator))
//
// For custom dialects, you can implement MinimalDialect for basic support,
// or the full DialectGenerator for complete feature support.
type DialectGenerator interface {
	MinimalDialect
	FeatureGenerator
	OptionalFeatureGenerator
}

// DialectOption configures dialect-specific options.
type DialectOption func(DialectGenerator)

// GeneratorHelper provides helper methods for dialect implementations.
// JenniferGenerator implements this interface, allowing dialect packages
// to use helper methods without importing the full generator.
type GeneratorHelper interface {
	// NewFile creates a new Jennifer file with the standard header comment.
	NewFile(pkg string) *jen.File

	// GoType returns the Jennifer code for a field's Go type.
	GoType(f *Field) jen.Code

	// BaseType returns the Jennifer code for a field's base type (without pointer).
	BaseType(f *Field) jen.Code

	// IDType returns the Jennifer code for the ID field type of a type.
	IDType(t *Type) jen.Code

	// ZeroValue returns the Jennifer code for a field's zero value.
	ZeroValue(f *Field) jen.Code

	// BaseZeroValue returns the Jennifer code for a field's base type zero value.
	BaseZeroValue(f *Field) jen.Code

	// StructTags returns the struct tags for a field.
	StructTags(f *Field) map[string]string

	// EdgeStructTags returns the struct tags for an edge field.
	EdgeStructTags(e *Edge) map[string]string

	// VeloxPkg returns the import path for the velox package.
	VeloxPkg() string

	// SQLPkg returns the import path for the dialect/sql package.
	SQLPkg() string

	// SQLGraphPkg returns the import path for the dialect/sql/sqlgraph package.
	SQLGraphPkg() string

	// FieldPkg returns the import path for the schema field package.
	FieldPkg() string

	// PredicatePkg returns the import path for the predicate package.
	PredicatePkg() string

	// EntityPkgPath returns the full import path for an entity's subpackage.
	EntityPkgPath(t *Type) string

	// EdgeRelType returns the sqlgraph relationship type constant name for an edge.
	EdgeRelType(e *Edge) string

	// FieldTypeConstant returns the field type constant name for a field.
	FieldTypeConstant(f *Field) string

	// PredicateType returns the predicate type for an entity (e.g., predicate.User).
	PredicateType(t *Type) jen.Code

	// EdgePredicateType returns the predicate type for an edge's target entity.
	EdgePredicateType(e *Edge) jen.Code

	// Graph returns the schema graph.
	Graph() *Graph

	// Pkg returns the output package name.
	Pkg() string

	// CheckEnumGenerated checks if an enum type has already been generated.
	// Returns true if it was already generated, false if this is the first time.
	CheckEnumGenerated(enumName string) bool

	// FeatureEnabled reports if the given feature name is enabled.
	FeatureEnabled(name string) bool

	// AnnotationExists checks if a global annotation with the given name exists.
	AnnotationExists(name string) bool

	// InternalPkg returns the import path for the internal package.
	InternalPkg() string
}
