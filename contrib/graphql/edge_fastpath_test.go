package graphql

import (
	"testing"

	"github.com/stretchr/testify/assert"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// edgeMethodSource renders entity/gql_edge_user.go for a User with a posts
// connection edge whose target ID has the given type.
func edgeMethodSource(t *testing.T, targetID field.Type) string {
	t.Helper()
	postType := &entgen.Type{
		Name:        "Post",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: targetID}},
		Annotations: map[string]any{AnnotationName: &Annotation{RelayConnection: true}},
	}
	userType := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Annotations: map[string]any{AnnotationName: &Annotation{RelayConnection: true}},
	}
	userType.Edges = []*entgen.Edge{{Name: "posts", Type: postType}}
	g := &entgen.Graph{
		Config: &entgen.Config{Package: "example/ent"},
		Nodes:  []*entgen.Type{userType, postType},
	}
	gen := NewGenerator(g, Config{Package: "graphql", RelaySpec: true, RelayConnection: true, ORMPackage: "example/ent"})
	return gen.genEntityEdge(userType).GoString()
}

// The eager-load fast path must return what Paginate returns. Paginate orders
// by ID, applies orderBy in SQL and counts the whole edge, so the fast path is
// taken only without orderBy, sorts by ID and reports the loaded length.
// It once ignored orderBy, handed `last` pages back reversed and counted only
// the rows on the page (examples/fullgql TestEntityEdgeMethod_FastPathMatchesPaginate).
func TestConnectionEdgeFastPath_MatchesPaginate(t *testing.T) {
	t.Parallel()
	src := edgeMethodSource(t, field.TypeInt64)
	assert.Contains(t, src, "after == nil && before == nil && orderBy == nil",
		"a custom order must go to Paginate:\n%s", src)
	assert.Contains(t, src, "gqlrelay.PageLoaded(nodes, func(a, b *Post) int {\n\t\t\treturn cmp.Compare(a.ID, b.ID)\n\t\t}, first, last)")
	assert.Contains(t, src, "BuildPostConnection(page.Nodes, page.TotalCount, nil, nil, nil, nil, nil)")

	uuidSrc := edgeMethodSource(t, field.TypeUUID)
	assert.Contains(t, uuidSrc, "strings.Compare(a.ID.String(), b.ID.String())")
}

// A string ID's database order depends on the column collation, which Go's
// byte comparison does not reproduce, so such edges always use Paginate.
func TestConnectionEdgeFastPath_NotForStringIDs(t *testing.T) {
	t.Parallel()
	src := edgeMethodSource(t, field.TypeString)
	assert.NotContains(t, src, "PostsOrErr", "string IDs must not take the in-memory fast path:\n%s", src)
	assert.Contains(t, src, "Paginate(ctx, after, first, before, last, opts...)")
}
