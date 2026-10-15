package gen

import (
	"errors"
	"math/rand"
	randv2 "math/rand/v2"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/syssam/velox2/schema"
	"github.com/syssam/velox2/schema/edge"
	"github.com/syssam/velox2/schema/field"
	"github.com/syssam/velox2/schema/mixin"
)

const lowerSchemaPath = "github.com/syssam/velox2/internal/gen"

// The schemas of the lowering tests. The prefix Lw makes the label lw_user, so
// that the join entity of lw_user_groups is LwUserGroup. (snake("LUser") is
// "luser": an upper-case letter after a first upper-case letter starts no word.)
type LwUser struct{ schema.Schema }

func (LwUser) Fields() []schema.Field {
	return []schema.Field{
		field.String("name").NotEmpty(),
		field.String("nick").Optional(),
		field.Bool("active").Default(true),
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
		{"nick", "*string", "velox.NullStringCol[LwUser, string]"},        // Optional makes a pointer (Ruling 2)
		{"active", "*bool", "velox.NullCol[LwUser, bool]"},                // a literal Default on a bool (plan 1 Ruling 4)
		{"role", "LwUserRole", "velox.StringCol[LwUser, LwUserRole]"},     // the generated enum type (Ruling 3)
		{"seen", "*time.Time", "velox.NullOrderedCol[LwUser, time.Time]"}, //
		{"tags", "[]string", "velox.Col[LwUser, []string]"},               // a JSON slice is never a pointer
	} {
		f := u.field(c.name)
		if f == nil || f.GoType != c.goType || f.Handle != c.handle {
			t.Errorf("%s: %+v, want %s %s", c.name, f, c.goType, c.handle)
		}
	}
	if f := u.field("nick"); !f.Pointer || f.ElemType != "string" || f.GoName != "Nick" || f.Column != "nick" {
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
	if !reflect.DeepEqual(u.Imports, []string{"errors", "time"}) || u.Handles != "LwUsers" || u.Label != "lw_user" || u.Table != "lw_users" {
		t.Errorf("user imports %v handles %s label %s table %s", u.Imports, u.Handles, u.Label, u.Table)
	}
	if o.Package != "gen" || o.SchemaPath != lowerSchemaPath || len(o.Hash) != 64 || o.NeedsSch {
		t.Errorf("out %q %q %q %v", o.Package, o.SchemaPath, o.Hash, o.NeedsSch)
	}
	if !reflect.DeepEqual(u.PrimaryKey, []int{0}) || u.Tenant != -1 {
		t.Errorf("pk %v tenant %d", u.PrimaryKey, u.Tenant)
	}
}

func TestLowerRelationsAndJoin(t *testing.T) {
	o := mustLower(t, Config{Package: "gen"}, LwUser{}, LwPost{}, LwGroup{})
	u, p, j := o.entity("LwUser"), o.entity("LwPost"), o.entity("LwUserGroup")
	if r := u.rel("posts"); r == nil || !r.Many || r.Target != "LwPost" || r.GoName != "Posts" || r.Desc != `velox.EdgeTo[LwPost]("Posts", 0, 1)` {
		t.Fatalf("posts %+v", r)
	}
	if r := p.rel("author"); r == nil || r.Many || r.Desc != `velox.EdgeTo[LwUser]("Author", 1, 0)` {
		t.Fatalf("author %+v", r)
	}
	if j == nil || !j.Join || len(j.Fields) != 2 || j.Fields[0].GoName != "LwUserID" || j.Fields[1].GoName != "LwGroupID" ||
		len(j.PrimaryKey) != 2 || j.Tenant != -1 || j.Table != "lw_user_groups" || len(j.Rels) != 0 ||
		j.Fields[0].Handle != "velox.NumberCol[LwUserGroup, int64]" || j.Handles != "LwUserGroups" {
		t.Fatalf("join %+v", j)
	}
	if r := u.rel("groups"); r.Desc != `velox.EdgeThrough[LwUserGroup, LwGroup]("Groups", 0, 0, 1, 0)` || !r.Many {
		t.Fatalf("groups %s", r.Desc)
	}
	// The inverse sees the same join entity with its columns the other way round.
	if r := o.entity("LwGroup").rel("users"); r.Desc != `velox.EdgeThrough[LwUserGroup, LwUser]("Users", 0, 1, 0, 0)` {
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
	if r := n.rel("children"); r.Desc != `velox.EdgeTo[LwNode]("Children", 0, 1)` || !r.Many {
		t.Fatalf("children %+v", r)
	}
	if r := n.rel("parent"); r.Desc != `velox.EdgeTo[LwNode]("Parent", 1, 0)` || r.Many {
		t.Fatalf("parent %+v", r)
	}
	if r := n.rel("friends"); r.Desc != `velox.EdgeThrough[LwNodeFriend, LwNode]("Friends", 0, 0, 1, 0)` {
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
		`v0 := sch.LwChecked{}.Fields()[0].Descriptor().Validators[0].(func(string) error)`,
		`v1 := sch.LwChecked{}.Fields()[1].Descriptor().Validators[0].(func(int) error)`,
	} {
		if !strings.Contains(e.CheckSetup, want) {
			t.Errorf("setup lacks %s:\n%s", want, e.CheckSetup)
		}
	}
	// The ID is column 0; code is column 1.
	code := []string{
		"case 1:",
		"x := v.(string)",
		`if x == "" {`, `errors.New("must not be empty")`,
		"if len(x) > 5 {", `errors.New("length is more than 5")`,
		"if !re0.MatchString(string(x)) {", `errors.New("does not match ^[a-z]+$")`,
		"if err := v0(string(x)); err != nil {",
		"case 2:",
		"x := v.(int)",
		"if x < int(1) {", `errors.New("is less than 1")`,
		"if x > int(120) {", `errors.New("is more than 120")`,
		"if err := v1(int(x)); err != nil {",
		"case 3:",
		"x := v.(LwCheckedState)",
		`case "on", "off":`, "default:", `errors.New("is not one of the values")`,
		"case 4:",
		"x := v.(int64)",
		"if x <= 0 {", `errors.New("is not positive")`,
	}
	at := 0
	for _, w := range code {
		i := strings.Index(e.CheckCode[at:], w)
		if i < 0 {
			t.Fatalf("CheckCode lacks %q after offset %d:\n%s", w, at, e.CheckCode)
		}
		at += i + len(w)
	}
	if strings.Contains(e.CheckCode, "case 5:") || strings.Contains(e.CheckCode, "case 0:") {
		t.Errorf("a column without checks has a case:\n%s", e.CheckCode)
	}
	if !reflect.DeepEqual(e.Imports, []string{"errors", "regexp"}) {
		t.Errorf("imports %v", e.Imports)
	}
	// An entity without checks has no code.
	if u := mustLower(t, Config{}, LwNode{}).entity("LwNode"); u.CheckCode != "" || u.CheckSetup != "" || u.NeedsSch {
		t.Errorf("node %q %q", u.CheckCode, u.CheckSetup)
	}
}

// A mixin's field is read at Mixin()[m].Fields()[i], and every default that is
// not a literal comes from the descriptor (Ruling 5).
type lwMix struct{ mixin.Schema }

func (lwMix) Fields() []schema.Field {
	return []schema.Field{
		field.String("slug").Validate(func(string) error { return nil }),
		field.Time("made").Default(time.Now),
		field.Int64("rev").DefaultFunc(func() int64 { return 1 }),
	}
}

type LwMixed struct{ schema.Schema }

func (LwMixed) Mixin() []schema.Mixin { return []schema.Mixin{mixin.Schema{}, lwMix{}} }
func (LwMixed) Fields() []schema.Field {
	return []schema.Field{
		field.String("own").Validate(func(string) error { return nil }).DefaultFunc(func() string { return "x" }),
		field.Time("touched").Optional().UpdateDefault(time.Now),
		field.Bytes("blob").Default([]byte("b")),
		field.String("lit").Default("hi"),
		field.Int("count").Default(5),
		field.Float("ratio").Default(1.5),
	}
}

func TestLowerMixinPosition(t *testing.T) {
	o := mustLower(t, Config{SchemaPath: lowerSchemaPath}, LwMixed{})
	e := o.entity("LwMixed")
	const mix = "sch.LwMixed{}.Mixin()[1].Fields()"
	for _, want := range []string{
		`v0 := ` + mix + `[0].Descriptor().Validators[0].(func(string) error)`,
		`v1 := sch.LwMixed{}.Fields()[0].Descriptor().Validators[0].(func(string) error)`,
	} {
		if !strings.Contains(e.CheckSetup, want) {
			t.Errorf("setup lacks %s:\n%s", want, e.CheckSetup)
		}
	}
	for _, c := range []struct{ name, def, fn, upd string }{
		{"made", "", `func() any { return ` + mix + `[1].Descriptor().Default.(func() time.Time)() }`, ""},
		{"rev", "", `func() any { return ` + mix + `[2].Descriptor().DefaultFunc.(func() int64)() }`, ""},
		{"own", "", `func() any { return sch.LwMixed{}.Fields()[0].Descriptor().DefaultFunc.(func() string)() }`, ""},
		{"touched", "", "", `func() any { return sch.LwMixed{}.Fields()[1].Descriptor().UpdateDefault.(func() time.Time)() }`},
		{"blob", "", `func() any { return sch.LwMixed{}.Fields()[2].Descriptor().Default }`, ""},
		{"lit", `"hi"`, "", ""},
		{"count", `int(5)`, "", ""},
		{"ratio", `float64(1.5)`, "", ""},
	} {
		f := e.field(c.name)
		if f == nil || f.DefaultCode != c.def || f.DefaultFuncCode != c.fn || f.UpdateDefaultCode != c.upd {
			t.Errorf("%s: %+v, want %q %q %q", c.name, f, c.def, c.fn, c.upd)
		}
	}
	if !reflect.DeepEqual(e.Imports, []string{"time"}) {
		t.Errorf("imports %v", e.Imports)
	}
}

type ClBox struct{ schema.Schema }
type ClBoxes struct{ schema.Schema }

type ClUser struct{ schema.Schema }

func (ClUser) Fields() []schema.Field { return []schema.Field{field.Enum("role").Values("a")} }

type ClUserRole struct{ schema.Schema }

type ClTag struct{ schema.Schema }

func (ClTag) Edges() []schema.Edge { return []schema.Edge{edge.To("links", ClItem.Type)} }

type ClItem struct{ schema.Schema }

func (ClItem) Edges() []schema.Edge {
	return []schema.Edge{edge.From("tags", ClTag.Type).Ref("links").StorageKey(edge.Table("cl_joins"))}
}

// ClJoin has the name pascal(singular("cl_joins")).
type ClJoin struct{ schema.Schema }

func (ClJoin) Annotations() []schema.Annotation {
	return []schema.Annotation{schema.Table("cl_join_rows")}
}

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
			var e *Error
			if !errors.As(err, &e) {
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

// A tenant, an index, a string ID, a uuid ID and a foreign key that is a
// pointer to a uuid, from build_test.go's schemas.
func TestLowerTenantIndexGoTypes(t *testing.T) {
	o := mustLower(t, Config{}, User{}, Post{}, Group{})
	u := o.entity("User")
	if u.Tenant != 1 || len(u.Indexes) != 1 || !u.Indexes[0].Unique || !reflect.DeepEqual(u.Indexes[0].Columns, []int{1, 2}) {
		t.Errorf("tenant %d indexes %+v", u.Tenant, u.Indexes)
	}

	o = mustLower(t, Config{}, Account{}, Profile{}, Role{}, Home{})
	const pkg = "github.com/syssam/velox2/internal/gen"
	a, h := o.entity("Account"), o.entity("Home")
	if f := h.field("id"); f.GoType != "gen.uuidValue" || f.Handle != "velox.Col[Home, gen.uuidValue]" || f.Pointer {
		t.Errorf("home id %+v", f)
	}
	if f := a.field("account_home"); f.GoType != "gen.uuidValue" || f.Pointer || f.Handle != "velox.Col[Account, gen.uuidValue]" {
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
	if r := a.rel("roles"); r.Desc != `velox.EdgeThrough[AccountRoleLink, Role]("Roles", 0, 0, 1, 0)` {
		t.Errorf("roles %s", r.Desc)
	}
	if r := a.rel("profile"); r.Desc != `velox.EdgeTo[Profile]("Profile", 0, 1)` {
		t.Errorf("profile %s", r.Desc)
	}
}
