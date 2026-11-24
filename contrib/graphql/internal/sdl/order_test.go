package sdl

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
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
		user.Annotations = ann(t, graphql.QueryField("users"), graphql.RelayConnection(), userAnn)
		user.Edges = []*plugin.Edge{edge("posts", plugin.O2M, "Post"), edge("tags", plugin.M2M, "Tag")}
		title := field("title", "string")
		title.Annotations = ann(t, graphql.OrderField("TITLE"))
		post := entity("Post", title)
		post.Annotations = ann(t, graphql.QueryField("posts"), graphql.RelayConnection(), graphql.MultiOrder())
		post.Edges = []*plugin.Edge{edge("author", plugin.M2O, "User")}
		tag := entity("Tag", field("label", "string"))
		tag.Annotations = ann(t, graphql.QueryField("tags"), graphql.RelayConnection())
		tag.Edges = []*plugin.Edge{edge("users", plugin.M2M, "User")}
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

// graphql.OrderVia orders a connection by a column of a to-one edge's target.
func TestOrderVia(t *testing.T) {
	build := func(edgeAnn []graphql.Annotation, mutate func(user, post *plugin.Entity, edge *plugin.Edge)) *plugin.Model {
		name := field("name", "string")
		user := entity("User", name)
		user.Annotations = ann(t, graphql.QueryField("users"), graphql.RelayConnection())
		title := field("title", "string")
		title.Annotations = ann(t, graphql.OrderField("TITLE"))
		post := entity("Post", title)
		post.Annotations = ann(t, graphql.QueryField("posts"), graphql.RelayConnection())
		author := edge("author", plugin.M2O, "User")
		author.Annotations = ann(t, edgeAnn...)
		post.Edges = []*plugin.Edge{author}
		if mutate != nil {
			mutate(user, post, author)
		}
		return model(post, user)
	}
	via := []graphql.Annotation{graphql.OrderVia("name", "AUTHOR_NAME")}

	s, _ := load(t, build(via, nil))
	var values []string
	for _, v := range s.Types["PostOrderField"].EnumValues {
		values = append(values, v.Name)
	}
	if want := []string{"TITLE", "AUTHOR_NAME"}; !slices.Equal(values, want) {
		t.Errorf("PostOrderField: %v, want %v", values, want)
	}
	// The user has no order of its own, and none comes of the edge.
	if s.Types["UserOrder"] != nil {
		t.Error("User has an order")
	}
	// A type whose only order values come from an edge has an order.
	m := build(via, func(_, post *plugin.Entity, _ *plugin.Edge) { post.Fields[1].Annotations = nil })
	if s, _ = load(t, m); s.Types["PostOrderField"] == nil || len(s.Types["PostOrderField"].EnumValues) != 1 {
		t.Errorf("an order of an edge's value alone: %+v", s.Types["PostOrderField"])
	}
	// A nullable field of the target orders as an OrderField's does.
	m = build(via, func(user, _ *plugin.Entity, _ *plugin.Edge) {
		user.Fields[1].Nullable, user.Fields[1].Pointer = true, true
	})
	if s, _ = load(t, m); s.Types["PostOrderField"] == nil || s.Types["PostOrderField"].EnumValues.ForName("AUTHOR_NAME") == nil {
		t.Errorf("an order of a nullable field of an edge: %+v", s.Types["PostOrderField"])
	}
	surface, err := Build(build(via, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, ty := range surface.Types {
		if ty.Name != "Post" {
			continue
		}
		v := ty.Order[1]
		if v.Value != "AUTHOR_NAME" || v.Edge != 1 || v.Target == nil || v.Target.Name != "User" || v.Target.Fields[v.Field].Name != "name" {
			t.Errorf("the surface's value: %+v", v)
		}
	}

	for _, c := range []struct {
		name   string
		edge   []graphql.Annotation
		mutate func(user, post *plugin.Entity, edge *plugin.Edge)
		want   []string
	}{
		{"a to-many edge", via, func(_, _ *plugin.Entity, e *plugin.Edge) { e.Unique, e.Kind = false, plugin.O2M }, []string{"schema Post, edge author", "to-many"}},
		{"a field the target lacks", []graphql.Annotation{graphql.OrderVia("nope", "X")}, nil, []string{"schema Post, edge author", "User has no field nope"}},
		{"an enum value that is not one", []graphql.Annotation{graphql.OrderVia("name", "by-name")}, nil, []string{`"by-name"`}},
		{"a value that clashes with an own value", []graphql.Annotation{graphql.OrderVia("name", "TITLE")}, nil, []string{"both order by", `"TITLE"`}},
		{"a sensitive field", via, func(user, _ *plugin.Entity, _ *plugin.Edge) { user.Fields[1].Sensitive = true }, []string{"sensitive"}},
		{"a json field", via, func(user, _ *plugin.Entity, _ *plugin.Edge) { user.Fields[1].Type = "json" }, []string{"json"}},
		{"a field with an application directive", via, func(user, _ *plugin.Entity, _ *plugin.Edge) {
			user.Fields[1].Annotations = ann(t, graphql.Directives(graphql.Directive{Name: "auth"}))
		}, []string{"@auth", "OrderUnguarded"}},
		{"a field the type leaves out", via, func(user, _ *plugin.Entity, _ *plugin.Edge) {
			user.Fields[1].Annotations = ann(t, graphql.Skip())
		}, []string{"leaves out"}},
		{"an edge with an application directive", append(slices.Clone(via), graphql.Directives(graphql.Directive{Name: "auth"})), nil, []string{"edge author", "@auth"}},
		{"an edge the type leaves out", append(slices.Clone(via), graphql.Skip()), nil, []string{"edge author", "leaves out"}},
		{"an edge to a type that is skipped", via, func(user, _ *plugin.Entity, _ *plugin.Edge) { user.Annotations = ann(t, graphql.Skip()) }, []string{"edge author", "no GraphQL type"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			renderErr(t, build(c.edge, c.mutate), c.want...)
		})
	}

	// The entity's SkipOrderField leaves out the values of its edges too.
	m = build(via, func(_, post *plugin.Entity, _ *plugin.Edge) {
		post.Annotations = ann(t, graphql.QueryField("posts"), graphql.RelayConnection(), graphql.Skip(graphql.SkipOrderField))
	})
	if s, _ = load(t, m); s.Types["PostOrder"] != nil {
		t.Error("Post's order is not skipped")
	}

	// OrderVia is for edges.
	f := field("name", "string")
	f.Annotations = ann(t, graphql.OrderVia("name", "X"))
	renderErr(t, model(entity("User", f)), "graphql.OrderVia", "an edge")
}

// graphql.OrderField on a to-many edge orders a connection by the number of
// the edge's nodes, as entgql's POSTS_COUNT.
func TestOrderCount(t *testing.T) {
	build := func(edgeAnn []graphql.Annotation, mutate func(user, post *plugin.Entity, edge *plugin.Edge)) *plugin.Model {
		name := field("name", "string")
		name.Annotations = ann(t, graphql.OrderField("NAME"))
		user := entity("User", name)
		user.Annotations = ann(t, graphql.QueryField("users"), graphql.RelayConnection())
		post := entity("Post", field("title", "string"))
		post.Annotations = ann(t, graphql.QueryField("posts"), graphql.RelayConnection())
		posts := edge("posts", plugin.O2M, "Post")
		posts.Annotations = ann(t, edgeAnn...)
		user.Edges = []*plugin.Edge{posts}
		if mutate != nil {
			mutate(user, post, posts)
		}
		return model(post, user)
	}
	count := []graphql.Annotation{graphql.OrderField("POSTS_COUNT")}
	s, text := load(t, build(count, nil))
	var values []string
	for _, v := range s.Types["UserOrderField"].EnumValues {
		values = append(values, v.Name)
	}
	if want := []string{"NAME", "POSTS_COUNT"}; !slices.Equal(values, want) {
		t.Errorf("UserOrderField: %v, want %v", values, want)
	}
	if d := s.Types["UserOrderField"].EnumValues.ForName("POSTS_COUNT").Description; !strings.HasPrefix(d, CountOrderDoc+"posts of each User") {
		t.Errorf("POSTS_COUNT's description: %q\n%s", d, text)
	}
	surface, err := Build(build(count, nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, ty := range surface.Types {
		if ty.Name == "User" {
			if v := ty.Order[1]; v.Value != "POSTS_COUNT" || !v.Count || v.Edge != 1 || v.Field != -1 || v.Target == nil || v.Target.Name != "Post" {
				t.Errorf("the surface's value: %+v", v)
			}
		}
	}
	// A many-to-many edge counts too, and a type whose only value is a count
	// has an order.
	m := build(count, func(user, _ *plugin.Entity, e *plugin.Edge) { e.Kind, user.Fields[1].Annotations = plugin.M2M, nil })
	if s, _ = load(t, m); s.Types["UserOrderField"] == nil || len(s.Types["UserOrderField"].EnumValues) != 1 {
		t.Errorf("a count alone, many-to-many: %+v", s.Types["UserOrderField"])
	}

	for _, c := range []struct {
		name   string
		edge   []graphql.Annotation
		mutate func(user, post *plugin.Entity, edge *plugin.Edge)
		want   []string
	}{
		{"a to-one edge", count, func(_, _ *plugin.Entity, e *plugin.Edge) { e.Unique, e.Kind = true, plugin.O2O }, []string{"schema User, edge posts", "to-one", "OrderVia"}},
		{"an enum value that is not one", []graphql.Annotation{graphql.OrderField("by-count")}, nil, []string{`"by-count"`}},
		{"a value that clashes with an own value", []graphql.Annotation{graphql.OrderField("NAME")}, nil, []string{"both order by", `"NAME"`}},
		{"an edge with an application directive", append(slices.Clone(count), graphql.Directives(graphql.Directive{Name: "auth"})), nil, []string{"edge posts", "@auth"}},
		{"an edge the type leaves out", append(slices.Clone(count), graphql.Skip()), nil, []string{"edge posts", "leaves out"}},
		{"an edge to a type that is skipped", count, func(_, post *plugin.Entity, _ *plugin.Edge) { post.Annotations = ann(t, graphql.Skip()) }, []string{"edge posts", "no GraphQL type"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			renderErr(t, build(c.edge, c.mutate), c.want...)
		})
	}
}
