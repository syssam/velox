// Package compiler provides the Velox code generation API.
//
// # Project Structure
//
// Recommended layout separates schema from generated code:
//
//	myproject/
//	├── schema/           # Your schema definitions
//	│   ├── user.go
//	│   └── post.go
//	├── velox/            # Generated code (don't edit)
//	│   └── ...
//	├── generate.go
//	└── go.mod
//
// # Usage
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
//	        gen.WithTarget("./velox"),  // Generate to velox/ folder
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
package compiler

import (
	"context"
	"errors"
	"fmt"
	"go/token"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/syssam/velox/compiler/gen"
	_ "github.com/syssam/velox/compiler/gen/sql" // Register SQL as default generator via init().
	"github.com/syssam/velox/compiler/internal"
	"github.com/syssam/velox/compiler/internal/reflectutil"
	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"

	"golang.org/x/tools/go/packages"
)

// LoadGraph loads the schema package from the given schema path,
// and constructs a *gen.Graph.
func LoadGraph(schemaPath string, cfg *gen.Config) (*gen.Graph, error) {
	spec, err := (&load.Config{Path: schemaPath, BuildFlags: cfg.BuildFlags}).Load()
	if err != nil {
		return nil, err
	}
	cfg.Schema = spec.PkgPath
	if err := defaultTarget(schemaPath, cfg); err != nil {
		return nil, err
	}
	if cfg.Package == "" {
		// Derive package from module path + relative path from module root to target.
		// For schema at "compiler/integration/privacy/velox/schema" with module "github.com/syssam/velox",
		// the package becomes "github.com/syssam/velox/compiler/integration/privacy/velox".
		if spec.Module != nil && spec.Module.Dir != "" && cfg.Target != "" {
			// Compute absolute path of target
			absTarget, err := filepath.Abs(cfg.Target)
			if err != nil {
				return nil, fmt.Errorf("failed to get absolute path of target: %w", err)
			}
			// Compute relative path from module root to target
			relPath, err := filepath.Rel(spec.Module.Dir, absTarget)
			if err != nil {
				return nil, fmt.Errorf("failed to compute relative path: %w", err)
			}
			// Convert to forward slashes for Go import path
			cfg.Package = path.Join(spec.Module.Path, filepath.ToSlash(relPath))
		} else {
			// Fallback: use parent of schema package path.
			// This path is taken when module info is unavailable (e.g. GOPATH mode),
			// and may produce an incorrect package path if the target directory
			// doesn't match the schema package's parent.
			cfg.Package = path.Dir(spec.PkgPath)
			slog.Warn("package path inferred from schema path (module info unavailable)",
				"package", cfg.Package,
				"hint", "set gen.WithPackage() explicitly if this is incorrect")
		}
	}
	return gen.NewGraph(cfg, spec.Schemas...)
}

// Generate runs the codegen on the schema path. The default target
// directory for the assets, is one directory above the schema path.
// Hence, if the schema package resides in "<project>/velox/schema",
// the base directory for codegen will be "<project>/velox".
//
// If no storage driver provided by option, SQL driver will be used.
//
//	config, _ := gen.NewConfig(
//	    gen.WithHeader("// Custom header"),
//	    gen.WithIDType("int"),
//	)
//	compiler.Generate("./velox/path", config)
func Generate(schemaPath string, cfg *gen.Config, options ...Option) error {
	return GenerateContext(context.Background(), schemaPath, cfg, options...)
}

// GenerateContext is like [Generate] but accepts a context for cancellation.
// The context is propagated to the code generation pipeline, allowing
// long-running generation to be canceled (e.g., via SIGINT in velox watch).
func GenerateContext(ctx context.Context, schemaPath string, cfg *gen.Config, options ...Option) error {
	if err := defaultTarget(schemaPath, cfg); err != nil {
		return err
	}
	for _, opt := range options {
		if err := opt(cfg); err != nil {
			return err
		}
	}
	if cfg.Storage == nil {
		driver, err := gen.NewStorage("sql")
		if err != nil {
			return err
		}
		cfg.Storage = driver
	}
	undo, err := gen.PrepareEnv(cfg)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = undo()
		}
	}()
	return generateCtx(ctx, schemaPath, cfg)
}

func normalizePkg(c *gen.Config) error {
	// Validate that the base package name can be used as a Go identifier.
	// If the last segment contains hyphens, rewrite the import path to use
	// the normalized identifier so generated cross-package references match.
	base := path.Base(c.Package)
	identifier := strings.ReplaceAll(base, "-", "_")
	if !token.IsIdentifier(identifier) {
		return fmt.Errorf("invalid package identifier: %q", base)
	}
	if identifier != base {
		c.Package = path.Join(path.Dir(c.Package), identifier)
	}
	return nil
}

// Option allows for managing codegen configuration using functional options.
type Option func(*gen.Config) error

// Storage sets the storage-driver type to support by the codegen.
func Storage(typ string) Option {
	return func(cfg *gen.Config) error {
		storage, err := gen.NewStorage(typ)
		if err != nil {
			return err
		}
		cfg.Storage = storage
		return nil
	}
}

// FeatureNames enables sets of features by their names.
// Returns an error if any name does not match a known feature.
func FeatureNames(names ...string) Option {
	return func(cfg *gen.Config) error {
		for _, name := range names {
			found := false
			for _, feat := range gen.AllFeatures {
				if name == feat.Name {
					cfg.Features = append(cfg.Features, feat)
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("unknown feature name: %q", name)
			}
		}
		return nil
	}
}

// Annotation is used to attach arbitrary metadata to the schema objects in codegen.
// Unlike schema annotations, being serializable to JSON raw value is not mandatory.
//
// Template extensions can retrieve this metadata and use it inside their execution.
type Annotation = schema.Annotation

// Annotations appends the given annotations to the codegen config.
func Annotations(annotations ...Annotation) Option {
	return func(cfg *gen.Config) error {
		if cfg.Annotations == nil {
			cfg.Annotations = gen.Annotations{}
		}
		for _, ant := range annotations {
			name := ant.Name()
			if curr, ok := cfg.Annotations[name]; !ok {
				cfg.Annotations[name] = ant
			} else if m, ok := curr.(schema.Merger); ok {
				cfg.Annotations[name] = m.Merge(ant)
			} else {
				return fmt.Errorf("duplicate annotations with name %q", name)
			}
		}
		return nil
	}
}

// BuildFlags appends the given build flags to the codegen config.
func BuildFlags(flags ...string) Option {
	return func(cfg *gen.Config) error {
		cfg.BuildFlags = append(cfg.BuildFlags, flags...)
		return nil
	}
}

// BuildTags appends the given build tags as build flags to the codegen
// config.
func BuildTags(tags ...string) Option {
	return BuildFlags("-tags", strings.Join(tags, ","))
}

// TemplateFiles parses the named files and associates the resulting templates
// with codegen templates.
func TemplateFiles(filenames ...string) Option {
	return templateOption(func(t *gen.Template) (*gen.Template, error) {
		return t.ParseFiles(filenames...)
	})
}

// TemplateGlob parses the template definitions from the files identified
// by the pattern and associates the resulting templates with codegen templates.
func TemplateGlob(pattern string) Option {
	return templateOption(func(t *gen.Template) (*gen.Template, error) {
		return t.ParseGlob(pattern)
	})
}

// TemplateDir parses the template definitions from the files in the directory
// and associates the resulting templates with codegen templates.
func TemplateDir(dir string) Option {
	return templateOption(func(t *gen.Template) (*gen.Template, error) {
		return t.ParseDir(dir)
	})
}

// Extension describes a Velox code generation extension that
// allows customizing the code generation and integrating with
// other tools and libraries (e.g. GraphQL, OpenAPI) by
// registering hooks, templates and global annotations in one
// simple call.
//
//	ex, err := graphql.NewExtension(
//		graphql.WithConfigPath("../gqlgen.yml"),
//		graphql.WithSchemaPath("../schema.graphql"),
//	)
//	if err != nil {
//		log.Fatalf("creating graphql extension: %v", err)
//	}
//
//	config, _ := gen.NewConfig()
//
//	err = compiler.Generate("./schema", config, compiler.Extensions(ex))
//	if err != nil {
//		log.Fatalf("running velox codegen: %v", err)
//	}
type Extension interface {
	// Hooks holds an optional list of Hooks to apply
	// on the graph before/after the code-generation.
	Hooks() []gen.Hook

	// Annotations injects global annotations to the gen.Config object that
	// can be accessed globally in all templates. Unlike schema annotations,
	// being serializable to JSON raw value is not mandatory.
	//
	//	{{- with $.Config.Annotations.GQL }}
	//		{{/* Annotation usage goes here. */}}
	//	{{- end }}
	//
	Annotations() []Annotation

	// Templates specifies a list of alternative templates
	// to execute or to override the default.
	Templates() []*gen.Template

	// Options specifies a list of compiler.Options to evaluate on
	// the gen.Config before executing the code generation.
	Options() []Option
}

// Extensions evaluates the list of Extensions on the gen.Config.
func Extensions(extensions ...Extension) Option {
	return func(cfg *gen.Config) error {
		for _, ex := range extensions {
			cfg.Hooks = append(cfg.Hooks, ex.Hooks()...)
			cfg.Templates = append(cfg.Templates, ex.Templates()...)
			for _, opt := range ex.Options() {
				if err := opt(cfg); err != nil {
					return err
				}
			}
			if err := Annotations(ex.Annotations()...)(cfg); err != nil {
				return err
			}
		}
		return nil
	}
}

// DefaultExtension is the default implementation for compiler.Extension.
//
// Embedding this type allows third-party packages to create extensions
// without implementing all methods.
//
//	type Extension struct {
//		compiler.DefaultExtension
//	}
type DefaultExtension struct{}

// Hooks of the extensions.
func (DefaultExtension) Hooks() []gen.Hook { return nil }

// Annotations of the extensions.
func (DefaultExtension) Annotations() []Annotation { return nil }

// Templates of the extensions.
func (DefaultExtension) Templates() []*gen.Template { return nil }

// Options of the extensions.
func (DefaultExtension) Options() []Option { return nil }

var _ Extension = (*DefaultExtension)(nil)

// DependencyOption allows configuring optional dependencies using functional options.
type DependencyOption func(*gen.Dependency) error

// DependencyType sets the type of the struct field in
// the generated builders for the configured dependency.
func DependencyType(v any) DependencyOption {
	return func(d *gen.Dependency) error {
		if v == nil {
			return errors.New("nil dependency type")
		}
		t := reflect.TypeOf(v)
		tv := indirect(t)
		d.Type = &field.TypeInfo{
			Ident:   t.String(),
			PkgPath: tv.PkgPath(),
			RType: &field.RType{
				Kind:    t.Kind(),
				Name:    tv.Name(),
				Ident:   tv.String(),
				PkgPath: tv.PkgPath(),
			},
		}
		return nil
	}
}

// DependencyTypeInfo is similar to DependencyType, but
// allows setting the field.TypeInfo explicitly.
func DependencyTypeInfo(t *field.TypeInfo) DependencyOption {
	return func(d *gen.Dependency) error {
		if t == nil {
			return errors.New("nil dependency type info")
		}
		d.Type = t
		return nil
	}
}

// DependencyName sets the struct field and the option name
// of the dependency in the generated builders.
func DependencyName(name string) DependencyOption {
	return func(d *gen.Dependency) error {
		d.Field = name
		d.Option = name
		return nil
	}
}

// Dependency allows configuring optional dependencies as struct fields on the
// generated builders. For example:
//
//	opts := []compiler.Option{
//		compiler.Dependency(
//			compiler.DependencyType(&http.Client{}),
//		),
//		compiler.Dependency(
//			compiler.DependencyName("DB"),
//			compiler.DependencyType(&sql.DB{}),
//		),
//	}
//
//	config, _ := gen.NewConfig()
//	if err := compiler.Generate("./velox/path", config, opts...); err != nil {
//		log.Fatalf("running velox codegen: %v", err)
//	}
func Dependency(opts ...DependencyOption) Option {
	return func(cfg *gen.Config) error {
		d := &gen.Dependency{}
		for _, opt := range opts {
			if err := opt(d); err != nil {
				return err
			}
		}
		if err := d.Build(); err != nil {
			return err
		}
		return Annotations(gen.Dependencies{d})(cfg)
	}
}

// templateOption ensures the template instantiate
// once for config and execute the given Option.
func templateOption(next func(t *gen.Template) (*gen.Template, error)) Option {
	return func(cfg *gen.Config) (err error) {
		tmpl, err := next(gen.NewTemplate("external"))
		if err != nil {
			return err
		}
		cfg.Templates = append(cfg.Templates, tmpl)
		return nil
	}
}

func generateCtx(ctx context.Context, schemaPath string, cfg *gen.Config) error {
	graph, err := LoadGraph(schemaPath, cfg)
	if err != nil {
		if err = mayRecover(err, schemaPath, cfg); err != nil {
			return err
		}
		if graph, err = LoadGraph(schemaPath, cfg); err != nil {
			return err
		}
	}
	graph.Ctx = ctx
	if err := normalizePkg(cfg); err != nil {
		return err
	}
	// Route through Graph.Gen() which applies hooks consistently,
	// then dispatches to cfg.Generator or the default SQL generator.
	return graph.Gen()
}

func mayRecover(err error, schemaPath string, cfg *gen.Config) error {
	if ok, _ := cfg.FeatureEnabled(gen.FeatureSnapshot.Name); !ok {
		return err
	}
	if _, ok := errors.AsType[*packages.Error](err); !ok && !internal.IsBuildError(err) {
		return err
	}
	// If the build error comes from the schema package.
	if err := internal.CheckDir(schemaPath); err != nil {
		return fmt.Errorf("schema failure: %w", err)
	}
	if ok, _ := cfg.FeatureEnabled(gen.FeatureGlobalID.Name); ok {
		if internal.CheckDir(filepath.Dir(gen.IncrementStartsFilePath(cfg.Target))) != nil {
			// Resolve the conflict by accepting the remote version of the file.
			if err := gen.ResolveIncrementStartsConflict(cfg.Target); err != nil {
				return err
			}
		}
	}
	target := filepath.Join(cfg.Target, "internal/schema.go")
	// Use Graph.Gen() which applies hooks consistently and dispatches
	// to cfg.Generator or the registered default generator.
	genFn := func(graph *gen.Graph) error {
		return graph.Gen()
	}
	return (&internal.Snapshot{Path: target, Config: cfg, Generator: genFn}).Restore()
}

// indirect returns the type at the end of indirection.
var indirect = reflectutil.Indirect

// defaultTarget computes and sets the default target-path for codegen (one level above schema-path).
func defaultTarget(schemaPath string, cfg *gen.Config) error {
	if cfg.Target != "" {
		return validateTarget(cfg.Target)
	}
	abs, err := filepath.Abs(schemaPath)
	if err != nil {
		return err
	}
	// Default target-path for codegen is one dir above the schema.
	cfg.Target = filepath.Dir(abs)
	return validateTarget(cfg.Target)
}

// validateTarget checks that the target directory is not a module root.
// Generated code should always go into a subdirectory (e.g., "./velox") to
// avoid polluting the project root with generated files.
func validateTarget(target string) error {
	abs, err := filepath.Abs(target)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(abs, "go.mod")); err == nil {
		return fmt.Errorf(
			"target directory %q is a Go module root (contains go.mod); "+
				"generated code should go into a subdirectory, e.g. gen.WithTarget(\"./velox\")",
			abs,
		)
	}
	return nil
}
