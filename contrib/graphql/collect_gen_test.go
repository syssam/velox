package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGenEntityCollection_GofmtSimplifiedLiterals pins that the generated
// gql_collection.go is gofmt -s canonical: map values in the Edges map are
// bare {...} literals, never the redundant runtime.EdgeMeta{...} form. The
// repo formats with gofmt -s (regen.sh's final pass, the style rule) —
// un-simplified generator output makes the format pass and the next
// regeneration ping-pong the file forever, defeating write-if-changed
// mtime stability.
func TestGenEntityCollection_GofmtSimplifiedLiterals(t *testing.T) {
	graph := mockGraph()
	g := NewGenerator(graph, Config{
		ORMPackage: "example.com/app/velox",
		Package:    "velox",
	})

	for _, typ := range graph.Nodes {
		f := g.genEntityCollection(typ)
		require.NotNil(t, f, "entity %s must produce a collection file", typ.Name)
		code := f.GoString()
		if len(typ.Edges) > 0 {
			require.Contains(t, code, ".Edges = map[string]runtime.EdgeMeta{",
				"fixture for %s must reach the Edges map emission", typ.Name)
			assert.NotContains(t, code, ": runtime.EdgeMeta{",
				"%s: Edges map values must be bare {...} literals (gofmt -s form)", typ.Name)
		}
	}
}
