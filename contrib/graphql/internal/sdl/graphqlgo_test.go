package sdl

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/parser"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/plugin"
)

const genPath = "example.com/app/gen"

// graphqlGo is the Config of engine=graphql-go, compat=veloxe or not.
func graphqlGo(veloxe bool) Config {
	return Config{GraphQLGo: true, Veloxe: veloxe, Package: genPath}
}

// TestGraphQLGoGolden pins the SDL of engine=graphql-go, with and without
// compat=veloxe, over the test model less what the engine refuses. Run it
// with -update to rewrite testdata/graphqlgo*.graphql.
func TestGraphQLGoGolden(t *testing.T) {
	for name, veloxe := range map[string]bool{"graphqlgo.graphql": false, "graphqlgo_veloxe.graphql": true} {
		t.Run(name, func(t *testing.T) {
			got, err := RenderConfig(modeltest.GraphQLGo(t), graphqlGo(veloxe))
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", name)
			if *update {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if want := readFile(t, path); got != want {
				gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
				for i := range min(len(gl), len(wl)) {
					if gl[i] != wl[i] {
						t.Fatalf("line %d:\n got %q\nwant %q\n(run go test -update to accept)", i+1, gl[i], wl[i])
					}
				}
				t.Fatalf("got %d lines, want %d (run go test -update to accept)", len(gl), len(wl))
			}
		})
	}
}

// TestGraphQLGoBindsEveryType: under engine=graphql-go every type the SDL
// declares but Query carries @goModel, so gqlc binds it to the Go velox
// writes and not by autobind's order; the scalars bind the entities' own
// Go types; and gqlgen's introspection binds are gone. gqlgen's SDL of the
// same model binds only what it did.
func TestGraphQLGoBindsEveryType(t *testing.T) {
	for _, veloxe := range []bool{false, true} {
		got, err := RenderConfig(modeltest.GraphQLGo(t), graphqlGo(veloxe))
		if err != nil {
			t.Fatal(err)
		}
		doc, perr := parser.ParseSchema(&ast.Source{Input: got})
		if perr != nil {
			t.Fatal(perr)
		}
		models := map[string]string{}
		for _, d := range doc.Definitions {
			if d.Name == "Query" {
				continue
			}
			dir := d.Directives.ForName("goModel")
			if dir == nil {
				t.Errorf("veloxe=%v: %s %s has no @goModel", veloxe, d.Kind, d.Name)
				continue
			}
			models[d.Name] = dir.Arguments.ForName("model").Value.Raw
		}
		for typ, model := range map[string]string{
			"Time": "time.Time", "Cursor": "github.com/syssam/velox/contrib/graphql/veloxgql.Cursor",
			"PageInfo": "github.com/syssam/velox/contrib/graphql/veloxgql.PageInfo", "OrderDirection": "github.com/syssam/velox/contrib/graphql/veloxgql.OrderDirection",
			"Account": genPath + ".User", "PostStatus": genPath + ".PostStatus", "PostWhereInput": genPath + ".PostWhereInput",
			"CreatePostInput": genPath + ".CreatePostInput", "Bytes": "[]byte",
		} {
			if models[typ] != model {
				t.Errorf("veloxe=%v: %s binds %q, want %q", veloxe, typ, models[typ], model)
			}
		}
		// Under compat=veloxe a type with a QueryField has no connection,
		// and the application declares JSON, UUID and Uint64, as veloxe left
		// them to it.
		own := map[string]string{"UUID": "github.com/google/uuid.UUID", "Uint64": "uint64", "JSON": "github.com/syssam/velox/contrib/graphql/veloxgql.JSON",
			"AccountConnection": genPath + ".AccountConnection", "AccountEdge": genPath + ".AccountEdge", "PostOrder": genPath + ".PostOrder", "PostOrderField": genPath + ".PostOrderField"}
		if veloxe {
			own = map[string]string{"Node": genPath + ".Noder", "LikeConnection": genPath + ".LikeConnection", "LikeOrder": genPath + ".LikeOrder"}
		}
		for typ, model := range own {
			if models[typ] != model {
				t.Errorf("veloxe=%v: %s binds %q, want %q", veloxe, typ, models[typ], model)
			}
		}
		if strings.Contains(got, "extend ") {
			t.Errorf("veloxe=%v: an introspection bind in\n%s", veloxe, got)
		}
	}
	// gqlgen's: the scalars stay unbound, gqlgen.yml binds them.
	got, err := RenderConfig(modeltest.GraphQLGo(t), Config{Package: genPath})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "scalar Time @goModel") || strings.Contains(got, "type Account @goModel") {
		t.Errorf("gqlgen's SDL binds velox's types:\n%s", got)
	}
}

// TestGraphQLGoNeedsPackage: the models name the generated package, so
// engine=graphql-go without one is an error.
func TestGraphQLGoNeedsPackage(t *testing.T) {
	if _, err := RenderConfig(modeltest.GraphQLGo(t), Config{GraphQLGo: true}); err == nil || !strings.Contains(err.Error(), "engine=graphql-go") {
		t.Errorf("%v, want an error naming engine=graphql-go", err)
	}
}

// TestGraphQLGoRefusesNameBindings: an element gqlgen binds through
// @goField(name:) is refused under engine=graphql-go, naming the schema,
// the field or edge and the reason, as gqlc does not read the argument
// yet: a field graphql.FieldName renames, an edge it renames, a non-null
// bytes field, whose getter writes an empty blob as "", and a key whose Go
// name is not ID. gqlgen's SDL of the same model has them.
func TestGraphQLGoRefusesNameBindings(t *testing.T) {
	for name, c := range map[string]struct {
		edit func(*plugin.Model)
		want string
	}{
		"renamed field": {func(m *plugin.Model) { annotate(m, "User", "name", "", `{"field_name":"displayName"}`) }, `schema User, field name: engine=graphql-go: graphql.FieldName("displayName")`},
		"renamed edge":  {func(m *plugin.Model) { annotate(m, "Post", "", "author", `{"field_name":"writer"}`) }, `schema Post, edge author: engine=graphql-go: graphql.FieldName("writer")`},
		"bytes": {func(m *plugin.Model) {
			for _, e := range m.Entities {
				if e.Name == "User" {
					e.Fields = append(e.Fields, &plugin.Field{Name: "photo", GoName: "Photo", Type: "bytes", Column: "photo"})
				}
			}
		}, "schema User, field photo: engine=graphql-go: a non-null Bytes field needs its method PhotoGQL, which writes an empty blob as \"\" and not null, and gqlc does not yet honour"},
		"key": {func(m *plugin.Model) {
			for _, e := range m.Entities {
				if e.Name == "Category" {
					e.Fields[e.PrimaryKey[0]].GoName = "Code"
				}
			}
		}, "schema Category, field id: engine=graphql-go: the field id binds to the key's Go field Code"},
	} {
		t.Run(name, func(t *testing.T) {
			m := modeltest.GraphQLGo(t)
			c.edit(m)
			if _, err := RenderConfig(m, graphqlGo(false)); err == nil || !strings.Contains(err.Error(), c.want) || !strings.Contains(err.Error(), "gqlc does not yet honour @goField(name:)") {
				t.Errorf("%v, want an error with %q and the reason", err, c.want)
			}
			if _, err := BuildConfig(m, graphqlGo(false)); err == nil {
				t.Error("BuildConfig, which the Go is written from, takes it")
			}
			if got, err := RenderConfig(m, Config{}); err != nil || !strings.Contains(got, "@goField(name:") {
				t.Errorf("gqlgen's: %v", err)
			}
		})
	}
}

// TestGraphQLGoRefusesAllAtOnce: one run names every element
// engine=graphql-go refuses, and not the first alone.
func TestGraphQLGoRefusesAllAtOnce(t *testing.T) {
	m := modeltest.GraphQLGo(t)
	annotate(m, "User", "name", "", `{"field_name":"displayName"}`)
	annotate(m, "Post", "", "author", `{"field_name":"writer"}`)
	_, err := RenderConfig(m, graphqlGo(false))
	for _, want := range []string{"schema User, field name:", "schema Post, edge author:"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%v, want %q among the refusals", err, want)
		}
	}
}

// TestGraphQLGoForcesResolvers: under engine=graphql-go a field gqlgen reads
// through a getter, a JSON field or one of the application's own Go type,
// is on the Resolver, with @goField(forceResolver: true) and no name, and
// its getter is still written for the resolver to return.
func TestGraphQLGoForcesResolvers(t *testing.T) {
	s, err := RenderConfig(modeltest.GraphQLGo(t), graphqlGo(false))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(s, "  settings: JSON @goField(forceResolver: true)\n") {
		t.Errorf("no forced resolver for User.settings:\n%s", s)
	}
	if strings.Contains(s, "@goField(name:") {
		t.Error("a field bound by name")
	}
	surf, err := BuildConfig(modeltest.GraphQLGo(t), graphqlGo(false))
	if err != nil {
		t.Fatal(err)
	}
	var getters []string
	for _, ty := range surf.Types {
		for _, g := range ty.Getters {
			getters = append(getters, ty.Entity.Name+"."+g.Method)
		}
	}
	if !slices.Contains(getters, "User.SettingsGQL") {
		t.Errorf("getters %v, want User.SettingsGQL", getters)
	}
}

// annotate sets the graphql annotation of e's field or edge to raw.
func annotate(m *plugin.Model, e, field, edge, raw string) {
	for _, x := range m.Entities {
		if x.Name != e {
			continue
		}
		for _, f := range x.Fields {
			if f.Name == field {
				f.Annotations = map[string]json.RawMessage{"graphql": json.RawMessage(raw)}
			}
		}
		for _, ed := range x.Edges {
			if ed.Name == edge {
				ed.Annotations = map[string]json.RawMessage{"graphql": json.RawMessage(raw)}
			}
		}
	}
}
