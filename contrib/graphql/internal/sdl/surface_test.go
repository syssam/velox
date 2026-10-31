package sdl

import (
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/plugin"
)

// A key of a type an ID does not hold makes no Node: the type implements
// nothing and shows its key as a field, and update and delete, which take an
// ID, are refused.
func TestNodeKeyTypes(t *testing.T) {
	for typ, key := range map[string]string{"int64": "int64", "uint8": "uint8", "string": "string", "enum": "string", "uuid": "[16]byte", "float64": "", "time": ""} {
		id := &plugin.Field{Name: "id", GoName: "ID", Type: typ}
		if typ == "enum" {
			id.EnumValues = []string{"a"}
		}
		e := &plugin.Entity{Name: "Thing", Tenant: -1, PrimaryKey: []int{0}, Fields: []*plugin.Field{id, field("x", "string")}}
		s, err := Build(model(e))
		if err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if got := s.Types[0]; got.Key != key || got.Node != (key != "") {
			t.Errorf("a %s key: Node %v, Key %q, want %q", typ, got.Node, got.Key, key)
		}
	}
	id := &plugin.Field{Name: "id", GoName: "ID", Type: "float64"}
	clock := &plugin.Entity{Name: "Clock", Tenant: -1, PrimaryKey: []int{0}, Fields: []*plugin.Field{id, field("x", "string")}}
	sch, sdl := load(t, model(clock))
	if def := sch.Types["Clock"]; len(def.Interfaces) != 0 || typeOf(sch, "Clock", "id") != "Float!" || goField(sch, "Clock", "id") != "" {
		t.Errorf("a float key:\n%s", sdl)
	}
	clock.Annotations = ann(t, graphql.Mutations(graphql.MutationDelete()))
	renderErr(t, model(clock), "schema Clock", "delete", "of type float64, which an ID does not hold")
}

// A field whose Go type is the application's own binds to a getter, which
// converts it to the Go type its scalar binds to; a JSON field always does.
// Velox's own Go types, and enum types, bind as they are.
func TestGetters(t *testing.T) {
	f := func(name, typ, elem, pkg string) *plugin.Field {
		fd := field(name, typ)
		fd.ElemType, fd.PkgPath = elem, pkg
		return fd
	}
	renamed := f("email", "string", "types.Email", "example.com/types")
	renamed.Annotations = ann(t, graphql.FieldName("mail"))
	e := entity("Thing",
		f("name", "string", "string", ""),
		renamed,
		f("rank", "int8", "types.Rank", "example.com/types"),
		f("at", "time", "time.Time", "time"),
		f("seen", "time", "types.Stamp", "example.com/types"),
		f("ref", "uuid", "uuid.UUID", "github.com/google/uuid"),
		f("other", "uuid", "uuid.UUID", "github.com/gofrs/uuid"),
		f("size", "uint64", "uint64", ""),
		f("tags", "json", "[]string", ""),
		f("blob", "bytes", "[]byte", ""),
	)
	s, sdl := load(t, model(e))
	for name, want := range map[string]string{
		"name": "", "mail": "EmailGQL", "rank": "RankGQL", "at": "", "seen": "SeenGQL",
		"ref": "", "other": "OtherGQL", "size": "", "tags": "TagsGQL", "blob": "",
	} {
		if got := goField(s, "Thing", name); got != want {
			t.Errorf("Thing.%s: @goField(name: %q), want %q\n%s", name, got, want, sdl)
		}
	}
	surf, err := Build(model(e))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, g := range surf.Types[0].Getters {
		got = append(got, g.Method+" "+g.Returns+" "+g.Pkg)
	}
	want := []string{"EmailGQL string ", "RankGQL int8 ", "SeenGQL veloxgql.Time " + veloxgqlPath, "OtherGQL veloxgql.UUID " + veloxgqlPath, "TagsGQL veloxgql.JSON " + veloxgqlPath}
	if len(got) != len(want) {
		t.Fatalf("getters %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("getter %d: %q, want %q", i, got[i], want[i])
		}
	}
}

// The edge fields of each type, with their kind, target and method; an edge
// that is skipped, or leads to a skipped type, has none.
func TestSurfaceEdges(t *testing.T) {
	s, err := Build(modeltest.Model(t))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[EdgeKind]string{EdgeUnique: "unique", EdgeList: "list", EdgeConnection: "connection"}
	got := map[string][]string{}
	for _, ty := range s.Types {
		for _, ed := range ty.Edges {
			got[ty.Name] = append(got[ty.Name], ty.Entity.Edges[ed.Edge].Name+" "+kinds[ed.Kind]+" "+ed.Target.Name+" "+ed.Method)
		}
	}
	want := map[string][]string{
		"Account":  {"posts connection Post PostsGQL", "groups list Group GroupsGQL", "likes connection Like LikesGQL"},
		"Post":     {"author unique Account AuthorGQL", "likes list Like LikesGQL", "related connection Post RelatedGQL", "related_by connection Post RelatedByGQL"},
		"Category": {"children connection Category ChildrenGQL", "parent unique Category ParentGQL"},
		"Group":    {"users connection Account UsersGQL"},
		"Like":     {"user unique Account UserGQL", "post unique Post PostGQL"},
	}
	for name, w := range want {
		if g := got[name]; !slices.Equal(g, w) {
			t.Errorf("%s: edges %q, want %q", name, g, w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("edges %q", got)
	}
}

// inputKinds names the InputKinds, for test output.
var inputKinds = map[InputKind]string{
	InputValue: "value", InputClear: "clear", InputEdgeID: "edgeID", InputEdgeIDs: "edgeIDs",
	InputAddIDs: "add", InputRemoveIDs: "remove", InputClearEdge: "clearEdge",
}

// typeNamed is the Type of s named name, failing the test without one.
func typeNamed(t *testing.T, s *Surface, name string) *Type {
	t.Helper()
	for _, ty := range s.Types {
		if ty.Name == name {
			return ty
		}
	}
	t.Fatalf("no type %s", name)
	return nil
}

// The mutation inputs of each type, as the SDL declares them, with what each
// field does and the field or edge it is about.
func TestSurfaceInputs(t *testing.T) {
	m := modeltest.Model(t)
	s, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	sch, sdl := load(t, m, readFile(t, filepath.Join("testdata", "app.graphqls")))

	for _, c := range []struct {
		name                   string
		create, update, delete bool
	}{
		{"Post", true, true, true}, {"Category", true, true, false}, {"Like", true, false, false},
		{"Account", true, false, false}, {"Group", false, false, false},
	} {
		ty := typeNamed(t, s, c.name)
		if (ty.Create != nil) != c.create || (ty.Update != nil) != c.update || ty.Delete != c.delete {
			t.Errorf("%s: Create %v, Update %v, Delete %v; want %v %v %v", c.name, ty.Create != nil, ty.Update != nil, ty.Delete, c.create, c.update, c.delete)
		}
	}

	// Each field is the SDL's line, in its order; Required is its "!".
	for _, ty := range s.Types {
		for _, in := range []struct {
			typ    string
			fields []InputField
		}{{"Create" + ty.Name + "Input", ty.Create}, {"Update" + ty.Name + "Input", ty.Update}} {
			var got []string
			for _, f := range in.fields {
				got = append(got, f.Name+": "+f.Type)
				if f.Required != strings.HasSuffix(f.Type, "!") {
					t.Errorf("%s.%s: Required %v", in.typ, f.Name, f.Required)
				}
				if !strings.EqualFold(f.GoName, f.Name) {
					t.Errorf("%s.%s: GoName %q, which gqlgen does not bind", in.typ, f.Name, f.GoName)
				}
			}
			if want := fieldsOf(sch, in.typ); !slices.Equal(got, want) && (len(got) > 0 || want != nil) {
				t.Errorf("%s:\n got %v\nwant %v\n%s", in.typ, got, want, sdl)
			}
		}
	}

	describe := func(ty *Type, fs []InputField) []string {
		var out []string
		for _, f := range fs {
			d := f.Name + " " + inputKinds[f.Kind]
			if f.Field >= 0 {
				d += " field " + ty.Entity.Fields[f.Field].Name
			}
			if f.Edge >= 0 {
				d += " edge " + ty.Entity.Edges[f.Edge].Name + " " + f.Target
			}
			if f.GoType != "" {
				d += " " + f.GoType
			}
			if f.GoPkg != "" {
				d += " (" + f.GoPkg + ")"
			}
			out = append(out, f.GoName+": "+d)
		}
		return out
	}
	post := typeNamed(t, s, "Post")
	// A nullable field gives a value and a clear, of one field; a JSON
	// value is veloxgql's.
	if got, want := describe(post, post.Update), []string{
		"Title: title value field title string",
		"Status: status value field status PostStatus",
		"PublishedAt: publishedAt value field published_at time.Time (time)",
		"ClearPublishedAt: clearPublishedAt clear field published_at",
		"Tags: tags value field tags veloxgql.JSON (" + veloxgqlPath + ")",
		"Views: views value field views int",
		"Rating: rating value field rating float64",
		"ClearRating: clearRating clear field rating",
		"WriterID: writerID edgeID edge author Account",
		// An M2M to its own type, from both sides.
		"AddRelatedIDs: addRelatedIDs add edge related Post",
		"RemoveRelatedIDs: removeRelatedIDs remove edge related Post",
		"ClearRelated: clearRelated clearEdge edge related Post",
		"AddRelatedByIDs: addRelatedByIDs add edge related_by Post",
		"RemoveRelatedByIDs: removeRelatedByIDs remove edge related_by Post",
		"ClearRelatedBy: clearRelatedBy clearEdge edge related_by Post",
	}; !slices.Equal(got, want) {
		t.Errorf("UpdatePostInput:\n got %q\nwant %q", got, want)
	}
	cat := typeNamed(t, s, "Category")
	if got, want := describe(cat, cat.Create), []string{
		"Title: title value field title string",
		"ChildIDs: childIDs edgeIDs edge children Category",
		"ParentID: parentID edgeID edge parent Category",
	}; !slices.Equal(got, want) {
		t.Errorf("CreateCategoryInput:\n got %q\nwant %q", got, want)
	}
	if got, want := describe(cat, cat.Update), []string{
		"Title: title value field title string",
		"AddChildIDs: addChildIDs add edge children Category",
		"RemoveChildIDs: removeChildIDs remove edge children Category",
		"ClearChildren: clearChildren clearEdge edge children Category",
		"ParentID: parentID edgeID edge parent Category",
		"ClearParent: clearParent clearEdge edge parent Category",
	}; !slices.Equal(got, want) {
		t.Errorf("UpdateCategoryInput:\n got %q\nwant %q", got, want)
	}

	// An M2M edge: its IDs on create; add, remove and clear on update.
	b, err := Build(blogInputs(t))
	if err != nil {
		t.Fatal(err)
	}
	user := typeNamed(t, b, "User")
	var got []string
	for _, f := range append(slices.Clone(user.Create), user.Update...) {
		if f.Edge >= 0 && user.Entity.Edges[f.Edge].Name == "groups" {
			got = append(got, describe(user, []InputField{f})...)
		}
	}
	if want := []string{
		"MembershipIDs: membershipIDs edgeIDs edge groups Group",
		"AddMembershipIDs: addMembershipIDs add edge groups Group",
		"RemoveMembershipIDs: removeMembershipIDs remove edge groups Group",
		"ClearMemberships: clearMemberships clearEdge edge groups Group",
	}; !slices.Equal(got, want) {
		t.Errorf("User's groups:\n got %q\nwant %q", got, want)
	}
}

// Every input object velox.graphqls declares for a type is the Surface's:
// its create, update and where inputs, field by field.
func TestSurfaceMatchesSDL(t *testing.T) {
	m := modeltest.Model(t)
	s, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	sch, _ := load(t, m, readFile(t, filepath.Join("testdata", "app.graphqls")))
	surface := map[string][]string{}
	for _, ty := range s.Types {
		for _, in := range []struct {
			typ    string
			fields []InputField
		}{{"Create" + ty.Name + "Input", ty.Create}, {"Update" + ty.Name + "Input", ty.Update}} {
			if in.fields == nil {
				continue
			}
			for _, f := range in.fields {
				surface[in.typ] = append(surface[in.typ], f.Name+": "+f.Type)
			}
		}
		for _, f := range ty.Where {
			surface[ty.Name+"WhereInput"] = append(surface[ty.Name+"WhereInput"], f.Name+": "+f.Type)
		}
	}
	n := 0
	for name, def := range sch.Types {
		if def.Kind != ast.InputObject || def.BuiltIn || !strings.HasSuffix(name, "Input") {
			continue
		}
		n++
		if got, want := surface[name], fieldsOf(sch, name); !slices.Equal(got, want) {
			t.Errorf("%s:\n Surface %v\n     SDL %v", name, got, want)
		}
	}
	if n != len(surface) {
		t.Errorf("the SDL declares %d inputs, the Surface %d: %v", n, len(surface), slices.Sorted(maps.Keys(surface)))
	}
}
