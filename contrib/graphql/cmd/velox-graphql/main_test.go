package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/syssam/velox2/plugin"
)

func respond(t *testing.T, in string) plugin.Response {
	t.Helper()
	var out, errs bytes.Buffer
	if code := run(strings.NewReader(in), &out, &errs); code != 0 {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	var resp plugin.Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("%v: %q", err, out.String())
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

func TestRun(t *testing.T) {
	m := &plugin.Model{Version: plugin.ModelVersion, Entities: []*plugin.Entity{{
		Name: "User", Tenant: -1, PrimaryKey: []int{0},
		Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64"}, {Name: "name", GoName: "Name", Type: "string"}},
	}}}
	resp := respond(t, request(t, plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", Model: m}))
	if resp.Error != "" || len(resp.Files) != 1 || resp.Files[0].Path != "velox.graphqls" ||
		!strings.Contains(resp.Files[0].Content, "type User implements Node {\n") || strings.Contains(resp.Files[0].Content, "DO NOT EDIT") {
		t.Errorf("%+v", resp)
	}

	for name, c := range map[string]struct{ in, want string }{
		"protocol":  {request(t, plugin.Request{Protocol: 2, Model: m}), "protocol 2"},
		"version":   {request(t, plugin.Request{Protocol: 1, Model: &plugin.Model{Version: 2}}), "model version 2"},
		"no model":  {request(t, plugin.Request{Protocol: 1}), "no model"},
		"malformed": {"{", "request"},
		"render": {request(t, plugin.Request{Protocol: 1, Model: &plugin.Model{Version: 1, Entities: []*plugin.Entity{{
			Name: "User", Tenant: -1, PrimaryKey: []int{0},
			Fields: []*plugin.Field{{Name: "id", GoName: "ID", Type: "int64"}, {Name: "x", GoName: "X", Type: "other"}},
		}}}}), "User"},
	} {
		t.Run(name, func(t *testing.T) {
			resp := respond(t, c.in)
			if len(resp.Files) != 0 || !strings.Contains(resp.Error, c.want) {
				t.Errorf("%+v, want an error with %q", resp, c.want)
			}
		})
	}
}
