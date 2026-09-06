package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
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

// TestGenEntityCollection_EmitsCollectedFor pins that graphql.CollectedFor
// annotations reach the generated CollectMeta: every annotated field is
// listed under each name it names, in declaration order, and a field hidden
// from the GraphQL type (Skip(SkipType)) is still collected — hiding a
// column while feeding it to a resolver is the annotation's purpose.
func TestGenEntityCollection_EmitsCollectedFor(t *testing.T) {
	graph := mockGraph()
	g := NewGenerator(graph, Config{
		ORMPackage: "example.com/app/velox",
		Package:    "velox",
	})
	typ := graph.Nodes[0]
	ann := func(a Annotation) map[string]any { return map[string]any{AnnotationName: a} }
	typ.Fields = append(typ.Fields,
		&gen.Field{Name: "first_name", Type: &field.TypeInfo{Type: field.TypeString},
			Annotations: ann(Annotation{CollectedFor: []string{"fullName", "initials"}})},
		&gen.Field{Name: "last_name", Type: &field.TypeInfo{Type: field.TypeString},
			Annotations: ann(Annotation{CollectedFor: []string{"fullName"}, Skip: SkipType})},
	)

	code := g.genEntityCollection(typ).GoString()
	require.Contains(t, code, `.CollectedFor = map[string][]string{`)
	assert.Contains(t, code, `"fullName": {FieldFirstName, FieldLastName}`)
	assert.Contains(t, code, `"initials": {FieldFirstName}`)
	assert.NotContains(t, code, `"lastName":`, "a SkipType field must not appear in FieldColumns")
}

// TestGenCollectionQueries_PassesWholeCollectMeta pins the generated call
// site: CollectFields on each query must pass the entity's whole CollectMeta
// by pointer — passing the two maps separately is what used to drop
// CollectedFor on the floor.
func TestGenCollectionQueries_PassesWholeCollectMeta(t *testing.T) {
	graph := mockGraph()
	g := NewGenerator(graph, Config{ORMPackage: "example.com/app/velox", Package: "velox"})
	code := g.genCollectionQueries(graph.Nodes).GoString()
	require.Contains(t, code, "runtime.CollectFields(ctx, q, &")
	assert.NotContains(t, code, ".FieldColumns,")
}
