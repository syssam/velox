package sql

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/schema/field"
)

// bidiFixture builds User.posts (O2M) paired with its unique inverse
// Post.author (M2O), the shape for which eager loading can set a back-reference.
func bidiFixture(features ...string) (*featureMockHelper, *gen.Type) {
	h := newFeatureMockHelper().withFeatures(features...)
	userType := createTestTypeWithFields("User", []*gen.Field{createTestField("name", field.TypeString)})
	postType := createTestTypeWithFields("Post", []*gen.Field{createTestField("title", field.TypeString)})

	posts := createO2MEdge("posts", postType, "posts", "user_posts")
	author := createM2OEdge("author", userType, "posts", "user_posts")
	author.Inverse = "posts"
	posts.Ref, author.Ref = author, posts
	userType.Edges = []*gen.Edge{posts}
	postType.Edges = []*gen.Edge{author}

	h.graph.Nodes = []*gen.Type{userType, postType}
	return h, userType
}

// TestBidiEdgeRefsIsGated pins that the eager-load back-reference
// (`e.Edges.Author = n` while loading User.posts) is emitted only under
// FeatureBidiEdgeRefs, as in Ent (dialect/sql/query.tmpl, "bidiedges").
//
// It used to be emitted unconditionally, so every O2M/O2O eager load built a
// parent→child→parent cycle and json.Marshal of an ordinary
// `Query().WithPosts().All(ctx)` result failed with "encountered a cycle".
// The flag existed, documented exactly this trade-off, and had no reader.
func TestBidiEdgeRefsIsGated(t *testing.T) {
	const backRef = "e.Edges.Author = n"
	cases := []struct {
		name     string
		features []string
		want     bool
	}{
		{"default", nil, false},
		{"namedges only", []string{gen.FeatureNamedEdges.Name}, false},
		{"bidiedges", []string{gen.FeatureBidiEdgeRefs.Name}, true},
		{"bidiedges+namedges", []string{gen.FeatureBidiEdgeRefs.Name, gen.FeatureNamedEdges.Name}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, userType := bidiFixture(tc.features...)
			src := genQueryPkg(h, userType, h.graph.Nodes, h.SharedEntityPkg()).GoString()
			if got := strings.Contains(src, backRef); got != tc.want {
				t.Errorf("back-reference emitted = %v, want %v", got, tc.want)
			}
			// With named edges on, the named-edge loader carries its own copy
			// of the back-reference; it must follow the same flag.
			if tc.want && slices.Contains(tc.features, gen.FeatureNamedEdges.Name) {
				if n := strings.Count(src, backRef); n != 2 {
					t.Errorf("want the back-reference in both the standard and the named loader, got %d", n)
				}
			}
		})
	}
}
