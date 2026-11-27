package sdl

import (
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
)

// TestResolversFields: a graphql.Map is a field of the type, bound to a
// resolver, with its arguments, its nullability, and its description. The
// type's key column organization_id has no edge: the application's own type,
// PublicOrganization, answers for it.
func TestResolversFields(t *testing.T) {
	e := entity("Asset", field("name", "string"), field("organization_id", "int64"))
	e.Annotations = ann(t, graphql.Resolvers(
		graphql.Map("organization", "PublicOrganization!").WithComment("Owning organization."),
		graphql.Map("tags(first: Int, search: String)", "[Tag!]!"),
	))
	s, sdl := load(t, model(e), `type PublicOrganization { id: ID! } type Tag { id: ID! }`)

	if got := typeOf(s, "Asset", "organization"); got != "PublicOrganization!" {
		t.Errorf("organization is %q\n%s", got, sdl)
	}
	if got := typeOf(s, "Asset", "tags"); got != "[Tag!]!" {
		t.Errorf("tags is %q", got)
	}
	if got := strings.Join(argsOf(s, "Asset", "tags"), ", "); got != "first: Int, search: String" {
		t.Errorf("tags takes %q", got)
	}
	for _, name := range []string{"organization", "tags"} {
		d := s.Types["Asset"].Fields.ForName(name).Directives.ForName("goField")
		if d == nil || d.Arguments.ForName("forceResolver") == nil || d.Arguments.ForName("forceResolver").Value.Raw != "true" {
			t.Errorf("%s is not bound to a resolver:\n%s", name, sdl)
		}
	}
	if d := s.Types["Asset"].Fields.ForName("organization").Description; d != "Owning organization." {
		t.Errorf("description %q", d)
	}
	// The generated fields are where they were: a resolver field adds, and
	// moves none.
	if got := strings.Join(fieldsOf(s, "Asset"), ", "); !strings.HasPrefix(got, "id: ID!, name: String!, organizationID: Int!, organization:") {
		t.Errorf("fields %q", got)
	}
}

// TestResolversRefused: a mapping that would not be a field of the type, or
// that the plugin cannot stand behind, is refused naming the schema and the
// mapping.
func TestResolversRefused(t *testing.T) {
	for _, c := range []struct {
		name string
		m    graphql.ResolverMapping
		want []string
	}{
		{"name", graphql.Map("1st", "Int"), []string{"schema Asset", `graphql.Map("1st", "Int")`, "not a GraphQL field name"}},
		{"reserved", graphql.Map("__x", "Int"), []string{"not a GraphQL field name"}},
		{"empty", graphql.Map("", "Int"), []string{"not a GraphQL field name"}},
		{"no type", graphql.Map("x", " "), []string{"no type"}},
		{"after the name", graphql.Map("x y", "Int"), []string{"not a GraphQL field name"}},
		{"bad type", graphql.Map("x", "[Int"), []string{"not a field of an SDL type"}},
		{"bad args", graphql.Map("x(first Int)", "Int"), []string{"not a field of an SDL type"}},
		{"second field", graphql.Map("x", "Int y: Int"), []string{"one field and its type"}},
		{"second type", graphql.Map("x", "Int } type Y { y: Int"), []string{"one field and its type"}},
		{"directive", graphql.Map("x", "Int @deprecated"), []string{"one field and its type, without directives"}},
		{"same as a field", graphql.Map("name", "Int"), []string{"are both the GraphQL field Asset.name"}},
		{"same as an id", graphql.Map("id", "Int"), []string{"are both the GraphQL field Asset.id"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := entity("Asset", field("name", "string"))
			e.Annotations = ann(t, graphql.Resolvers(c.m))
			renderErr(t, model(e), c.want...)
		})
	}
}

// TestResolversPlacement: Resolvers is on a schema whose type is shown; a
// field, an edge and a join entity have no type to add to. On a skipped
// schema it adds nothing, silently: a mixin's Resolvers reach every schema
// that uses the mixin, skipped ones too, and none of them asked for it.
func TestResolversPlacement(t *testing.T) {
	r := graphql.Resolvers(graphql.Map("x", "Int"))

	f := field("name", "string")
	f.Annotations = ann(t, r)
	renderErr(t, model(entity("Asset", f)), "graphql.Resolvers does not apply to a field", "applies to a schema")

	skipped := entity("Asset", field("name", "string"))
	skipped.Annotations = ann(t, graphql.Skip(), r)
	if got := warningsOf(t, model(entity("Other", field("n", "string")), skipped), Config{}); len(got) != 0 {
		t.Errorf("skipped: %q, want no warning", got)
	}

	join := entity("Link", field("n", "string"))
	join.Join = true
	join.Annotations = ann(t, r)
	renderErr(t, model(entity("Other", field("n", "string")), join), "graphql.Resolvers on a join entity")
}

// TestResolversMerged: two Resolvers on a schema, as a mixin's and the
// schema's own, make one list; a field both name is the later one's.
func TestResolversMerged(t *testing.T) {
	e := entity("Asset", field("name", "string"))
	e.Annotations = ann(t,
		graphql.Resolvers(graphql.Map("organization", "PublicOrganization"), graphql.Map("owner", "PublicEmployee")),
		graphql.Resolvers(graphql.Map("organization(active: Boolean)", "PublicOrganization!").WithComment("Later.")),
	)
	s, sdl := load(t, model(e), `type PublicOrganization { id: ID! } type PublicEmployee { id: ID! }`)
	if got := typeOf(s, "Asset", "organization"); got != "PublicOrganization!" {
		t.Errorf("organization is %q, want the later mapping's\n%s", got, sdl)
	}
	if got := strings.Join(argsOf(s, "Asset", "organization"), ","); got != "active: Boolean" {
		t.Errorf("organization takes %q", got)
	}
	if got := typeOf(s, "Asset", "owner"); got != "PublicEmployee" {
		t.Errorf("owner is %q", got)
	}
	n := 0
	for _, f := range fieldsOf(s, "Asset") {
		if strings.HasPrefix(f, "organization:") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("organization is %d fields:\n%s", n, sdl)
	}
}
