package graphqlgen

import (
	"reflect"
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
			// A JSON list: the generated Go input has IsNil and NotNil
			// for a nillable one where the schema has only what the
			// annotation names.
			{Name: "tags", Type: &field.TypeInfo{Type: field.TypeJSON, Ident: "[]string", RType: &field.RType{Kind: reflect.Slice, Ident: "[]string"}}, Optional: true, Nillable: true,
				Annotations: map[string]any{graphql.AnnotationName: &graphql.Annotation{WhereOps: graphql.OpHas, HasWhereOps: true}}},
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
		"nameNot": {"name_not", "EQ"}, "ageIsNil": {"age", "IsNil"}, "tagsHas": {"tags", "Has"},
	} {
		w, ok := by[name]
		if !ok || w.Field == nil || w.Field.Name != want.field || w.Op != want.op {
			t.Errorf("%s: got %+v, want field %s with %s", name, w, want.field, want.op)
		}
	}
	if _, listed := by["tagsIsNil"]; listed {
		t.Error("tagsIsNil is described and is not in the schema")
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

// An entity hidden from the schema keeps its where input in every split
// mode: another entity's hasXWith names it. Split per entity, the hidden
// entity had no file, and so no where input.
func TestSchemaFiles_HiddenEntityKeepsItsWhereInput(t *testing.T) {
	filterable := map[string]any{graphql.AnnotationName: &graphql.Annotation{WhereInputEnabled: true}}
	secret := &entgen.Type{
		Name: "Secret",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			{Name: "label", Type: &field.TypeInfo{Type: field.TypeString}, Annotations: filterable},
		},
		Annotations: map[string]any{graphql.AnnotationName: &graphql.Annotation{Skip: graphql.SkipType}},
	}
	user := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}, Annotations: filterable}},
		Annotations: map[string]any{},
	}
	user.Edges = []*entgen.Edge{{Name: "secrets", Type: secret, Annotations: filterable}}
	graph := &entgen.Graph{Config: &entgen.Config{Package: "example/ent"}, Nodes: []*entgen.Type{user, secret}}
	for _, mode := range []SchemaSplitMode{SchemaSplitNone, SchemaSplitByCategory, SchemaSplitPerEntity} {
		g := NewGenerator(graph, Config{Package: "graphql", ORMPackage: "example/ent", WhereInputs: true, SchemaSplitMode: mode})
		files, err := g.SchemaFiles()
		if err != nil {
			t.Fatalf("mode %v: %v", mode, err)
		}
		var all string
		for _, src := range files {
			all += src
		}
		for _, want := range []string{"hasSecretsWith: [SecretWhereInput!]", "input SecretWhereInput"} {
			if !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(all) {
				t.Errorf("mode %v: the schema does not contain %q", mode, want)
			}
		}
		if regexp.MustCompile(`(?m)^type Secret\b`).MatchString(all) {
			t.Errorf("mode %v: the hidden entity has a type", mode)
		}
	}
}

// The whole schema loads, in every split mode, when a hidden entity is
// filtered by an enum field, and when it shares an enum with a shown one
// and comes first: the enum types its where input names were not written
// in two of the modes, and a shared enum owned by a hidden entity in none.
func TestSchemaFiles_HiddenEntityEnumsAreDefined(t *testing.T) {
	filterable := func(typ string) map[string]any {
		return map[string]any{graphql.AnnotationName: &graphql.Annotation{WhereInputEnabled: true, Type: typ}}
	}
	enum := func(name, typ string) *entgen.Field {
		return &entgen.Field{Name: name, Type: &field.TypeInfo{Type: field.TypeEnum}, Annotations: filterable(typ),
			Enums: []entgen.Enum{{Name: "A", Value: "a"}, {Name: "B", Value: "b"}}}
	}
	secret := &entgen.Type{
		Name: "Secret",
		ID:   &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields: []*entgen.Field{
			enum("status", ""), enum("kind", "Kind"),
			// An enum the where input does not name is not the schema's business.
			{Name: "inner", Type: &field.TypeInfo{Type: field.TypeEnum}, Enums: []entgen.Enum{{Name: "X", Value: "x"}}},
		},
		Annotations: map[string]any{graphql.AnnotationName: &graphql.Annotation{Skip: graphql.SkipType}},
	}
	user := &entgen.Type{
		Name:        "User",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{enum("kind", "Kind")},
		Annotations: map[string]any{},
	}
	user.Edges = []*entgen.Edge{{Name: "secrets", Type: secret, Annotations: filterable("")}}
	// The hidden entity first: it is then the one a shared enum is "of".
	graph := &entgen.Graph{Config: &entgen.Config{Package: "example/ent"}, Nodes: []*entgen.Type{secret, user}}
	for _, mode := range []SchemaSplitMode{SchemaSplitNone, SchemaSplitByCategory, SchemaSplitPerEntity} {
		g := NewGenerator(graph, Config{Package: "graphql", ORMPackage: "example/ent", WhereInputs: true, SchemaSplitMode: mode})
		files, err := g.SchemaFiles()
		if err != nil {
			t.Fatalf("mode %v: %v", mode, err)
		}
		var all string
		for _, src := range files {
			all += src + "\n"
		}
		for name, want := range map[string]int{"enum SecretStatus": 1, "enum Kind": 1, "enum SecretInner": 0} {
			if got := len(regexp.MustCompile(`(?m)^`+regexp.QuoteMeta(name)+`\b`).FindAllString(all, -1)); got != want {
				t.Errorf("mode %v: %q is defined %d times, want %d", mode, name, got, want)
			}
		}
		// Every type the where inputs name is defined.
		defined := map[string]bool{"ID": true, "Boolean": true, "String": true, "Int": true}
		for _, m := range regexp.MustCompile(`(?m)^(?:type|input|enum|scalar|interface) ([A-Za-z0-9_]+)`).FindAllStringSubmatch(all, -1) {
			defined[m[1]] = true
		}
		for _, in := range regexp.MustCompile(`(?s)input [A-Za-z]+WhereInput[^{]*\{(.*?)\n\}`).FindAllStringSubmatch(all, -1) {
			for _, m := range regexp.MustCompile(`(?m)^  [A-Za-z0-9_]+: \[?([A-Za-z0-9_]+)`).FindAllStringSubmatch(in[1], -1) {
				if !defined[m[1]] {
					t.Errorf("mode %v: a where input names the type %s, which the schema does not define", mode, m[1])
				}
			}
		}
	}
}

// An entity nothing can be ordered by (bytes and JSON fields only) has no
// order in the schema: no <Type>Order, no <Type>OrderField, no orderBy
// argument on its lists. The order enum was written without a value, and
// the generator then refused its own schema.
func TestSchemaFiles_NothingToOrderBy(t *testing.T) {
	relay := map[string]any{graphql.AnnotationName: &graphql.Annotation{RelayConnection: true, QueryField: true}}
	blob := &entgen.Type{
		Name:        "Blob",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{{Name: "data", Type: &field.TypeInfo{Type: field.TypeBytes}}},
		Annotations: relay,
	}
	owner := &entgen.Type{
		Name:        "Owner",
		ID:          &entgen.Field{Name: "id", Type: &field.TypeInfo{Type: field.TypeInt64}},
		Fields:      []*entgen.Field{{Name: "name", Type: &field.TypeInfo{Type: field.TypeString}}},
		Annotations: relay,
	}
	owner.Edges = []*entgen.Edge{{Name: "blobs", Type: blob}}
	graph := &entgen.Graph{Config: &entgen.Config{Package: "example/ent"}, Nodes: []*entgen.Type{owner, blob}}
	for _, mode := range []SchemaSplitMode{SchemaSplitNone, SchemaSplitByCategory, SchemaSplitPerEntity} {
		g := NewGenerator(graph, Config{Package: "graphql", ORMPackage: "example/ent", RelayConnection: true, Ordering: true, SchemaSplitMode: mode})
		files, err := g.SchemaFiles()
		if err != nil {
			t.Fatalf("mode %v: %v", mode, err)
		}
		var all string
		for _, src := range files {
			all += src + "\n"
		}
		for _, absent := range []string{"BlobOrder", "orderBy: BlobOrder"} {
			if regexp.MustCompile(regexp.QuoteMeta(absent)).MatchString(all) {
				t.Errorf("mode %v: the schema has %s, and Blob has nothing to order by", mode, absent)
			}
		}
		for _, present := range []string{"enum OwnerOrderField", "orderBy: OwnerOrder", "type BlobConnection"} {
			if !regexp.MustCompile(regexp.QuoteMeta(present)).MatchString(all) {
				t.Errorf("mode %v: the schema lacks %q", mode, present)
			}
		}
	}
}
