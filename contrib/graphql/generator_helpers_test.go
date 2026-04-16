package graphql

import (
	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// mockGraph creates a test graph with sample types.
// Note: Uses opt-in mutations following Ent-style (graphql.Mutations() required for mutation inputs).
func mockGraph() *entgen.Graph {
	// Default mutation annotation for test entities
	mutationAnnotation := map[string]any{
		"graphql": Annotation{
			Mutations:       mutCreate | mutUpdate,
			HasMutationsSet: true,
		},
	}

	userType := &entgen.Type{
		Name: "User",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "email",
				Type: &field.TypeInfo{Type: field.TypeString},
			},
			{
				Name: "name",
				Type: &field.TypeInfo{Type: field.TypeString},
			},
			{
				Name:     "age",
				Type:     &field.TypeInfo{Type: field.TypeInt},
				Optional: true,
			},
			{
				Name: "created_at",
				Type: &field.TypeInfo{Type: field.TypeTime},
			},
			{
				Name: "status",
				Type: &field.TypeInfo{Type: field.TypeEnum},
				Enums: []entgen.Enum{
					{Name: "Active", Value: "active"},
					{Name: "Inactive", Value: "inactive"},
					{Name: "Pending", Value: "pending"},
				},
			},
		},
		Annotations: mutationAnnotation,
	}

	postType := &entgen.Type{
		Name: "Post",
		ID: &entgen.Field{
			Name: "id",
			Type: &field.TypeInfo{Type: field.TypeInt64},
		},
		Fields: []*entgen.Field{
			{
				Name: "title",
				Type: &field.TypeInfo{Type: field.TypeString},
			},
			{
				Name: "content",
				Type: &field.TypeInfo{Type: field.TypeString},
			},
		},
		Annotations: mutationAnnotation,
	}

	// Set up edges
	userType.Edges = []*entgen.Edge{
		{
			Name:   "posts",
			Type:   postType,
			Unique: false,
		},
	}

	postType.Edges = []*entgen.Edge{
		{
			Name:    "author",
			Type:    userType,
			Unique:  true,
			Inverse: "posts",
		},
	}

	return &entgen.Graph{
		Config: &entgen.Config{
			Package:  "example/ent",
			Target:   "/tmp/test-ent",
			Features: []entgen.Feature{entgen.FeatureWhereInputAll},
		},
		Nodes: []*entgen.Type{userType, postType},
	}
}

func newTestGenerator(types ...*entgen.Type) *Generator {
	return newTestGeneratorWithConfig(Config{
		ORMPackage: "example.com/app/velox",
		Package:    "velox",
	}, types...)
}

// newTestGeneratorWithConfig creates a Generator with a custom Config.
func newTestGeneratorWithConfig(cfg Config, types ...*entgen.Type) *Generator {
	g := &entgen.Graph{Nodes: types, Config: &entgen.Config{
		Package: cfg.ORMPackage,
		Target:  "./velox",
	}}
	return NewGenerator(g, cfg)
}
