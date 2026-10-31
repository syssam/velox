package codegen

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox/plugin"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
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
	if bio := pluginField(t, user, "bio"); bio.Optional || !bio.Pointer || !bio.Nullable || bio.GoType != "*string" || bio.ElemType != "string" || bio.PkgPath != "" {
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

// TestPluginModel2 pins what model 2 adds to the blog's model and what it
// drops: the table's schema, the indexes with their names, a field's imports,
// size, literal default, checks and schema types; Nillable and the entity's
// imports are gone.
func TestPluginModel2(t *testing.T) {
	g, o := blogModel(t)
	m := PluginModel(g, o)
	if m.Version != 2 {
		t.Fatalf("model version %d, want 2", m.Version)
	}
	user, post, cat, group := pluginEntity(t, m, "User"), pluginEntity(t, m, "Post"), pluginEntity(t, m, "Category"), pluginEntity(t, m, "Group")
	if cat.TableSchema != "blog" || cat.Table != "categories" || user.TableSchema != "" {
		t.Errorf("table schema %q.%q, user %q", cat.TableSchema, cat.Table, user.TableSchema)
	}

	// Indexes: by field Names, implicit foreign keys included, with the
	// name the database gets.
	if len(user.Indexes) != 1 {
		t.Fatalf("user indexes %+v", user.Indexes)
	}
	if ix := user.Indexes[0]; ix.Name != "user_org_id_name" || strings.Join(ix.Fields, ",") != "org_id,name" || !ix.Unique || ix.Where != "" {
		t.Errorf("user index %+v", ix)
	}
	parent := pluginEdge(t, cat, "parent")
	if pluginField(t, cat, parent.Field).Implicit != "parent" {
		t.Fatalf("parent's key %q is not implicit", parent.Field)
	}
	if len(cat.Indexes) != 1 {
		t.Fatalf("category indexes %+v", cat.Indexes)
	}
	if ix := cat.Indexes[0]; ix.Name != "category_title_"+parent.Field || strings.Join(ix.Fields, ",") != "title,"+parent.Field || ix.Unique {
		t.Errorf("category index %+v", ix)
	}
	if post.Indexes == nil || len(post.Indexes) != 0 {
		t.Errorf("post indexes %#v: want an empty list", post.Indexes)
	}

	// Imports: the paths a field's Go type mentions; none for a builtin type
	// or velox-gen's own enum, nor for a join entity's int64 IDs.
	if c := pluginField(t, user, "created_at"); strings.Join(c.Imports, ",") != "time" {
		t.Errorf("created_at imports %v", c.Imports)
	}
	for _, f := range []*plugin.Field{pluginField(t, user, "name"), pluginField(t, user, "role"), pluginField(t, post, "tags"), pluginField(t, user, "id")} {
		if f.Imports != nil {
			t.Errorf("%s imports %v, want none", f.Name, f.Imports)
		}
	}
	for _, f := range pluginEntity(t, m, "UserGroup").Fields {
		if f.Imports != nil {
			t.Errorf("join field %s imports %v", f.Name, f.Imports)
		}
	}

	// Size, from MaxLen; Text's is the largest.
	if n, b := pluginField(t, user, "name"), pluginField(t, user, "bio"); n.Size != 50 || b.Size != math.MaxInt32 || pluginField(t, user, "email").Size != 0 {
		t.Errorf("sizes: name %d, bio %d", n.Size, b.Size)
	}

	// Default: the literal, as JSON; absent for a func default and a json
	// field.
	for f, want := range map[*plugin.Field]string{
		pluginField(t, user, "role"):       `"viewer"`,
		pluginField(t, user, "score"):      `1.5`,
		pluginField(t, post, "tags"):       ``, // json: no Default
		pluginField(t, post, "status"):     `"draft"`,
		pluginField(t, group, "token"):     ``,
		pluginField(t, user, "created_at"): ``,
		pluginField(t, user, "nickname"):   ``,
	} {
		if string(f.Default) != want {
			t.Errorf("%s default %s, want %s", f.Name, f.Default, want)
		}
	}
	if !pluginField(t, group, "token").HasDefault {
		t.Error("token has a func default")
	}

	// Checks, in declaration order.
	kinds := func(f *plugin.Field) string {
		var ks []string
		for _, c := range f.Checks {
			ks = append(ks, c.Kind)
		}
		return strings.Join(ks, ",")
	}
	if n := pluginField(t, user, "name"); kinds(n) != "NotEmpty,MaxLen" || len(n.Checks[1].Args) != 1 || string(n.Checks[1].Args[0]) != "50" || n.Checks[0].Args != nil {
		t.Errorf("name checks %+v", n.Checks)
	}
	if a := pluginField(t, user, "age"); kinds(a) != "Range" || len(a.Checks[0].Args) != 2 {
		t.Errorf("age checks %+v", a.Checks)
	}
	if kinds(pluginField(t, user, "nickname")) != "" {
		t.Error("nickname has checks")
	}

	// SchemaType, per dialect.
	if st := pluginField(t, cat, "title").SchemaType; len(st) != 1 || st["postgres"] != "varchar(200)" {
		t.Errorf("title schema type %v", st)
	}

	// Edge kinds are the constants.
	if pluginEdge(t, user, "groups").Kind != plugin.M2M || pluginEdge(t, user, "posts").Kind != plugin.O2M || parent.Kind != plugin.M2O {
		t.Error("edge kinds")
	}

	// Nillable and the entity's imports are gone from the JSON.
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var raw struct {
		Entities []map[string]json.RawMessage `json:"entities"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, e := range raw.Entities {
		if _, ok := e["imports"]; ok {
			t.Errorf("entity %s has imports", e["name"])
		}
		if _, ok := e["indexes"]; !ok {
			t.Errorf("entity %s has no indexes key", e["name"])
		}
	}
	if bytes.Contains(b, []byte(`"nillable"`)) {
		t.Error("the model has a nillable key")
	}
}

// A many-to-many edge to an entity whose ID has a Go type: the join entity's
// field holds that ID, and imports its package.
type PlDocID string

type PlDoc struct{ schema.Schema }

func (PlDoc) Fields() []schema.Field {
	return []schema.Field{
		field.String("id").GoType(PlDocID("")).MaxLen(36),
		field.JSON("stamps", map[string][]time.Time{}).Optional(),
		field.Time("at").GoType(PlStamp{}).Default(func() time.Time { return time.Time{} }),
	}
}
func (PlDoc) Edges() []schema.Edge {
	return []schema.Edge{edge.From("readers", PlReader.Type).Ref("docs")}
}

type PlStamp time.Time

type PlReader struct{ schema.Schema }

func (PlReader) Edges() []schema.Edge { return []schema.Edge{edge.To("docs", PlDoc.Type)} }

func TestPluginModel2Imports(t *testing.T) {
	g := mustBuild(t, PlDoc{}, PlReader{})
	o, err := Lower(g, Config{})
	if err != nil {
		t.Fatal(err)
	}
	m := PluginModel(g, o)
	doc := pluginEntity(t, m, "PlDoc")
	const self = "github.com/syssam/velox/internal/codegen"
	// A time GoType's own package alone: its type mentions no other.
	for name, want := range map[string]string{"id": self, "stamps": "time", "at": self} {
		if got := strings.Join(pluginField(t, doc, name).Imports, ","); got != want {
			t.Errorf("%s imports %s, want %s", name, got, want)
		}
	}
	if d := pluginField(t, doc, "at"); d.Default != nil || !d.HasDefault {
		t.Errorf("at: a func default has no literal: %s", d.Default)
	}
	join := pluginEntity(t, m, pluginEdge(t, pluginEntity(t, m, "PlReader"), "docs").Join)
	if join.Fields[0].Imports != nil || strings.Join(join.Fields[1].Imports, ",") != self {
		t.Errorf("join imports %v, %v", join.Fields[0].Imports, join.Fields[1].Imports)
	}
}

// Join fields hold the IDs of the entities they join, typed as those are: a
// uuid ID and a named string ID through many-to-many joins.
type PlUUID [16]byte

func (PlUUID) Value() (driver.Value, error) { return nil, nil }

type PlBadge struct{ schema.Schema }

func (PlBadge) Fields() []schema.Field { return []schema.Field{field.UUID("id", PlUUID{})} }
func (PlBadge) Edges() []schema.Edge {
	return []schema.Edge{edge.From("holders", PlHolder.Type).Ref("badges")}
}

type PlHolder struct{ schema.Schema }

func (PlHolder) Edges() []schema.Edge {
	return []schema.Edge{edge.To("badges", PlBadge.Type)}
}

func TestPluginModel2JoinFieldsTyped(t *testing.T) {
	g := mustBuild(t, PlBadge{}, PlHolder{}, PlDoc{}, PlReader{})
	o, err := Lower(g, Config{})
	if err != nil {
		t.Fatal(err)
	}
	m := PluginModel(g, o)
	const self = "github.com/syssam/velox/internal/codegen"
	for _, c := range []struct{ owner, edge, want string }{{"PlHolder", "badges", "uuid array"}, {"PlReader", "docs", "string string"}} {
		holder, edge, want := pluginEntity(t, m, c.owner), c.edge, c.want
		j := pluginEntity(t, m, pluginEdge(t, holder, edge).Join)
		owner, target := j.Fields[0], j.Fields[1]
		if owner.Type != "int64" || owner.PkgPath != "" || owner.Kind != "" || owner.Imports != nil {
			t.Errorf("%s: owner column %+v", edge, owner)
		}
		if got := target.Type + " " + target.Kind; got != want || target.PkgPath != self || strings.Join(target.Imports, ",") != self {
			t.Errorf("%s: target column %+v, want type and kind %s of %s", edge, target, want, self)
		}
		// As the target's own ID field is.
		id := pluginEntity(t, m, pluginEdge(t, holder, edge).Target).Fields[0]
		if id.PkgPath != target.PkgPath || id.Kind != target.Kind || id.ElemType != target.ElemType {
			t.Errorf("%s: join column %+v, the ID %+v", edge, target, id)
		}
	}
}

// Defaults are in the model only where their JSON is unambiguous: bool,
// number, string and enum, as their base value.
type PlCount int32

type PlLevel int

func (l PlLevel) MarshalJSON() ([]byte, error) { return []byte(strconv.Itoa(int(l))), nil }

type PlDefaults struct{ schema.Schema }

func (PlDefaults) Fields() []schema.Field {
	return []schema.Field{
		field.Number[PlCount]("count").Nillable().Default(5),
		field.Number[PlLevel]("level").Nillable().Default(2),
		field.Bool("on").Nillable().Default(true),
		field.String("s").Default("x"),
		field.Bytes("b").Default([]byte("hi")),
		field.JSON("doc", map[string]int{}).Default(map[string]int{"a": 1}),
		field.Strings("tags").Default([]string{"new"}),
	}
}

func TestPluginModel2Defaults(t *testing.T) {
	g := mustBuild(t, PlDefaults{})
	o, err := Lower(g, Config{})
	if err != nil {
		t.Fatal(err)
	}
	e := pluginEntity(t, PluginModel(g, o), "PlDefaults")
	for name, want := range map[string]string{
		"count": `5`,    // a named int: a number
		"level": ``,     // its own MarshalJSON: no Default
		"on":    `true`, // a bool
		"s":     `"x"`,
		"b":     ``, // bytes: no Default, not base64
		"doc":   ``, // json: no Default
		"tags":  ``,
	} {
		f := pluginField(t, e, name)
		if string(f.Default) != want {
			t.Errorf("%s default %s, want %q", name, f.Default, want)
		}
		if !f.HasDefault {
			t.Errorf("%s has a default", name)
		}
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
	return []schema.Field{field.String("note").Optional().Comment("A note."), field.Enum("st").GoType(LwStatus("")).Default("off"), field.Strings("labels").Optional()}
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
	if st.Enum != "LwStatus" || strings.Join(st.EnumValues, ",") != "on,off" || st.Nullable || st.ElemType != "codegen.LwStatus" || st.PkgPath != "github.com/syssam/velox/internal/codegen" || st.Kind != "string" {
		t.Errorf("st %+v", st)
	}
	if strings.Join(st.Imports, ",") != "github.com/syssam/velox/internal/codegen" {
		t.Errorf("st imports %v", st.Imports)
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
		kind plugin.EdgeKind
		owns bool
	}{
		"children": {plugin.O2M, false}, // the children's rows hold it
		"parent":   {plugin.M2O, true},
		"mentee":   {plugin.O2O, false}, // the mentee's row holds it, through mentor
		"mentor":   {plugin.O2O, true},  // the inverse side holds it
		"twin":     {plugin.O2O, true},  // bidirectional: the one key is on the row
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

// A plugin does not see SchemaDefault: it is for the migration planner.
func TestPluginModelHasNoSchemaDefault(t *testing.T) {
	g, err := build(t, sdUser{withDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if f := g.Entity("sdUser").Fields[1]; len(f.SchemaDefault) == 0 {
		t.Fatalf("the graph's field %s has no SchemaDefault", f.Name)
	}
	o, err := Lower(g, Config{Package: "gen", SchemaPath: lowerSchemaPath})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(PluginModel(g, o))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(b, []byte("schema_default")) || bytes.Contains(b, []byte("now()")) {
		t.Fatalf("the plugin model carries a SchemaDefault: %s", b)
	}
}

// nullMix has JSON fields of every kind a column can hold NULL for, required,
// Optional and Nillable.
type nullMix struct{ schema.Schema }

func (nullMix) Fields() []schema.Field {
	return []schema.Field{
		field.JSON("req_slice", []string{}),
		field.JSON("opt_slice", []string{}).Optional(),
		field.JSON("nil_slice", []string{}).Nillable(),
		field.JSON("req_map", map[string]int{}),
		field.JSON("opt_map", map[string]int{}).Optional(),
		field.JSON("nil_map", map[string]int{}).Nillable(),
		field.JSON("req_ptr", &JSONShape{}),
		field.JSON("opt_ptr", &JSONShape{}).Optional(),
		field.JSON("nil_ptr", &JSONShape{}).Nillable(),
		field.JSON("req_struct", JSONShape{}),
		field.JSON("nil_struct", JSONShape{}).Nillable(),
	}
}

// For an Optional or Nillable JSON field, the plugin's Nullable is what the
// core reads of the generated code: a pointer field, a ColumnDesc.Nullable
// (NilIsNull), or a Go type that is a pointer. A required field of a pointer
// type is the exception: the core's flag reports the Go type's nil, and its
// column is NOT NULL.
func TestNullableMatchesCore(t *testing.T) {
	g := mustBuild(t, nullMix{})
	o, err := Lower(g, Config{Package: "gen", SchemaPath: lowerSchemaPath})
	if err != nil {
		t.Fatal(err)
	}
	pe := pluginEntity(t, PluginModel(g, o), "nullMix")
	for _, of := range o.Entities[0].Fields[1:] {
		got := pluginField(t, pe, of.Name).Nullable
		if want := !strings.HasPrefix(of.Name, "req_"); got != want {
			t.Errorf("field %s: Nullable %v, want %v", of.Name, got, want)
		}
		if strings.HasPrefix(of.Name, "req_") {
			continue
		}
		if core := of.Pointer || of.NilIsNull || strings.HasPrefix(of.ElemType, "*"); got != core {
			t.Errorf("field %s: the plugin says Nullable %v, the core reads %v", of.Name, got, core)
		}
	}
}
