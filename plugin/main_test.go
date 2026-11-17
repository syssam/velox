package plugin_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/syssam/velox/plugin"
)

// run is plugin.Run on the request in, and the response it writes, decoded,
// with the raw JSON.
func run(t *testing.T, s plugin.Supports, in string, gen plugin.GenerateFunc) (plugin.Response, map[string]json.RawMessage) {
	t.Helper()
	var out bytes.Buffer
	if err := plugin.Run(s, strings.NewReader(in), &out, gen); err != nil {
		t.Fatal(err)
	}
	var resp plugin.Response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("%v: %q", err, out.String())
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	return resp, raw
}

func request(t *testing.T, r plugin.Request) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var current = plugin.Supports{Protocol: 2, Model: 3}

func good(t *testing.T) string {
	return request(t, plugin.Request{Protocol: 2, Generator: "velox-gen (devel)", OutPackage: "gen", Model: &plugin.Model{Version: 3}})
}

func TestVersions(t *testing.T) {
	if plugin.ProtocolVersion != 2 || plugin.ModelVersion != 3 {
		t.Errorf("ProtocolVersion %d, ModelVersion %d; want 2 and 3", plugin.ProtocolVersion, plugin.ModelVersion)
	}
}

// TestRunChecksLiterals pins Run's refusals: the plugin's own literals, then
// the request against them. gen is never called for one.
func TestRunChecksLiterals(t *testing.T) {
	cases := []struct {
		name string
		s    plugin.Supports
		in   string
		want string
	}{
		{"plugin written for protocol 1", plugin.Supports{Protocol: 1, Model: 3}, good(t),
			"this plugin was written for plugin protocol 1; the velox it is built with speaks 2: update the plugin"},
		{"plugin written for model 1", plugin.Supports{Protocol: 2, Model: 1}, good(t),
			"this plugin was written for model 1; the velox it is built with has model 3: update the plugin"},
		{"plugin written for protocol 3", plugin.Supports{Protocol: 3, Model: 3}, good(t),
			"this plugin was written for plugin protocol 3; the velox it is built with speaks 2: build it with a newer velox"},
		{"plugin written for model 4", plugin.Supports{Protocol: 2, Model: 4}, good(t),
			"this plugin was written for model 4; the velox it is built with has model 3: build it with a newer velox"},
		{"request of protocol 1", current, request(t, plugin.Request{Protocol: 1, Model: &plugin.Model{Version: 3}}),
			"the request is plugin protocol 1; this plugin speaks 2"},
		{"no model", current, request(t, plugin.Request{Protocol: 2}), "the request has no model"},
		{"model 1", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 1}}),
			"the model is version 1; this plugin reads 3"},
		{"malformed", current, "{", "reading the request"},
		{"null entity", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{nil}}}),
			"the model is malformed: entity 0 is null"},
		{"key out of fields", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{
			{Name: "User", Fields: []*plugin.Field{{Name: "id"}}, PrimaryKey: []int{7}}}}}),
			"the model is malformed: entity User: primary key 7 is not one of its 1 fields"},
		{"null field", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{
			{Name: "User", Fields: []*plugin.Field{nil}}}}}),
			"entity User: field 0 is null"},
		{"index of no field", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{
			{Name: "User", Indexes: []*plugin.Index{{Name: "u", Fields: []string{"x"}}}}}}}),
			"entity User: index u: no field x"},
		{"null index", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{
			{Name: "User", Indexes: []*plugin.Index{nil}}}}}),
			"entity User: index 0 is null"},
		{"null edge", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{
			{Name: "User", Edges: []*plugin.Edge{nil}}}}}),
			"entity User: edge 0 is null"},
		{"no target", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{
			{Name: "User", Edges: []*plugin.Edge{{Name: "pets", Target: "Pet"}}}}}}),
			"entity User: edge pets: no target entity Pet"},
		{"no pair", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{
			{Name: "User", Edges: []*plugin.Edge{{Name: "friends", Target: "User", Ref: "x"}}}}}}),
			"edge friends: its pair x is not an edge of User"},
		{"no join", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{
			{Name: "User", Edges: []*plugin.Edge{{Name: "friends", Target: "User", Join: "J"}}}}}}),
			"edge friends: no join entity J"},
		{"no key holder", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{
			{Name: "User", Edges: []*plugin.Edge{{Name: "boss", Target: "User", Field: "boss_id", FieldOn: "X"}}}}}}),
			"edge boss: no entity X, which holds its key"},
		{"no key", current, request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{
			{Name: "User", Edges: []*plugin.Edge{{Name: "boss", Target: "User", Field: "boss_id", FieldOn: "User"}}}}}}),
			"edge boss: its key boss_id is not a field of User"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			called := false
			resp, _ := run(t, c.s, c.in, func(*plugin.Request) ([]plugin.File, []string, error) {
				called = true
				return []plugin.File{{Path: "x.go"}}, nil, nil
			})
			if called {
				t.Error("gen was called")
			}
			if !strings.Contains(resp.Error, c.want) || len(resp.Files) != 0 || resp.Protocol != plugin.ProtocolVersion {
				t.Errorf("%+v, want an error with %q", resp, c.want)
			}
		})
	}
}

// TestRunEchoesProtocol pins that every response says its protocol, which is
// how velox-gen tells a plugin of another protocol apart.
func TestRunEchoesProtocol(t *testing.T) {
	var got *plugin.Request
	resp, raw := run(t, current, good(t), func(req *plugin.Request) ([]plugin.File, []string, error) {
		got = req
		return []plugin.File{{Path: "pb/x.proto", Content: "syntax = \"proto3\";\n"}}, nil, nil
	})
	if string(raw["plugin_protocol"]) != "2" || resp.Error != "" || len(resp.Files) != 1 || resp.Files[0].Path != "pb/x.proto" {
		t.Errorf("%s", raw)
	}
	if got == nil || got.Generator != "velox-gen (devel)" || got.OutPackage != "gen" {
		t.Errorf("gen got %+v", got)
	}
	// An error response says it too.
	_, raw = run(t, current, "{", nil)
	if string(raw["plugin_protocol"]) != "2" {
		t.Errorf("error response: %s", raw)
	}
}

func TestRunPassesWarnings(t *testing.T) {
	resp, raw := run(t, current, good(t), func(*plugin.Request) ([]plugin.File, []string, error) {
		return []plugin.File{{Path: "x.go", Content: "package gen\n"}}, []string{"field x: no type, skipped"}, nil
	})
	if len(resp.Warnings) != 1 || resp.Warnings[0] != "field x: no type, skipped" || len(resp.Files) != 1 {
		t.Errorf("%s", raw)
	}
	if _, ok := raw["warnings"]; !ok {
		t.Errorf("no warnings key: %s", raw)
	}

	// An error drops the files and keeps the warnings.
	resp, _ = run(t, current, good(t), func(*plugin.Request) ([]plugin.File, []string, error) {
		return []plugin.File{{Path: "x.go"}}, []string{"w"}, errors.New("cannot")
	})
	if resp.Error != "cannot" || len(resp.Files) != 0 || len(resp.Warnings) != 1 {
		t.Errorf("%+v", resp)
	}

	// No warnings: no key.
	_, raw = run(t, current, good(t), func(*plugin.Request) ([]plugin.File, []string, error) { return nil, nil, nil })
	if _, ok := raw["warnings"]; ok {
		t.Errorf("warnings key without warnings: %s", raw)
	}
}

func TestRunOptions(t *testing.T) {
	in := request(t, plugin.Request{Protocol: 2, Options: map[string]string{"go_package": "x/pb"}, Model: &plugin.Model{Version: 3}})
	var opts map[string]string
	run(t, current, in, func(req *plugin.Request) ([]plugin.File, []string, error) {
		opts = req.Options
		return nil, nil, nil
	})
	if opts["go_package"] != "x/pb" {
		t.Errorf("options %v", opts)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestRunWriteError(t *testing.T) {
	if err := plugin.Run(current, strings.NewReader(good(t)), failWriter{}, func(*plugin.Request) ([]plugin.File, []string, error) { return nil, nil, nil }); err == nil {
		t.Error("no error for an output that cannot be written")
	}
}

func TestRunRecoversPanic(t *testing.T) {
	panics := func(*plugin.Request) ([]plugin.File, []string, error) { panic("index out of range") }
	t.Setenv("VELOX_PLUGIN_TRACE", "")
	resp, _ := run(t, current, good(t), panics)
	if resp.Error != "the plugin panicked: index out of range" || len(resp.Files) != 0 || resp.Protocol != plugin.ProtocolVersion {
		t.Errorf("%+v", resp)
	}
	// VELOX_PLUGIN_TRACE=1 adds the stack, which names the panicking line.
	t.Setenv("VELOX_PLUGIN_TRACE", "1")
	resp, _ = run(t, current, good(t), panics)
	if !strings.HasPrefix(resp.Error, "the plugin panicked: index out of range\n") || !strings.Contains(resp.Error, "main_test.go") || len(resp.Files) != 0 {
		t.Errorf("traced: %+v", resp)
	}
}

// TestRunWholeModel: a model whose every reference holds, a foreign key on
// the other entity, a pair, a join entity and an index, reaches gen.
func TestRunWholeModel(t *testing.T) {
	user := &plugin.Entity{Name: "User", Fields: []*plugin.Field{{Name: "id"}, {Name: "email"}}, PrimaryKey: []int{0},
		Indexes: []*plugin.Index{{Name: "user_email", Fields: []string{"email"}}},
		Edges: []*plugin.Edge{
			{Name: "posts", Target: "Post", Ref: "author", Kind: plugin.O2M, Field: "author_id", FieldOn: "Post"},
			{Name: "groups", Target: "Group", Kind: plugin.M2M, Join: "UserGroup"},
		}}
	post := &plugin.Entity{Name: "Post", Fields: []*plugin.Field{{Name: "id"}, {Name: "author_id"}}, PrimaryKey: []int{0},
		Edges: []*plugin.Edge{{Name: "author", Target: "User", Ref: "posts", Kind: plugin.M2O, Field: "author_id", FieldOn: "Post", OwnsKey: true}}}
	group := &plugin.Entity{Name: "Group", Fields: []*plugin.Field{{Name: "id"}}, PrimaryKey: []int{0}}
	join := &plugin.Entity{Name: "UserGroup", Join: true, Fields: []*plugin.Field{{Name: "user_id"}, {Name: "group_id"}}, PrimaryKey: []int{0, 1}}
	in := request(t, plugin.Request{Protocol: 2, Model: &plugin.Model{Version: 3, Entities: []*plugin.Entity{group, post, user, join}}})
	called := false
	resp, _ := run(t, current, in, func(*plugin.Request) ([]plugin.File, []string, error) {
		called = true
		return nil, nil, nil
	})
	if !called || resp.Error != "" {
		t.Errorf("gen called %v, error %q", called, resp.Error)
	}
}
