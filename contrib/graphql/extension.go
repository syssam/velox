package graphql

import (
	"encoding/json"
	"fmt"
	"path"
	"path/filepath"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema"
)

// SchemaHook is a function that is called after GraphQL schema generation.
// It receives the graph and the typed AST schema, and can make structural
// modifications (add/remove types, fields, directives). This matches Ent's
// entgql.SchemaHook signature for typed AST access.
type SchemaHook func(g *gen.Graph, schema *ast.Schema) error

// Extension implements the compiler.Extension interface for GraphQL code generation.
// It integrates with the velox code generation pipeline to generate GraphQL schema,
// models, and resolvers alongside ORM code.
//
// Usage:
//
//	import (
//	    "github.com/syssam/velox/compiler"
//	    "github.com/syssam/velox/compiler/gen"
//	    "github.com/syssam/velox/contrib/graphql"
//	)
//
//	ex, err := graphql.NewExtension(
//	    graphql.WithConfigPath("./gqlgen.yml"),
//	    graphql.WithSchemaPath("./velox/schema.graphql"),
//	)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	cfg, err := gen.NewConfig(
//	    gen.WithTarget("./velox"),
//	)
//	err = compiler.Generate("./schema", cfg, compiler.Extensions(ex))
type Extension struct {
	config Config
	hooks  []gen.Hook

	// gqlgenConfig is the parsed gqlgen configuration (read-only, never saved back).
	gqlgenConfig *GQLGenConfig

	// schemaHooks are hooks to run after schema generation.
	schemaHooks []SchemaHook

	// templates are additional templates to include in code generation.
	templates []*gen.Template

	// schemaGenerator enables automatic GraphQL schema file generation.
	// When false, only Go files are generated (no .graphql schema file).
	schemaGenerator bool
}

// ExtensionOption is a function that configures the Extension.
type ExtensionOption func(*Extension) error

// NewExtension creates a new GraphQL extension with the given options.
//
// Most settings are controlled by schema annotations on each entity:
//   - graphql.RelayConnection() - enable Relay connections for an entity
//   - graphql.Mutations(...) - control which mutations are generated
//   - graphql.QueryField() - include entity in Query type
//   - graphql.Skip(...) - skip generation for entity/field
//
// Extension options are for global settings only:
//   - WithConfigPath() - path to gqlgen.yml for auto model binding
//   - WithSchemaPath() - output path for generated schema
func NewExtension(opts ...ExtensionOption) (*Extension, error) {
	// Register gqlgen field collector with the Velox runtime.
	// This is done here (not init()) to avoid side effects when importing
	// the package only for annotation types.
	RegisterFieldCollector()

	ex := &Extension{
		config: Config{
			// Defaults - these enable features globally,
			// but per-entity control is via schema annotations
			RelayConnection: true,
			WhereInputs:     true,
			Ordering:        true,
			RelaySpec:       true,
			Mutations:       true,
		},
	}
	for _, opt := range opts {
		if err := opt(ex); err != nil {
			return nil, err
		}
	}
	// Add the generate hook
	ex.hooks = append(ex.hooks, ex.generateHook())
	return ex, nil
}

// Hooks returns the hooks for code generation.
func (e *Extension) Hooks() []gen.Hook {
	return e.hooks
}

// Annotations returns global annotations to inject into gen.Config.
func (e *Extension) Annotations() []schema.Annotation {
	return []schema.Annotation{
		&extensionAnnotation{},
	}
}

// Templates returns any additional templates for code generation.
// Use WithTemplates() to add custom templates.
func (e *Extension) Templates() []*gen.Template {
	return e.templates
}

// Options returns compiler options required by the GraphQL extension.
// This enables features that the GraphQL code generation depends on, such as
// FeatureNamedEdges for the field collection and edge resolver code.
func (e *Extension) Options() []compiler.Option {
	return []compiler.Option{
		// Enable FeatureNamedEdges - required for WithNamed{Edge} and Named{Edge} methods
		// which are used by gql_collection.go
		func(cfg *gen.Config) error {
			// Add FeatureNamedEdges if not already present
			hasNamedEdges := false
			for _, f := range cfg.Features {
				if f.Name == gen.FeatureNamedEdges.Name {
					hasNamedEdges = true
					break
				}
			}
			if !hasNamedEdges {
				cfg.Features = append(cfg.Features, gen.FeatureNamedEdges)
			}
			return nil
		},
	}
}

// Config returns the GraphQL configuration.
// Use this when calling graphql.Generate() directly instead of using hooks.
func (e *Extension) Config() Config {
	return e.config
}

// GQLGenConfig returns the loaded gqlgen configuration, if any.
func (e *Extension) GQLGenConfig() *GQLGenConfig {
	return e.gqlgenConfig
}

// generateHook returns a hook that generates GraphQL code after ORM generation.
func (e *Extension) generateHook() gen.Hook {
	return func(next gen.Generator) gen.Generator {
		return gen.GenerateFunc(func(g *gen.Graph) error {
			// Pre-generation: inject RelayConnection annotation into entity types
			// so the core generator includes Paginate in the Querier interface.
			// The core can only read map[string]any annotations (no graphql import),
			// so we serialize the flag before it generates the entity package.
			if e.config.RelayConnection {
				for _, t := range g.Nodes {
					ann := extractGraphQLAnnotation(t.Annotations)
					if ann.HasRelayConnection() {
						continue // already annotated
					}
					if ann.IsSkipType() {
						continue
					}
					if ann.HasQueryField() {
						continue // explicit QueryField without RelayConnection = simple list
					}
					// Default: this entity gets Relay pagination — mark it.
					// Store via JSON round-trip so the core generator (which reads
					// map[string]any, not the Annotation struct) sees the flag.
					ann.RelayConnection = true
					if t.Annotations == nil {
						t.Annotations = make(map[string]any)
					}
					data, err := json.Marshal(ann)
					if err == nil {
						var m map[string]any
						if err := json.Unmarshal(data, &m); err == nil {
							t.Annotations[AnnotationName] = m
						}
					}
				}
			}

			// First run the normal generation
			if err := next.Generate(g); err != nil {
				return err
			}

			// Auto-inject @deprecated directives for deprecated fields (like Ent).
			// This runs before schema generation so the directives appear in SDL.
			for _, t := range g.Nodes {
				for _, f := range t.DeprecatedFields() {
					ann := extractGraphQLAnnotation(f.Annotations)
					// Skip if @deprecated already present
					hasDeprecated := false
					for _, d := range ann.GetDirectives() {
						if d.Name == "deprecated" {
							hasDeprecated = true
							break
						}
					}
					if !hasDeprecated {
						ann.Directives = append(ann.Directives, Deprecated(f.DeprecationReason()))
						if f.Annotations == nil {
							f.Annotations = make(map[string]any)
						}
						f.Annotations[AnnotationName] = ann
					}
				}
			}

			// Then generate GraphQL code
			cfg := e.config

			// Default to same directory as ORM (like entgql does)
			if cfg.OutDir == "" && g.Config != nil {
				cfg.OutDir = g.Target
			}
			if cfg.ORMPackage == "" && g.Config != nil {
				cfg.ORMPackage = g.Package
			}
			// Use same package name as ORM if not specified
			if cfg.Package == "graphql" && g.Config != nil {
				// Extract package name from path (e.g., "myapp/velox" -> "velox")
				cfg.Package = packageName(g.Package)
			}

			// Pass schema generator setting
			cfg.SchemaGenerator = e.schemaGenerator

			// Auto-detect gqlgen nullable_input_omittable
			if e.gqlgenConfig != nil && e.gqlgenConfig.NullableInputOmittable {
				cfg.NullableInputOmittable = true
			}

			// Pass schema hooks to config for application during schema file writing
			cfg.schemaHooks = e.schemaHooks
			cfg.graph = g

			// Generate GraphQL schema and Go code
			// Note: gqlgen.yml is NOT modified - users configure it manually
			return Generate(g, cfg)
		})
	}
}

// packageName extracts the package name from an import path.
func packageName(pkg string) string {
	return path.Base(pkg)
}

// extensionAnnotation is a marker annotation that identifies the GraphQL extension
// in the compiler pipeline. The Config is passed separately via the generate hook.
type extensionAnnotation struct{}

func (a *extensionAnnotation) Name() string {
	return "GraphQL"
}

// =============================================================================
// Extension options
// =============================================================================

// WithSchemaPath sets the output path for GraphQL schema files (.graphql).
// The path can be either a directory or a file path:
//   - "schema/" or "schema" -> outputs to schema/schema.graphql
//   - "schema/custom.graphql" -> outputs to schema/custom.graphql
//
// Note: Go files (gql_*.go) always go to the ORM target directory (e.g., velox/),
// only schema files go to this path.
//
// If not set, defaults to the ORM target directory.
func WithSchemaPath(schemaPath string) ExtensionOption {
	return func(e *Extension) error {
		// Check if path looks like a file (has .graphql extension)
		if filepath.Ext(schemaPath) == ".graphql" {
			e.config.SchemaOutDir = filepath.Dir(schemaPath)
			e.config.SchemaFilename = filepath.Base(schemaPath)
		} else {
			e.config.SchemaOutDir = schemaPath
		}
		return nil
	}
}

// WithPackage sets the Go package name for generated code.
// If not set, defaults to the ORM package name.
func WithPackage(pkg string) ExtensionOption {
	return func(e *Extension) error {
		e.config.Package = pkg
		return nil
	}
}

// WithSplitSchema enables/disables splitting schema into multiple files by category.
//
// Deprecated: Use WithSchemaSplitMode instead.
func WithSplitSchema(enabled bool) ExtensionOption {
	return func(e *Extension) error {
		if enabled {
			e.config.SchemaSplitMode = SchemaSplitByCategory
		} else {
			e.config.SchemaSplitMode = SchemaSplitNone
		}
		return nil
	}
}

// WithSchemaSplitMode sets how GraphQL schema files are organized.
//
// Available modes:
//   - SchemaSplitNone: single file (default)
//   - SchemaSplitByCategory: split by type category (types, inputs, connections, scalars)
//   - SchemaSplitPerEntity: one file per entity + shared root file
//
// For SchemaSplitPerEntity, configure gqlgen.yml with:
//
//	schema:
//	  - "./schema/velox*.graphql"
func WithSchemaSplitMode(mode SchemaSplitMode) ExtensionOption {
	return func(e *Extension) error {
		e.config.SchemaSplitMode = mode
		return nil
	}
}

// =============================================================================
// Global default options (typically controlled per-entity via schema annotations)
// =============================================================================
//
// The following options set global defaults. Per-entity behavior is controlled
// via schema annotations:
//
//   - graphql.RelayConnection() - enable Relay connections for an entity
//   - graphql.Mutations(...) - control which mutations are generated
//   - graphql.QueryField() - include entity in Query type
//   - graphql.Skip(...) - skip generation for entity/field
//
// Use these options only to disable features globally.

// WithRelayConnection sets the global default for Relay-style cursor connections.
// Default is true. Per-entity control via graphql.RelayConnection() annotation.
func WithRelayConnection(enabled bool) ExtensionOption {
	return func(e *Extension) error {
		e.config.RelayConnection = enabled
		return nil
	}
}

// WithWhereInputs sets the global default for WhereInput filter generation.
// Default is true. Per-entity control via graphql.Skip(graphql.SkipWhereInput).
func WithWhereInputs(enabled bool) ExtensionOption {
	return func(e *Extension) error {
		e.config.WhereInputs = enabled
		return nil
	}
}

// WithMutations sets the global default for mutation generation.
// Default is true. Per-entity control via graphql.Mutations() annotation.
func WithMutations(enabled bool) ExtensionOption {
	return func(e *Extension) error {
		e.config.Mutations = enabled
		return nil
	}
}

// WithOrdering sets the global default for OrderBy enum generation.
// Default is true. Per-entity control via graphql.Skip(graphql.SkipOrderField).
func WithOrdering(enabled bool) ExtensionOption {
	return func(e *Extension) error {
		e.config.Ordering = enabled
		return nil
	}
}

// WithRelaySpec sets the global default for Relay specification support.
// This includes Node interface, connections, and global IDs.
// Default is true. Per-entity control via graphql.Skip(graphql.SkipType).
//
// This is equivalent to Ent's entgql.WithRelaySpec() option.
func WithRelaySpec(enabled bool) ExtensionOption {
	return func(e *Extension) error {
		e.config.RelaySpec = enabled
		return nil
	}
}

// WithNodeInterface is deprecated: use WithRelaySpec instead.
// This function is kept for backward compatibility.
//
// Deprecated: Use WithRelaySpec for Ent compatibility.
func WithNodeInterface(enabled bool) ExtensionOption {
	return WithRelaySpec(enabled)
}

// =============================================================================
// Optional/advanced options (auto-inferred in most cases)
// =============================================================================

// WithORMPackage sets the ORM package import path for resolver stubs.
// Usually not needed - auto-inferred from graph.Config.Package.
func WithORMPackage(pkg string) ExtensionOption {
	return func(e *Extension) error {
		e.config.ORMPackage = pkg
		return nil
	}
}

// WithConfig sets the full GraphQL configuration.
// Prefer using individual options instead.
func WithConfig(cfg Config) ExtensionOption {
	return func(e *Extension) error {
		e.config = cfg
		return nil
	}
}

// WithConfigPath sets the path to gqlgen.yml configuration file.
// When set, the extension reads the gqlgen config to understand existing
// scalar mappings and model bindings. This is read-only - the extension
// does NOT modify gqlgen.yml. Users must configure gqlgen.yml manually.
//
// Example:
//
//	ex, err := graphql.NewExtension(
//	    graphql.WithConfigPath("./gqlgen.yml"),
//	    graphql.WithSchemaPath("./schema.graphql"),
//	)
func WithConfigPath(configPath string) ExtensionOption {
	return func(e *Extension) error {
		cfg, err := LoadGQLGenConfig(configPath)
		if err != nil {
			return fmt.Errorf("load gqlgen config %q: %w", configPath, err)
		}
		e.gqlgenConfig = cfg
		return nil
	}
}

// WithSchemaGenerator enables automatic GraphQL schema file generation.
// When enabled, the extension generates .graphql schema files in addition to Go code.
// Use WithSchemaPath() to specify where the schema file should be written.
//
// This is equivalent to Ent's entgql.WithSchemaGenerator() option.
//
// Example:
//
//	ex, err := graphql.NewExtension(
//	    graphql.WithSchemaGenerator(),
//	    graphql.WithSchemaPath("./schema.graphql"),
//	    graphql.WithConfigPath("./gqlgen.yml"),
//	)
func WithSchemaGenerator() ExtensionOption {
	return func(e *Extension) error {
		e.schemaGenerator = true
		return nil
	}
}

// WithNodeDescriptor enables NodeDescriptor generation for admin-tool introspection.
// When enabled, each entity gets a Node(ctx) method that returns a structured
// NodeDescriptor containing fields (JSON-serialized) and edge IDs.
//
// This is equivalent to Ent's entgql.WithNodeDescriptor() option.
//
// Example:
//
//	ex, err := graphql.NewExtension(
//	    graphql.WithNodeDescriptor(),
//	)
func WithNodeDescriptor() ExtensionOption {
	return func(e *Extension) error {
		e.config.NodeDescriptor = true
		return nil
	}
}

// WithFederation enables Apollo Federation v2 support.
func WithFederation() ExtensionOption {
	return func(ext *Extension) error {
		ext.config.Federation = true
		return nil
	}
}

// WithMaxFilterDepth sets the maximum nesting depth for WhereInput filters.
// Default is 5 (DefaultMaxFilterDepth). Set to 0 to use the default.
func WithMaxFilterDepth(depth int) ExtensionOption {
	return func(e *Extension) error {
		e.config.MaxFilterDepth = depth
		return nil
	}
}

// WithSchemaHook adds a hook that runs after GraphQL schema generation.
// Multiple hooks can be added and will be executed in order.
// Each hook receives the graph and the typed *ast.Schema so hooks can
// make structural modifications (add/remove types, fields, directives).
// This matches Ent's entgql.WithSchemaHook signature.
//
// Hooks are applied to all schema files including per-entity files
// in SchemaSplitPerEntity mode, so directives can be added to any type.
//
// Example:
//
//	ex, err := graphql.NewExtension(
//	    graphql.WithSchemaHook(func(g *gen.Graph, schema *ast.Schema) error {
//	        // Add a custom directive to the schema
//	        schema.Directives["auth"] = &ast.DirectiveDefinition{
//	            Name:      "auth",
//	            Locations: []ast.DirectiveLocation{ast.LocationFieldDefinition},
//	        }
//	        return nil
//	    }),
//	)
func WithSchemaHook(hooks ...SchemaHook) ExtensionOption {
	return func(e *Extension) error {
		e.schemaHooks = append(e.schemaHooks, hooks...)
		return nil
	}
}

// WithOutputWriter sets a custom function to receive the final *ast.Schema
// instead of writing it to a file. Use this to send the schema to a registry,
// validation service, or custom output.
// This matches Ent's entgql.WithOutputWriter option.
//
// Example:
//
//	ex, err := graphql.NewExtension(
//	    graphql.WithOutputWriter(func(schema *ast.Schema) error {
//	        // Send schema to Apollo registry
//	        return publishToRegistry(schema)
//	    }),
//	)
func WithOutputWriter(w SchemaOutputWriter) ExtensionOption {
	return func(e *Extension) error {
		e.config.outputWriter = w
		return nil
	}
}

// WithGQLGenConfig sets the gqlgen configuration directly.
// Use this when you want to provide a pre-configured GQLGenConfig
// instead of loading from a file. This is read-only - the extension
// does NOT modify gqlgen.yml. Users must configure gqlgen.yml manually.
func WithGQLGenConfig(cfg *GQLGenConfig) ExtensionOption {
	return func(e *Extension) error {
		e.gqlgenConfig = cfg
		return nil
	}
}

// =============================================================================
// Advanced options (Ent compatibility)
// =============================================================================

// WithMapScalarFunc sets a custom function that maps fields to GraphQL scalars.
// If the function returns an empty string, the default scalar mapping is used.
//
// This is equivalent to Ent's entgql.WithMapScalarFunc() option.
//
// Example:
//
//	ex, err := graphql.NewExtension(
//	    graphql.WithMapScalarFunc(func(t *gen.Type, f *gen.Field) string {
//	        // Map all net.IP fields to IPAddress scalar
//	        if f.Type != nil && f.Type.Ident == "net.IP" {
//	            return "IPAddress"
//	        }
//	        return "" // Use default mapping
//	    }),
//	)
func WithMapScalarFunc(fn func(*gen.Type, *gen.Field) string) ExtensionOption {
	return func(e *Extension) error {
		e.config.MapScalarFunc = fn
		return nil
	}
}

// WithTemplates adds custom templates for additional code generation.
// Templates are executed during the code generation phase and can generate
// additional files or modify the GraphQL schema.
//
// This is equivalent to Ent's entgql.WithTemplates() option.
//
// Example:
//
//	ex, err := graphql.NewExtension(
//	    graphql.WithTemplates(
//	        gen.NewTemplate("custom").
//	            Parse("{{ define \"custom\" }}...{{ end }}"),
//	    ),
//	)
func WithTemplates(templates ...*gen.Template) ExtensionOption {
	return func(e *Extension) error {
		e.templates = append(e.templates, templates...)
		return nil
	}
}
