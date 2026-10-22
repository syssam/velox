package graphql_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/internal/tempmod"
	"github.com/syssam/velox2/schema"
)

func TestAnnotationJSON(t *testing.T) {
	for _, tc := range []struct {
		name string
		a    graphql.Annotation
		want string
	}{
		{"zero", graphql.Annotation{}, `{}`},
		{"Skip()", graphql.Skip(), `{"skip":63}`},
		{"Skip(one)", graphql.Skip(graphql.SkipOrderField), `{"skip":4}`},
		{"Skip(two)", graphql.Skip(graphql.SkipType, graphql.SkipWhereInput), `{"skip":9}`},
		{"Type", graphql.Type("Account"), `{"type":"Account"}`},
		{"QueryField", graphql.QueryField("users"), `{"query_field":"users"}`},
		{"Mutations()", graphql.Mutations(), `{"mutations":["create","update"]}`},
		{"Mutations(all)", graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete()), `{"mutations":["create","update","delete"]}`},
		{"MultiOrder", graphql.MultiOrder(), `{"multi_order":true}`},
		{"FieldName", graphql.FieldName("fullName"), `{"field_name":"fullName"}`},
		{"OrderField", graphql.OrderField("CREATED_AT"), `{"order_field":"CREATED_AT"}`},
		{"OrderUnguarded", graphql.OrderUnguarded(), `{"order_unguarded":true}`},
		{"WhereOps()", graphql.WhereOps(), `{"where_ops":2147483648}`},
		{"WhereOps(preset)", graphql.WhereOps(graphql.OpsEquality), `{"where_ops":15}`},
		{"WhereOps(mixed)", graphql.WhereOps(graphql.OpEQ, graphql.OpsNullable), `{"where_ops":6145}`},
		{"WhereOps(all)", graphql.WhereOps(graphql.OpsAll), `{"where_ops":8191}`},
		{"WhereEdge", graphql.WhereEdge(), `{"where_edge":true}`},
		{"Connection(false)", graphql.Connection(false), `{"connection":false}`},
		{"Connection(true)", graphql.Connection(true), `{"connection":true}`},
		{"CreateInput(false)", graphql.CreateInput(false), `{"create_input":false}`},
		{"UpdateInput(true)", graphql.UpdateInput(true), `{"update_input":true}`},
		{"EnumValues", graphql.EnumValues(map[string]string{"b": "B", "a": "A"}), `{"enum_values":{"a":"A","b":"B"}}`},
		{"Directives", graphql.Directives(graphql.Directive{Name: "auth", Args: map[string]any{"role": "admin"}}, graphql.Directive{Name: "x"}),
			`{"directives":[{"name":"auth","args":{"role":"admin"}},{"name":"x"}]}`},
	} {
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
		got := merge(t, graphql.Mutations(graphql.Create()), graphql.Mutations(graphql.Delete()))
		if want := []graphql.MutationKind{"create", "delete"}; !reflect.DeepEqual(got.Mutations, want) {
			t.Errorf("Mutations = %v, want %v", got.Mutations, want)
		}
		got = merge(t, graphql.Mutations(), graphql.Mutations(graphql.Update(), graphql.Delete()))
		if want := []graphql.MutationKind{"create", "update", "delete"}; !reflect.DeepEqual(got.Mutations, want) {
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
	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
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
		graphql.Mutations(graphql.Delete()),
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
		"tool github.com/syssam/velox2/cmd/velox-gen\n\n" +
		"require (\n" +
		"\tgithub.com/syssam/velox2 v0.0.0\n" +
		"\tgithub.com/syssam/velox2/contrib/graphql v0.0.0\n" +
		")\n\n" +
		"replace (\n" +
		"\tgithub.com/syssam/velox2 => " + repo + "\n" +
		"\tgithub.com/syssam/velox2/contrib/graphql => " + filepath.Join(repo, "contrib/graphql") + "\n" +
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
			Mutations:  []graphql.MutationKind{"create", "update", "delete"},
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
