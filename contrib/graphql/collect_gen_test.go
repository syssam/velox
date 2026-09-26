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
	require.Contains(t, code, "gqlrelay.CollectFields(ctx, q, &")
	assert.Contains(t, code, ") CollectMeta() *runtime.CollectMeta {")
	assert.NotContains(t, code, ".FieldColumns,")
}

// TestGenCollectionQueries_ReturnsQuerier pins the concrete CollectFields
// signature to the method markCollectFields adds to entity.XxxQuerier: the
// two must match exactly or the generated query no longer implements its
// interface.
func TestGenCollectionQueries_ReturnsQuerier(t *testing.T) {
	graph := mockGraph()
	g := NewGenerator(graph, Config{ORMPackage: "example.com/app/velox", Package: "velox"})
	code := g.genCollectionQueries(graph.Nodes).GoString()
	for _, n := range graph.Nodes {
		assert.Contains(t, code, ") CollectFields(ctx context.Context, satisfies ...string) (entity."+n.Name+"Querier, error) {")
	}
}

// TestMarkCollectFields pins that the extension marks exactly the types it
// generates CollectFields for — the core adds the method to the Querier
// interface from that mark, so marking a type without the method is a
// compile error and missing one forces resolvers back to a type assertion.
func TestMarkCollectFields(t *testing.T) {
	graph := mockGraph()
	skipped := &gen.Type{Name: "Hidden", ID: graph.Nodes[0].ID, Annotations: map[string]any{AnnotationName: Annotation{Skip: SkipType}}}
	plain := &gen.Type{Name: "Plain", ID: graph.Nodes[0].ID}
	graph.Nodes = append(graph.Nodes, skipped, plain)

	markCollectFields(graph.Nodes)

	collected := map[string]bool{}
	for _, n := range collectionNodes(graph.Nodes) {
		collected[n.Name] = true
	}
	require.True(t, collected["Plain"])
	require.False(t, collected["Hidden"])
	for _, n := range graph.Nodes {
		m, _ := n.Annotations[AnnotationName].(map[string]any)
		marked, _ := m["CollectFields"].(bool)
		assert.Equal(t, collected[n.Name], marked, n.Name)
	}
	// The mark must not disturb the annotation it rides on.
	ann := extractGraphQLAnnotation(skipped.Annotations)
	assert.True(t, ann.IsSkipType())
}

// graphql.Map(...).Loads and .Reads reach the generated CollectMeta: the
// edges under LoadsFor by their collection key, and the columns merged into
// CollectedFor, so the collector loads the edges whole and keeps projecting.
func TestGenEntityCollection_EmitsMapLoadsAndReads(t *testing.T) {
	graph := mockGraph()
	g := NewGenerator(graph, Config{ORMPackage: "example.com/app/velox", Package: "velox"})
	typ := graph.Nodes[0] // User: fields email, name, ...; edge posts
	typ.Annotations = map[string]any{AnnotationName: Annotation{ResolverMappings: []ResolverMapping{
		Map("postCount", "Int!").Loads("posts"),
		Map("greeting", "String!").Reads("name", "email").Loads("posts"),
		Map("plain", "String!"),
	}}}
	require.NoError(t, g.validateResolverMappings(typ))

	code := g.genEntityCollection(typ).GoString()
	require.Contains(t, code, `.LoadsFor = map[string][]string{`)
	assert.Contains(t, code, `"postCount": {"posts"}`)
	assert.Contains(t, code, `"greeting": {"posts"}`)
	assert.Contains(t, code, `"greeting": {FieldName, FieldEmail}`)
	assert.NotContains(t, code, `"plain":`, "a Map declaring nothing stays unknown to the collector")
}

// A Loads or Reads naming something the entity does not have fails the
// build, naming the mapping, rather than generating a load that never
// happens.
func TestValidateResolverMappings_LoadsAndReads(t *testing.T) {
	for name, tc := range map[string]struct {
		rm   ResolverMapping
		want string
	}{
		"unknown edge":  {Map("x", "Int!").Loads("comments"), `Map("x").Loads("comments"): User has no edge "comments"`},
		"unknown field": {Map("x", "Int!").Reads("nickname"), `Map("x").Reads("nickname"): User has no field "nickname"`},
	} {
		t.Run(name, func(t *testing.T) {
			graph := mockGraph()
			g := NewGenerator(graph, Config{ORMPackage: "example.com/app/velox", Package: "velox"})
			typ := graph.Nodes[0]
			typ.Annotations = map[string]any{AnnotationName: Annotation{ResolverMappings: []ResolverMapping{tc.rm}}}
			err := g.validateResolverMappings(typ)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}

	t.Run("edge hidden from the GraphQL type", func(t *testing.T) {
		graph := mockGraph()
		g := NewGenerator(graph, Config{ORMPackage: "example.com/app/velox", Package: "velox"})
		typ := graph.Nodes[0]
		typ.Edges[0].Annotations = map[string]any{AnnotationName: Annotation{Skip: SkipType}}
		typ.Annotations = map[string]any{AnnotationName: Annotation{ResolverMappings: []ResolverMapping{Map("x", "Int!").Loads("posts")}}}
		err := g.validateResolverMappings(typ)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "not on the GraphQL type")
	})
}
