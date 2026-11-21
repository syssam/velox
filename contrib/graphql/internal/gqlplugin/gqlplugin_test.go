package gqlplugin

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

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
	// Go in noder_gql.go.
	resp = respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", OutPath: "example.com/app/gen", Options: map[string]string{"compat": "veloxe"}, Model: &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "User", Label: "user", Handles: "Users", PrimaryKey: []int{0},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64", ElemType: "int64"}, {Name: "image_url", GoName: "ImageURL", Type: "string", ElemType: "string"}},
	}}}}))
	if resp.Error != "" || len(resp.Files) != 3 || resp.Files[1].Path != "noder_gql.go" || !strings.Contains(resp.Files[0].Content, "  imageUrl: String!\n") ||
		!strings.Contains(resp.Files[0].Content, "interface Node @goModel(model: \"example.com/app/gen.Noder\")") {
		t.Errorf("compat=veloxe: %+v", resp)
	}

	for name, c := range map[string]struct{ in, want string }{
		"compat and only":    {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "compat": "veloxe"}, Model: m}), "give one or the other"},
		"whitelist and only": {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "where": "whitelist"}, Model: m}), "give one or the other"},
		"compat":             {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"compat": "ent"}, Model: m}), "it is given compat=ent"},
		"protocol":           {request(t, plugin.Request{Protocol: 1, Model: m}), "plugin protocol 1"},
		"version":            {request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 1}}), "model is version 1"},
		"no model":           {request(t, plugin.Request{Protocol: 2}), "no model"},
		"malformed":          {"{", "request"},
		"options":            {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"x": "y", "a": ""}, Model: m}), "velox-graphql takes the options only=enums, compat=veloxe and where=whitelist; it is given a, x=y"},
		"only":               {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "types"}, Model: m}), "it is given only=types"},
		"only and":           {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums", "x": ""}, Model: m}), "it is given only=enums, x"},
		"enums":              {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"only": "enums"}, Model: m}), "the request names no generated package"},
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
