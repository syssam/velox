package gen

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/load"
	"github.com/syssam/velox/schema/field"
)

func TestJenniferGenerator(t *testing.T) {
	t.Run("creates generator with graph", func(t *testing.T) {
		target := t.TempDir()
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers[0],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		gen := NewJenniferGenerator(graph, target)
		require.NotNil(t, gen)
	})
}

func TestGeneratorHelper(t *testing.T) {
	t.Run("Graph returns graph", func(t *testing.T) {
		target := t.TempDir()
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers[0],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		gen := NewJenniferGenerator(graph, target)
		assert.Equal(t, graph, gen.Graph())
	})
}

func TestGraphValidation(t *testing.T) {
	t.Run("validates missing edge type", func(t *testing.T) {
		_, err := NewGraph(&Config{
			Package: "test/gen",
			Storage: drivers[0],
		}, &load.Schema{
			Name: "User",
			Edges: []*load.Edge{
				{Name: "posts", Type: "Post"},
			},
		})
		require.Error(t, err)
	})

	t.Run("validates invalid schema name", func(t *testing.T) {
		_, err := NewGraph(&Config{
			Package: "test/gen",
			Storage: drivers[0],
		}, &load.Schema{Name: "Type"}) // Reserved keyword
		require.Error(t, err)
	})
}

func TestGraphTables(t *testing.T) {
	t.Run("returns tables for schema", func(t *testing.T) {
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Storage: drivers[0],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{
			Name: "User",
			Fields: []*load.Field{
				{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
			},
		})
		require.NoError(t, err)

		tables, err := graph.Tables()
		require.NoError(t, err)
		assert.Len(t, tables, 1)
		assert.Equal(t, "users", tables[0].Name)
	})
}

func TestGraphNodes(t *testing.T) {
	t.Run("returns nodes for schema", func(t *testing.T) {
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Storage: drivers[0],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{
			Name: "User",
			Fields: []*load.Field{
				{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
			},
		})
		require.NoError(t, err)

		assert.Len(t, graph.Nodes, 1)
		assert.Equal(t, "User", graph.Nodes[0].Name)
	})
}

func TestGenerateWithFeatures(t *testing.T) {
	schemas := []*load.Schema{
		{
			Name: "User",
			Fields: []*load.Field{
				{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
				{Name: "age", Info: &field.TypeInfo{Type: field.TypeInt}, Optional: true, Nillable: true},
			},
		},
	}

	features := []struct {
		name    string
		feature Feature
	}{
		{"Privacy", FeaturePrivacy},
		{"Intercept", FeatureIntercept},
		{"Snapshot", FeatureSnapshot},
		{"SchemaConfig", FeatureSchemaConfig},
		{"Lock", FeatureLock},
		{"Modifier", FeatureModifier},
		{"Upsert", FeatureUpsert},
		{"ExecQuery", FeatureExecQuery},
	}

	for _, tt := range features {
		t.Run(tt.name, func(t *testing.T) {
			target := t.TempDir()
			graph, err := NewGraph(&Config{
				Package:  "test/gen",
				Target:   target,
				Storage:  drivers[0],
				IDType:   &field.TypeInfo{Type: field.TypeInt},
				Features: []Feature{tt.feature},
			}, schemas...)
			require.NoError(t, err)

			err = graph.Gen()
			require.NoError(t, err)

			// Verify core files exist
			_, err = os.Stat(filepath.Join(target, "velox.go"))
			require.NoError(t, err)
			_, err = os.Stat(filepath.Join(target, "client.go"))
			require.NoError(t, err)
		})
	}
}

func TestGenerateWithHooks(t *testing.T) {
	hookCalled := false
	hook := func(next Generator) Generator {
		return GenerateFunc(func(g *Graph) error {
			hookCalled = true
			return next.Generate(g)
		})
	}

	target := t.TempDir()
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Target:  target,
		Storage: drivers[0],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
		Hooks:   []Hook{hook},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)

	err = graph.Gen()
	require.NoError(t, err)
	assert.True(t, hookCalled)
}

func TestGenerateWithTemplates(t *testing.T) {
	target := t.TempDir()
	tmpl := MustParse(NewTemplate("custom").Parse("// Custom template output\npackage gen"))

	graph, err := NewGraph(&Config{
		Package:   "test/gen",
		Target:    target,
		Storage:   drivers[0],
		IDType:    &field.TypeInfo{Type: field.TypeInt},
		Templates: []*Template{tmpl},
	}, &load.Schema{Name: "User"})
	require.NoError(t, err)

	err = graph.Gen()
	require.NoError(t, err)

	// Verify custom template was generated
	content, err := os.ReadFile(filepath.Join(target, "custom.go"))
	require.NoError(t, err)
	assert.Contains(t, string(content), "Custom template output")
}

func TestGeneratorWriteFile(t *testing.T) {
	t.Run("creates file in target directory", func(t *testing.T) {
		target := t.TempDir()
		graph, err := NewGraph(&Config{
			Package: "test/gen",
			Target:  target,
			Storage: drivers[0],
			IDType:  &field.TypeInfo{Type: field.TypeInt},
		}, &load.Schema{Name: "User"})
		require.NoError(t, err)

		err = graph.Gen()
		require.NoError(t, err)

		// Verify files exist
		entries, err := os.ReadDir(target)
		require.NoError(t, err)
		assert.True(t, len(entries) > 0)
	})
}

func TestTypeNames(t *testing.T) {
	graph, err := NewGraph(&Config{
		Package: "test/gen",
		Storage: drivers[0],
		IDType:  &field.TypeInfo{Type: field.TypeInt},
	}, &load.Schema{
		Name: "User",
		Fields: []*load.Field{
			{Name: "name", Info: &field.TypeInfo{Type: field.TypeString}},
		},
	})
	require.NoError(t, err)
	require.Len(t, graph.Nodes, 1)

	typ := graph.Nodes[0]

	t.Run("QueryName", func(t *testing.T) {
		assert.Equal(t, "UserQuery", typ.QueryName())
	})

	t.Run("MutationName", func(t *testing.T) {
		assert.Equal(t, "UserMutation", typ.MutationName())
	})

	t.Run("CreateName", func(t *testing.T) {
		assert.Equal(t, "UserCreate", typ.CreateName())
	})

	t.Run("UpdateName", func(t *testing.T) {
		assert.Equal(t, "UserUpdate", typ.UpdateName())
	})

	t.Run("UpdateOneName", func(t *testing.T) {
		assert.Equal(t, "UserUpdateOne", typ.UpdateOneName())
	})

	t.Run("DeleteName", func(t *testing.T) {
		assert.Equal(t, "UserDelete", typ.DeleteName())
	})

	t.Run("DeleteOneName", func(t *testing.T) {
		assert.Equal(t, "UserDeleteOne", typ.DeleteOneName())
	})

	t.Run("ClientName", func(t *testing.T) {
		assert.Equal(t, "UserClient", typ.ClientName())
	})
}
