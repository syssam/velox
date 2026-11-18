package sdl

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// The old velox's spellings of an opt-in filter give the where input that the
// velox2 ones do: WhereInput() and EnableWhereInputs(true) on a field are
// WhereOps(), on an edge WhereEdge(), and WhereInputEdges names edges.
func TestOldWhereInputSpellings(t *testing.T) {
	build := func(t *testing.T, name, edgeAnn, schemaAnn []graphql.Annotation) []string {
		t.Helper()
		n := field("name", "string")
		n.Annotations = ann(t, name...)
		posts := edge("posts", plugin.O2M, "Post")
		posts.Annotations = ann(t, edgeAnn...)
		user := entity("User", n)
		user.Edges = []*plugin.Edge{posts}
		user.Annotations = ann(t, append([]graphql.Annotation{graphql.QueryField("users")}, schemaAnn...)...)
		title := field("title", "string")
		title.Annotations = ann(t, graphql.WhereOps())
		s, sdl := load(t, model(entity("Post", title), user))
		if s.Types["UserWhereInput"] == nil {
			t.Fatalf("no UserWhereInput\n%s", sdl)
		}
		return fieldsOf(s, "UserWhereInput")
	}
	want := build(t, []graphql.Annotation{graphql.WhereOps()}, []graphql.Annotation{graphql.WhereEdge()}, nil)
	for name, c := range map[string]struct{ field, edge, schema []graphql.Annotation }{
		"WhereInput":        {[]graphql.Annotation{graphql.WhereInput()}, []graphql.Annotation{graphql.WhereInput()}, nil},
		"EnableWhereInputs": {[]graphql.Annotation{graphql.EnableWhereInputs(true)}, []graphql.Annotation{graphql.EnableWhereInputs(true)}, nil},
		"WhereInputEdges":   {[]graphql.Annotation{graphql.WhereInput()}, nil, []graphql.Annotation{graphql.WhereInputEdges("posts")}},
	} {
		if got := build(t, c.field, c.edge, c.schema); !slices.Equal(got, want) {
			t.Errorf("%s: got %v\nwant %v", name, got, want)
		}
	}
	if !slices.Contains(want, "hasPostsWith: [PostWhereInput!]") || !slices.Contains(want, "name: String") {
		t.Errorf("the reference where input lacks the filters: %v", want)
	}
}

// EnableWhereInputs(false) is Skip(SkipWhereInput), so it wins over WhereOps.
func TestEnableWhereInputsFalse(t *testing.T) {
	n := field("name", "string")
	n.Annotations = ann(t, graphql.WhereOps(), graphql.EnableWhereInputs(false))
	m := field("mail", "string")
	m.Annotations = ann(t, graphql.EnableWhereInputs(true))
	s, _ := load(t, model(entity("User", n, m)))
	got := fieldsOf(s, "UserWhereInput")
	if slices.ContainsFunc(got, func(f string) bool { return strings.HasPrefix(f, "name") }) {
		t.Errorf("name is filterable: %v", got)
	}
	if !slices.Contains(got, "mail: String") {
		t.Errorf("mail is not filterable: %v", got)
	}
}

// Each spelling is refused where it does not apply, or names no edge.
func TestOldWhereInputRefusals(t *testing.T) {
	for _, c := range []struct {
		name, want string
		build      func() *plugin.Model
	}{
		{"WhereInput on a schema", "graphql.WhereInput does not apply to a schema", func() *plugin.Model {
			u := entity("User")
			u.Annotations = ann(t, graphql.WhereInput())
			return model(u)
		}},
		{"WhereInputEdges on a field", "graphql.WhereInputEdges does not apply to a field", func() *plugin.Model {
			f := field("name", "string")
			f.Annotations = ann(t, graphql.WhereInputEdges("posts"))
			return model(entity("User", f))
		}},
		{"WhereInputEdges names no edge", `WhereInputEdges("nope"): the schema has no such edge`, func() *plugin.Model {
			u := entity("User")
			u.Annotations = ann(t, graphql.WhereInputEdges("nope"))
			return model(u)
		}},
	} {
		_, err := Render(c.build())
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}
