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

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/plugin"
)

// compareGolden compares got with the file path, or rewrites it under
// -update.
func compareGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if want := readFile(t, path); got != want {
		t.Errorf("%s differs (run go test -update to accept):\n%s", path, got)
	}
}

// definitions are the names of the definitions of sdl, each with its
// fields' names, in a stable order.
func definitions(t *testing.T, sdl string) []string {
	t.Helper()
	doc, err := parser.ParseSchema(&ast.Source{Input: sdl})
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, d := range doc.Definitions {
		s := string(d.Kind) + " " + d.Name
		for _, f := range d.Fields {
			s += " " + f.Name
		}
		for _, v := range d.EnumValues {
			s += " " + v.Name
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// TestPerEntityGolden: under PerEntity the SDL is velox.graphql, with the
// vocabulary and Query, and velox_<label>.graphql for each entity, in name
// order; together they declare what the one file does, field for field.
func TestPerEntityGolden(t *testing.T) {
	m := modeltest.Model(t)
	files, _, err := RenderFiles(m, Config{PerEntity: true})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Path)
		compareGolden(t, filepath.Join("testdata", "perentity", f.Path), f.Content)
	}
	want := []string{"velox.graphql", "velox_category.graphql", "velox_group.graphql", "velox_like.graphql", "velox_post.graphql", "velox_user.graphql"}
	if !slices.Equal(names, want) {
		t.Fatalf("files %v, want %v", names, want)
	}
	if strings.Contains(files[0].Content, "type Post ") || !strings.Contains(files[0].Content, "type Query {") || !strings.HasPrefix(files[4].Content, "# Post\n") {
		t.Errorf("velox.graphql holds an entity's type, or Query is not in it:\n%s", files[0].Content)
	}
	one, err := Render(modeltest.Model(t))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := definitions(t, joinFiles(files)), definitions(t, one); !slices.Equal(got, want) {
		t.Errorf("the files declare\n%v\nwant the one file's\n%v", got, want)
	}
}

// TestPerEntityFileNames: an entity's file is velox_<schema name in lower
// case>.graphql, a Go package name for gqlc; two entities whose names
// differ only in case are refused.
func TestPerEntityFileNames(t *testing.T) {
	m := modeltest.Model(t)
	files, _, err := RenderFiles(m, Config{PerEntity: true, Node: "on", Package: "example.com/app/gen"})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Path)
	}
	if !slices.Contains(names, "velox_user.graphql") || !slices.Contains(names, "velox_post.graphql") || names[0] != "velox.graphql" {
		t.Errorf("files %v", names)
	}
	if !strings.Contains(files[0].Content, "interface Node") || !strings.Contains(files[0].Content, "node(id: ID!): Node") {
		t.Errorf("velox.graphql has no Node:\n%s", files[0].Content)
	}

	e := &emit{full: map[*object]bool{}, files: map[*object]string{}}
	g := &graph{}
	for _, name := range []string{"SalesOrder", "Salesorder"} {
		o := &object{ent: &plugin.Entity{Name: name, Label: strings.ToLower(name)}, name: name}
		g.objects = append(g.objects, o)
		e.full[o] = true
	}
	if err := e.name(g); err == nil || !strings.Contains(err.Error(), "schema SalesOrder and schema Salesorder both to velox_salesorder.graphql") {
		t.Errorf("err = %v", err)
	}
}

// TestPerEntitySharedEnum: under PerEntity an enum two entities' fields
// share is declared once, in velox.graphql, and in neither entity's file.
func TestPerEntitySharedEnum(t *testing.T) {
	m := modeltest.Model(t)
	for _, e := range m.Entities {
		if e.Name == "Category" {
			e.Fields = append(e.Fields, &plugin.Field{Name: "status", GoName: "Status", Column: "status", Type: "enum", GoType: "PostStatus", ElemType: "PostStatus", HasDefault: true,
				Enum: "PostStatus", EnumValues: []string{"draft", "in_review", "published"}, Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"enum_values": {"published": "LIVE"}}`)}})
		}
	}
	files, _, err := RenderFiles(m, Config{PerEntity: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		n := strings.Count(f.Content, "enum PostStatus {")
		if want := map[bool]int{true: 1, false: 0}[f.Path == "velox.graphql"]; n != want {
			t.Errorf("%s declares PostStatus %d times, want %d", f.Path, n, want)
		}
	}
	if !strings.Contains(files[1].Content, "  status: PostStatus!\n") {
		t.Errorf("Category has no status:\n%s", files[1].Content)
	}
}

// TestEntitiesGolden: Entities writes Post's SDL alone. Its edges to
// Account and Like are left out of its type, its Query field is the only
// one, and its where input keeps hasWriterWith, so AccountWhereInput is
// written, with UserRole, which it names, and no other of User's elements.
func TestEntitiesGolden(t *testing.T) {
	got, err := RenderConfig(modeltest.Model(t), Config{Entities: []string{"Post"}})
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, filepath.Join("testdata", "entities_post.graphql"), got)
	for _, absent := range []string{"type Account ", "type Like ", "  writer: ", "  likes: ", "AccountConnection", "CreateAccountInput", "categories(", "type Category", "input AccountOrder"} {
		if strings.Contains(got, absent) {
			t.Errorf("the SDL has %q", absent)
		}
	}
	for _, present := range []string{"input AccountWhereInput", "hasWriterWith: [AccountWhereInput!]", "related(", "posts(", "enum PostStatus", "type Post "} {
		if !strings.Contains(got, present) {
			t.Errorf("the SDL has no %q", present)
		}
	}
	// AccountWhereInput's enum, if it names one, is declared.
	if strings.Contains(got, ": UserRole") != strings.Contains(got, "enum UserRole") {
		t.Errorf("UserRole is named and not declared, or declared and not named")
	}
}

// TestEntitiesPerEntity: Entities and PerEntity together write a file for
// each entity chosen and for each whose inputs alone a written input names.
func TestEntitiesPerEntity(t *testing.T) {
	files, _, err := RenderFiles(modeltest.Model(t), Config{Entities: []string{"Post"}, PerEntity: true})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range files {
		names = append(names, f.Path)
	}
	if want := []string{"velox.graphql", "velox_post.graphql"}; !slices.Equal(names, want) {
		t.Fatalf("files %v, want %v", names, want)
	}
	if c := files[0].Content; !strings.Contains(c, "# User: only the inputs another written input names\n") || !strings.Contains(c, "input AccountWhereInput") || strings.Contains(c, "type Account") {
		t.Errorf("velox.graphql:\n%s", c)
	}
}

// appTypesModel is Asset, whose price is the application's scalar Money and
// whose resolvers name the application's PublicUser, Asset itself and Tag,
// an entity apptypes= tests leave out.
func appTypesModel(t *testing.T, priceWhere bool) *plugin.Model {
	price := field("price", "float64")
	as := []graphql.Annotation{graphql.Type("Money")}
	if priceWhere {
		as = append(as, graphql.WhereOps(graphql.OpsEquality))
	}
	price.Annotations = ann(t, as...)
	name := field("name", "string")
	name.Annotations = ann(t, graphql.WhereOps(graphql.OpsEquality))
	a := entity("Asset", name, price)
	a.Annotations = ann(t, graphql.QueryField(), graphql.Resolvers(
		graphql.Map("owner", "PublicUser"),
		graphql.Map("history(first: Int)", "[PublicChange!]!"),
		graphql.Map("related", "[Asset!]"),
		graphql.Map("tags", "[Tag!]"),
	))
	return model(a, entity("Tag", field("label", "string")))
}

// TestEntitiesAppTypes: under apptypes=Money a field of Asset that names an
// application type it does not list, PublicUser or PublicChange, is left
// out; price, of the listed Money, and related, of Asset, stay. tags is left
// out as before, as Tag is not written.
func TestEntitiesAppTypes(t *testing.T) {
	m := appTypesModel(t, true)
	got, err := RenderConfig(m, Config{Entities: []string{"Asset"}, AppTypes: []string{"Money"}})
	if err != nil {
		t.Fatal(err)
	}
	compareGolden(t, filepath.Join("testdata", "entities_apptypes.graphql"), got)
	for _, absent := range []string{"  owner: ", "  history(", "  tags: ", "PublicUser", "PublicChange"} {
		if strings.Contains(got, absent) {
			t.Errorf("the SDL has %q", absent)
		}
	}
	for _, present := range []string{"  price: Money!", "  related: [Asset!]", "input AssetWhereInput", "  price: Money\n"} {
		if !strings.Contains(got, present) {
			t.Errorf("the SDL has no %q", present)
		}
	}
	if _, err := Check(got, `scalar Money`); err != nil {
		t.Errorf("the SDL beside scalar Money alone: %v", err)
	}

	// Without apptypes= every application type stays, as ADR 0125 has it.
	all, err := RenderConfig(m, Config{Entities: []string{"Asset"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, present := range []string{"  owner: PublicUser", "  history(first: Int): [PublicChange!]!"} {
		if !strings.Contains(all, present) {
			t.Errorf("without apptypes= the SDL has no %q", present)
		}
	}
}

// TestEntitiesAppTypesRefused: an input that names an application type
// apptypes= does not list is refused, as graphql-go binds every field of an
// input; so are a name velox-graphql declares, other than a scalar of its
// vocabulary, and one that is no GraphQL name. Without entities= apptypes=
// only leaves the vocabulary's scalars it lists to the application.
func TestEntitiesAppTypesRefused(t *testing.T) {
	for _, c := range []struct {
		m        *plugin.Model
		entities []string
		apptypes []string
		want     string
	}{
		{appTypesModel(t, true), []string{"Asset"}, []string{"PublicUser"}, "AssetWhereInput.price names Money, which apptypes= does not list"},
		{appTypesModel(t, false), []string{"Asset"}, []string{"Tag"}, "apptypes= names Tag, which velox-graphql declares itself"},
		{appTypesModel(t, false), []string{"Asset"}, []string{"Cursor"}, "apptypes= names Cursor, which velox-graphql declares itself"},
		{appTypesModel(t, false), []string{"Asset"}, []string{"9x"}, `apptypes= names "9x", which is not a GraphQL name`},
	} {
		_, err := RenderConfig(c.m, Config{Entities: c.entities, AppTypes: c.apptypes})
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want %q", c.apptypes, err, c.want)
		}
	}
	if got, err := RenderConfig(appTypesModel(t, false), Config{AppTypes: []string{"Money", "Time"}}); err != nil || strings.Contains(got, "scalar Time") || !strings.Contains(got, "  owner: PublicUser") {
		t.Errorf("apptypes=Money+Time without entities=: %v\n%s", err, got)
	}
	// Without a where on price, Money is named by Asset's type alone, and
	// apptypes=PublicUser leaves price out with owner kept.
	got, err := RenderConfig(appTypesModel(t, false), Config{Entities: []string{"Asset"}, AppTypes: []string{"PublicUser"}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "  price: ") || !strings.Contains(got, "  owner: PublicUser") {
		t.Errorf("apptypes=PublicUser:\n%s", got)
	}
}

// TestEntitiesRefused: a name the schema package does not declare, and an
// entity with no GraphQL type, are refused.
func TestEntitiesRefused(t *testing.T) {
	for _, c := range []struct {
		names []string
		want  string
	}{
		{[]string{"Post", "Nope"}, "entities= names Nope, which the schema package does not declare"},
		{[]string{"PostRelated"}, "entities= names PostRelated, which has no GraphQL type"},
	} {
		if _, err := RenderConfig(modeltest.Model(t), Config{Entities: c.names}); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: err = %v, want %q", c.names, err, c.want)
		}
	}
}
