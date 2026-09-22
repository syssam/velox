package graphql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// graphql.MapsTo / graphql.Mapping / graphql.Unbind are parsed and stored but
// no generator reads them: an edge annotated with MapsTo("subTasks") used to
// build cleanly and still emit the edge under its original name. They must
// fail the build rather than silently do nothing — the same call made for
// schema-level Interceptors().
func TestGenerate_UnimplementedEdgeAnnotations_ReturnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		ann  Annotation
		want string
	}{
		{"Mapping", Mapping("authorPosts", "publishedPosts"), "graphql.Mapping/graphql.MapsTo is not implemented"},
		{"MapsTo", MapsTo("subTasks"), "graphql.Mapping/graphql.MapsTo is not implemented"},
		{"Unbind", Unbind(), "graphql.Unbind is not implemented"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := mockGraph()
			giveOwnAnnotations(g)
			g.Nodes[0].Edges[0].Annotations = map[string]any{AnnotationName: tc.ann}

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
