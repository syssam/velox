package gen

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/dave/jennifer/jen"
	"golang.org/x/sync/errgroup"

	"github.com/syssam/velox/schema/field"
)

// JenniferGenerator generates code using Jennifer instead of templates.
// This provides better performance by:
// - Auto-tracking imports (no goimports needed)
// - Streaming writes to disk (lower memory)
// - Compile-time type safety
type JenniferGenerator struct {
	graph   *Graph
	workers int
	outDir  string
	pkg     string

	// Dialect generator for database-specific code
	// Requires at least MinimalDialect, but full DialectGenerator is supported
	dialect MinimalDialect

	// Optional interface implementations detected at runtime
	featureGen       FeatureGenerator
	optionalGen      OptionalFeatureGenerator
	migrateGen       MigrateGenerator
	privacyFilterGen PrivacyFilterGenerator

	// Track generated enum types to avoid duplicates
	enumsMu        sync.Mutex
	generatedEnums map[string]bool
}

// NewJenniferGenerator creates a new Jennifer-based generator.
// You must call WithDialect() to set a dialect before calling Generate().
//
// Example:
//
//	import "github.com/syssam/velox/compiler/gen/sql"
//
//	gen := gen.NewJenniferGenerator(graph, outDir)
//	dialect := sql.NewDialect(gen)
//	gen.WithDialect(dialect)
//	gen.Generate(ctx)
func NewJenniferGenerator(g *Graph, outDir string) *JenniferGenerator {
	return &JenniferGenerator{
		graph:          g,
		workers:        runtime.GOMAXPROCS(0),
		outDir:         outDir,
		pkg:            filepath.Base(outDir),
		generatedEnums: make(map[string]bool),
	}
}

// WithWorkers sets the number of parallel workers.
func (g *JenniferGenerator) WithWorkers(n int) *JenniferGenerator {
	if n > 0 {
		g.workers = n
	}
	return g
}

// WithPackage sets the output package name.
func (g *JenniferGenerator) WithPackage(pkg string) *JenniferGenerator {
	if pkg != "" {
		g.pkg = pkg
	}
	return g
}

// WithDialect sets a custom dialect generator.
// This allows using different database dialects (e.g., Gremlin).
// The dialect must implement MinimalDialect at minimum.
// Additional capabilities are detected via FeatureGenerator and OptionalFeatureGenerator.
func (g *JenniferGenerator) WithDialect(d MinimalDialect) *JenniferGenerator {
	if d != nil {
		g.dialect = d
		// Detect optional capabilities via type assertion
		if fg, ok := d.(FeatureGenerator); ok {
			g.featureGen = fg
		}
		if og, ok := d.(OptionalFeatureGenerator); ok {
			g.optionalGen = og
		}
		if mg, ok := d.(MigrateGenerator); ok {
			g.migrateGen = mg
		}
		if pf, ok := d.(PrivacyFilterGenerator); ok {
			g.privacyFilterGen = pf
		}
	}
	return g
}

// WithFullDialect sets a full DialectGenerator.
// This is a convenience method for dialects that implement all interfaces.
// Deprecated: Use WithDialect instead, which auto-detects capabilities.
func (g *JenniferGenerator) WithFullDialect(d DialectGenerator) *JenniferGenerator {
	return g.WithDialect(d)
}

// Generate generates all code with parallel execution and streaming writes.
// It uses the configured dialect generator for database-specific code.
// Returns an error if no dialect has been set via WithDialect().
func (g *JenniferGenerator) Generate(ctx context.Context) error {
	if g.dialect == nil {
		return NewConfigError("Dialect", nil, "no dialect set: call WithDialect() before Generate()")
	}
	if err := os.MkdirAll(g.outDir, 0o755); err != nil {
		return err
	}

	errg, _ := errgroup.WithContext(ctx)
	errg.SetLimit(g.workers)

	// Generate per-entity files in parallel using dialect interface
	for _, t := range g.graph.Nodes {
		t := t // capture loop variable for goroutine closures
		// Entity struct
		errg.Go(func() error {
			return g.writeFile(g.dialect.GenEntity(t), "", strings.ToLower(t.Name)+".go")
		})

		// Create builder
		errg.Go(func() error {
			return g.writeFile(g.dialect.GenCreate(t), "", strings.ToLower(t.Name)+"_create.go")
		})

		// Update builder
		errg.Go(func() error {
			return g.writeFile(g.dialect.GenUpdate(t), "", strings.ToLower(t.Name)+"_update.go")
		})

		// Delete builder
		errg.Go(func() error {
			return g.writeFile(g.dialect.GenDelete(t), "", strings.ToLower(t.Name)+"_delete.go")
		})

		// Query builder
		errg.Go(func() error {
			return g.writeFile(g.dialect.GenQuery(t), "", strings.ToLower(t.Name)+"_query.go")
		})

		// Mutation type
		errg.Go(func() error {
			return g.writeFile(g.dialect.GenMutation(t), "", strings.ToLower(t.Name)+"_mutation.go")
		})

		// Per-entity constant package
		errg.Go(func() error {
			return g.writeFile(g.dialect.GenPackage(t), t.PackageDir(), t.PackageDir()+".go")
		})

		// Predicates (where.go in entity subpackage)
		errg.Go(func() error {
			return g.writeFile(g.dialect.GenPredicate(t), t.PackageDir(), "where.go")
		})

		// Filter file for privacy feature ({entity}_filter.go)
		// Only generate if privacy feature is enabled and dialect supports it
		if g.privacyFilterGen != nil {
			if enabled, _ := g.graph.Config.FeatureEnabled(FeaturePrivacy.Name); enabled {
				errg.Go(func() error {
					return g.writeFile(g.privacyFilterGen.GenFilter(t), "", strings.ToLower(t.Name)+"_filter.go")
				})
			}
		}
	}

	// Generate shared files using dialect interface
	errg.Go(func() error {
		return g.writeFile(g.dialect.GenPredicatePackage(), "predicate", "predicate.go")
	})

	errg.Go(func() error {
		return g.writeFile(g.dialect.GenClient(), "", "client.go")
	})

	errg.Go(func() error {
		return g.writeFile(g.dialect.GenVelox(), "", "velox.go")
	})

	errg.Go(func() error {
		return g.writeFile(g.dialect.GenTx(), "", "tx.go")
	})

	errg.Go(func() error {
		return g.writeFile(g.dialect.GenRuntime(), "runtime", "runtime.go")
	})

	// Generate optional feature files if supported by dialect
	if g.featureGen != nil {
		for _, feature := range []string{"migrate", "hook", "intercept", "privacy"} {
			feature := feature // capture loop variable for goroutine closure
			if g.featureGen.SupportsFeature(feature) {
				if f := g.featureGen.GenFeature(feature); f != nil {
					// f is already a fresh variable from the if-statement declaration
					errg.Go(func() error {
						return g.writeFile(f, "", feature+".go")
					})
				}
			}
		}
	}

	// Generate optional features if the dialect supports them
	if g.optionalGen != nil {
		// Generate schemaconfig if the feature is enabled
		if enabled, _ := g.graph.Config.FeatureEnabled(FeatureSchemaConfig.Name); enabled {
			if f := g.optionalGen.GenSchemaConfig(); f != nil {
				errg.Go(func() error {
					return g.writeFile(f, "internal", "schemaconfig.go")
				})
			}
		}

		// Generate intercept package if the feature is enabled
		if enabled, _ := g.graph.Config.FeatureEnabled(FeatureIntercept.Name); enabled {
			if f := g.optionalGen.GenIntercept(); f != nil {
				errg.Go(func() error {
					return g.writeFile(f, "intercept", "intercept.go")
				})
			}
		}

		// Generate privacy package if the feature is enabled
		if enabled, _ := g.graph.Config.FeatureEnabled(FeaturePrivacy.Name); enabled {
			if f := g.optionalGen.GenPrivacy(); f != nil {
				errg.Go(func() error {
					return g.writeFile(f, "privacy", "privacy.go")
				})
			}
		}

		// Generate snapshot if the feature is enabled
		if enabled, _ := g.graph.Config.FeatureEnabled(FeatureSnapshot.Name); enabled {
			if f := g.optionalGen.GenSnapshot(); f != nil {
				errg.Go(func() error {
					return g.writeFile(f, "internal", "schema.go")
				})
			}
		}

		// Generate versioned migration if the feature is enabled
		if enabled, _ := g.graph.Config.FeatureEnabled(FeatureVersionedMigration.Name); enabled {
			if f := g.optionalGen.GenVersionedMigration(); f != nil {
				errg.Go(func() error {
					return g.writeFile(f, "migrate", "migrate.go")
				})
			}
		}

		// Generate global ID if the feature is enabled
		if enabled, _ := g.graph.Config.FeatureEnabled(FeatureGlobalID.Name); enabled {
			if f := g.optionalGen.GenGlobalID(); f != nil {
				errg.Go(func() error {
					return g.writeFile(f, "internal", "globalid.go")
				})
			}
		}

		// Generate entql if the feature is enabled
		if enabled, _ := g.graph.Config.FeatureEnabled(FeatureEntQL.Name); enabled {
			if f := g.optionalGen.GenEntQL(); f != nil {
				errg.Go(func() error {
					return g.writeFile(f, "", "querylanguage.go")
				})
			}
		}
	}

	// Generate migrate package if the dialect supports it
	if g.migrateGen != nil {
		files := g.migrateGen.GenMigrate()
		if len(files) >= 2 {
			// First file is schema.go
			if files[0] != nil {
				schemaFile := files[0]
				errg.Go(func() error {
					return g.writeFile(schemaFile, "migrate", "schema.go")
				})
			}
			// Second file is migrate.go
			if files[1] != nil {
				migrateFile := files[1]
				errg.Go(func() error {
					return g.writeFile(migrateFile, "migrate", "migrate.go")
				})
			}
		}
	}

	return errg.Wait()
}

// =============================================================================
// GeneratorHelper interface implementation
// These exported methods allow dialect packages to access helper functionality.
// =============================================================================

// NewFile creates a new Jennifer file with the standard header comment.
func (g *JenniferGenerator) NewFile(pkg string) *jen.File {
	return g.newFile(pkg)
}

// GoType returns the Jennifer code for a field's Go type.
func (g *JenniferGenerator) GoType(f *Field) jen.Code {
	return g.goType(f)
}

// BaseType returns the Jennifer code for a field's base type (without pointer).
func (g *JenniferGenerator) BaseType(f *Field) jen.Code {
	return g.baseType(f)
}

// IDType returns the Jennifer code for the ID field type of a type.
func (g *JenniferGenerator) IDType(t *Type) jen.Code {
	return g.idType(t)
}

// StructTags returns the struct tags for a field.
func (g *JenniferGenerator) StructTags(f *Field) map[string]string {
	return g.structTags(f)
}

// EdgeStructTags returns the struct tags for an edge field.
func (g *JenniferGenerator) EdgeStructTags(e *Edge) map[string]string {
	return g.edgeStructTags(e)
}

// VeloxPkg returns the import path for the velox package.
func (g *JenniferGenerator) VeloxPkg() string {
	return g.veloxPkg()
}

// SQLPkg returns the import path for the dialect/sql package.
func (g *JenniferGenerator) SQLPkg() string {
	return g.sqlPkg()
}

// SQLGraphPkg returns the import path for the dialect/sql/sqlgraph package.
func (g *JenniferGenerator) SQLGraphPkg() string {
	return g.sqlgraphPkg()
}

// FieldPkg returns the import path for the schema field package.
func (g *JenniferGenerator) FieldPkg() string {
	return g.fieldPkg()
}

// EntityPkgPath returns the full import path for an entity's subpackage.
func (g *JenniferGenerator) EntityPkgPath(t *Type) string {
	return g.entityPkgPath(t)
}

// EdgeRelType returns the sqlgraph relationship type constant name for an edge.
func (g *JenniferGenerator) EdgeRelType(e *Edge) string {
	return g.edgeRelType(e)
}

// FieldTypeConstant returns the field type constant name for a field.
func (g *JenniferGenerator) FieldTypeConstant(f *Field) string {
	return g.fieldTypeConstant(f)
}

// Graph returns the schema graph.
func (g *JenniferGenerator) Graph() *Graph {
	return g.graph
}

// Pkg returns the output package name.
func (g *JenniferGenerator) Pkg() string {
	return g.pkg
}

// CheckEnumGenerated checks if an enum type has already been generated.
// Returns true if it was already generated, false if this is the first time.
// This method is thread-safe.
func (g *JenniferGenerator) CheckEnumGenerated(enumName string) bool {
	g.enumsMu.Lock()
	defer g.enumsMu.Unlock()
	if g.generatedEnums[enumName] {
		return true
	}
	g.generatedEnums[enumName] = true
	return false
}

// ZeroValue returns the Jennifer code for a field's zero value.
func (g *JenniferGenerator) ZeroValue(f *Field) jen.Code {
	return g.zeroValue(f)
}

// BaseZeroValue returns the Jennifer code for a field's base type zero value.
func (g *JenniferGenerator) BaseZeroValue(f *Field) jen.Code {
	return g.baseZeroValue(f)
}

// PredicatePkg returns the import path for the predicate package.
func (g *JenniferGenerator) PredicatePkg() string {
	return g.predicatePkg()
}

// PredicateType returns the predicate type for an entity (e.g., predicate.User).
func (g *JenniferGenerator) PredicateType(t *Type) jen.Code {
	return g.predicateType(t)
}

// EdgePredicateType returns the predicate type for an edge's target entity.
func (g *JenniferGenerator) EdgePredicateType(e *Edge) jen.Code {
	return g.edgePredicateType(e)
}

// FeatureEnabled reports if the given feature name is enabled.
func (g *JenniferGenerator) FeatureEnabled(name string) bool {
	enabled, _ := g.graph.Config.FeatureEnabled(name)
	return enabled
}

// InternalPkg returns the import path for the internal package.
func (g *JenniferGenerator) InternalPkg() string {
	return path.Join(g.graph.Config.Package, "internal")
}

// AnnotationExists checks if a global annotation with the given name exists.
func (g *JenniferGenerator) AnnotationExists(name string) bool {
	if g.graph.Config.Annotations == nil {
		return false
	}
	_, exists := g.graph.Config.Annotations[name]
	return exists
}

// Verify JenniferGenerator implements GeneratorHelper at compile time.
var _ GeneratorHelper = (*JenniferGenerator)(nil)

// =============================================================================
// Internal helper methods (unexported)
// =============================================================================

// writeFile writes jennifer file directly to disk (no buffering).
func (g *JenniferGenerator) writeFile(f *jen.File, subdir, filename string) error {
	dir := g.outDir
	if subdir != "" {
		dir = filepath.Join(g.outDir, subdir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	path := filepath.Join(dir, filename)
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer out.Close()

	// Jennifer renders with correct imports and formatting
	return f.Render(out)
}

// newFile creates a new Jennifer file with the header comment.
func (g *JenniferGenerator) newFile(pkg string) *jen.File {
	f := jen.NewFile(pkg)
	f.HeaderComment("Code generated by velox. DO NOT EDIT.")
	return f
}

// goType returns the Jennifer code for a field's Go type.
func (g *JenniferGenerator) goType(f *Field) jen.Code {
	if f.Nillable {
		return g.pointerType(f)
	}
	return g.baseType(f)
}

// pointerType returns the Jennifer code for a pointer to the field's base type.
// For built-in types, we use Id("*type") to avoid whitespace issues that occur
// when using Add() with Op("*") in struct field definitions.
func (g *JenniferGenerator) pointerType(f *Field) jen.Code {
	if f.Type == nil {
		return jen.Id("*any")
	}

	// If field has a custom Go type, use it
	if f.HasGoType() && f.Type.Ident != "" {
		if f.Type.PkgPath != "" {
			typeName := f.Type.Ident
			isSlice := strings.HasPrefix(typeName, "[]")
			if isSlice {
				typeName = typeName[2:]
			}
			if idx := strings.LastIndex(typeName, "."); idx >= 0 {
				typeName = typeName[idx+1:]
			}
			if isSlice {
				// *[]pkg.Type - needs Op("*") with Qual
				return jen.Op("*").Index().Qual(f.Type.PkgPath, typeName)
			}
			// *pkg.Type - needs Op("*") with Qual
			return jen.Op("*").Qual(f.Type.PkgPath, typeName)
		}
		return jen.Id("*" + f.Type.Ident)
	}

	// For local enum fields, need Op("*") with Qual
	if f.IsEnum() {
		return jen.Op("*").Qual(f.EnumPkgPath(), f.SubpackageEnumTypeName())
	}

	// For primitive types, use Id("*type") to avoid whitespace issues
	switch f.Type.Type.String() {
	case "string":
		return jen.Id("*string")
	case "int":
		return jen.Id("*int")
	case "int8":
		return jen.Id("*int8")
	case "int16":
		return jen.Id("*int16")
	case "int32":
		return jen.Id("*int32")
	case "int64":
		return jen.Id("*int64")
	case "uint":
		return jen.Id("*uint")
	case "uint8":
		return jen.Id("*uint8")
	case "uint16":
		return jen.Id("*uint16")
	case "uint32":
		return jen.Id("*uint32")
	case "uint64":
		return jen.Id("*uint64")
	case "float32":
		return jen.Id("*float32")
	case "float64":
		return jen.Id("*float64")
	case "bool":
		return jen.Id("*bool")
	case "time.Time":
		// time.Time needs qualified import
		return jen.Op("*").Qual("time", "Time")
	case "[16]byte":
		// uuid.UUID needs qualified import
		return jen.Op("*").Qual("github.com/google/uuid", "UUID")
	case "[]byte":
		return jen.Id("*[]byte")
	case "json.RawMessage":
		// For JSON fields, use the custom type if available
		if f.Type.Ident != "" {
			return jen.Id("*" + f.Type.Ident)
		}
		return jen.Id("*any")
	default:
		return jen.Id("*any")
	}
}

// baseType returns the Jennifer code for a field's base type (without pointer).
func (g *JenniferGenerator) baseType(f *Field) jen.Code {
	if f.Type == nil {
		return jen.Any()
	}

	// If field has a custom Go type, use it
	if f.HasGoType() && f.Type.Ident != "" {
		if f.Type.PkgPath != "" {
			// Extract just the type name from Ident (e.g., "decimal.Decimal" -> "Decimal")
			// because jen.Qual will add the package qualifier
			typeName := f.Type.Ident
			// Strip the [] prefix if present (we'll add it back with Index() if needed)
			isSlice := strings.HasPrefix(typeName, "[]")
			if isSlice {
				typeName = typeName[2:]
			}
			if idx := strings.LastIndex(typeName, "."); idx >= 0 {
				typeName = typeName[idx+1:]
			}
			baseType := jen.Qual(f.Type.PkgPath, typeName)
			// Re-add the slice if it was present in the original type
			if isSlice {
				return jen.Index().Add(baseType)
			}
			return baseType
		}
		return jen.Id(f.Type.Ident)
	}

	// For local enum fields (without custom GoType), use the subpackage enum type
	// The enum type is defined in the entity's subpackage (e.g., abtesting.Type)
	// Note: TypeEnum.String() returns "string", so we check IsEnum() separately
	if f.IsEnum() {
		return jen.Qual(f.EnumPkgPath(), f.SubpackageEnumTypeName())
	}

	switch f.Type.Type.String() {
	case "string":
		return jen.String()
	case "int":
		return jen.Int()
	case "int8":
		return jen.Int8()
	case "int16":
		return jen.Int16()
	case "int32":
		return jen.Int32()
	case "int64":
		return jen.Int64()
	case "uint":
		return jen.Uint()
	case "uint8":
		return jen.Uint8()
	case "uint16":
		return jen.Uint16()
	case "uint32":
		return jen.Uint32()
	case "uint64":
		return jen.Uint64()
	case "float32":
		return jen.Float32()
	case "float64":
		return jen.Float64()
	case "bool":
		return jen.Bool()
	case "time.Time":
		return jen.Qual("time", "Time")
	case "uuid.UUID":
		return jen.Qual("github.com/google/uuid", "UUID")
	case "[]byte":
		return jen.Index().Byte()
	case "json":
		if f.Type.Ident != "" {
			if f.Type.PkgPath != "" {
				return jen.Qual(f.Type.PkgPath, f.Type.Ident)
			}
			return jen.Id(f.Type.Ident)
		}
		return jen.Any()
	default:
		// For other/custom types
		if f.Type.Ident != "" {
			if f.Type.PkgPath != "" {
				return jen.Qual(f.Type.PkgPath, f.Type.Ident)
			}
			return jen.Id(f.Type.Ident)
		}
		return jen.Any()
	}
}

// idType returns the Jennifer code for the ID field type of a type.
func (g *JenniferGenerator) idType(t *Type) jen.Code {
	if t.ID == nil {
		return jen.Int()
	}
	return g.baseType(t.ID)
}

// structTags returns the struct tags for a field.
func (g *JenniferGenerator) structTags(f *Field) map[string]string {
	tags := make(map[string]string)
	if f.StructTag != "" {
		// Parse existing struct tags
		// For simplicity, just use the raw tag if present
		return map[string]string{"json": f.Name + ",omitempty"}
	}
	tags["json"] = f.Name + ",omitempty"
	return tags
}

// edgeStructTags returns the struct tags for an edge field.
func (g *JenniferGenerator) edgeStructTags(e *Edge) map[string]string {
	if e.StructTag != "" {
		return map[string]string{"json": e.Name + ",omitempty"}
	}
	return map[string]string{"json": e.Name + ",omitempty"}
}

// veloxPkg returns the import path for the velox package.
func (g *JenniferGenerator) veloxPkg() string {
	return "github.com/syssam/velox"
}

// sqlPkg returns the import path for the dialect/sql package.
func (g *JenniferGenerator) sqlPkg() string {
	return "github.com/syssam/velox/dialect/sql"
}

// sqlgraphPkg returns the import path for the dialect/sql/sqlgraph package.
func (g *JenniferGenerator) sqlgraphPkg() string {
	return "github.com/syssam/velox/dialect/sql/sqlgraph"
}

// fieldPkg returns the import path for the schema field package.
func (g *JenniferGenerator) fieldPkg() string {
	return "github.com/syssam/velox/schema/field"
}

// fieldTypeConstant returns the field type constant name for a field.
func (g *JenniferGenerator) fieldTypeConstant(f *Field) string {
	if f.Type == nil {
		return "TypeString"
	}
	switch f.Type.Type {
	case field.TypeBool:
		return "TypeBool"
	case field.TypeTime:
		return "TypeTime"
	case field.TypeJSON:
		return "TypeJSON"
	case field.TypeUUID:
		return "TypeUUID"
	case field.TypeBytes:
		return "TypeBytes"
	case field.TypeEnum:
		return "TypeEnum"
	case field.TypeString:
		return "TypeString"
	case field.TypeOther:
		return "TypeOther"
	case field.TypeInt:
		return "TypeInt"
	case field.TypeInt8:
		return "TypeInt8"
	case field.TypeInt16:
		return "TypeInt16"
	case field.TypeInt32:
		return "TypeInt32"
	case field.TypeInt64:
		return "TypeInt64"
	case field.TypeUint:
		return "TypeUint"
	case field.TypeUint8:
		return "TypeUint8"
	case field.TypeUint16:
		return "TypeUint16"
	case field.TypeUint32:
		return "TypeUint32"
	case field.TypeUint64:
		return "TypeUint64"
	case field.TypeFloat32:
		return "TypeFloat32"
	case field.TypeFloat64:
		return "TypeFloat64"
	default:
		return "TypeString"
	}
}

// entityPkgPath returns the full import path for an entity's subpackage.
func (g *JenniferGenerator) entityPkgPath(t *Type) string {
	if g.graph.Config != nil && g.graph.Config.Package != "" {
		return g.graph.Config.Package + "/" + t.PackageDir()
	}
	return g.pkg + "/" + t.PackageDir()
}

// edgeRelType returns the sqlgraph relationship type constant name for an edge.
func (g *JenniferGenerator) edgeRelType(e *Edge) string {
	switch e.Rel.Type {
	case O2O:
		return "O2O"
	case O2M:
		return "O2M"
	case M2O:
		return "M2O"
	case M2M:
		return "M2M"
	default:
		return "O2M" // default to O2M
	}
}

// predicatePkg returns the import path for the predicate package.
func (g *JenniferGenerator) predicatePkg() string {
	if g.graph.Config != nil && g.graph.Config.Package != "" {
		return g.graph.Config.Package + "/predicate"
	}
	return g.pkg + "/predicate"
}

// predicateType returns the predicate type for an entity (e.g., predicate.User).
func (g *JenniferGenerator) predicateType(t *Type) jen.Code {
	return jen.Qual(g.predicatePkg(), t.Name)
}

// edgePredicateType returns the predicate type for an edge's target entity (e.g., predicate.Car).
func (g *JenniferGenerator) edgePredicateType(edge *Edge) jen.Code {
	return jen.Qual(g.predicatePkg(), edge.Type.Name)
}

// zeroValue returns the zero value for a field type.
// For nillable fields, returns nil (for pointer types).
func (g *JenniferGenerator) zeroValue(f *Field) jen.Code {
	if f == nil {
		return jen.Lit(0)
	}
	if f.Nillable {
		return jen.Nil()
	}
	return g.baseZeroValue(f)
}

// baseZeroValue returns the zero value for the base type (ignoring nillability).
// Used in mutation getters where the return type is always the base type.
func (g *JenniferGenerator) baseZeroValue(f *Field) jen.Code {
	if f == nil {
		return jen.Lit(0)
	}

	// Check for enum first - enums are string-based, not struct-based
	// They may have a custom package path but their zero value is "" not {}
	if f.IsEnum() {
		return jen.Lit("")
	}

	// If field has a custom Go type, return appropriate zero value
	if f.HasGoType() && f.Type.PkgPath != "" {
		typeName := f.Type.Ident
		// For slice types, return nil
		if strings.HasPrefix(typeName, "[]") {
			return jen.Nil()
		}
		if idx := strings.LastIndex(typeName, "."); idx >= 0 {
			typeName = typeName[idx+1:]
		}
		return jen.Qual(f.Type.PkgPath, typeName).Values()
	}

	switch f.Type.Type.String() {
	case "string", "enum":
		return jen.Lit("")
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64":
		return jen.Lit(0)
	case "bool":
		return jen.False()
	case "time.Time":
		return jen.Qual("time", "Time").Values()
	case "uuid.UUID":
		return jen.Qual("github.com/google/uuid", "Nil")
	default:
		// For unknown types, try empty struct literal
		if f.Type.Ident != "" {
			return jen.Id(f.Type.Ident).Values()
		}
		return jen.Nil()
	}
}

// GenerateJennifer is a convenience function to generate code using the Jennifer generator.
// It replaces the template-based generation for improved performance.
//
// IMPORTANT: You must set a dialect before calling this function.
// Use the sql.Generate() helper from gen/sql package instead:
//
//	import "github.com/syssam/velox/compiler/gen/sql"
//	err := sql.Generate(graph)
//
// Or manually:
//
//	import "github.com/syssam/velox/compiler/gen/sql"
//	gen := gen.NewJenniferGenerator(graph, outDir)
//	dialect := sql.NewDialect(gen)
//	gen.WithDialect(dialect).Generate(ctx)
func GenerateJennifer(g *Graph) error {
	if g.Config == nil || g.Config.Target == "" {
		return NewConfigError("Target", nil, "missing target directory in config")
	}
	gen := NewJenniferGenerator(g, g.Config.Target)
	if g.Config.Package != "" {
		gen.WithPackage(filepath.Base(g.Config.Package))
	}
	// Dialect must be set externally to avoid import cycles
	// Use gen/sql.Generate() or set dialect manually with gen.WithDialect()
	return gen.Generate(context.Background())
}
