package graphql_test

import (
	"encoding/json"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/internal/tempmod"
	"github.com/syssam/velox/schema"
)

type annotationCase struct {
	name string
	a    graphql.Annotation
	want string
}

// annotationCases are annotations with their JSON, which TestAnnotationJSON
// round-trips and TestAnnotationJSONCoversEveryField checks cover every
// field.
var annotationCases = []annotationCase{
	{"zero", graphql.Annotation{}, `{}`},
	{"Skip()", graphql.Skip(), `{"skip":63}`},
	{"Skip(one)", graphql.Skip(graphql.SkipOrderField), `{"skip":4}`},
	{"Skip(two)", graphql.Skip(graphql.SkipType, graphql.SkipWhereInput), `{"skip":9}`},
	{"Type", graphql.Type("Account"), `{"type":"Account"}`},
	{"QueryField", graphql.QueryField("users"), `{"query_field":"users"}`},
	{"QueryField default", graphql.QueryField(), `{"query_field_default":true}`},
	{"Mutations()", graphql.Mutations(), `{"mutations":["create","update"]}`},
	{"Mutations(one)", graphql.Mutations(graphql.MutationUpdate), `{"mutations":["update"]}`},
	{"MultiOrder", graphql.MultiOrder(), `{"multi_order":true}`},
	{"FieldName", graphql.FieldName("fullName"), `{"field_name":"fullName"}`},
	{"OrderField", graphql.OrderField("CREATED_AT"), `{"order_field":"CREATED_AT"}`},
	{"OrderUnguarded", graphql.OrderUnguarded(), `{"order_unguarded":true}`},
	{"WhereOps()", graphql.WhereOps(), `{"where_ops":2147483648}`},
	{"WhereOps(preset)", graphql.WhereOps(graphql.OpsEquality), `{"where_ops":15}`},
	{"WhereOps(mixed)", graphql.WhereOps(graphql.OpEQ, graphql.OpsNullable), `{"where_ops":6145}`},
	{"WhereOps(all)", graphql.WhereOps(graphql.OpsAll), `{"where_ops":8191}`},
	{"OrderVia", graphql.OrderVia("name", "AUTHOR_NAME"), `{"order_via":[{"field":"name","value":"AUTHOR_NAME"}]}`},
	{"Resolvers", graphql.Resolvers(graphql.Map("organization", "PublicOrganization!").WithComment("Owner."), graphql.Map("tags(first: Int)", "[Tag!]!")),
		`{"resolvers":[{"field":"organization","type":"PublicOrganization!","comment":"Owner."},{"field":"tags(first: Int)","type":"[Tag!]!"}]}`},
	{"WhereEdge", graphql.WhereEdge(), `{"where_edge":true}`},
	{"WhereInput", graphql.WhereInput(), `{"where_input":true}`},
	{"WhereInputEdges", graphql.WhereInputEdges("posts", "groups"), `{"where_input_edges":["posts","groups"]}`},
	{"Connection(false)", graphql.Connection(false), `{"connection":false}`},
	{"Connection(true)", graphql.Connection(true), `{"connection":true}`},
	{"MaxList", graphql.MaxList(5), `{"max_list":5}`},
	{"CreateInput(false)", graphql.CreateInput(false), `{"create_input":false}`},
	{"UpdateInput(true)", graphql.UpdateInput(true), `{"update_input":true}`},
	{"CreateDefault(true)", graphql.CreateDefault(true), `{"create_default":true}`},
	{"CreateDefault(int8)", graphql.CreateDefault(int8(-3)), `{"create_default":-3}`},
	{"CreateDefault(uint64)", graphql.CreateDefault(uint64(math.MaxUint64)), `{"create_default":18446744073709551615}`},
	{"CreateDefault(float)", graphql.CreateDefault(1.5), `{"create_default":1.5}`},
	{"CreateDefault(level)", graphql.CreateDefault(slog.LevelWarn), `{"create_default":4}`},
	{"CreateDefault(NaN)", graphql.CreateDefault(math.NaN()), `{"create_default":"NaN"}`},
	{"EnumValues", graphql.EnumValues(map[string]string{"b": "B", "a": "A"}), `{"enum_values":{"a":"A","b":"B"}}`},
	{"Directives", graphql.Directives(graphql.Directive{Name: "auth", Args: map[string]any{"role": "admin"}}, graphql.Directive{Name: "x"}),
		`{"directives":[{"name":"auth","args":{"role":"admin"}},{"name":"x"}]}`},
}

func TestAnnotationJSON(t *testing.T) {
	for _, tc := range annotationCases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.a)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("got  %s\nwant %s", b, tc.want)
			}
			var back graphql.Annotation
			if err := json.Unmarshal(b, &back); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(back, tc.a) {
				t.Errorf("round trip: got %+v, want %+v", back, tc.a)
			}
		})
	}
	if n := (graphql.Annotation{}).Name(); n != "graphql" {
		t.Errorf("Name = %q", n)
	}
}

// TestAnnotationJSONCoversEveryField: each field of Annotation is set in at
// least one of annotationCases, so a field the JSON drops or misnames cannot
// pass the round trip unnoticed.
func TestAnnotationJSONCoversEveryField(t *testing.T) {
	typ := reflect.TypeFor[graphql.Annotation]()
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !slices.ContainsFunc(annotationCases, func(tc annotationCase) bool {
			return !reflect.ValueOf(tc.a).Field(i).IsZero()
		}) {
			t.Errorf("no case of annotationCases sets %s", f.Name)
		}
	}
}

// merge is what the loader does with two annotations named "graphql" on one
// element: the later one is the receiver.
func merge(t *testing.T, earlier, later graphql.Annotation) graphql.Annotation {
	t.Helper()
	var s schema.Annotation = later
	m, ok := s.(schema.Merger)
	if !ok {
		t.Fatal("Annotation is not a schema.Merger")
	}
	out, ok := m.Merge(earlier).(graphql.Annotation)
	if !ok {
		t.Fatal("Merge did not return a graphql.Annotation")
	}
	return out
}

// TestMutationsDefault: Mutations with no argument means create and update.
func TestMutationsDefault(t *testing.T) {
	got := graphql.Mutations().Mutations
	if want := []graphql.MutationKind{graphql.MutationCreate, graphql.MutationUpdate}; !reflect.DeepEqual(got, want) {
		t.Errorf("Mutations() = %v, want %v", got, want)
	}
}

// OrderVia merges as a list: each call names one field, the earlier first.
func TestMergeOrderVia(t *testing.T) {
	got := merge(t, graphql.OrderVia("name", "A"), graphql.OrderVia("email", "B"))
	want := []graphql.ViaOrder{{Field: "name", Value: "A"}, {Field: "email", Value: "B"}}
	if !reflect.DeepEqual(got.OrderVia, want) {
		t.Errorf("OrderVia: got %+v, want %+v", got.OrderVia, want)
	}
}

// Resolvers merge as a list, the earlier first; a field both name, whatever its
// arguments, is kept once where the earlier had it, as the later one wrote it.
func TestMergeResolvers(t *testing.T) {
	got := merge(t,
		graphql.Resolvers(graphql.Map("organization", "PublicOrganization"), graphql.Map("owner", "PublicEmployee")),
		graphql.Resolvers(graphql.Map("organization(active: Boolean)", "PublicOrganization!").WithComment("Later."), graphql.Map("tags", "[Tag!]!")),
	).Resolvers
	want := []graphql.ResolverMapping{
		{Field: "organization(active: Boolean)", Type: "PublicOrganization!", Comment: "Later."},
		{Field: "owner", Type: "PublicEmployee"},
		{Field: "tags", Type: "[Tag!]!"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if n := graphql.Map("priceListItem(priceListId: ID!)", "X").Name(); n != "priceListItem" {
		t.Errorf("Name() = %q", n)
	}
}

func TestMerge(t *testing.T) {
	t.Run("skip ORs", func(t *testing.T) {
		got := merge(t, graphql.Skip(graphql.SkipOrderField), graphql.Skip(graphql.SkipWhereInput))
		if want := graphql.SkipOrderField | graphql.SkipWhereInput; got.Skip != want {
			t.Errorf("Skip = %d, want %d", got.Skip, want)
		}
	})
	t.Run("where ops OR", func(t *testing.T) {
		got := merge(t, graphql.WhereOps(graphql.OpsEquality), graphql.WhereOps(graphql.OpsString))
		if want := graphql.OpsEquality | graphql.OpsString; got.WhereOps != want {
			t.Errorf("WhereOps = %d, want %d", got.WhereOps, want)
		}
	})
	t.Run("mutations append", func(t *testing.T) {
		got := merge(t, graphql.Mutations(graphql.MutationCreate), graphql.Mutations(graphql.MutationUpdate))
		if want := []graphql.MutationKind{"create", "update"}; !reflect.DeepEqual(got.Mutations, want) {
			t.Errorf("Mutations = %v, want %v", got.Mutations, want)
		}
		got = merge(t, graphql.Mutations(), graphql.Mutations(graphql.MutationUpdate))
		if want := []graphql.MutationKind{"create", "update"}; !reflect.DeepEqual(got.Mutations, want) {
			t.Errorf("repeated kind: Mutations = %v, want %v", got.Mutations, want)
		}
	})
	t.Run("later scalar wins", func(t *testing.T) {
		got := merge(t, graphql.Type("A"), graphql.Type("B"))
		if got.Type != "B" {
			t.Errorf("Type = %q, want B", got.Type)
		}
		got = merge(t, graphql.Type("A"), graphql.QueryField("users"))
		if got.Type != "A" || got.QueryField != "users" {
			t.Errorf("an unset scalar must not erase: %+v", got)
		}
	})
	t.Run("later pointer wins, even false", func(t *testing.T) {
		got := merge(t, graphql.CreateInput(true), graphql.CreateInput(false))
		if got.CreateInput == nil || *got.CreateInput {
			t.Errorf("CreateInput = %v, want false", got.CreateInput)
		}
		got = merge(t, graphql.Connection(false), graphql.FieldName("x"))
		if got.Connection == nil || *got.Connection {
			t.Errorf("an unset pointer must not erase: %v", got.Connection)
		}
		got = merge(t, graphql.CreateDefault(true), graphql.CreateDefault(false))
		if string(got.CreateDefault) != "false" {
			t.Errorf("CreateDefault = %s, want false", got.CreateDefault)
		}
		got = merge(t, graphql.CreateDefault(3), graphql.FieldName("x"))
		if string(got.CreateDefault) != "3" {
			t.Errorf("an unset CreateDefault must not erase: %s", got.CreateDefault)
		}
	})
	t.Run("bools, lists and maps", func(t *testing.T) {
		d1, d2 := graphql.Directive{Name: "a"}, graphql.Directive{Name: "b"}
		got := merge(t, graphql.Directives(d1), graphql.Directives(d2))
		got = merge(t, got, graphql.MultiOrder())
		got = merge(t, got, graphql.OrderUnguarded())
		got = merge(t, got, graphql.FieldName("x")) // an unset bool does not erase
		got = merge(t, got, graphql.EnumValues(map[string]string{"x": "X", "y": "Y1"}))
		got = merge(t, got, graphql.EnumValues(map[string]string{"y": "Y2"}))
		if !reflect.DeepEqual(got.Directives, []graphql.Directive{d1, d2}) || !got.MultiOrder || !got.OrderUnguarded ||
			!reflect.DeepEqual(got.EnumValues, map[string]string{"x": "X", "y": "Y2"}) {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("operands are not changed", func(t *testing.T) {
		a, b := graphql.EnumValues(map[string]string{"x": "X"}), graphql.EnumValues(map[string]string{"y": "Y"})
		merge(t, a, b)
		if len(a.EnumValues) != 1 || len(b.EnumValues) != 1 {
			t.Errorf("a = %v, b = %v", a.EnumValues, b.EnumValues)
		}
	})
	t.Run("a pointer is accepted", func(t *testing.T) {
		e := graphql.Type("A")
		var s schema.Annotation = graphql.QueryField("q")
		got := s.(schema.Merger).Merge(&e).(graphql.Annotation)
		if got.Type != "A" || got.QueryField != "q" {
			t.Errorf("got %+v", got)
		}
	})
	t.Run("a foreign annotation panics", func(t *testing.T) {
		defer func() {
			if r := recover(); r == nil {
				t.Error("no panic")
			}
		}()
		graphql.Type("A").Merge(schema.Comment("x"))
	})
}

const loaderSchema = `package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
)

type Pet struct{ schema.Schema }

func (Pet) Fields() []schema.Field { return []schema.Field{field.String("name")} }

type User struct{ schema.Schema }

func (User) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").Annotations(graphql.OrderField("NAME"), graphql.WhereOps()),
		field.Time("created_at").Annotations(graphql.OrderField("CREATED_AT")),
	}
}

func (User) Edges() []schema.Edge {
	return []schema.Edge{edge.To("pets", Pet.Type).Annotations(graphql.Connection(false), graphql.WhereEdge())}
}

func (User) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField("users"),
		graphql.Mutations(),
		graphql.Type("First"),
		graphql.Type("Account"),
		graphql.Mutations(graphql.MutationUpdate),
		graphql.Skip(graphql.SkipOrderField),
		graphql.Skip(graphql.SkipWhereInput),
	}
}
`

// TestThroughTheLoader is the real path: a schema package in another module
// lists the annotations, velox-gen loads it, and describe -json shows them,
// merged, under annotations.graphql.
func TestThroughTheLoader(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a module")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sum, err := os.ReadFile(filepath.Join(repo, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	mod := "module example.com/app\n\ngo 1.27.0\n\n" +
		"tool github.com/syssam/velox/cmd/velox-gen\n\n" +
		"require (\n" +
		"\tgithub.com/syssam/velox v0.0.0\n" +
		"\tgithub.com/syssam/velox/contrib/graphql v0.0.0\n" +
		")\n\n" +
		"replace (\n" +
		"\tgithub.com/syssam/velox => " + repo + "\n" +
		"\tgithub.com/syssam/velox/contrib/graphql => " + filepath.Join(repo, "contrib/graphql") + "\n" +
		")\n"
	for name, src := range map[string]string{"go.mod": mod, "go.sum": string(sum), "schema/schema.go": loaderSchema} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("go", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off", "GOTOOLCHAIN=local", "GOWORK=off")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %v: %v\n%s%s", args, err, out, tempmod.Hint(string(out)))
		}
		return string(out)
	}
	run("mod", "tidy")
	out := run("tool", "velox-gen", "describe", "-json", "./schema")

	var model struct {
		Entities []struct {
			Name        string
			Annotations map[string]graphql.Annotation
			Fields      []struct {
				Name        string
				Annotations map[string]graphql.Annotation
			}
			Edges []struct {
				Name        string
				Annotations map[string]graphql.Annotation
			}
		}
	}
	if err := json.Unmarshal([]byte(out), &model); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	var users bool
	for _, s := range model.Entities {
		if s.Name != "User" {
			if _, ok := s.Annotations["graphql"]; ok {
				t.Errorf("%s has a graphql annotation", s.Name)
			}
			continue
		}
		users = true
		got, ok := s.Annotations["graphql"]
		if !ok {
			t.Fatalf("no annotations.graphql on User:\n%s", out)
		}
		want := graphql.Annotation{
			Skip:       graphql.SkipOrderField | graphql.SkipWhereInput,
			Type:       "Account",
			QueryField: "users",
			Mutations:  []graphql.MutationKind{"create", "update"},
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("User: got %+v\nwant %+v", got, want)
		}
		for _, f := range s.Fields {
			switch f.Name {
			case "name":
				if a := f.Annotations["graphql"]; a.OrderField != "NAME" || a.WhereOps != graphql.OpsDefault {
					t.Errorf("name: %+v", a)
				}
			case "created_at":
				if a := f.Annotations["graphql"]; a.OrderField != "CREATED_AT" {
					t.Errorf("created_at: %+v", a)
				}
			}
		}
		if len(s.Edges) != 1 {
			t.Fatalf("edges: %+v", s.Edges)
		}
		if a := s.Edges[0].Annotations["graphql"]; a.Connection == nil || *a.Connection || !a.WhereEdge {
			t.Errorf("pets: %+v", a)
		}
	}
	if !users {
		t.Fatalf("no User in:\n%s", out)
	}
}

// Merge and Directives hand out their own copies of a directive's arguments:
// changing the result must not change the annotation it was made from.
func TestMergeClonesArgs(t *testing.T) {
	a := graphql.Directives(graphql.Directive{Name: "d", Args: map[string]any{"k": 1}})
	merged := merge(t, a, graphql.Type("T"))
	merged.Directives[0].Args["k"] = 2
	if got := a.Directives[0].Args["k"]; got != 1 {
		t.Errorf("Merge shares the arguments: the source now has k = %v", got)
	}
	merged = merge(t, graphql.Type("T"), a)
	merged.Directives[0].Args["k"] = 3
	if got := a.Directives[0].Args["k"]; got != 1 {
		t.Errorf("Merge shares the arguments (as the earlier side): the source now has k = %v", got)
	}

	ds := []graphql.Directive{{Name: "d", Args: map[string]any{"k": 1}}}
	got := graphql.Directives(ds...)
	got.Directives[0].Args["k"] = 2
	if ds[0].Args["k"] != 1 {
		t.Errorf("Directives shares the arguments: the argument now has k = %v", ds[0].Args["k"])
	}
}

// Directives copies each Args map one level: a key added to the caller's map
// stays out of the annotation, but a nested map is shared.
func TestDirectivesCopyArgsOneLevel(t *testing.T) {
	args := map[string]any{"x": map[string]any{"y": 1}}
	a := graphql.Directives(graphql.Directive{Name: "d", Args: args})
	args["z"] = 2
	if _, ok := a.Directives[0].Args["z"]; ok {
		t.Errorf("Directives shares the Args map: z shows through")
	}
	args["x"].(map[string]any)["y"] = 3
	if got := a.Directives[0].Args["x"].(map[string]any)["y"]; got != 3 {
		t.Errorf("a nested map is copied: y = %v, want 3 (shared)", got)
	}
}

// RelayConnection is accepted and says nothing (connections are always
// written), QueryField takes at most one name, and OpsNone offers no
// predicate.
func TestPortSpellings(t *testing.T) {
	if got := graphql.RelayConnection(); !reflect.DeepEqual(got, graphql.Annotation{}) {
		t.Errorf("RelayConnection: %+v", got)
	}
	if got := graphql.QueryField(); !got.HasQueryField() || got.QueryField != "" || !got.QueryFieldDefault {
		t.Errorf("QueryField(): %+v", got)
	}
	if got := graphql.QueryField("users"); !got.HasQueryField() || got.QueryFieldDefault {
		t.Errorf("QueryField(users): %+v", got)
	}
	if (graphql.Annotation{}).HasQueryField() {
		t.Error("an empty annotation has a Query field")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("QueryField with two names did not panic")
			}
		}()
		graphql.QueryField("a", "b")
	}()
	if got := graphql.WhereOps(graphql.OpsNone); got.WhereOps != 0 {
		t.Errorf("WhereOps(OpsNone): %v", got.WhereOps)
	}
}

// NewDirective and Deprecated build the directives the old velox's did, and
// copy what they are given; WhereInputEdges merges by name, the receiver's
// after the argument's.
func TestOldDirectiveSpellings(t *testing.T) {
	args := map[string]any{"maxAge": 300}
	d := graphql.NewDirective("cacheControl", args)
	args["maxAge"] = 1
	if d.Name != "cacheControl" || d.Args["maxAge"] != 300 {
		t.Errorf("NewDirective = %+v", d)
	}
	if d := graphql.Deprecated(""); d.Name != "deprecated" || d.Args != nil {
		t.Errorf("Deprecated(\"\") = %+v", d)
	}
	if d := graphql.Deprecated("use b"); d.Args["reason"] != "use b" {
		t.Errorf("Deprecated(reason) = %+v", d)
	}
	m := graphql.WhereInputEdges("a", "b").Merge(graphql.WhereInputEdges("b", "c")).(graphql.Annotation)
	if got := strings.Join(m.WhereInputEdges, ","); got != "b,c,a" {
		t.Errorf("merged names %q", got)
	}
}
