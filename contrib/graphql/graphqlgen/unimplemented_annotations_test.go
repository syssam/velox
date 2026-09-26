package graphqlgen

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql"
)

// graphql.MapsTo / graphql.Mapping / graphql.Unbind are parsed and stored but
// no generator reads them: an edge annotated with MapsTo("subTasks") used to
// build cleanly and still emit the edge under its original name. They must
// fail the build rather than silently do nothing — the same call made for
// schema-level Interceptors().
func TestGenerate_UnimplementedEdgeAnnotations_ReturnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		ann  graphql.Annotation
		want string
	}{
		{"Mapping", graphql.Mapping("authorPosts", "publishedPosts"), "graphql.Mapping/graphql.MapsTo is not implemented"},
		{"MapsTo", graphql.MapsTo("subTasks"), "graphql.Mapping/graphql.MapsTo is not implemented"},
		{"Unbind", graphql.Unbind(), "graphql.Unbind is not implemented"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := mockGraph()
			giveOwnAnnotations(g)
			g.Nodes[0].Edges[0].Annotations = map[string]any{graphql.AnnotationName: tc.ann}

			gen := NewGenerator(g, Config{
				OutDir:     t.TempDir(),
				Package:    "graphql",
				ORMPackage: "example/ent",
			})
			err := gen.Generate(context.Background())
			require.Error(t, err, "an annotation nothing implements must not build silently")
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "User.posts", "the error must name the offending edge")
		})
	}
}

// The guard must not fire on edges that carry no such annotation, otherwise
// every existing schema breaks.
func TestGenerate_PlainEdges_StillGenerate(t *testing.T) {
	g := mockGraph()
	giveOwnAnnotations(g)
	gen := NewGenerator(g, Config{
		OutDir:     t.TempDir(),
		Package:    "graphql",
		ORMPackage: "example/ent",
	})
	require.NoError(t, gen.Generate(context.Background()))
}

// graphql.QueryField("allUsers").Description(...).Directives(...) collected its
// configuration into Annotation.QueryFieldConfig and nothing read it: the Query
// field was always named camel(pluralize(typeName)) with no description and no
// directives, despite the doc comment promising parity with entgql.QueryField
// (which honors all three — ent-contrib entgql/schema.go).
func TestGenQueryType_HonorsQueryFieldConfig(t *testing.T) {
	t.Run("custom name, description and directives", func(t *testing.T) {
		g := mockGraph()
		giveOwnAnnotations(g)
		ann := graphql.QueryField("allUsers").
			Description("Every user in the system").
			Directives(graphql.Directive{Name: "deprecated", Args: map[string]any{"reason": "use people"}})
		g.Nodes[0].Annotations[graphql.AnnotationName] = ann.Annotation

		sdl := NewGenerator(g, Config{OutDir: t.TempDir(), Package: "graphql", ORMPackage: "example/ent"}).genQueryType()

		assert.Contains(t, sdl, "allUsers", "custom query field name must be used")
		assert.NotContains(t, sdl, "\n  users", "the derived plural name must be replaced")
		assert.Contains(t, sdl, "Every user in the system", "description must reach the SDL")
		assert.Contains(t, sdl, "@deprecated", "directives must reach the SDL")
	})

	t.Run("no config keeps the derived plural name", func(t *testing.T) {
		g := mockGraph()
		giveOwnAnnotations(g)
		sdl := NewGenerator(g, Config{OutDir: t.TempDir(), Package: "graphql", ORMPackage: "example/ent"}).genQueryType()
		assert.Contains(t, sdl, "users", "default naming must be unchanged")
	})
}
