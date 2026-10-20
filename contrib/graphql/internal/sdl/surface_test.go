package sdl

import (
	"slices"
	"testing"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/plugin"
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
	clock.Annotations = ann(t, graphql.Mutations(graphql.Delete()))
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
	s, err := Build(testModel(t))
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
		"Post":     {"author unique Account AuthorGQL", "likes list Like LikesGQL"},
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
