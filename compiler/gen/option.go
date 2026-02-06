package gen

import (
	"errors"
	"maps"

	"github.com/syssam/velox/schema/field"
)

// Option configures code generation.
type Option func(*Config) error

// WithHeader sets the file header comment.
// The header is added at the top of each generated file.
func WithHeader(header string) Option {
	return func(c *Config) error {
		c.Header = header
		return nil
	}
}

// WithIDType sets the default ID field type.
// Supported types: "int", "int64", "uint64", "string", "uuid".
func WithIDType(t string) Option {
	return func(c *Config) error {
		var info *field.TypeInfo
		switch t {
		case "int":
			info = &field.TypeInfo{Type: field.TypeInt}
		case "int64":
			info = &field.TypeInfo{Type: field.TypeInt64}
		case "uint64":
			info = &field.TypeInfo{Type: field.TypeUint64}
		case "string":
			info = &field.TypeInfo{Type: field.TypeString}
		case "uuid":
			info = &field.TypeInfo{Type: field.TypeUUID}
		default:
			return NewConfigError("IDType", t, "unsupported ID type; use int, int64, uint64, string, or uuid")
		}
		c.IDType = info
		return nil
	}
}

// WithIDTypeInfo sets the default ID field type using a TypeInfo struct.
// This allows for more fine-grained control over the ID type configuration.
func WithIDTypeInfo(info *field.TypeInfo) Option {
	return func(c *Config) error {
		if info == nil {
			return NewConfigError("IDType", nil, "TypeInfo cannot be nil")
		}
		c.IDType = info
		return nil
	}
}

// WithPackage sets the output package import path.
// For example: "github.com/org/project/velox".
func WithPackage(pkg string) Option {
	return func(c *Config) error {
		if pkg == "" {
			return NewConfigError("Package", nil, "package cannot be empty")
		}
		c.Package = pkg
		return nil
	}
}

// WithSchema sets the schema package import path.
// For example: "<project>/velox/schema".
func WithSchema(schema string) Option {
	return func(c *Config) error {
		if schema == "" {
			return NewConfigError("Schema", nil, "schema cannot be empty")
		}
		c.Schema = schema
		return nil
	}
}

// WithTarget sets the output directory.
// The directory where generated code will be written.
func WithTarget(dir string) Option {
	return func(c *Config) error {
		if dir == "" {
			return NewConfigError("Target", nil, "target directory cannot be empty")
		}
		c.Target = dir
		return nil
	}
}

// WithFeatures enables specific features.
// Features control optional code generation capabilities.
func WithFeatures(features ...Feature) Option {
	return func(c *Config) error {
		c.Features = append(c.Features, features...)
		return nil
	}
}

// WithStorage sets the storage configuration.
// The storage configuration controls database dialect and schema migration.
func WithStorage(storage *Storage) Option {
	return func(c *Config) error {
		c.Storage = storage
		return nil
	}
}

// WithStorageDriver sets the database driver type by name.
// This is a convenience function that creates a Storage configuration.
// Supported drivers: "sqlite", "mysql", "postgres".
func WithStorageDriver(driver string) Option {
	return func(_ *Config) error {
		switch driver {
		case "sqlite", "mysql", "postgres":
			// Storage initialization happens later during graph building
			return nil
		default:
			return NewConfigError("StorageDriver", driver, "unsupported driver; use sqlite, mysql, or postgres")
		}
	}
}

// WithHooks adds generation hooks.
// Hooks are called before/after code generation.
func WithHooks(hooks ...Hook) Option {
	return func(c *Config) error {
		c.Hooks = append(c.Hooks, hooks...)
		return nil
	}
}

// WithTemplates adds custom templates for code generation.
// Templates allow extending or overriding default code generation.
func WithTemplates(templates ...*Template) Option {
	return func(c *Config) error {
		c.Templates = append(c.Templates, templates...)
		return nil
	}
}

// WithAnnotations sets global annotations.
// Annotations are accessible from all templates.
func WithAnnotations(annotations Annotations) Option {
	return func(c *Config) error {
		if c.Annotations == nil {
			c.Annotations = make(Annotations)
		}
		maps.Copy(c.Annotations, annotations)
		return nil
	}
}

// WithBuildFlags sets custom build flags for loading schema packages.
func WithBuildFlags(flags ...string) Option {
	return func(c *Config) error {
		c.BuildFlags = append(c.BuildFlags, flags...)
		return nil
	}
}

// WithGenerator sets a custom code generator.
// This allows using custom dialects or completely custom code generation.
// If not set, defaults to the SQL dialect generator.
func WithGenerator(g Generator) Option {
	return func(c *Config) error {
		if g == nil {
			return NewConfigError("Generator", nil, "generator cannot be nil")
		}
		c.Generator = g
		return nil
	}
}

// Apply applies options to the config.
// It returns the first error encountered.
func (c *Config) Apply(opts ...Option) error {
	for _, opt := range opts {
		if err := opt(c); err != nil {
			return err
		}
	}
	return nil
}

// ApplyAll applies options and collects all errors.
// Returns a joined error if any options failed.
func (c *Config) ApplyAll(opts ...Option) error {
	var errs []error
	for _, opt := range opts {
		if err := opt(c); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// NewConfig creates a new Config with the given options.
func NewConfig(opts ...Option) (*Config, error) {
	c := &Config{}
	if err := c.Apply(opts...); err != nil {
		return nil, err
	}
	return c, nil
}

// MustNewConfig creates a new Config with the given options.
// It panics if any option fails.
func MustNewConfig(opts ...Option) *Config {
	c, err := NewConfig(opts...)
	if err != nil {
		panic(err)
	}
	return c
}
