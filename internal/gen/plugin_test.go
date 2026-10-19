package gen

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/syssam/velox2/plugin"
)

// blogModel loads the golden blog schema and lowers it, as TestGolden does.
func blogModel(t *testing.T) (*Graph, *Out) {
	t.Helper()
	dir := filepath.Join("testdata", "golden", "blog")
	files := map[string]string{}
	matches, err := filepath.Glob(filepath.Join(dir, "schema", "*.go"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("no schema files: %v", err)
	}
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		_, src, _ := bytes.Cut(b, []byte("\n")) // the //go:build ignore line
		files["schema/"+filepath.Base(p)] = string(src)
	}
	m, err := Load(module(t, files), "./schema", offline())
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	o, err := Lower(g, Config{Package: "gen", Bound: map[string]Bound{"Post": {Policy: true}}})
	if err != nil {
		t.Fatal(err)
	}
	return g, o
}

func pluginEntity(t *testing.T, m *plugin.Model, name string) *plugin.Entity {
	t.Helper()
	for _, e := range m.Entities {
		if e.Name == name {
			return e
		}
	}
	t.Fatalf("no entity %s", name)
	return nil
}

func pluginEdge(t *testing.T, e *plugin.Entity, name string) *plugin.Edge {
	t.Helper()
	for _, ed := range e.Edges {
		if ed.Name == name {
			return ed
		}
	}
	t.Fatalf("no edge %s.%s", e.Name, name)
	return nil
}

func pluginField(t *testing.T, e *plugin.Entity, name string) *plugin.Field {
	t.Helper()
	for _, f := range e.Fields {
		if f.Name == name {
			return f
		}
	}
	t.Fatalf("no field %s.%s", e.Name, name)
	return nil
}

// TestPluginModelGolden pins the JSON a plugin receives for the blog. Run it
// with -update to write testdata/golden/blog/plugin.json.
func TestPluginModelGolden(t *testing.T) {
	g, o := blogModel(t)
	m := PluginModel(g, o)

	if m.Version != plugin.Version || m.SchemaPath != o.SchemaPath {
		t.Fatalf("model %d %q", m.Version, m.SchemaPath)
	}
	user, post := pluginEntity(t, m, "User"), pluginEntity(t, m, "Post")
	if posts := pluginEdge(t, user, "posts"); posts.Kind != "O2M" || posts.Field != "author_id" || posts.Target != "Post" || posts.Ref != "author" || posts.Join != "" {
		t.Fatalf("posts %+v", posts)
	}
	if author := pluginEdge(t, post, "author"); author.Kind != "M2O" || author.Field != "author_id" || !author.Unique || !author.Required || !author.Inverse {
		t.Fatalf("author %+v", author)
	}
	groups := pluginEdge(t, user, "groups")
	if groups.Kind != "M2M" || groups.Join != "UserGroup" || groups.Field != "" {
		t.Fatalf("groups %+v", groups)
	}
	join := pluginEntity(t, m, groups.Join)
	if !join.Join || join.Table != "user_groups" || len(join.Edges) != 0 || len(join.PrimaryKey) != 2 {
		t.Fatalf("join %+v", join)
	}
	if id := pluginField(t, user, "id"); !id.AutoID || id.Type != "int64" || id.GoType != "int64" || id.Pointer {
		t.Fatalf("id %+v", id)
	}
	if user.Tenant < 0 || user.Fields[user.Tenant].Name != "org_id" {
		t.Fatalf("user tenant %d", user.Tenant)
	}
	if post.Tenant != -1 || join.Tenant != -1 {
		t.Fatalf("post tenant %d, join tenant %d: want -1", post.Tenant, join.Tenant)
	}
	if role := pluginField(t, user, "role"); role.Enum != "UserRole" || role.Type != "enum" || !role.HasDefault {
		t.Fatalf("role %+v", role)
	}
	if pluginField(t, user, "name").Enum != "" {
		t.Fatal("a string field names an enum")
	}
	if pw := pluginField(t, user, "password_hash"); !pw.Sensitive {
		t.Fatalf("password_hash %+v", pw)
	}
	if bio := pluginField(t, user, "bio"); !bio.Nillable || !bio.Pointer || bio.GoType != "*string" || bio.ElemType != "string" {
		t.Fatalf("bio %+v", bio)
	}
	if len(m.Enums) != len(o.Enums) || m.Enums[0].Type != o.Enums[0].Type {
		t.Fatalf("enums %+v", m.Enums)
	}
	if u := pluginEntity(t, m, "User"); u.Handles != "Users" || u.Label != "user" {
		t.Fatalf("user %+v", u)
	}
	if len(m.Entities) != len(o.Entities) {
		t.Fatalf("%d entities, want %d", len(m.Entities), len(o.Entities))
	}

	got, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	path := filepath.Join("testdata", "golden", "blog", "plugin.json")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("plugin.json differs at line %d; run with -update", firstDiff(string(got), string(want)))
	}
}

// TestPluginModelRoundTrip checks that the model round-trips through JSON.
func TestPluginModelRoundTrip(t *testing.T) {
	g, o := blogModel(t)
	b, err := json.Marshal(plugin.Request{Protocol: plugin.Version, OutPackage: "gen", Model: PluginModel(g, o)})
	if err != nil {
		t.Fatal(err)
	}
	var back plugin.Request
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	b2, _ := json.Marshal(back)
	if !bytes.Equal(b, b2) {
		t.Fatal("the request does not survive a JSON round trip")
	}
}
