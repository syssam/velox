package gqlplugin

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox/plugin"
)

func respond(t *testing.T, in string) plugin.Response {
	t.Helper()
	var out bytes.Buffer
	if err := plugin.Run(Supports, strings.NewReader(in), &out, Generate); err != nil {
		t.Fatal(err)
	}
	var resp plugin.Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("%v: %q", err, out.String())
	}
	if resp.Protocol != plugin.ProtocolVersion {
		t.Errorf("the response says protocol %d", resp.Protocol)
	}
	return resp
}

func request(t *testing.T, r plugin.Request) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSupportsThisVelox(t *testing.T) {
	if Supports.Protocol != plugin.ProtocolVersion || Supports.Model != plugin.ModelVersion {
		t.Errorf("velox-graphql is written for protocol %d and model %d; velox has %d and %d: update it", Supports.Protocol, Supports.Model, plugin.ProtocolVersion, plugin.ModelVersion)
	}
}

func TestRun(t *testing.T) {
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", ElemType: "int64"}, {Name: "name", GoName: "Name", Type: "string", ElemType: "string"}},
	}}}
	resp := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, Generator: "velox-gen (devel)", OutPackage: "gen", OutPath: "example.com/app/gen", Model: m}))
	var paths []string
	for _, f := range resp.Files {
		paths = append(paths, f.Path)
		if strings.Contains(f.Content, "DO NOT EDIT") {
			t.Errorf("%s has the generated-file header, which velox-gen adds", f.Path)
		}
	}
	if resp.Error != "" || len(resp.Warnings) != 0 || strings.Join(paths, " ") != "velox.graphql user_gql.go" ||
		!strings.Contains(resp.Files[0].Content, "type User {\n  id: ID!\n") ||
		!strings.HasPrefix(resp.Files[1].Content, "package gen\n") || !strings.Contains(resp.Files[1].Content, "type UserConnection struct {") {
		t.Errorf("%+v", resp)
	}

	// only=enums: the enum marshalers alone, of a model whose User has a
	// role; no velox.graphql.
	em := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64"}, {Name: "role", GoName: "Role", Type: "enum", Enum: "UserRole", EnumValues: []string{"admin", "viewer"}}},
	}}, Enums: []*plugin.Enum{{Type: "UserRole", Values: []string{"admin", "viewer"}, Consts: []string{"UserRoleAdmin", "UserRoleViewer"}}}}
	resp = respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"only": "enums"}, Model: em}))
	if resp.Error != "" || len(resp.Files) != 1 || resp.Files[0].Path != "user_gql.go" ||
		!strings.Contains(resp.Files[0].Content, "func (e UserRole) MarshalGQL(") || strings.Contains(resp.Files[0].Content, "UserConnection") {
		t.Errorf("only=enums: %+v", resp)
	}

	// compat=veloxe: veloxe's names, here the key of a field named from
	// its schema name, and Node, bound to the request's package, with its
	// Go in noder_gql.go; and veloxe's files, velox.graphql and
	// velox_user.graphql, unless sdlfiles=one.
	vm := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", ElemType: "int64"}, {Name: "image_url", GoName: "ImageURL", Type: "string", ElemType: "string"}},
	}}}
	resp = respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"compat": "veloxe"}, Model: vm}))
	if resp.Error != "" || len(resp.Files) != 4 || resp.Files[1].Path != "velox_user.graphql" || resp.Files[2].Path != "noder_gql.go" ||
		!strings.Contains(resp.Files[1].Content, "  imageUrl: String!\n") ||
		!strings.Contains(resp.Files[0].Content, "interface Node @goModel(model: \"example.com/app/gen.Noder\")") {
		t.Errorf("compat=veloxe: %+v", resp)
	}
	resp = respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"compat": "veloxe", "sdlfiles": "one"}, Model: vm}))
	if resp.Error != "" || len(resp.Files) != 3 || resp.Files[0].Path != "velox.graphql" || !strings.Contains(resp.Files[0].Content, "  imageUrl: String!\n") {
		t.Errorf("compat=veloxe,sdlfiles=one: %+v", resp)
	}

	// sdlfiles=entity without compat: velox_<label>.graphql. entities=User
	// writes User's SDL alone, and the same Go.
	resp = respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"sdlfiles": "entity"}, Model: m}))
	if resp.Error != "" || len(resp.Files) != 3 || resp.Files[0].Path != "velox.graphql" || resp.Files[1].Path != "velox_user.graphql" ||
		!strings.HasPrefix(resp.Files[1].Content, "# User\n") || strings.Contains(resp.Files[0].Content, "type User ") {
		t.Errorf("sdlfiles=entity: %+v", resp)
	}
	resp = respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"entities": "User"}, Model: m}))
	if resp.Error != "" || len(resp.Files) != 2 || !reflect.DeepEqual(resp.Files, respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Model: m})).Files) {
		t.Errorf("entities=User: %+v", resp)
	}

	// engine: graphql-go's SDL binds User with @goModel and its Go is the
	// same files; engine=gqlgen is the default's bytes; only=enums takes
	// either.
	gg := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"engine": "graphql-go"}, Model: m}))
	if gg.Error != "" || len(gg.Files) != 2 || !strings.Contains(gg.Files[0].Content, "type User @goModel(model: \"example.com/app/gen.User\") {\n") || gg.Files[1].Path != "user_gql.go" {
		t.Errorf("engine=graphql-go: %+v", gg)
	}
	def := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Model: m}))
	if eg := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"engine": "gqlgen"}, Model: m})); !reflect.DeepEqual(eg, def) {
		t.Errorf("engine=gqlgen: %+v, want the default's %+v", eg, def)
	}
	if en := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"only": "enums", "engine": "graphql-go"}, Model: em})); en.Error != "" || len(en.Files) != 1 {
		t.Errorf("only=enums,engine=graphql-go: %+v", en)
	}

	for name, c := range map[string]struct{ in, want string }{
		"compat and only":    {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "compat": "veloxe"}, Model: m}), "give one or the other"},
		"whitelist and only": {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "where": "whitelist"}, Model: m}), "give one or the other"},
		"compat":             {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"compat": "ent"}, Model: m}), "it is given compat=ent"},
		"protocol":           {request(t, plugin.Request{Protocol: 1, Model: m}), "plugin protocol 1"},
		"version":            {request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 1}}), "model is version 1"},
		"no model":           {request(t, plugin.Request{Protocol: 2}), "no model"},
		"malformed":          {"{", "request"},
		"options":            {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"x": "y", "a": ""}, Model: m}), "velox-graphql takes the options only=enums, compat=veloxe, where=whitelist, engine=gqlgen or engine=graphql-go, sdlfiles=one or sdlfiles=entity, and entities=A+B; it is given a, x=y"},
		"sdlfiles":           {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"sdlfiles": "type"}, Model: m}), "it is given sdlfiles=type"},
		"sdlfiles and only":  {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "sdlfiles": "entity"}, Model: m}), "give one or the other"},
		"entities and only":  {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "entities": "User"}, Model: m}), "give one or the other"},
		"entities empty":     {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"entities": ""}, Model: m}), "it is given entities"},
		"entities twice":     {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"entities": "User+User"}, Model: m}), "entities=User+User: name each entity once, separated by +"},
		"entities gap":       {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"entities": "User++Post"}, Model: m}), "name each entity once"},
		"entities unknown":   {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"entities": "User+Post"}, Model: m}), "entities= names Post, which the schema package does not declare"},
		"engine":             {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"engine": "graphql-go-v2"}, Model: m}), "it is given engine=graphql-go-v2"},
		"engine no package":  {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"engine": "graphql-go"}, Model: m}), "engine=graphql-go binds every type to the generated package, and the request names none"},
		"engine refuses": {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"engine": "graphql-go"}, Model: &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
			Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", ElemType: "int64"}, {Name: "name", GoName: "Name", Type: "string", ElemType: "string", Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"field_name":"displayName"}`)}}},
		}}}}), "schema User, field name: engine=graphql-go: graphql.FieldName(\"displayName\") binds it to its Go field Name through @goField(name:), and gqlc does not yet honour @goField(name:)"},
		"only":     {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "types"}, Model: m}), "it is given only=types"},
		"only and": {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "x": ""}, Model: m}), "it is given only=enums, x"},
		"enums":    {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums"}, Model: m}), "the request names no generated package"},
		"render": {request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "User", PrimaryKey: []int{0},
			Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64"}, {Name: "x", GoName: "X", Type: "other"}},
		}}}}), "User"},
		"go": {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Model: &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "Thing", PrimaryKey: []int{0},
			Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64"}, {Name: "x", GoName: "X", Type: "string"}},
		}}}}), "schema Thing: the model gives it no label or no handles"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := respond(t, c.in)
			if len(resp.Files) != 0 || !strings.Contains(resp.Error, c.want) {
				t.Errorf("%+v, want an error with %q", resp, c.want)
			}
		})
	}
}

// TestUnfilledWarning: an M2M edge whose edge schema has a column Add cannot
// fill is written without add inputs, and velox-graphql says so, once per
// edge, for either engine, naming fields by their Go names. The edge whose
// edge schema fills every column draws no warning, and neither does an edge
// of a type with no create or update input; a create input alone is enough.
func TestUnfilledWarning(t *testing.T) {
	generate := func(m *plugin.Model, opts map[string]string) []string {
		t.Helper()
		files, warnings, err := Generate(&plugin.Request{Protocol: 2, OutPackage: "gen", OutPath: "example.com/app/gen", Options: opts, Model: m})
		if err != nil {
			t.Fatal(err)
		}
		if len(files) == 0 {
			t.Fatal("no files")
		}
		return warnings
	}
	want := []string{
		"User.following: no add inputs; Follow.Note has no default",
		"User.followers: no add inputs; Follow.Note has no default",
	}
	for _, opts := range []map[string]string{nil, {"engine": "graphql-go"}, {"compat": "veloxe"}} {
		if got := generate(modeltest.EdgeSchema(t), opts); !reflect.DeepEqual(got, want) {
			t.Errorf("%v: warnings %q, want %q", opts, got, want)
		}
	}
	m := modeltest.EdgeSchema(t)
	for _, e := range m.Entities {
		for _, ed := range e.Edges {
			if ed.Name == "following" {
				ed.Unfilled = append(ed.Unfilled, "id")
			}
		}
	}
	if got, want := generate(m, nil), "User.following: no add inputs; Follow.Note, Follow.ID have no default"; len(got) == 0 || got[0] != want {
		t.Errorf("warnings %q, want %q first", got, want)
	}
	for ann, want := range map[string][]string{`{"query_field": "users"}`: nil, `{"query_field": "users", "mutations": ["create"]}`: want} {
		m = modeltest.EdgeSchema(t)
		for _, e := range m.Entities {
			if e.Name == "User" {
				e.Annotations["graphql"] = json.RawMessage(ann)
			}
		}
		if got := generate(m, nil); !reflect.DeepEqual(got, want) {
			t.Errorf("User %s: warnings %q, want %q", ann, got, want)
		}
	}
}
