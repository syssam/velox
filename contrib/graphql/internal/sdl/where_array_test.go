package sdl

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// head is what a where input starts with: the logic and the node key's predicates.
func head() []string { return append(logic("UserWhereInput"), nodeKey()...) }

// array is a JSON field of the Go slice type goType.
func array(name, goType string) *plugin.Field {
	f := field(name, "json")
	f.ElemType = goType
	return f
}

// A JSON slice of strings, numbers or booleans offers has, hasSome, hasEvery
// and isEmpty on its elements, whose GraphQL scalar the slice's list is of,
// when WhereOps asks for OpsJSONArray, or for the defaults, which are the same.
func TestWhereJSONArray(t *testing.T) {
	for _, c := range []struct {
		goType, scalar string
	}{{"[]string", "String"}, {"[]int", "Int"}, {"[]int64", "Int"}, {"[]float64", "Float"}, {"[]bool", "Boolean"}} {
		want := []string{"tagsHas: " + c.scalar, "tagsHasSome: [" + c.scalar + "!]", "tagsHasEvery: [" + c.scalar + "!]", "tagsIsEmpty: Boolean"}
		for name, ops := range map[string]graphql.Annotation{
			"OpsJSONArray": graphql.WhereOps(graphql.OpsJSONArray),
			"defaults":     graphql.WhereOps(),
			"OpsAll":       graphql.WhereOps(graphql.OpsAll),
		} {
			f := array("tags", c.goType)
			f.Annotations = ann(t, ops)
			s, sdl := load(t, model(entity("User", f)))
			got := fieldsOf(s, "UserWhereInput")
			if got = got[len(head()):]; !slices.Equal(got, want) {
				t.Errorf("%s with %s:\n got %v\nwant %v\n%s", c.goType, name, got, want, sdl)
			}
		}
	}

	// One predicate alone, and a nullable (Nillable) field's IsNil and NotNil.
	f := array("tags", "[]string")
	f.Annotations = ann(t, graphql.WhereOps(graphql.OpHas))
	if s, _ := load(t, model(entity("User", f))); !slices.Equal(fieldsOf(s, "UserWhereInput")[len(head()):], []string{"tagsHas: String"}) {
		t.Errorf("OpHas alone: %v", fieldsOf(s, "UserWhereInput"))
	}
	// As for a scalar, the defaults of a nullable field include IsNil and NotNil,
	// and explicit ops do not unless OpsNullable is among them.
	withNull := []string{"tagsHas: String", "tagsHasSome: [String!]", "tagsHasEvery: [String!]", "tagsIsEmpty: Boolean", "tagsIsNil: Boolean", "tagsNotNil: Boolean"}
	for name, c := range map[string]struct {
		ops  graphql.Annotation
		want []string
	}{
		"defaults":                  {graphql.WhereOps(), withNull},
		"OpsJSONArray and nullable": {graphql.WhereOps(graphql.OpsJSONArray, graphql.OpsNullable), withNull},
		"OpsJSONArray alone":        {graphql.WhereOps(graphql.OpsJSONArray), withNull[:4]},
	} {
		f = array("tags", "[]string")
		f.Nullable, f.Pointer = true, true
		f.Annotations = ann(t, c.ops)
		if s, _ := load(t, model(entity("User", f))); !slices.Equal(fieldsOf(s, "UserWhereInput")[len(head()):], c.want) {
			t.Errorf("nullable, %s: %v", name, fieldsOf(s, "UserWhereInput"))
		}
	}
}

// A predicate that a JSON array does not have, and a JSON field that is no
// slice of a scalar, are refused at the field.
func TestWhereJSONArrayRefusals(t *testing.T) {
	for _, c := range []struct {
		name string
		f    *plugin.Field
		ops  graphql.Annotation
		want string
	}{
		{"a scalar's predicate", array("tags", "[]string"), graphql.WhereOps(graphql.OpEQ), "not a predicate of"},
		{"a map", array("attrs", "map[string]any"), graphql.WhereOps(graphql.OpsJSONArray), "has no predicates"},
		{"a slice of structs", array("items", "[]Item"), graphql.WhereOps(graphql.OpsJSONArray), "has no predicates"},
		{"array predicates on a string", field("s", "string"), graphql.WhereOps(graphql.OpHas), "not a predicate of"},
		{"array predicates on an int", field("n", "int"), graphql.WhereOps(graphql.OpIsEmpty), "not a predicate of"},
	} {
		c.f.Annotations = ann(t, c.ops)
		if _, err := Render(model(entity("User", c.f))); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}
