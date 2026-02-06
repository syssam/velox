package gen

import (
	"os"
	"path/filepath"
)

var (
	// FeaturePrivacy provides a feature-flag for the privacy extension.
	FeaturePrivacy = Feature{
		Name:        "privacy",
		Stage:       Alpha,
		Default:     false,
		Description: "Privacy provides a privacy layer for ent through the schema configuration",
		cleanup: func(c *Config) error {
			return os.RemoveAll(filepath.Join(c.Target, "privacy"))
		},
	}

	// FeatureIntercept provides a feature-flag for the interceptors' extension.
	FeatureIntercept = Feature{
		Name:        "intercept",
		Stage:       Alpha,
		Default:     false,
		Description: "Intercept generates a helper package to make working with interceptors easier",
		cleanup: func(c *Config) error {
			return os.RemoveAll(filepath.Join(c.Target, "intercept"))
		},
	}

	// FeatureEntQL provides a feature-flag for the EntQL extension.
	FeatureEntQL = Feature{
		Name:        "entql",
		Stage:       Experimental,
		Default:     false,
		Description: "EntQL provides a generic filtering capability at runtime",
		cleanup: func(c *Config) error {
			return os.RemoveAll(filepath.Join(c.Target, "querylanguage.go"))
		},
	}

	// FeatureNamedEdges provides a feature-flag for eager-loading edges with dynamic names.
	FeatureNamedEdges = Feature{
		Name:        "namedges",
		Stage:       Experimental,
		Default:     false,
		Description: "NamedEdges provides an API for eager-loading edges with dynamic names",
	}

	// FeatureBidiEdgeRefs provides a feature-flag for sql dialect to set two-way
	// references when loading (unique) edges. Note, users that use the standard
	// encoding/json.MarshalJSON should detach the circular references before marshaling.
	FeatureBidiEdgeRefs = Feature{
		Name:        "bidiedges",
		Stage:       Experimental,
		Default:     false,
		Description: "This features guides Ent to set two-way references when loading (O2M/O2O) edges",
	}

	// FeatureSnapshot stores a snapshot of ent/schema and auto-solve merge-conflict (issue #852).
	FeatureSnapshot = Feature{
		Name:        "schema/snapshot",
		Stage:       Experimental,
		Default:     false,
		Description: "Schema snapshot stores a snapshot of ent/schema and auto-solve merge-conflict (issue #852)",
		GraphTemplates: []GraphTemplate{
			{
				Name:   "internal/schema",
				Format: "internal/schema.go",
			},
		},
		cleanup: func(c *Config) error {
			return remove(filepath.Join(c.Target, "internal"), "schema.go")
		},
	}

	// FeatureSchemaConfig allows users to pass init time alternate schema names
	// for each ent model. This is useful if your SQL tables are spread out against
	// multiple databases.
	FeatureSchemaConfig = Feature{
		Name:        "sql/schemaconfig",
		Stage:       Stable,
		Default:     false,
		Description: "Allows alternate schema names for each ent model. Useful if SQL tables are spread out against multiple databases",
		GraphTemplates: []GraphTemplate{
			{
				Name:   "dialect/sql/internal/schemaconfig",
				Format: "internal/schemaconfig.go",
			},
		},
		cleanup: func(c *Config) error {
			return remove(filepath.Join(c.Target, "internal"), "schemaconfig.go")
		},
	}

	// featureMultiSchema indicates that ent/schema is annotated with multiple schemas.
	// This feature-flag is enabled by default by the storage driver and exists to pass
	// this info to the templates.
	featureMultiSchema = Feature{
		Name:  "sql/multischema",
		Stage: Beta,
	}

	// FeatureLock provides a feature-flag for sql locking extension.
	FeatureLock = Feature{
		Name:        "sql/lock",
		Stage:       Experimental,
		Default:     false,
		Description: "Allows users to use row-level locking in SQL using the 'FOR {UPDATE|SHARE}' clauses",
	}

	// FeatureModifier provides a feature-flag for adding query modifiers.
	FeatureModifier = Feature{
		Name:        "sql/modifier",
		Stage:       Experimental,
		Default:     false,
		Description: "Allows users to attach custom modifiers to queries",
	}

	// FeatureExecQuery provides a feature-flag for exposing the ExecContext/QueryContext methods of the underlying SQL drivers.
	FeatureExecQuery = Feature{
		Name:        "sql/execquery",
		Stage:       Experimental,
		Default:     false,
		Description: "Allows users to execute statements using the ExecContext/QueryContext methods of the underlying driver",
	}

	// FeatureUpsert provides a feature-flag for adding upsert (ON CONFLICT) capabilities to create builders.
	FeatureUpsert = Feature{
		Name:        "sql/upsert",
		Stage:       Experimental,
		Default:     false,
		Description: "Allows users to configure the `ON CONFLICT`/`ON DUPLICATE KEY` clause for `INSERT` statements",
	}

	FeatureVersionedMigration = Feature{
		Name:        "sql/versioned-migration",
		Stage:       Experimental,
		Default:     false,
		Description: "Allows users to work with versioned migrations / migration files",
	}

	FeatureGlobalID = Feature{
		Name:        "sql/globalid",
		Stage:       Experimental,
		Default:     false,
		Description: "Ensures all nodes have a unique global identifier", GraphTemplates: []GraphTemplate{
			{
				Name:   "internal/globalid",
				Format: "internal/globalid.go",
			},
		},
		cleanup: func(c *Config) error {
			return remove(filepath.Join(c.Target, "internal"), "globalid.go")
		},
	}

	// FeatureValidator enables ORM-level validator code generation.
	// When enabled, built-in validators (NotEmpty, MaxLen, Range, etc.) generate
	// validation code that runs before save operations.
	//
	// Example:
	//
	//	err := entc.Generate("./schema", &gen.Config{
	//	    Features: []gen.Feature{
	//	        gen.FeatureValidator,
	//	    },
	//	})
	//
	// Without this feature, use GraphQL/gRPC annotations for API-layer validation:
	//
	//	field.String("email").Annotations(
	//	    graphql.CreateInputValidate("required,email"),
	//	)
	FeatureValidator = Feature{
		Name:        "validator",
		Stage:       Stable,
		Default:     false,
		Description: "Enables ORM-level validator code generation for built-in validators (NotEmpty, MaxLen, Range, etc.)",
	}

	// FeatureEntPredicates generates Ent-compatible predicate functions.
	// By default, Velox uses generic predicates with a Where struct (compact, ~90% less code).
	// Enable this feature for Ent-style standalone predicate functions.
	//
	// Example usage:
	//
	//	// Default (generic predicates via Where struct):
	//	user.Where.Email.EQ("test@example.com")
	//	user.Where.Age.GT(18)
	//
	//	// With FeatureEntPredicates (Ent-compatible functions):
	//	user.EmailEQ("test@example.com")
	//	user.AgeGT(18)
	//
	// Use this feature when migrating from Ent or when you prefer the function-based API.
	FeatureEntPredicates = Feature{
		Name:        "sql/entpredicates",
		Stage:       Stable,
		Default:     false,
		Description: "Generates Ent-compatible predicate functions (EmailEQ, AgeGT, etc.)",
	}

	// FeatureAutoDefault automatically adds database DEFAULT values for ALL NOT NULL fields
	// (both Required and Optional) that don't have an explicit Default() set. This ensures
	// safe migrations on tables with existing data by using zero values (empty string for
	// strings, 0 for integers, false for booleans, etc.).
	//
	// This follows big tech best practices where DB DEFAULT is for migration safety,
	// while application-layer validation enforces "required" semantics.
	//
	// Without this feature:
	//   field.String("email").Unique()        // DB: NOT NULL, no DEFAULT
	//   field.String("nickname").Optional()   // DB: NOT NULL, no DEFAULT
	//
	// With this feature enabled:
	//   field.String("email").Unique()        // DB: NOT NULL DEFAULT ''
	//   field.String("nickname").Optional()   // DB: NOT NULL DEFAULT ''
	//
	// Note: Enum and JSON fields still require explicit Default() as they have no universal zero value.
	FeatureAutoDefault = Feature{
		Name:        "sql/autodefault",
		Stage:       Alpha,
		Default:     false,
		Description: "Automatically adds database DEFAULT values for all NOT NULL fields using zero values, ensuring safe migrations",
	}

	// AllFeatures holds a list of all feature-flags.
	AllFeatures = []Feature{
		FeaturePrivacy,
		FeatureIntercept,
		FeatureEntQL,
		FeatureNamedEdges,
		FeatureBidiEdgeRefs,
		FeatureSnapshot,
		FeatureSchemaConfig,
		FeatureLock,
		FeatureModifier,
		FeatureExecQuery,
		FeatureUpsert,
		FeatureVersionedMigration,
		FeatureGlobalID,
		FeatureValidator,
		FeatureEntPredicates,
		FeatureAutoDefault,
	}
	// allFeatures includes all public and private features.
	allFeatures = append(AllFeatures, featureMultiSchema)
)

// FeatureStage describes the stage of the codegen feature.
type FeatureStage int

const (
	_ FeatureStage = iota

	// Experimental features are in development, and actively being tested in the
	// integration environmvelox.
	Experimental

	// Alpha features are features whose initial development was finished, tested
	// on the infra of the ent team, but we expect breaking-changes to their APIs.
	Alpha

	// Beta features are Alpha features that were added to the entgo.io
	// documentation, and no breaking-changes are expected for them.
	Beta

	// Stable features are Beta features that were running for a while on ent
	// infra.
	Stable
)

// A Feature of the ent codegen.
type Feature struct {
	// Name of the feature.
	Name string

	// Stage of the feature.
	Stage FeatureStage

	// Default values indicates if this feature is enabled by default.
	Default bool

	// A Description of this feature.
	Description string

	// Templates defines list of templates for extending or overriding the default
	// templates. In order to write the template output to a standalone file, use
	// the GraphTemplates below.
	Templates []*Template

	// GraphTemplates defines optional templates to be executed on the graph
	// and will their output will be written to the configured destination.
	GraphTemplates []GraphTemplate

	// cleanup used to cleanup all changes when a feature-flag is removed.
	// e.g. delete files from previous codegen runs.
	cleanup func(*Config) error
}

// remove file (if exists) and its dir if it's empty.
func remove(dir, file string) error {
	if err := os.Remove(filepath.Join(dir, file)); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	infos, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		return os.Remove(dir)
	}
	return nil
}
