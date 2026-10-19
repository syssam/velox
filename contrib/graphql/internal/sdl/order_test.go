package sdl

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/plugin"
)

func TestOrder(t *testing.T) {
	conn := []string{"after: Cursor", "first: Int", "before: Cursor", "last: Int"}
	build := func(userAnn graphql.Annotation) *plugin.Model {
		name := field("name", "string")
		name.Annotations = ann(t, graphql.OrderField("NAME"))
		created := field("created_at", "time")
		created.Annotations = ann(t, graphql.OrderField("CREATED_AT"))
		rank := field("rank", "int")
		rank.Annotations = ann(t, graphql.OrderField("RANK"), graphql.Skip(graphql.SkipOrderField))
		user := entity("User", name, field("email", "string"), created, rank)
		user.Annotations = ann(t, graphql.QueryField("users"), userAnn)
		user.Edges = []*plugin.Edge{edge("posts", "O2M", "Post"), edge("tags", "M2M", "Tag")}
		title := field("title", "string")
		title.Annotations = ann(t, graphql.OrderField("TITLE"))
		post := entity("Post", title)
		post.Annotations = ann(t, graphql.QueryField("posts"), graphql.MultiOrder())
		post.Edges = []*plugin.Edge{edge("author", "M2O", "User")}
		tag := entity("Tag", field("label", "string"))
		tag.Annotations = ann(t, graphql.QueryField("tags"))
		tag.Edges = []*plugin.Edge{edge("users", "M2M", "User")}
		return model(post, tag, user)
	}

	s, sdl := load(t, build(graphql.Annotation{}))
	var values []string
	for _, v := range s.Types["UserOrderField"].EnumValues {
		values = append(values, v.Name)
	}
	// The fields with OrderField, in the model's order; RANK's field skips
	// its order.
	if want := []string{"NAME", "CREATED_AT"}; !slices.Equal(values, want) {
		t.Errorf("UserOrderField: %v, want %v", values, want)
	}
	if got, want := fieldsOf(s, "UserOrder"), []string{"direction: OrderDirection! = ASC", "field: UserOrderField!"}; !slices.Equal(got, want) {
		t.Errorf("UserOrder: %v, want %v", got, want)
	}
	if !strings.Contains(sdl, "input UserOrder {\n") || !strings.Contains(sdl, "  direction: OrderDirection! = ASC\n") {
		t.Errorf("UserOrder:\n%s", sdl)
	}

	for _, c := range []struct {
		typ, name string
		want      []string
	}{
		{"Query", "users", append(slices.Clone(conn), "orderBy: UserOrder")},
		{"Tag", "users", append(slices.Clone(conn), "orderBy: UserOrder")},
		// MultiOrder: a list.
		{"Query", "posts", append(slices.Clone(conn), "orderBy: [PostOrder!]")},
		{"User", "posts", append(slices.Clone(conn), "orderBy: [PostOrder!]")},
		// Tag has no OrderField: no orderBy.
		{"Query", "tags", conn},
		{"User", "tags", conn},
		// A unique edge is not a connection.
		{"Post", "author", []string{}},
	} {
		if got := argsOf(s, c.typ, c.name); !slices.Equal(got, c.want) {
			t.Errorf("%s.%s: %v, want %v", c.typ, c.name, got, c.want)
		}
	}
	if s.Types["TagOrder"] != nil || s.Types["TagOrderField"] != nil {
		t.Errorf("Tag has order types:\n%s", sdl)
	}

	// The entity's SkipOrderField leaves out its order altogether.
	s, sdl = load(t, build(graphql.Skip(graphql.SkipOrderField)))
	if s.Types["UserOrder"] != nil || s.Types["UserOrderField"] != nil || strings.Contains(sdl, "orderBy: UserOrder") {
		t.Errorf("User's order is not skipped:\n%s", sdl)
	}
	if !slices.Equal(argsOf(s, "Query", "users"), conn) {
		t.Errorf("Query.users: %v", argsOf(s, "Query", "users"))
	}

	// Two fields with one order name.
	a, b := field("a", "int"), field("b", "int")
	a.Annotations = ann(t, graphql.OrderField("X"))
	b.Annotations = ann(t, graphql.OrderField("X"))
	renderErr(t, model(entity("User", a, b)), "schema User", "field a", "field b", "X")
	// An order name that is not an enum value.
	a.Annotations = ann(t, graphql.OrderField("by-a"))
	renderErr(t, model(entity("User", a)), "schema User, field a", `"by-a"`)
}
