package graphqlgen

import (
	"regexp"
	"slices"
	"testing"

	entgen "github.com/syssam/velox/compiler/gen"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema/field"
)

// WhereFields and OrderFields say what each field of a where input and each
// value of an order enum stands for. A generator of its own Go builds on
// them instead of reading that back out of the schema's names, which only
// works while they list exactly what the schema lists, in its order.
func TestWhereFieldsAndOrderFieldsAreTheSchema(t *testing.T) {
	filterable := map[string]any{graphql.AnnotationName: &graphql.Annotation{WhereInputEnabled: true}}
	str := func(name string, ann map[string]any) *entgen.Field {
		return &entgen.Field{Name: name, Type: &field.TypeInfo{Type: field.TypeString}, Annotations: ann}
	}
	post := &entgen.Type{
		Name:        "Post",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{str("title", filterable)},
		Annotations: map[string]any{},
	}
	user := &entgen.Type{
		Name: "User",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			// Names that read two ways: "logIn" is log with In, or login;
			// "nameNotIn" is name with NotIn, or name_not with In;
			// "hasPosts" is the edge, or the field below.
			str("log", filterable), str("login", nil),
			str("name", nil), str("name_not", filterable),
			str("title", filterable), str("title_contains", nil),
			{Name: "has_posts", Type: &field.TypeInfo{Type: field.TypeBool}},
			{Name: "age", Type: &field.TypeInfo{Type: field.TypeInt}, Optional: true, Nillable: true, Annotations: filterable},
			{Name: "active", Type: &field.TypeInfo{Type: field.TypeBool}, Annotations: filterable},
			{Name: "created_at", Type: &field.TypeInfo{Type: field.TypeTime}, Annotations: filterable},
		},
		Annotations: map[string]any{},
	}
	user.Edges = []*entgen.Edge{{Name: "posts", Type: post, Annotations: filterable}}
	g := NewGenerator(&entgen.Graph{Config: &entgen.Config{Package: "example/ent"}, Nodes: []*entgen.Type{user, post}},
		Config{Package: "graphql", ORMPackage: "example/ent", WhereInputs: true, Ordering: true})

	fieldRx := regexp.MustCompile(`(?m)^  ([A-Za-z0-9_]+):`)
	for _, typ := range []*entgen.Type{user, post} {
		var inSchema []string
		for _, m := range fieldRx.FindAllStringSubmatch(g.genWhereInput(typ), -1) {
			inSchema = append(inSchema, m[1])
		}
		if len(inSchema) < 4 || !slices.Equal(inSchema[:3], []string{"not", "and", "or"}) {
			t.Fatalf("%s: the where input starts with %v", typ.Name, inSchema)
		}
		var described []string
		for _, w := range g.WhereFields(typ) {
			described = append(described, w.Name)
			if (w.Field == nil) == (w.Edge == nil) {
				t.Errorf("%s.%s: a filter is on a field or on an edge: %+v", typ.Name, w.Name, w)
			}
		}
		if !slices.Equal(inSchema[3:], described) {
			t.Errorf("%s: the schema lists\n  %v\nand WhereFields\n  %v", typ.Name, inSchema[3:], described)
		}
	}

	// The names that read two ways are described as what they are.
	by := map[string]WhereField{}
	for _, w := range g.WhereFields(user) {
		by[w.Name] = w
	}
	for name, want := range map[string]struct{ field, op string }{
		"logIn": {"log", "In"}, "nameNotIn": {"name_not", "In"}, "titleContains": {"title", "Contains"},
		"nameNot": {"name_not", "EQ"}, "ageIsNil": {"age", "IsNil"},
	} {
		w, ok := by[name]
		if !ok || w.Field == nil || w.Field.Name != want.field || w.Op != want.op {
			t.Errorf("%s: got %+v, want field %s with %s", name, w, want.field, want.op)
		}
	}
	if w := by["hasPosts"]; w.Edge == nil || w.Edge.Name != "posts" || w.With {
		t.Errorf("hasPosts: %+v", w)
	}
	if w := by["hasPostsWith"]; w.Edge == nil || !w.With {
		t.Errorf("hasPostsWith: %+v", w)
	}

	valueRx := regexp.MustCompile(`(?m)^  ([A-Z][A-Z0-9_]*)$`)
	var values, described []string
	orderBy := g.genOrderBy(user)
	for _, m := range valueRx.FindAllStringSubmatch(orderBy[regexpIndex(orderBy, `enum UserOrderField`):], -1) {
		values = append(values, m[1])
	}
	for _, o := range g.OrderFields(user) {
		described = append(described, o.Name)
	}
	if len(values) == 0 || !slices.Equal(values, described) {
		t.Errorf("the order enum lists %v and OrderFields %v", values, described)
	}
}

func regexpIndex(s, pattern string) int {
	if loc := regexp.MustCompile(pattern).FindStringIndex(s); loc != nil {
		return loc[0]
	}
	return 0
}
