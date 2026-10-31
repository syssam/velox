package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/syssam/velox/plugin"
)

// supports is what main passes plugin.Main.
var supports = plugin.Supports{Protocol: 2, Model: 2}

func respond(t *testing.T, in string) plugin.Response {
	t.Helper()
	var out bytes.Buffer
	if err := plugin.Run(supports, strings.NewReader(in), &out, generate); err != nil {
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
	if supports.Protocol != plugin.ProtocolVersion || supports.Model != plugin.ModelVersion {
		t.Errorf("velox-graphql is written for protocol %d and model %d; velox has %d and %d: update it", supports.Protocol, supports.Model, plugin.ProtocolVersion, plugin.ModelVersion)
	}
}

func TestRun(t *testing.T) {
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "User", Label: "user", Handles: "Users", Tenant: -1, PrimaryKey: []int{0},
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
	if resp.Error != "" || len(resp.Warnings) != 0 || strings.Join(paths, " ") != "velox.graphqls gql_node.go gql_user.go" ||
		!strings.Contains(resp.Files[0].Content, "type User implements Node {\n") ||
		!strings.HasPrefix(resp.Files[2].Content, "package gen\n") || !strings.Contains(resp.Files[2].Content, "type UserConnection struct {") {
		t.Errorf("%+v", resp)
	}

	for name, c := range map[string]struct{ in, want string }{
		"protocol":  {request(t, plugin.Request{Protocol: 1, Model: m}), "plugin protocol 1"},
		"version":   {request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 1}}), "model is version 1"},
		"no model":  {request(t, plugin.Request{Protocol: 2}), "no model"},
		"malformed": {"{", "request"},
		"options":   {request(t, plugin.Request{Protocol: 2, Options: map[string]string{"x": "y", "a": ""}, Model: m}), "velox-graphql takes no options; it is given a, x"},
		"render": {request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "User", Tenant: -1, PrimaryKey: []int{0},
			Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64"}, {Name: "x", GoName: "X", Type: "other"}},
		}}}}), "User"},
		"go": {request(t, plugin.Request{Protocol: 2, OutPackage: "gen", Model: &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
			Name: "Node", Label: "node", Handles: "Nodes", Tenant: -1, PrimaryKey: []int{0},
			Fields:      []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64"}, {Name: "x", GoName: "X", Type: "string"}},
			Annotations: map[string]json.RawMessage{"graphql": json.RawMessage(`{"type":"Vertex"}`)},
		}}}}), "gql_node.go"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := respond(t, c.in)
			if len(resp.Files) != 0 || !strings.Contains(resp.Error, c.want) {
				t.Errorf("%+v, want an error with %q", resp, c.want)
			}
		})
	}
}
