package graphql

import (
	"fmt"
	"path"
	"path/filepath"

	"github.com/syssam/velox/compiler"
	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema"
)

// SchemaHook is a function that is called after GraphQL schema generation.
// It receives the graph and the generated schema content, and can modify
// or perform additional processing on the schema.
type SchemaHook func(g *gen.Graph, schema string) (string, error)

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
//	    graphql.WithSchemaPath("./velox/ent.graphql"),
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
		&extensionAnnotation{config: e.config},
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
		// which are used by gql_collection.go and gql_edge.go
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
			// First run the normal generation
			if err := next.Generate(g); err != nil {
				return err
			}

			// Then generate GraphQL code
			cfg := e.config

			// Default to same directory as ORM (like entgql does)
			if cfg.OutDir == "" && g.Config != nil {
				cfg.OutDir = g.Config.Target
			}
			if cfg.ORMPackage == "" && g.Config != nil {
				cfg.ORMPackage = g.Config.Package
			}
			// Use same package name as ORM if not specified
			if cfg.Package == "graphql" && g.Config != nil {
				// Extract package name from path (e.g., "myapp/velox" -> "velox")
				cfg.Package = packageName(g.Config.Package)
			}

			// Pass schema generator setting
			cfg.SchemaGenerator = e.schemaGenerator

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

// extensionAnnotation holds the GraphQL extension configuration.
type extensionAnnotation struct {
	config Config
}

func (a *extensionAnnotation) Name() string {
	return "GraphQL"
}

// =============================================================================
// Extension options
// =============================================================================

// WithSchemaPath sets the output path for GraphQL schema files (.graphql).
// The path can be either a directory or a file path:
//   - "schema/" or "schema" -> outputs to schema/schema.graphql
//   - "schema/ent.graphql" -> outputs to schema/ent.graphql
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

// WithSplitSchema enables/disables splitting schema into multiple files.
func WithSplitSchema(enabled bool) ExtensionOption {
	return func(e *Extension) error {
		e.config.SplitSchema = enabled
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
//	    graphql.WithSchemaPath("./ent.graphql"),
//	)
func WithConfigPath(path string) ExtensionOption {
	return func(e *Extension) error {
		cfg, err := LoadGQLGenConfig(path)
		if err != nil {
			return fmt.Errorf("load gqlgen config %q: %w", path, err)
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
//	    graphql.WithSchemaPath("./ent.graphql"),
//	    graphql.WithConfigPath("./gqlgen.yml"),
//	)
func WithSchemaGenerator() ExtensionOption {
	return func(e *Extension) error {
		e.schemaGenerator = true
		return nil
	}
}

// WithSchemaHook adds a hook that runs after GraphQL schema generation.
// Multiple hooks can be added and will be executed in order.
// Each hook receives the graph and schema content, and can modify the schema.
//
// Example:
//
//	ex, err := graphql.NewExtension(
//	    graphql.WithSchemaHook(func(g *gen.Graph, schema string) (string, error) {
//	        // Add custom directives or types
//	        return schema + "\ndirective @auth on FIELD_DEFINITION\n", nil
//	    }),
//	)
func WithSchemaHook(hooks ...SchemaHook) ExtensionOption {
	return func(e *Extension) error {
		e.schemaHooks = append(e.schemaHooks, hooks...)
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
