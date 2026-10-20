package gen

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/syssam/velox2/plugin"
	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
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

	if m.Version != plugin.ModelVersion || m.SchemaPath != o.SchemaPath {
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
	if bio := pluginField(t, user, "bio"); bio.Optional || !bio.Nillable || !bio.Pointer || !bio.Nullable || bio.GoType != "*string" || bio.ElemType != "string" || bio.PkgPath != "" {
		t.Fatalf("bio %+v", bio)
	}
	// The package of a named Go type, which a plugin imports to write it.
	if c := pluginField(t, user, "created_at"); c.ElemType != "time.Time" || c.PkgPath != "time" || c.Kind != "" {
		t.Fatalf("created_at %+v", c)
	}
	if role := pluginField(t, user, "role"); role.PkgPath != "" {
		t.Fatalf("role %+v: velox-gen's own enum type is in the generated package", role)
	}
	if role := pluginField(t, user, "role"); strings.Join(role.EnumValues, ",") != "admin,editor,viewer" {
		t.Fatalf("role values %v", role.EnumValues)
	}
	if author, posts := pluginEdge(t, post, "author"), pluginEdge(t, user, "posts"); author.FieldOn != "Post" || posts.FieldOn != "Post" {
		t.Fatalf("foreign key owner: author %q, posts %q", author.FieldOn, posts.FieldOn)
	}
	if name := pluginField(t, user, "name"); name.Comment != "The display name." || name.Nullable {
		t.Fatalf("name %+v", name)
	}
	if views := pluginField(t, post, "views"); views.Pointer || views.Nullable || views.GoType != "int" {
		t.Fatalf("views %+v: a required number is no pointer", views)
	}
	if nick := pluginField(t, user, "nickname"); nick.Pointer || nick.Nullable || !nick.Optional || nick.GoType != "string" {
		t.Fatalf("nickname %+v: Optional alone is a value on a NOT NULL column", nick)
	}
	if score := pluginField(t, user, "score"); !score.Pointer || !score.Nullable || !score.HasDefault || score.GoType != "*float64" {
		t.Fatalf("score %+v: Nillable makes a pointer; its Default does not change it", score)
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
	b, err := json.Marshal(plugin.Request{Protocol: plugin.ProtocolVersion, OutPackage: "gen", Model: PluginModel(g, o)})
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

// A many-to-many edge between an int64 ID and a string ID, declared from
// either side, so that a swap of the join entity's column types shows.
type PlOwner struct{ schema.Schema }

func (PlOwner) Fields() []schema.Field {
	return []schema.Field{field.String("note").Optional().Comment("A note."), field.Enum("st").GoType(lwStatus("")).Default("off"), field.Strings("labels").Optional()}
}
func (PlOwner) Edges() []schema.Edge {
	return []schema.Edge{edge.To("keys", PlKey.Type).Comment("The keys."), edge.To("pets", PlPet.Type)}
}
func (PlOwner) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Comment("An owner.")}
}

type PlKey struct{ schema.Schema }

func (PlKey) Fields() []schema.Field { return []schema.Field{field.String("id").MaxLen(36)} }
func (PlKey) Edges() []schema.Edge {
	return []schema.Edge{edge.From("owners", PlOwner.Type).Ref("keys")}
}

type PlPet struct{ schema.Schema }

type PlSKey struct{ schema.Schema }

func (PlSKey) Fields() []schema.Field { return []schema.Field{field.String("id").MaxLen(36)} }
func (PlSKey) Edges() []schema.Edge {
	return []schema.Edge{edge.To("owners", PlOwner2.Type)}
}

type PlOwner2 struct{ schema.Schema }

func (PlOwner2) Edges() []schema.Edge {
	return []schema.Edge{edge.From("skeys", PlSKey.Type).Ref("owners")}
}

func TestPluginModelJoinTypesAndComments(t *testing.T) {
	g := mustBuild(t, PlOwner{}, PlKey{}, PlPet{}, PlSKey{}, PlOwner2{})
	o, err := Lower(g, Config{})
	if err != nil {
		t.Fatal(err)
	}
	m := PluginModel(g, o)
	types := func(name string) string {
		j := pluginEntity(t, m, name)
		return j.Fields[0].Type + "," + j.Fields[1].Type
	}
	// The owner's column comes first: PlOwner (int64) owns plowner_keys, PlSKey
	// (string) owns plskey_owners.
	if got := types(pluginEdge(t, pluginEntity(t, m, "PlOwner"), "keys").Join); got != "int64,string" {
		t.Errorf("owner int64, target string: join types %s", got)
	}
	if got := types(pluginEdge(t, pluginEntity(t, m, "PlSKey"), "owners").Join); got != "string,int64" {
		t.Errorf("owner string, target int64: join types %s", got)
	}

	owner := pluginEntity(t, m, "PlOwner")
	if owner.Comment != "An owner." || pluginEdge(t, owner, "keys").Comment != "The keys." || pluginField(t, owner, "note").Comment != "A note." {
		t.Errorf("comments: %q %q %q", owner.Comment, pluginEdge(t, owner, "keys").Comment, pluginField(t, owner, "note").Comment)
	}
	if n := pluginField(t, owner, "note"); n.Nullable || !n.Optional || n.Pointer || n.GoType != "string" {
		t.Errorf("note %+v: Optional alone is a value on a NOT NULL column", n)
	}
	// An Optional JSON field is the exception: a slice has no zero value to
	// write, so its column is NULL-able, and NULL decodes to nil.
	if l := pluginField(t, owner, "labels"); !l.Nullable || l.Pointer || l.GoType != "[]string" {
		t.Errorf("labels %+v: an Optional JSON field's column is NULL-able", l)
	}
	// An enum with a Go type declares no enum type: the field names the Go
	// type, and holds the values.
	st := pluginField(t, owner, "st")
	if st.Enum != "lwStatus" || strings.Join(st.EnumValues, ",") != "on,off" || st.Nullable || st.ElemType != "gen.lwStatus" || st.PkgPath != "github.com/syssam/velox2/internal/gen" || st.Kind != "string" {
		t.Errorf("st %+v", st)
	}
	if len(owner.Imports) == 0 {
		t.Errorf("imports %v", owner.Imports)
	}
	// The foreign key of an O2M is in the target's table; of an M2M, none.
	if pets := pluginEdge(t, owner, "pets"); pets.FieldOn != "PlPet" || pets.Field == "" {
		t.Errorf("pets %+v", pets)
	}
	if keys := pluginEdge(t, owner, "keys"); keys.FieldOn != "" || keys.Field != "" {
		t.Errorf("keys %+v", keys)
	}
}

// Self-edges: FieldOn names the entity itself either way, so only OwnsKey
// says whose row holds the key. Tree has a self M2O/O2M pair, an inverse
// self O2O pair and a bidirectional O2O.
type PlTree struct{ schema.Schema }

func (PlTree) Edges() []schema.Edge {
	return []schema.Edge{
		edge.To("children", PlTree.Type),
		edge.From("parent", PlTree.Type).Ref("children").Unique(),
		edge.To("mentee", PlTree.Type).Unique(),
		edge.From("mentor", PlTree.Type).Ref("mentee").Unique(),
		edge.To("twin", PlTree.Type).Unique(),
	}
}

func TestPluginModelOwnsKey(t *testing.T) {
	g := mustBuild(t, PlTree{}, PlOwner{}, PlKey{}, PlPet{})
	o, err := Lower(g, Config{})
	if err != nil {
		t.Fatal(err)
	}
	m := PluginModel(g, o)
	tree := pluginEntity(t, m, "PlTree")
	for name, want := range map[string]struct {
		kind string
		owns bool
	}{
		"children": {"O2M", false}, // the children's rows hold it
		"parent":   {"M2O", true},
		"mentee":   {"O2O", false}, // the mentee's row holds it, through mentor
		"mentor":   {"O2O", true},  // the inverse side holds it
		"twin":     {"O2O", true},  // bidirectional: the one key is on the row
	} {
		ed := pluginEdge(t, tree, name)
		if ed.Kind != want.kind || ed.OwnsKey != want.owns || ed.FieldOn != "PlTree" || ed.Field == "" {
			t.Errorf("%s: kind %s owns %v field %q on %q, want %s owns %v on PlTree", name, ed.Kind, ed.OwnsKey, ed.Field, ed.FieldOn, want.kind, want.owns)
		}
	}
	// The pair's two edges name one key.
	if pluginEdge(t, tree, "mentee").Field != pluginEdge(t, tree, "mentor").Field || pluginEdge(t, tree, "children").Field != pluginEdge(t, tree, "parent").Field {
		t.Error("a pair names two keys")
	}
	// Across entities: an O2M's key is the target's; an M2M has none.
	owner := pluginEntity(t, m, "PlOwner")
	if pets := pluginEdge(t, owner, "pets"); pets.OwnsKey {
		t.Errorf("pets %+v", pets)
	}
	if keys := pluginEdge(t, owner, "keys"); keys.OwnsKey {
		t.Errorf("keys %+v", keys)
	}
}
