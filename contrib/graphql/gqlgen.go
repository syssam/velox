// Package graphql provides GraphQL code generation for Velox schemas.
package graphql

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// GQLGenConfig represents a subset of gqlgen.yml configuration.
// This is used to read and update gqlgen configuration for model bindings.
type GQLGenConfig struct {
	// SchemaFilename is the path(s) to the GraphQL schema file(s).
	SchemaFilename StringList `yaml:"schema,omitempty"`

	// Exec configures the generated executor.
	Exec ExecConfig `yaml:"exec,omitempty"`

	// Model configures the generated models.
	Model ModelConfig `yaml:"model,omitempty"`

	// Resolver configures the resolver generation.
	Resolver ResolverConfig `yaml:"resolver,omitempty"`

	// Autobind is a list of packages to autobind types from.
	Autobind []string `yaml:"autobind,omitempty"`

	// Models is a map of GraphQL type name to model configuration.
	Models map[string]TypeMapEntry `yaml:"models,omitempty"`

	// OmitSliceElementPointers removes pointers from slice elements.
	OmitSliceElementPointers bool `yaml:"omit_slice_element_pointers,omitempty"`

	// OmitGetters removes getter methods from models.
	OmitGetters bool `yaml:"omit_getters,omitempty"`

	// OmitComplexity disables complexity calculation.
	OmitComplexity bool `yaml:"omit_complexity,omitempty"`

	// OmitRootModels removes root query/mutation model generation.
	OmitRootModels bool `yaml:"omit_root_models,omitempty"`

	// StructFieldsAlwaysPointers makes struct fields pointers.
	StructFieldsAlwaysPointers bool `yaml:"struct_fields_always_pointers,omitempty"`

	// ReturnPointersInUmarshalInput returns pointers from UnmarshalInput.
	ReturnPointersInUmarshalInput bool `yaml:"return_pointers_in_unmarshalinput,omitempty"`

	// ResolversAlwaysReturnPointers makes resolvers return pointers.
	ResolversAlwaysReturnPointers bool `yaml:"resolvers_always_return_pointers,omitempty"`

	// NullableInputOmittable makes nullable input fields omittable.
	NullableInputOmittable bool `yaml:"nullable_input_omittable,omitempty"`

	// EnableModelJSONOmitemptyTag adds omitempty to model JSON tags.
	EnableModelJSONOmitemptyTag bool `yaml:"enable_model_json_omitempty_tag,omitempty"`
}

// ExecConfig configures the executor generation.
type ExecConfig struct {
	Filename string `yaml:"filename,omitempty"`
	Package  string `yaml:"package,omitempty"`
}

// ModelConfig configures the model generation.
type ModelConfig struct {
	Filename string `yaml:"filename,omitempty"`
	Package  string `yaml:"package,omitempty"`
}

// ResolverConfig configures the resolver generation.
type ResolverConfig struct {
	Filename         string `yaml:"filename,omitempty"`
	Package          string `yaml:"package,omitempty"`
	Layout           string `yaml:"layout,omitempty"`
	DirName          string `yaml:"dir,omitempty"`
	FilenameTemplate string `yaml:"filename_template,omitempty"`
}

// TypeMapEntry is the configuration for a single GraphQL type.
type TypeMapEntry struct {
	// Model is the Go model(s) to bind to this GraphQL type.
	Model StringList `yaml:"model,omitempty"`

	// Fields configures field-level mappings.
	Fields map[string]TypeMapField `yaml:"fields,omitempty"`

	// ExtraFields adds additional fields to the type.
	ExtraFields map[string]TypeMapField `yaml:"extraFields,omitempty"`
}

// TypeMapField is the configuration for a single field.
type TypeMapField struct {
	// Resolver indicates if this field needs a resolver.
	Resolver bool `yaml:"resolver,omitempty"`

	// FieldName is the Go struct field name.
	FieldName string `yaml:"fieldName,omitempty"`
}

// StringList is a YAML type that can be either a string or a list of strings.
type StringList []string

// UnmarshalYAML implements yaml.Unmarshaler for StringList.
func (s *StringList) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		*s = []string{node.Value}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := node.Decode(&list); err != nil {
			return err
		}
		*s = list
		return nil
	default:
		return fmt.Errorf("expected string or list, got %v", node.Kind)
	}
}

// MarshalYAML implements yaml.Marshaler for StringList.
func (s StringList) MarshalYAML() (any, error) {
	if len(s) == 1 {
		return s[0], nil
	}
	return []string(s), nil
}

// LoadGQLGenConfig loads a gqlgen.yml configuration file.
func LoadGQLGenConfig(path string) (*GQLGenConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// Return empty config if file doesn't exist
			return &GQLGenConfig{
				Models: make(map[string]TypeMapEntry),
			}, nil
		}
		return nil, fmt.Errorf("read gqlgen config: %w", err)
	}

	var cfg GQLGenConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse gqlgen config: %w", err)
	}

	if cfg.Models == nil {
		cfg.Models = make(map[string]TypeMapEntry)
	}

	return &cfg, nil
}

// SaveGQLGenConfig saves a gqlgen.yml configuration file.
func SaveGQLGenConfig(path string, cfg *GQLGenConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal gqlgen config: %w", err)
	}

	// Ensure directory exists
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create directory: %w", err)
		}
	}

	return os.WriteFile(path, data, 0o644)
}

// AddSchemaPath adds a schema path to the configuration if not already present.
func (c *GQLGenConfig) AddSchemaPath(path string) {
	if !slices.Contains(c.SchemaFilename, path) {
		c.SchemaFilename = append(c.SchemaFilename, path)
	}
}

// AddAutobind adds a package to the autobind list if not already present.
func (c *GQLGenConfig) AddAutobind(pkg string) {
	if !slices.Contains(c.Autobind, pkg) {
		c.Autobind = append(c.Autobind, pkg)
	}
}

// SetModel sets the model binding for a GraphQL type.
func (c *GQLGenConfig) SetModel(typeName string, modelPath string) {
	entry := c.Models[typeName]
	if !slices.Contains(entry.Model, modelPath) {
		entry.Model = append(entry.Model, modelPath)
	}
	c.Models[typeName] = entry
}

// InjectVeloxBindings adds minimal configuration to gqlgen.yml.
//
// Most type bindings are handled automatically by:
//   - autobind: finds types in the ORM package by matching names
//   - @goModel directives: in the generated GraphQL schema for edge cases
//     (Node→Noder, Cursor, typed JSON scalars, enum types in subpackages)
//
// This function only adds:
//   - Schema path
//   - Autobind package path
//   - ID/UUID → graphql.UUID (external package, no @goModel possible)
//   - JSON → graphql.Map (for generic JSON fields without custom types)
func (c *GQLGenConfig) InjectVeloxBindings(ormPackage string, schemaPath string) {
	if ormPackage == "" {
		return
	}

	// Add schema path
	if schemaPath != "" {
		c.AddSchemaPath(schemaPath)
	}

	// Add autobind for the ORM package
	c.AddAutobind(ormPackage)

	// Bind ID and UUID scalars to gqlgen's built-in UUID type
	c.SetModel("ID", "github.com/99designs/gqlgen/graphql.UUID")
	c.SetModel("UUID", "github.com/99designs/gqlgen/graphql.UUID")

	// Bind generic JSON scalar to graphql.Map (for untyped JSON fields)
	// Typed JSON fields use custom scalars with @goModel directives in the schema
	c.SetModel("JSON", "github.com/99designs/gqlgen/graphql.Map")
}
