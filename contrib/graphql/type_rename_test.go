package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	entgen "github.com/syssam/velox/compiler/gen"
)

// TestEntityTypeRename_GoNamesStayTheSchemaName pins the split between the
// two names an entity has once graphql.Type() renames it for GraphQL: the
// SDL says "Account", but the Go struct in entity/ is still User, because
// velox's core generator names it after the schema type. Anything velox
// generates itself alongside the SDL — Connection, Edge, PaginateOption,
// the mutation inputs — is named after the GraphQL type and agrees on both
// sides; only a reference to the entity struct must use the Go name.
//
// Before this split the GraphQL name was used for both, so a renamed entity
// produced an entity package referring to a type that does not exist.
func TestEntityTypeRename_GoNamesStayTheSchemaName(t *testing.T) {
	graph := mockGraph()
	var user, post *entgen.Type
	for _, n := range graph.Nodes {
		switch n.Name {
		case "User":
			user = n
		case "Post":
			post = n
		}
	}
	require.NotNil(t, user)
	require.NotNil(t, post)
	user.Annotations = map[string]any{AnnotationName: Annotation{Type: "Account", RelayConnection: true}}

	g := NewGenerator(graph, Config{
		ORMPackage: "example.com/app/velox", Package: "velox",
		RelaySpec: true, RelayConnection: true,
	})

	// SDL uses the GraphQL name.
	assert.Contains(t, g.genEntityType(user), "type Account implements Node")

	// Go references to the entity struct use the schema name. Post has a
	// unique edge to User, so its edge method returns the entity struct.
	edge := g.genEntityEdge(post)
	require.NotNil(t, edge)
	code := edge.GoString()
	assert.Contains(t, code, "*User", "the edge method must return the entity struct")
	assert.NotContains(t, code, "*Account", "there is no Go type named after the GraphQL name")

	// Pagination references the node as the entity struct while naming its
	// own generated types after the GraphQL type.
	if f := g.genModelPaginationTypes([]*entgen.Type{user}); f != nil {
		pag := f.GoString()
		assert.Contains(t, pag, "AccountConnection", "generated types follow the GraphQL name")
		// gofmt aligns struct fields, so match without fixed spacing.
		assert.Regexp(t, `Node\s+\*User`, pag, "the node field is the entity struct")
		assert.NotRegexp(t, `\*Account\b`, pag, "there is no Go type named after the GraphQL name")
		assert.Contains(t, pag, "BuildAccountConnection(nodes []*User", "builders take the entity struct")
	}
}
