package codegen

import (
	"encoding/json"
	"errors"
	"math/rand"
	randv2 "math/rand/v2"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox/internal/codegen/loadjson"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/index"
	"github.com/syssam/velox/schema/mixin"

	errpkg "github.com/syssam/velox/internal/codegen/internal/errors"
	schpkg "github.com/syssam/velox/internal/codegen/internal/sch"
)

const lowerSchemaPath = "github.com/syssam/velox/internal/codegen"

// The schemas of the lowering tests. The prefix Lw makes the label lw_user, so
// that the join entity of lw_user_groups is LwUserGroup. (snake("LUser") is
// "luser": an upper-case letter after a first upper-case letter starts no word.)
type LwUser struct{ schema.Schema }

func (LwUser) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").NotEmpty(),
		field.String("nick").Optional(),
		field.Bool("active").Nillable().Default(true),
		field.Enum("role").Values("viewer", "admin"),
		field.Time("seen").Optional().Nillable(),
		field.Strings("tags").Optional(),
	}
}
func (LwUser) Edges() []schema.Edge {
	return []schema.Edge{edge.To("posts", LwPost.Type), edge.To("groups", LwGroup.Type)}
}

type LwPost struct{ schema.Schema }

func (LwPost) Fields() []schema.Field {
	return []schema.Field{field.Int64("author_id"), field.String("title")}
}
func (LwPost) Edges() []schema.Edge {
	return []schema.Edge{edge.From("author", LwUser.Type).Ref("posts").Unique().Required().Field("author_id")}
}

type LwGroup struct{ schema.Schema }

func (LwGroup) Edges() []schema.Edge {
	return []schema.Edge{edge.From("users", LwUser.Type).Ref("groups")}
}

func lower(t *testing.T, cfg Config, ss ...schema.Interface) (*Out, error) {
	t.Helper()
	g, err := build(t, ss...)
	if err != nil {
		t.Fatal(err)
	}
	return Lower(g, cfg)
}

func mustLower(t *testing.T, cfg Config, ss ...schema.Interface) *Out {
	t.Helper()
	o, err := lower(t, cfg, ss...)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func (o *Out) entity(name string) *OutEntity {
	for _, e := range o.Entities {
		if e.Name == name {
			return e
		}
	}
	return nil
}

func (o *Out) enum(typ string) *OutEnum {
	for _, e := range o.Enums {
		if e.Type == typ {
			return e
		}
	}
	return nil
}

func (e *OutEntity) field(name string) *OutField {
	for _, f := range e.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

func (e *OutEntity) rel(name string) *OutRel {
	for _, r := range e.Rels {
		if r.Name == name {
			return r
		}
	}
	return nil
}

func TestLowerTypes(t *testing.T) {
	o := mustLower(t, Config{Package: "gen", SchemaPath: lowerSchemaPath}, LwUser{}, LwPost{}, LwGroup{})
	u := o.entity("LwUser")
	for _, c := range []struct{ name, goType, handle string }{
		{"id", "int64", "velox.NumberCol[LwUser, int64]"},
		{"name", "string", "velox.StringCol[LwUser, string]"},
		{"nick", "string", "velox.StringCol[LwUser, string]"},             // Optional alone is a value on a NOT NULL column
		{"active", "*bool", "velox.NullCol[LwUser, bool]"},                // Nillable, with a Default that does not change the type
		{"role", "LwUserRole", "velox.StringCol[LwUser, LwUserRole]"},     // the generated enum type
		{"seen", "*time.Time", "velox.NullOrderedCol[LwUser, time.Time]"}, //
		{"tags", "[]string", "velox.JSONCol[LwUser, []string]"},           // a JSON slice is never a pointer
	} {
		f := u.field(c.name)
		if f == nil || f.GoType != c.goType || f.Handle != c.handle {
			t.Errorf("%s: %+v, want %s %s", c.name, f, c.goType, c.handle)
		}
	}
	if f := u.field("nick"); f.Pointer || f.Unset != `x == ""` || f.ElemType != "string" || f.GoName != "Nick" || f.Column != "nick" {
		t.Errorf("nick %+v", f)
	}
	if f := u.field("tags"); f.Pointer || f.ElemType != "[]string" || f.Tag != "`json:\"tags,omitempty\"`" {
		t.Errorf("tags %+v", f)
	}
	if e := o.enum("LwUserRole"); e == nil || e.Consts[0] != "LwUserRoleViewer" || e.Values[1] != "admin" || len(e.Consts) != 2 {
		t.Fatalf("enum %+v", e)
	}
	if f := u.field("active"); f.DefaultCode != "true" {
		t.Fatalf("active default %q", f.DefaultCode)
	}
	if !reflect.DeepEqual(u.Imports, []string{"encoding/json", "errors", "time"}) || u.Handles != "LwUsers" || u.Label != "lw_user" || u.Table != "lw_users" {
		t.Errorf("user imports %v handles %s label %s table %s", u.Imports, u.Handles, u.Label, u.Table)
	}
	if o.Package != "gen" || o.SchemaPath != lowerSchemaPath || o.NeedsSch {
		t.Errorf("out %q %q %v", o.Package, o.SchemaPath, o.NeedsSch)
	}
	if !reflect.DeepEqual(u.PrimaryKey, []int{0}) {
		t.Errorf("pk %v", u.PrimaryKey)
	}
}

// TestLowerJSONFlag: a json field, and only a json field, is lowered with
// JSON set, for which render writes encoding/json's codec into its
// ColumnDescriptor and the file imports encoding/json
// (as the JSON fields plan amended it). A bytes field holds bytes, not JSON.
func TestLowerJSONFlag(t *testing.T) {
	o := mustLower(t, Config{Package: "gen", SchemaPath: lowerSchemaPath}, LwUser{}, LwPost{}, LwGroup{})
	u := o.entity("LwUser")
	for _, f := range u.Fields {
		if want := f.Name == "tags"; f.JSON != want {
			t.Errorf("%s: JSON %v, want %v", f.Name, f.JSON, want)
		}
	}
	m := mustLower(t, Config{Package: "gen", SchemaPath: lowerSchemaPath}, LwMixed{}).entity("LwMixed")
	if f := m.field("labels"); !f.JSON {
		t.Errorf("labels (field.Strings) is not JSON")
	}
	if f := m.field("blob"); f.JSON {
		t.Errorf("blob (field.Bytes) is JSON")
	}
	// A json field's handle only sets it (and, nullable, tests and sets NULL):
	// no condition or order compares JSON.
	if f := m.field("labels"); f.Handle != "velox.JSONCol[LwMixed, []string]" {
		t.Errorf("labels: handle %s, want velox.JSONCol", f.Handle)
	}
	if f := m.field("box"); !f.JSON || !f.Pointer || f.Handle != "velox.NullJSONCol[LwMixed, codegen.LwBox]" {
		t.Errorf("box (a Nillable field.JSON of a struct): JSON %v, pointer %v, handle %s, want velox.NullJSONCol", f.JSON, f.Pointer, f.Handle)
	}
	g, err := build(t, LwUser{}, LwPost{}, LwGroup{})
	if err != nil {
		t.Fatal(err)
	}
	files, err := Generate(g, Config{Package: "gen", SchemaPath: lowerSchemaPath})
	if err != nil {
		t.Fatal(err)
	}
	var src string
	for _, f := range files {
		src += string(f.Data)
	}
	// tags is Optional and a slice: its column holds NULL, which a nil
	// slice binds.
	if want := `{Field: "Tags", Name: "tags", Nullable: true, Encode: json.Marshal, Decode: json.Unmarshal},`; !strings.Contains(src, want) {
		t.Errorf("the generated code lacks %s", want)
	}
	if f := m.field("labels"); f.NilIsNull {
		t.Errorf("labels (a required slice) is NilIsNull")
	}
	if f := m.field("box"); f.NilIsNull {
		t.Errorf("box (a pointer) is NilIsNull: the pointer makes it nullable")
	}
	if f := u.field("tags"); !f.NilIsNull {
		t.Errorf("tags (an Optional slice) is not NilIsNull")
	}
	if n := strings.Count(src, "Encode: json.Marshal"); n != 1 {
		t.Errorf("the generated code gives %d columns a codec, want 1", n)
	}
	// The codec is the only use of encoding/json in LwUser's file, which
	// imports it; LwPost's, with no json field, does not.
	seen := 0
	for _, f := range files {
		imports := strings.Contains(string(f.Data), `"encoding/json"`)
		switch f.Name {
		case "lw_user_velox.go":
			seen++
			if !imports {
				t.Errorf("%s does not import encoding/json", f.Name)
			}
		case "lw_post_velox.go":
			seen++
			if imports {
				t.Errorf("%s imports encoding/json", f.Name)
			}
		}
	}
	if seen != 2 {
		t.Errorf("found %d of the 2 entity files", seen)
	}
}

func TestLowerRelationsAndJoin(t *testing.T) {
	o := mustLower(t, Config{Package: "gen"}, LwUser{}, LwPost{}, LwGroup{})
	u, p, j := o.entity("LwUser"), o.entity("LwPost"), o.entity("LwUserGroup")
	if r := u.rel("posts"); r == nil || !r.Many || r.Target != "LwPost" || r.GoName != "Posts" || r.Desc != `velox.EdgeTo[LwPost]("posts", lwUserColID, lwPostColAuthorID)` {
		t.Fatalf("posts %+v", r)
	}
	if r := p.rel("author"); r == nil || r.Many || r.Desc != `velox.EdgeTo[LwUser]("author", lwPostColAuthorID, lwUserColID)` {
		t.Fatalf("author %+v", r)
	}
	if j == nil || !j.Join || len(j.Fields) != 2 || j.Fields[0].GoName != "LwUserID" || j.Fields[1].GoName != "LwGroupID" ||
		len(j.PrimaryKey) != 2 || j.Table != "lw_user_groups" || len(j.Rels) != 0 ||
		j.Fields[0].Handle != "velox.NumberCol[LwUserGroup, int64]" || j.Handles != "LwUserGroups" {
		t.Fatalf("join %+v", j)
	}
	if r := u.rel("groups"); r.Desc != `velox.EdgeThrough[LwUserGroup, LwGroup]("groups", lwUserColID, lwUserGroupColLwUserID, lwUserGroupColLwGroupID, lwGroupColID)` || !r.Many {
		t.Fatalf("groups %s", r.Desc)
	}
	// The inverse sees the same join entity with its columns the other way round.
	if r := o.entity("LwGroup").rel("users"); r.Desc != `velox.EdgeThrough[LwUserGroup, LwUser]("users", lwGroupColID, lwUserGroupColLwGroupID, lwUserGroupColLwUserID, lwUserColID)` {
		t.Fatalf("users %s", r.Desc)
	}
	names := make([]string, len(o.Entities))
	for i, e := range o.Entities {
		names[i] = e.Name
	}
	if !reflect.DeepEqual(names, []string{"LwGroup", "LwPost", "LwUser", "LwUserGroup"}) {
		t.Fatalf("entities %v", names)
	}
}

// A foreign key in the owner's table, and a self-referencing tree.
type LwNode struct{ schema.Schema }

func (LwNode) Edges() []schema.Edge {
	return []schema.Edge{edge.To("children", LwNode.Type).From("parent").Unique(), edge.To("friends", LwNode.Type)}
}

func TestLowerSelfReference(t *testing.T) {
	n := mustLower(t, Config{}, LwNode{}).entity("LwNode")
	// Fields: id, then the implicit lw_node_children.
	if r := n.rel("children"); r.Desc != `velox.EdgeTo[LwNode]("children", lwNodeColID, lwNodeColLwNodeChildren)` || !r.Many {
		t.Fatalf("children %+v", r)
	}
	if r := n.rel("parent"); r.Desc != `velox.EdgeTo[LwNode]("parent", lwNodeColLwNodeChildren, lwNodeColID)` || r.Many {
		t.Fatalf("parent %+v", r)
	}
	if r := n.rel("friends"); r.Desc != `velox.EdgeThrough[LwNodeFriend, LwNode]("friends", lwNodeColID, lwNodeFriendColLwNodeID, lwNodeFriendColFriendID, lwNodeColID)` {
		t.Fatalf("friends %+v", r)
	}
	if f := n.field("lw_node_children"); f == nil || f.GoType != "*int64" || f.Handle != "velox.NullNumberCol[LwNode, int64]" {
		t.Fatalf("implicit %+v", f)
	}
}

type LwChecked struct{ schema.Schema }

func (LwChecked) Fields() []schema.Field {
	return []schema.Field{
		field.String("code").NotEmpty().MaxLen(5).Match(regexp.MustCompile(`^[a-z]+$`)).Validate(func(s string) error { return nil }),
		field.Int("age").Optional().Min(1).Max(120).Validate(func(int) error { return nil }),
		field.Enum("state").Values("on", "off"),
		field.Int64("n").Positive(),
		field.String("plain"),
		field.Bytes("raw").Optional().DefaultFunc(func() []byte { return []byte("x") }).MaxLen(3),
	}
}

func TestLowerChecks(t *testing.T) {
	o := mustLower(t, Config{SchemaPath: lowerSchemaPath}, LwChecked{})
	e := o.entity("LwChecked")
	if !o.NeedsSch || !e.NeedsSch {
		t.Error("a validator reads the schema package")
	}
	if n := strings.Count(e.CheckSetup, "regexp.MustCompile"); n != 1 {
		t.Errorf("setup has %d regexps:\n%s", n, e.CheckSetup)
	}
	for _, want := range []string{
		`re0 := regexp.MustCompile("^[a-z]+$")`,
		`f0 := schema.FieldDescriptor(&codegen.LwChecked{}, "code")`,
		`d0 := f0.Validators[0].(func(string) error)`,
		`f1 := schema.FieldDescriptor(&codegen.LwChecked{}, "age")`,
		`d1 := f1.Validators[0].(func(int) error)`,
	} {
		if !strings.Contains(e.CheckSetup, want) {
			t.Errorf("setup lacks %s:\n%s", want, e.CheckSetup)
		}
	}
	// The ID is column 0; code is column 1.
	code := []string{
		"case lwCheckedColCode:",
		"x := v.(string)",
		`if x == "" {`, `errors.New("must not be empty")`,
		"if len(x) > 5 {", `errors.New("length is more than 5")`,
		"if !re0.MatchString(x) {", `errors.New("does not match ^[a-z]+$")`,
		"if err := d0(x); err != nil {",
		"case lwCheckedColAge:",
		"x := v.(int)",
		"if x == 0 {", "return nil", // Optional: zero is unset, and valid
		"if x < 1 {", `errors.New("is less than 1")`,
		"if x > 120 {", `errors.New("is more than 120")`,
		"if err := d1(x); err != nil {",
		"case lwCheckedColState:",
		"x := v.(LwCheckedState)",
		"if !x.IsValid() {", `errors.New("is not one of on, off")`,
		"case lwCheckedColN:",
		"x := v.(int64)",
		"if x <= 0 {", `errors.New("is not positive")`,
		// An Optional bytes field: nil, or empty, is unset, and valid.
		"case lwCheckedColRaw:",
		"x := v.([]byte)",
		"if len(x) == 0 {", "return nil",
		"if len(x) > 3 {",
	}
	at := 0
	for _, w := range code {
		i := strings.Index(e.CheckCode[at:], w)
		if i < 0 {
			t.Fatalf("CheckCode lacks %q after offset %d:\n%s", w, at, e.CheckCode)
		}
		at += i + len(w)
	}
	if strings.Contains(e.CheckCode, "case lwCheckedColPlain:") || strings.Contains(e.CheckCode, "case lwCheckedColID:") {
		t.Errorf("a column without checks has a case:\n%s", e.CheckCode)
	}
	if !reflect.DeepEqual(e.Imports, []string{"errors", "github.com/syssam/velox/schema", "regexp"}) {
		t.Errorf("imports %v", e.Imports)
	}
	// An entity without checks has no code.
	if u := mustLower(t, Config{}, LwNode{}).entity("LwNode"); u.CheckCode != "" || u.CheckSetup != "" || u.NeedsSch {
		t.Errorf("node %q %q", u.CheckCode, u.CheckSetup)
	}
}

// LwDefaults pairs each field with a Default with the same field without one.
type LwDefaults struct{ schema.Schema }

func (LwDefaults) Fields() []schema.Field {
	return []schema.Field{
		field.Bool("b").Nillable(), field.Bool("b_def").Nillable().Default(true),
		field.Float("f").Optional().Nillable(), field.Float("f_def").Optional().Nillable().Default(1.5),
		field.Int("i").Nillable(), field.Int("i_def").Nillable().DefaultFunc(func() int { return 1 }),
		field.String("s"), field.String("s_def").Default("x"),
		field.String("o").Optional(), field.String("o_def").Optional().Default("x"),
		field.Time("t").Nillable(), field.Time("t_def").Nillable().Default(time.Now),
	}
}

// A Default never changes the Go type or the handle: only Nillable does.
type lwRuneSchema struct{ schema.Schema }

func (lwRuneSchema) Fields() []schema.Field {
	return []schema.Field{field.String("title").MinRuneLen(2).MaxRuneLen(5)}
}

func TestLowerRuneLen(t *testing.T) {
	src := mustLower(t, Config{SchemaPath: lowerSchemaPath}, lwRuneSchema{}).entity("lwRuneSchema").CheckCode
	for _, want := range []string{
		"utf8.RuneCountInString(x) < 2", `"length in characters is less than 2"`,
	} {
		if !strings.Contains(src, want) {
			t.Errorf("the generated code has no %s:\n%s", want, src)
		}
	}
}

func TestLowerDefaultKeepsType(t *testing.T) {
	e := mustLower(t, Config{SchemaPath: lowerSchemaPath}, LwDefaults{}).entity("LwDefaults")
	for _, c := range []struct{ name, goType, handle string }{
		{"b", "*bool", "velox.NullCol[LwDefaults, bool]"},
		{"f", "*float64", "velox.NullNumberCol[LwDefaults, float64]"},
		{"i", "*int", "velox.NullNumberCol[LwDefaults, int]"},
		{"s", "string", "velox.StringCol[LwDefaults, string]"},
		{"o", "string", "velox.StringCol[LwDefaults, string]"},
		{"t", "*time.Time", "velox.NullOrderedCol[LwDefaults, time.Time]"},
	} {
		for _, name := range []string{c.name, c.name + "_def"} {
			if f := e.field(name); f.GoType != c.goType || f.Handle != c.handle {
				t.Errorf("%s: %s %s, want %s %s", name, f.GoType, f.Handle, c.goType, c.handle)
			}
		}
	}
}

// A mixin's field is read by its name, as the schema's own are, and every
// default that is not a literal comes from the descriptor.
type lwMix struct{ mixin.Schema }

func (lwMix) Fields() []schema.Field {
	return []schema.Field{
		field.String("slug").Validate(func(string) error { return nil }),
		field.Time("made").Default(time.Now),
		field.Int64("rev").Nillable().DefaultFunc(func() int64 { return 1 }),
	}
}

type LwMixed struct{ schema.Schema }

func (LwMixed) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Schema{}, lwMix{}} }
func (LwMixed) Fields() []schema.Field {
	return []schema.Field{
		field.String("own").Validate(func(string) error { return nil }).DefaultFunc(func() string { return "x" }),
		field.Time("touched").Optional().Nillable().UpdateDefault(time.Now),
		field.Bytes("blob").Default([]byte("b")),
		field.String("lit").Default("hi"),
		field.Int("count").Nillable().Default(5),
		field.Float("ratio").Nillable().Default(1.5),
		field.Strings("labels").Default([]string{"a", "b"}),
		field.Other("pt", UUIDValue{}).SchemaType(map[string]string{"postgres": "uuid"}).Default(UUIDValue{1}),
		field.Enum("tier").Values("free", "paid").Default("paid"),
		field.JSON("box", LwBox{}).Optional().Nillable(),
	}
}

// LwBox is a struct a json field holds.
type LwBox struct{ W, H int }

func TestLowerMixinPosition(t *testing.T) {
	o := mustLower(t, Config{SchemaPath: lowerSchemaPath}, LwMixed{})
	e := o.entity("LwMixed")
	for _, want := range []string{
		`f0 := schema.FieldDescriptor(&codegen.LwMixed{}, "slug")`,
		`d0 := f0.Validators[0].(func(string) error)`,
		// The descriptor of the field read twice is read once.
		`f3 := schema.FieldDescriptor(&codegen.LwMixed{}, "own")`,
		`d4 := f3.Validators[0].(func(string) error)`,
	} {
		if !strings.Contains(e.CheckSetup, want) {
			t.Errorf("setup lacks %s:\n%s", want, e.CheckSetup)
		}
	}
	// Each descriptor read is a statement of the setup, run once; the closure
	// of a column only calls or returns what it read.
	for _, want := range []string{
		`d1 := f1.Default.(func() time.Time)`,
		`d2 := f2.DefaultFunc.(func() int64)`,
		`d3 := f3.DefaultFunc.(func() string)`,
		`d5 := f4.UpdateDefault.(func() time.Time)`,
		`f5 := schema.FieldDescriptor(&codegen.LwMixed{}, "pt")`,
		`d6 := f5.Default` + "\n",
	} {
		if !strings.Contains(e.CheckSetup+"\n", want) {
			t.Errorf("setup lacks %s:\n%s", want, e.CheckSetup)
		}
	}
	for _, c := range []struct{ name, def, fn, upd string }{
		{"made", "", `func() any { return d1() }`, ""},
		{"rev", "", `func() any { return d2() }`, ""},
		{"own", "", `func() any { return d3() }`, ""},
		{"touched", "", "", `func() any { return d5() }`},
		{"blob", "", `func() any { var v []byte; _ = json.Unmarshal([]byte("\"Yg==\""), &v); return v }`, ""},
		{"labels", "", `func() any { return []string{"a", "b"} }`, ""},
		{"pt", "", `func() any { return d6 }`, ""},
		{"tier", "LwMixedTierPaid", "", ""},
		{"lit", `"hi"`, "", ""},
		{"count", `int(5)`, "", ""},
		{"ratio", `float64(1.5)`, "", ""},
	} {
		f := e.field(c.name)
		if f == nil || f.DefaultCode != c.def || f.DefaultFuncCode != c.fn || f.UpdateDefaultCode != c.upd {
			t.Errorf("%s: %+v, want %q %q %q", c.name, f, c.def, c.fn, c.upd)
		}
	}
	// The per-row cost: no closure reads a descriptor.
	for _, f := range e.Fields {
		for _, code := range []string{f.DefaultFuncCode, f.UpdateDefaultCode} {
			if strings.Contains(code, "Descriptor") || strings.Contains(code, "sch.") {
				t.Errorf("%s: the closure reads the descriptor per call: %s", f.GoName, code)
			}
		}
	}
	if !reflect.DeepEqual(e.Imports, []string{"encoding/json", "errors", "github.com/syssam/velox/internal/codegen", "github.com/syssam/velox/schema", "time"}) {
		t.Errorf("imports %v", e.Imports)
	}
}

type ClBox struct{ schema.Schema }

// ClBoxes has a table of its own: its default, cl_boxes, is ClBox's.
type ClBoxes struct{ schema.Schema }

func (ClBoxes) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("cl_box_rows")}
}

type ClUser struct{ schema.Schema }

func (ClUser) Fields() []schema.Field { return []schema.Field{field.Enum("role").Values("a")} }

type ClUserRole struct{ schema.Schema }

type ClTag struct{ schema.Schema }

func (ClTag) Edges() []schema.Edge { return []schema.Edge{edge.To("links", ClItem.Type)} }

type ClItem struct{ schema.Schema }

func (ClItem) Edges() []schema.Edge {
	return []schema.Edge{edge.From("tags", ClTag.Type).Ref("links").StorageKey(edge.Table("cl_joins"))}
}

// ClJoin has the name pascal(plugin.Singular("cl_joins")).
type ClJoin struct{ schema.Schema }

func (ClJoin) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("cl_join_rows")}
}

// ClTagEdges has the name of the struct of ClTag's relations.
type ClTagEdges struct{ schema.Schema }

type ClReserved struct{ schema.Schema }

func (ClReserved) Fields() []schema.Field { return []schema.Field{field.String("string")} }
func (ClReserved) Edges() []schema.Edge   { return []schema.Edge{edge.To("hooks", ClBox.Type)} }

type ClImports struct{ schema.Schema }

func (ClImports) Fields() []schema.Field {
	return []schema.Field{field.JSON("a", &rand.Rand{}), field.JSON("b", &randv2.Rand{})}
}

func TestLowerCollisions(t *testing.T) {
	for _, c := range []struct {
		name string
		ss   []schema.Interface
		want []string
	}{
		{"handles", []schema.Interface{ClBox{}, ClBoxes{}}, []string{`schema ClBox: handles variable ClBoxes collides with schema ClBoxes`}},
		{"enum", []schema.Interface{ClUser{}, ClUserRole{}}, []string{`schema ClUser: field "role": enum type ClUserRole collides with schema ClUserRole`}},
		{"join", []schema.Interface{ClTag{}, ClItem{}, ClJoin{}}, []string{`schema ClTag: edge "links": join entity ClJoin collides with schema ClJoin`}},
		{"edges", []schema.Interface{ClTag{}, ClItem{}, ClTagEdges{}}, []string{`schema ClTag: edges type ClTagEdges collides with schema ClTagEdges`}},
		{"reserved", []schema.Interface{ClReserved{}, ClBox{}}, []string{
			`schema ClReserved: field "string": its Go name String is reserved`,
			`schema ClReserved: edge "hooks": its Go name Hooks is reserved`,
		}},
		{"imports", []schema.Interface{ClImports{}}, []string{`schema ClImports: field "b": imports math/rand and math/rand/v2 both named rand`}},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := lower(t, Config{}, c.ss...)
			if err == nil {
				t.Fatal("no error")
			}
			if _, ok := errors.AsType[*Error](err); !ok {
				t.Fatalf("%T %v is not an *Error", err, err)
			}
			for _, w := range c.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
			if n := strings.Count(err.Error(), "\n") + 1; n != len(c.want) {
				t.Errorf("%d errors, want %d: %v", n, len(c.want), err)
			}
		})
	}
}

func TestLowerImportClash(t *testing.T) {
	_, err := lower(t, Config{}, ClImports{})
	if err == nil || !strings.Contains(err.Error(), "both named rand") {
		t.Fatalf("err = %v", err)
	}
}

func TestLowerBound(t *testing.T) {
	o := mustLower(t, Config{Bound: map[string]Bound{"LwPost": {Policy: true}}}, LwUser{}, LwPost{}, LwGroup{})
	if b := o.entity("LwPost").Bound; !b.Policy || b.Hooks {
		t.Errorf("bound %+v", b)
	}
	if b := o.entity("LwUser").Bound; b != (Bound{}) {
		t.Errorf("bound %+v", b)
	}
}

func TestLowerDeterministic(t *testing.T) {
	cfg := Config{Package: "gen", SchemaPath: lowerSchemaPath}
	a := mustLower(t, cfg, LwUser{}, LwPost{}, LwGroup{})
	b := mustLower(t, cfg, LwUser{}, LwPost{}, LwGroup{})
	c := mustLower(t, cfg, LwGroup{}, LwPost{}, LwUser{})
	if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(a, c) {
		t.Fatal("Lower is not deterministic")
	}
}

// An index, a string ID, a uuid ID and a foreign key that is a
// pointer to a uuid, from build_test.go's schemas.
func TestLowerIndexGoTypes(t *testing.T) {
	o := mustLower(t, Config{}, User{}, Post{}, Group{})
	u := o.entity("User")
	if len(u.Indexes) != 1 || !u.Indexes[0].Unique || !reflect.DeepEqual(u.Indexes[0].Columns, []int{1, 2}) {
		t.Errorf("indexes %+v", u.Indexes)
	}

	o = mustLower(t, Config{}, Account{}, Profile{}, Role{}, Home{})
	const pkg = "github.com/syssam/velox/internal/codegen"
	a, h := o.entity("Account"), o.entity("Home")
	if f := h.field("id"); f.GoType != "codegen.UUIDValue" || f.Handle != "velox.Col[Home, codegen.UUIDValue]" || f.Pointer {
		t.Errorf("home id %+v", f)
	}
	if f := a.field("account_home"); f.GoType != "codegen.UUIDValue" || f.Pointer || f.Handle != "velox.Col[Account, codegen.UUIDValue]" {
		t.Errorf("account_home %+v", f) // a required edge: the key is not a pointer
	}
	if f := a.field("id"); f.GoType != "string" || f.Handle != "velox.StringCol[Account, string]" || f.Pointer {
		t.Errorf("account id %+v", f)
	}
	if !reflect.DeepEqual(a.Imports, []string{"errors", pkg}) || !reflect.DeepEqual(h.Imports, []string{pkg}) {
		t.Errorf("imports %v %v", a.Imports, h.Imports)
	}
	if p := o.entity("Profile"); p.TableSchema != "app" || p.Table != "account_profiles" || p.field("account_profile").Handle != "velox.StringCol[Profile, string]" {
		t.Errorf("profile %+v", p)
	}
	// The join of a storage key: its columns follow the key, its name its table.
	j := o.entity("AccountRoleLink")
	if j == nil || j.Fields[0].Column != "acct" || j.Fields[1].Column != "role" || j.Fields[0].ElemType != "string" {
		t.Fatalf("join %+v", j)
	}
	if r := a.rel("roles"); r.Desc != `velox.EdgeThrough[AccountRoleLink, Role]("roles", accountColID, accountRoleLinkColAcct, accountRoleLinkColRole, roleColID)` {
		t.Errorf("roles %s", r.Desc)
	}
	if r := a.rel("profile"); r.Desc != `velox.EdgeTo[Profile]("profile", accountColID, profileColAccountProfile)` {
		t.Errorf("profile %s", r.Desc)
	}
}

// Two rows never share a decoded default: the code decodes the literal on
// each call. The descriptor is not read for it.
func TestLowerJSONDefaultNeedsNoSchema(t *testing.T) {
	o := mustLower(t, Config{}, LwDecoded{})
	e := o.entity("LwDecoded")
	if e.NeedsSch || o.NeedsSch || !reflect.DeepEqual(e.Imports, []string{"encoding/json"}) {
		t.Errorf("sch %v imports %v", e.NeedsSch, e.Imports)
	}
	want := `func() any { var v map[string]int; _ = json.Unmarshal([]byte("{\"a\":1}"), &v); return v }`
	if f := e.field("m"); f.DefaultFuncCode != want || f.DefaultCode != "" {
		t.Errorf("m %q", f.DefaultFuncCode)
	}
}

type LwDecoded struct{ schema.Schema }

func (LwDecoded) Fields() []schema.Field {
	return []schema.Field{field.JSON("m", map[string]int{}).Default(map[string]int{"a": 1})}
}

type LwSecret struct{ schema.Schema }

func (LwSecret) Fields() []schema.Field {
	return []schema.Field{
		field.String("a").Sensitive().StructTag(`json:"-"`),
		field.String("b").Sensitive().StructTag(`json:"b" db:"x"`),
		field.String("c").Sensitive().StructTag(`db:"c"`),
		field.String("d").Sensitive(),
	}
}

func TestLowerSensitiveTag(t *testing.T) {
	_, err := lower(t, Config{}, LwSecret{})
	if err == nil {
		t.Fatal("no error")
	}
	for _, f := range []string{"b", "c"} {
		if w := `schema LwSecret: field "` + f + `": a Sensitive field's StructTag must not expose it to JSON`; !strings.Contains(err.Error(), w) {
			t.Errorf("error lacks %q: %v", w, err)
		}
	}
	if n := strings.Count(err.Error(), "\n") + 1; n != 2 {
		t.Errorf("%d errors: %v", n, err)
	}
}

type (
	LwPoint struct{ X, Y int }
	LwTags  []string
	LwAttrs map[string]string
)

type LwTagged struct{ schema.Schema }

func (LwTagged) Fields() []schema.Field {
	return []schema.Field{
		field.Time("at"),
		field.Time("seen").Optional().Nillable(),
		field.UUID("u", UUIDValue{}),
		field.JSON("p", LwPoint{}),
		field.JSON("m", map[string]int{}),
		field.JSON("tags", LwTags{}),
		field.JSON("attrs", LwAttrs{}),
		field.JSON("pp", &LwPoint{}),
		field.JSON("arr", [2]int{}),
		field.String("s"),
		field.String("pw").Sensitive(),
	}
}

// TestJSONTags: a struct or an array that is not a pointer is omitzero, as
// omitempty never omits it; a required bool, number, string or enum has no
// omitempty, as its zero is a value; every other field is omitempty, and a
// Sensitive field is never in JSON.
func TestJSONTags(t *testing.T) {
	e := mustLower(t, Config{}, LwTagged{}).entity("LwTagged")
	for name, want := range map[string]string{
		"at":    "`json:\"at,omitzero\"`",
		"seen":  "`json:\"seen,omitempty\"`",
		"u":     "`json:\"u,omitzero\"`",
		"p":     "`json:\"p,omitzero\"`",
		"m":     "`json:\"m,omitempty\"`",
		"tags":  "`json:\"tags,omitempty\"`",
		"attrs": "`json:\"attrs,omitempty\"`",
		"pp":    "`json:\"pp,omitempty\"`",
		"arr":   "`json:\"arr,omitzero\"`",
		"s":     "`json:\"s\"`",
		"pw":    "`json:\"-\"`",
	} {
		if f := e.field(name); f == nil || f.Tag != want {
			t.Errorf("%s: %+v, want tag %s", name, f, want)
		}
	}
}

type LwSchemaA struct{ schema.Schema }

func (LwSchemaA) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("app", "lw_schema_as")}
}
func (LwSchemaA) Edges() []schema.Edge { return []schema.Edge{edge.To("bs", LwSchemaB.Type)} }

type LwSchemaB struct{ schema.Schema }

func (LwSchemaB) Edges() []schema.Edge {
	return []schema.Edge{edge.From("as", LwSchemaA.Type).Ref("bs")}
}

type LwSchemaC struct{ schema.Schema }

func (LwSchemaC) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("app", "lw_schema_cs")}
}
func (LwSchemaC) Edges() []schema.Edge { return []schema.Edge{edge.To("ds", LwSchemaD.Type)} }

type LwSchemaD struct{ schema.Schema }

func (LwSchemaD) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.TableIn("app", "lw_schema_ds")}
}
func (LwSchemaD) Edges() []schema.Edge {
	return []schema.Edge{edge.From("cs", LwSchemaC.Type).Ref("ds")}
}

func TestLowerJoinTableSchema(t *testing.T) {
	// Both sides in "app": the join table is there too.
	o := mustLower(t, Config{}, LwSchemaC{}, LwSchemaD{})
	var join *OutEntity
	for _, e := range o.Entities {
		if e.Join {
			join = e
		}
	}
	if join == nil || join.TableSchema != "app" {
		t.Fatalf("join %+v", join)
	}
	// One side in "app", the other in the default schema.
	_, err := lower(t, Config{}, LwSchemaA{}, LwSchemaB{})
	want := `schema LwSchemaA: edge "bs": join table lw_schemaa_bs lies between tables in different schemas ("app" and "")`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want %s", err, want)
	}
}

// Numbers are checked against the element type, which the DSL's typed
// builders cannot get wrong: a model edited by hand can.
func TestLowerNumbersFit(t *testing.T) {
	for _, c := range []struct {
		name  string
		edit  func(*Field)
		field string
		want  string
	}{
		{"default overflows", func(f *Field) { f.Default = []byte("300") }, "n", `field "n": its Default 300 is not a int8`},
		{"default negative unsigned", func(f *Field) { f.Default = []byte("-1") }, "u", `field "u": its Default -1 is not a uint8`},
		{"default float32 overflow", func(f *Field) { f.Default = []byte("1e300") }, "f", `field "f": its Default 1e300 is not a float32`},
		{"check negative unsigned", func(f *Field) { f.Checks = []loadjson.Check{{Kind: "Min", Args: []json.RawMessage{[]byte("-1")}}} }, "u", `field "u": check Min: -1 is not a uint8`},
		{"check overflow", func(f *Field) { f.Checks = []loadjson.Check{{Kind: "Max", Args: []json.RawMessage{[]byte("128")}}} }, "n", `field "n": check Max: 128 is not a int8`},
		{"check fraction", func(f *Field) { f.Checks = []loadjson.Check{{Kind: "Min", Args: []json.RawMessage{[]byte("1.5")}}} }, "n", `field "n": check Min: 1.5 is not a int8`},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := mustBuild(t, LwNumbers{})
			c.edit(g.Entity("LwNumbers").Field(c.field))
			_, err := Lower(g, Config{})
			if err == nil || !strings.Contains(err.Error(), "schema LwNumbers: "+c.want) {
				t.Fatalf("err = %v, want %s", err, c.want)
			}
		})
	}
	// Edge values fit.
	g := mustBuild(t, LwNumbers{})
	g.Entity("LwNumbers").Field("n").Default = []byte("-128")
	g.Entity("LwNumbers").Field("u").Default = []byte("255")
	o, err := Lower(g, Config{})
	if err != nil {
		t.Fatal(err)
	}
	if f := o.entity("LwNumbers").field("n"); f.DefaultCode != "int8(-128)" {
		t.Errorf("n %q", f.DefaultCode)
	}
}

type LwNumbers struct{ schema.Schema }

func (LwNumbers) Fields() []schema.Field {
	return []schema.Field{field.Int8("n"), field.Uint8("u"), field.Float32("f")}
}

func TestLowerJoinTableIsEntityTable(t *testing.T) {
	g := mustBuild(t, LwUser{}, LwPost{}, LwGroup{})
	g.Entity("LwPost").Table = "lw_user_groups" // Build refuses this; Lower does too
	_, err := Lower(g, Config{})
	want := `schema LwUser: edge "groups": join table lw_user_groups is also the table of schema LwPost`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want %s", err, want)
	}
}

// LWUserGroup has the label of the join entity LwUserGroup.
type LWUserGroup struct{ schema.Schema }

func (LWUserGroup) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("lw_ugs")}
}

func TestLowerJoinLabelCollision(t *testing.T) {
	_, err := lower(t, Config{}, LwUser{}, LwPost{}, LwGroup{}, LWUserGroup{})
	want := `schema LwUser: edge "groups": join entity LwUserGroup has label lw_user_group, which is also LWUserGroup's`
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want %s", err, want)
	}
}

type LwPkgClash struct{ schema.Schema }

func (LwPkgClash) Fields() []schema.Field {
	return []schema.Field{
		field.JSON("a", &errpkg.T{}),
		field.JSON("b", &schpkg.T{}),
		field.JSON("c", &json.Decoder{}), // the package the generated code means by json
		field.Time("d").GoType(time.Time{}),
	}
}

func TestLowerPackageNameClash(t *testing.T) {
	_, err := lower(t, Config{SchemaPath: lowerSchemaPath}, LwPkgClash{})
	if err == nil {
		t.Fatal("no error")
	}
	w := `schema LwPkgClash: field "a": imports github.com/syssam/velox/internal/codegen/internal/errors, but the generated code uses the name errors for errors`
	if err.Error() != w {
		t.Errorf("error %v, want %s", err, w)
	}
	// A schema package named as a field type's package, sch here, takes
	// another name.
	g, err := build(t, LwPkgClash{})
	if err != nil {
		t.Fatal(err)
	}
	l := &lowerer{g: g, out: &Out{Package: "gen", SchemaPath: "example.com/x/sch"}, owned: map[string]string{}}
	l.schemaNames()
	if l.out.SchemaName != "userschema" {
		t.Errorf("SchemaName %s, want userschema", l.out.SchemaName)
	}
}

type LwTime struct{ time.Time }

type LwWhen struct{ schema.Schema }

func (LwWhen) Fields() []schema.Field {
	return []schema.Field{field.Time("at").GoType(LwTime{}), field.Time("opt").Nillable().GoType(LwTime{})}
}

func TestLowerTimeIsOrdered(t *testing.T) {
	e := mustLower(t, Config{}, LwWhen{}).entity("LwWhen")
	if f := e.field("at"); f.Handle != "velox.OrderedCol[LwWhen, codegen.LwTime]" {
		t.Errorf("at %+v", f)
	}
	if f := e.field("opt"); f.Handle != "velox.NullOrderedCol[LwWhen, codegen.LwTime]" || f.GoType != "*codegen.LwTime" {
		t.Errorf("opt %+v", f)
	}
}

type LwStatus string

func (LwStatus) Values() []string { return []string{"on", "off"} }

type LwStatusful struct{ schema.Schema }

func (LwStatusful) Fields() []schema.Field {
	return []schema.Field{field.Enum("st").GoType(LwStatus("")).Default("off")}
}

func TestLowerEnumDefault(t *testing.T) {
	o := mustLower(t, Config{}, LwStatusful{})
	f := o.entity("LwStatusful").field("st")
	if f.DefaultCode != `codegen.LwStatus("off")` || f.GoType != "codegen.LwStatus" {
		t.Errorf("st %+v", f)
	}
	if len(o.Enums) != 0 {
		t.Errorf("a GoType enum generates no type: %+v", o.Enums[0])
	}
	// An enum with no Go type has its constant: see LwMixed.tier.
}

func TestLowerOneToOneDescriptor(t *testing.T) {
	o := mustLower(t, Config{}, Account{}, Profile{}, Role{}, Home{}, Person{})
	if r := o.entity("Profile").rel("account"); r.Many || r.Desc != `velox.EdgeTo[Account]("account", profileColAccountProfile, accountColID)` {
		t.Errorf("inverse %+v", r) // the key is in Profile, the inverse's owner
	}
	if r := o.entity("Person").rel("spouse"); r.Many || r.Desc != `velox.EdgeBidi[Person]("spouse", personColPersonSpouse, personColID)` {
		t.Errorf("bidi %+v", r)
	}
}

type LwReserved struct{ schema.Schema }

func (LwReserved) Fields() []schema.Field {
	return []schema.Field{field.String("velox_meta"), field.String("policy"), field.String("go_string")}
}

func TestLowerReservedNames(t *testing.T) {
	_, err := lower(t, Config{}, LwReserved{})
	for _, w := range []string{
		`schema LwReserved: field "velox_meta": its Go name VeloxMeta is reserved`,
		`schema LwReserved: field "policy": its Go name Policy is reserved`,
		`schema LwReserved: field "go_string": its Go name GoString is reserved`,
	} {
		if err == nil || !strings.Contains(err.Error(), w) {
			t.Errorf("err = %v, want %s", err, w)
		}
	}
}

// A field's Unique is a single-column unique index, after the declared
// indexes and in field order; the ID has none.
type LwUniq struct{ schema.Schema }

func (LwUniq) Fields() []schema.Field {
	return []schema.Field{field.String("email").Unique(), field.String("name"), field.Int("n").Unique()}
}
func (LwUniq) Indexes() []schema.Index { return []schema.Index{index.Fields("name", "n").Unique()} }

func TestLowerFieldUniqueIndex(t *testing.T) {
	e := mustLower(t, Config{}, LwUniq{}).entity("LwUniq")
	want := []OutIndex{{Columns: []int{2, 3}, Unique: true}, {Columns: []int{1}, Unique: true}, {Columns: []int{3}, Unique: true}}
	if !reflect.DeepEqual(e.Indexes, want) {
		t.Fatalf("indexes %+v, want %+v", e.Indexes, want)
	}
}

// A declared single-column unique index on a Unique field is that field's
// index: it is not emitted twice.
type LwUniq2 struct{ schema.Schema }

func (LwUniq2) Fields() []schema.Field {
	return []schema.Field{field.String("email").Unique(), field.String("code").Unique(), field.String("plain")}
}
func (LwUniq2) Indexes() []schema.Index {
	return []schema.Index{index.Fields("email").Unique(), index.Fields("code"), index.Fields("plain").Unique()}
}

func TestLowerFieldUniqueIndexIsNotRepeated(t *testing.T) {
	e := mustLower(t, Config{}, LwUniq2{}).entity("LwUniq2")
	want := []OutIndex{
		{Columns: []int{1}, Unique: true}, // email: declared, and by Unique()
		{Columns: []int{2}},               // code: declared, not unique
		{Columns: []int{3}, Unique: true}, // plain: declared only
		{Columns: []int{2}, Unique: true}, // code: by Unique() only; the declared one is not unique
	}
	if !reflect.DeepEqual(e.Indexes, want) {
		t.Fatalf("indexes %+v, want %+v", e.Indexes, want)
	}
}

// A partial unique index is unique only over the rows its predicate keeps,
// so the core sees it as non-unique, and a Unique field on its column keeps
// its own index.
type LwPartial struct{ schema.Schema }

func (LwPartial) Fields() []schema.Field {
	return []schema.Field{field.String("email").Unique(), field.Bool("active").Nillable(), field.String("code")}
}
func (LwPartial) Indexes() []schema.Index {
	return []schema.Index{index.Fields("email").Where("active").Unique(), index.Fields("code").Where("active").Unique()}
}

func TestLowerPartialUniqueIndex(t *testing.T) {
	e := mustLower(t, Config{}, LwPartial{}).entity("LwPartial")
	want := []OutIndex{
		{Columns: []int{1}}, // email, partial: not unique for the table
		{Columns: []int{3}}, // code, partial
		{Columns: []int{1}, Unique: true},
	}
	if !reflect.DeepEqual(e.Indexes, want) {
		t.Fatalf("indexes %+v, want %+v", e.Indexes, want)
	}
}

// A conversion in the Check code is there only where the types differ: x is
// already a string or an int when the field has no Go type of its own.
type LwCents int64
type LwSlug string

type LwConv struct{ schema.Schema }

func (LwConv) Fields() []schema.Field {
	return []schema.Field{
		field.String("plain").Match(regexp.MustCompile(`^a`)).Validate(func(string) error { return nil }),
		field.String("slug").GoType(LwSlug("")).Match(regexp.MustCompile(`^b`)).Validate(func(string) error { return nil }),
		field.Int64("n").Range(0, 9).Validate(func(int64) error { return nil }),
		field.Int64("cents").GoType(LwCents(0)).Range(0, 9).Validate(func(int64) error { return nil }),
	}
}

func TestLowerChecksConvertOnlyWhenTypesDiffer(t *testing.T) {
	e := mustLower(t, Config{}, LwConv{}).entity("LwConv")
	for _, want := range []string{
		"x := v.(string)", "re0.MatchString(x)", "d0(x)",
		"x := v.(codegen.LwSlug)", "re1.MatchString(string(x))", "d1(string(x))",
		"x := v.(int64)", "if x < 0 {", "if x > 9 {", "d2(x)",
		"x := v.(codegen.LwCents)", "if x < codegen.LwCents(0) {", "if x > codegen.LwCents(9) {", "d3(int64(x))",
	} {
		if !strings.Contains(e.CheckCode, want) {
			t.Errorf("CheckCode lacks %q:\n%s", want, e.CheckCode)
		}
	}
	if strings.Contains(e.CheckCode, "int64(x)") && strings.Count(e.CheckCode, "int64(x)") != 1 {
		t.Errorf("a redundant int64(x):\n%s", e.CheckCode)
	}
	if strings.Count(e.CheckCode, "string(x)") != 2 {
		t.Errorf("want string(x) twice (the GoType field only):\n%s", e.CheckCode)
	}
}

// lwTypedSpelling picks how LwPriced spells its fields: with the typed
// constructors, or as in ent.
var lwTypedSpelling bool

type LwPriced struct{ schema.Schema }

func (LwPriced) Fields() []schema.Field {
	if lwTypedSpelling {
		return []schema.Field{
			field.Number[LwCents]("price").Nillable().Default(5).Min(1).Validate(func(LwCents) error { return nil }),
			field.EnumOf[LwStatus]("st").Default("off"),
			field.UUIDOf[UUIDValue]("ref").Default(func() UUIDValue { return UUIDValue{1} }),
		}
	}
	return []schema.Field{
		field.Int64("price").GoType(LwCents(0)).Nillable().Default(5).Min(1).Validate(func(int64) error { return nil }),
		field.Enum("st").GoType(LwStatus("")).Default("off"),
		field.UUID("ref", UUIDValue{}).Default(func() UUIDValue { return UUIDValue{1} }),
	}
}

// A typed constructor generates the code of its ent spelling (Review Focus
// 4), and a Number's validator, which takes the GoType, is called with the
// field's kind: the builder converts.
func TestLowerTypedConstructors(t *testing.T) {
	t.Cleanup(func() { lwTypedSpelling = false })
	var outs [2]*Out
	for i, typed := range []bool{true, false} {
		lwTypedSpelling = typed
		outs[i] = mustLower(t, Config{SchemaPath: lowerSchemaPath}, LwPriced{})
	}
	if !reflect.DeepEqual(outs[0], outs[1]) {
		t.Fatalf("typed %+v\nent %+v", outs[0].entity("LwPriced"), outs[1].entity("LwPriced"))
	}
	e := outs[0].entity("LwPriced")
	for _, want := range []string{
		`f0 := schema.FieldDescriptor(&codegen.LwPriced{}, "price")`,
		`d0 := f0.Validators[0].(func(int64) error)`,
	} {
		if !strings.Contains(e.CheckSetup, want) {
			t.Errorf("setup lacks %s:\n%s", want, e.CheckSetup)
		}
	}
	for _, want := range []string{"case lwPricedColPrice:", "x := v.(codegen.LwCents)", "if x < codegen.LwCents(1) {", "if err := d0(int64(x)); err != nil {"} {
		if !strings.Contains(e.CheckCode, want) {
			t.Errorf("CheckCode lacks %s:\n%s", want, e.CheckCode)
		}
	}
	if f := e.field("price"); f.DefaultCode != "codegen.LwCents(5)" || f.GoType != "*codegen.LwCents" {
		t.Errorf("price %+v", f)
	}
}

// LwStamp is a time type that converts from time.Time.
type LwStamp time.Time

type LwStamped struct{ schema.Schema }

func (LwStamped) Fields() []schema.Field {
	return []schema.Field{
		field.Time("at").GoType(LwStamp{}).Default(time.Now).UpdateDefault(time.Now),
		field.Time("plain").Default(time.Now),
	}
}

// A time default is a func() time.Time; with a GoType, the generated code
// converts what it returns.
func TestLowerTimeDefaultConverts(t *testing.T) {
	e := mustLower(t, Config{SchemaPath: lowerSchemaPath}, LwStamped{}).entity("LwStamped")
	for _, want := range []string{
		`f0 := schema.FieldDescriptor(&codegen.LwStamped{}, "at")`,
		`d0 := f0.Default.(func() time.Time)`,
		`d1 := f0.UpdateDefault.(func() time.Time)`,
		`f1 := schema.FieldDescriptor(&codegen.LwStamped{}, "plain")`,
		`d2 := f1.Default.(func() time.Time)`,
	} {
		if !strings.Contains(e.CheckSetup, want) {
			t.Errorf("setup lacks %s:\n%s", want, e.CheckSetup)
		}
	}
	if f := e.field("at"); f.DefaultFuncCode != "func() any { return codegen.LwStamp(d0()) }" || f.UpdateDefaultCode != "func() any { return codegen.LwStamp(d1()) }" {
		t.Errorf("at %+v", f)
	}
	if f := e.field("plain"); f.DefaultFuncCode != "func() any { return d2() }" {
		t.Errorf("plain %+v", f)
	}
	if !slices.Contains(e.Imports, "time") {
		t.Errorf("imports %v", e.Imports)
	}
}

// LwNull has a field of each way a column can hold NULL, or not.
type LwNull struct{ schema.Schema }

func (LwNull) Fields() []schema.Field {
	return []schema.Field{
		field.Strings("tags").Optional(),
		field.JSON("attrs", map[string]any{}).Optional(),
		field.JSON("box", LwBox{}).Optional().Nillable(),
		field.JSON("shape", LwBox{}),
		field.Bytes("blob").Nillable(),
		field.Bytes("raw").Optional().Default([]byte("r")),
		field.String("bio").Optional(),
	}
}

// TestLowerNullable: the plugin model's Nullable is a column that holds
// NULL: Nillable, or an Optional JSON slice or map; and the ColumnDescriptor of a
// NULL-able field that is a slice or a map, no pointer, says Nullable.
func TestLowerNullable(t *testing.T) {
	g := mustBuild(t, LwNull{})
	o, err := Lower(g, Config{Package: "gen", SchemaPath: lowerSchemaPath})
	if err != nil {
		t.Fatal(err)
	}
	pe := pluginEntity(t, PluginModel(g, o), "LwNull")
	e := o.entity("LwNull")
	for _, c := range []struct {
		name               string
		nullable, nilIsNil bool
	}{
		{"tags", true, true}, {"attrs", true, true}, {"box", true, false}, {"shape", false, false},
		{"blob", true, true}, {"raw", false, false}, {"bio", false, false},
	} {
		if got := pluginField(t, pe, c.name).Nullable; got != c.nullable {
			t.Errorf("%s: plugin Nullable %v, want %v", c.name, got, c.nullable)
		}
		if got := e.field(c.name).NilIsNull; got != c.nilIsNil {
			t.Errorf("%s: NilIsNull %v, want %v", c.name, got, c.nilIsNil)
		}
	}
	files, err := Generate(g, Config{Package: "gen", SchemaPath: lowerSchemaPath})
	if err != nil {
		t.Fatal(err)
	}
	var src string
	for _, f := range files {
		src += string(f.Data)
	}
	if want := `{Field: "Blob", Name: "blob", Nullable: true},`; !strings.Contains(src, want) {
		t.Errorf("the generated code lacks %s", want)
	}
}

// sdUser has a SchemaDefault when withDefault is set.
type sdUser struct {
	schema.Schema
	withDefault bool
}

func (u sdUser) Fields() []schema.Field {
	f := field.Time("seen")
	if u.withDefault {
		f = f.SchemaDefault(map[string]string{field.Postgres: "now()"})
	}
	return []schema.Field{f, field.String("name")}
}

// A SchemaDefault is for the migration planner only: Generate gives the same
// files with and without it.
func TestSchemaDefaultChangesNoCode(t *testing.T) {
	gen := func(u sdUser) map[string]string {
		g, err := build(t, u)
		if err != nil {
			t.Fatal(err)
		}
		files, err := Generate(g, Config{Package: "gen", SchemaPath: lowerSchemaPath})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, f := range files {
			out[f.Name] = string(f.Data)
		}
		return out
	}
	without, with := gen(sdUser{}), gen(sdUser{withDefault: true})
	if len(without) != len(with) {
		t.Fatalf("%d files without, %d with", len(without), len(with))
	}
	for name, a := range without {
		if b, ok := with[name]; !ok || a != b {
			t.Errorf("%s differs with a SchemaDefault", name)
		}
	}
}

// velox.go carries no hash of the model: no file Generate writes has one.
func TestPackageFileHasNoHash(t *testing.T) {
	files, err := Generate(mustBuild(t, User{}, Post{}, Group{}), Config{Package: "gen"})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(string(f.Data), "sha256") {
			t.Errorf("%s mentions sha256", f.Name)
		}
	}
}

// A Required edge whose key is NULL-able, an edge to its own schema, marks its
// key's column RequiredEdge; one whose key is NOT NULL does not, since the
// database refuses it unset.
func TestLowerRequiredEdge(t *testing.T) {
	o := mustLower(t, Config{Package: "gen", SchemaPath: lowerSchemaPath}, reqSelf{}, dodReq{}, dodReqPet{})
	if f := o.entity("reqSelf").field("req_self_children"); f == nil || f.RequiredEdge != "parent" {
		t.Errorf("req_self_children %+v", f)
	}
	if f := o.entity("dodReqPet").field("dod_req_pets"); f == nil || f.RequiredEdge != "" {
		t.Errorf("dod_req_pets %+v", f)
	}
	files, err := Render(o)
	if err != nil {
		t.Fatal(err)
	}
	var all, parent int
	for _, f := range files {
		all += strings.Count(string(f.Data), "RequiredEdge:")
		parent += strings.Count(string(f.Data), `RequiredEdge: "parent"`)
	}
	if all != 1 || parent != 1 {
		t.Errorf("RequiredEdge written %d times, %d of them for parent; want once, for parent", all, parent)
	}
}

type LwOptionalChecks struct{ schema.Schema }

func (LwOptionalChecks) Fields() []schema.Field {
	return []schema.Field{
		field.String("only_empty").Optional().NotEmpty(),               // col 1: no check can fail
		field.String("short").Optional().NotEmpty().MaxLen(5),          // col 2: MaxLen only
		field.String("min_one").Optional().MinLen(1).MinRuneLen(1),     // col 3: no check can fail
		field.String("min_two").Optional().MinLen(2),                   // col 4: "a" is shorter, and set
		field.String("required").NotEmpty(),                            // col 5: not Optional, so NotEmpty can fail
		field.Bytes("blob").Optional().Default([]byte("x")).NotEmpty(), // col 6: no check can fail
	}
}

// An Optional value field passes at its zero value, so a check that only the
// zero value fails is not written: it could never fire.
func TestLowerOptionalChecksThatCannotFail(t *testing.T) {
	o := mustLower(t, Config{SchemaPath: lowerSchemaPath}, LwOptionalChecks{})
	code := o.entity("LwOptionalChecks").CheckCode
	for _, col := range []string{"case lwOptionalChecksColOnlyEmpty:", "case lwOptionalChecksColMinOne:", "case lwOptionalChecksColBlob:"} {
		if strings.Contains(code, col) {
			t.Errorf("%s has no check that can fail, and is written:\n%s", col, code)
		}
	}
	for _, want := range []string{
		"case lwOptionalChecksColShort:", "if len(x) > 5 {",
		"case lwOptionalChecksColMinTwo:", "if len(x) < 2 {",
		"case lwOptionalChecksColRequired:", `if x == "" {`,
	} {
		if !strings.Contains(code, want) {
			t.Errorf("Check lacks %s:\n%s", want, code)
		}
	}
	if strings.Count(code, `"must not be empty"`) != 1 {
		t.Errorf("only the required field's NotEmpty can fail:\n%s", code)
	}
}

// A default that is a slice of strings, numbers or bools is a Go literal in
// the generated code, not JSON decoded on each insert; any other default is
// still decoded, so that no two rows share a slice or a map.
func TestSliceLiteral(t *testing.T) {
	for _, c := range []struct{ raw, elem, want string }{
		{`["new","a\"b"]`, "[]string", `[]string{"new", "a\"b"}`},
		{`[1,2]`, "[]int", `[]int{1, 2}`},
		{`[1.5]`, "[]float64", `[]float64{1.5}`},
		{`[true]`, "[]bool", `[]bool{true}`},
		{`[]`, "[]string", `[]string{}`},
		{`null`, "[]string", ``},
		{`[300]`, "[]int8", ``},
		{`[-1]`, "[]uint", ``},
		{`[["a"]]`, "[][]string", ``},
		{`["a"]`, "Tags", ``},
		{`[{"a":1}]`, "[]Box", ``},
	} {
		if got := sliceLiteral([]byte(c.raw), c.elem); got != c.want {
			t.Errorf("sliceLiteral(%s, %s) = %q, want %q", c.raw, c.elem, got, c.want)
		}
	}
}
