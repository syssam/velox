package gogen

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// TestCountOrderConstant: an OrderField on a to-many edge is a value of the
// owner's OrderField enum, a constant named for the edge's count, and a
// Column of the type marked Count (velox decision 0112).
func TestCountOrderConstant(t *testing.T) {
	enc := func(as ...graphql.Annotation) map[string]json.RawMessage {
		var a graphql.Annotation
		for _, o := range as {
			a = a.Merge(o).(graphql.Annotation)
		}
		b, err := json.Marshal(a)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]json.RawMessage{"graphql": b}
	}
	id := func() *plugin.Field {
		return &plugin.Field{Name: "id", GoName: "ID", Column: "id", Type: "int64", GoType: "int64", ElemType: "int64", AutoID: true}
	}
	posts := &plugin.Edge{Name: "posts", GoName: "Posts", Kind: plugin.O2M, Target: "Post",
		Annotations: enc(graphql.OrderField("POSTS_COUNT"))}
	user := &plugin.Entity{Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
		Fields: []*plugin.Field{id()}, Edges: []*plugin.Edge{posts},
		Annotations: enc(graphql.QueryField("users"), graphql.RelayConnection())}
	post := &plugin.Entity{Name: "Post", Label: "post", Handles: "Posts", PrimaryKey: []int{0},
		Fields: []*plugin.Field{id()}, Edges: []*plugin.Edge{},
		Annotations: enc(graphql.QueryField("posts"))}
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{post, user}}
	got := strings.Join(strings.Fields(render(t, m)["user_gql.go"]), " ")
	for _, want := range []string{
		`UserOrderFieldPostsCount UserOrderField = "POSTS_COUNT"`,
		"orders by the number of nodes of the edge posts",
		"Count: true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("no %q in:\n%s", want, got)
		}
	}
}
