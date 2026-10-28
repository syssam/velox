package sdl

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/contrib/graphql/internal/modeltest"
	"github.com/syssam/velox2/plugin"
)

var update = flag.Bool("update", false, "rewrite the golden SDL")

var golden = filepath.Join("testdata", "velox.graphqls")

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRenderGolden(t *testing.T) {
	got, err := Render(modeltest.Model(t))
	if err != nil {
		t.Fatal(err)
	}
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := readFile(t, golden)
	if got != want {
		gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
		for i := range min(len(gl), len(wl)) {
			if gl[i] != wl[i] {
				t.Fatalf("line %d:\n got %q\nwant %q\n(run go test -update to accept)", i+1, gl[i], wl[i])
			}
		}
		t.Fatalf("got %d lines, want %d (run go test -update to accept)", len(gl), len(wl))
	}
	again, err := Render(modeltest.Model(t))
	if err != nil || again != got {
		t.Errorf("a second render differs: %v", err)
	}
}

// TestSDLIsValid loads the golden SDL with gqlparser. The test model puts the
// application's own directive (@cost) on elements; the application
// declares it in its own schema file, testdata/app.graphqls, which gqlgen
// loads beside velox.graphqls. Without those directives the SDL is valid on
// its own: everything else it uses, it declares.
func TestSDLIsValid(t *testing.T) {
	sdl := readFile(t, golden)
	if _, err := Check(sdl, readFile(t, filepath.Join("testdata", "app.graphqls"))); err != nil {
		t.Fatalf("golden with the application's file: %v", err)
	}
	if _, err := Check(sdl); err == nil || !strings.Contains(err.Error(), "Undefined directive") {
		t.Errorf("golden alone: %v, want an undefined application directive", err)
	}

	m := modeltest.Model(t)
	strip := func(a map[string]json.RawMessage) {
		raw, ok := a["graphql"]
		if !ok {
			return
		}
		var ga graphql.Annotation
		if err := json.Unmarshal(raw, &ga); err != nil {
			t.Fatal(err)
		}
		ga.Directives = slices.DeleteFunc(ga.Directives, func(d graphql.Directive) bool { return d.Name != "deprecated" })
		a["graphql"] = encode(t, ga)
	}
	for _, e := range m.Entities {
		strip(e.Annotations)
		for _, f := range e.Fields {
			strip(f.Annotations)
		}
		for _, ed := range e.Edges {
			strip(ed.Annotations)
		}
	}
	alone, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Check(alone); err != nil {
		t.Errorf("the SDL without the application's directives: %v", err)
	}
}

// ---- model builders ----

func encode(t *testing.T, a graphql.Annotation) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// ann is the annotations of an element with the graphql annotations as.
func ann(t *testing.T, as ...graphql.Annotation) map[string]json.RawMessage {
	t.Helper()
	var a graphql.Annotation
	for _, x := range as {
		a = x.Merge(a).(graphql.Annotation)
	}
	return map[string]json.RawMessage{"graphql": encode(t, a)}
}

// goName is the Go name velox gives a snake_case name, near enough for tests.
func goName(name string) string {
	var b strings.Builder
	for w := range strings.SplitSeq(name, "_") {
		if w == "id" {
			b.WriteString("ID")
			continue
		}
		if w != "" {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}

func field(name, typ string) *plugin.Field {
	return &plugin.Field{Name: name, GoName: goName(name), Column: name, Type: typ}
}

// entity is a Node entity with an int64 id and these fields.
func entity(name string, fields ...*plugin.Field) *plugin.Entity {
	id := &plugin.Field{Name: "id", GoName: "ID", Column: "id", Type: "int64", AutoID: true}
	return &plugin.Entity{
		Name: name, Fields: append([]*plugin.Field{id}, fields...), Edges: []*plugin.Edge{},
		PrimaryKey: []int{0}, Tenant: -1,
	}
}

// edge is an edge as velox-gen gives it: an M2O holds its key on its own
// row; set OwnsKey on an inverse or bidirectional O2O.
func edge(name string, kind plugin.EdgeKind, target string) *plugin.Edge {
	return &plugin.Edge{Name: name, GoName: goName(name), Kind: kind, Target: target, Unique: kind == plugin.M2O || kind == plugin.O2O, OwnsKey: kind == plugin.M2O}
}

func model(es ...*plugin.Entity) *plugin.Model {
	return &plugin.Model{Version: plugin.ModelVersion, Entities: es}
}

// load renders m and loads the SDL with gqlparser, with others beside it.
func load(t *testing.T, m *plugin.Model, others ...string) (*ast.Schema, string) {
	t.Helper()
	sdl, err := Render(m)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Check(sdl, others...)
	if err != nil {
		t.Fatalf("%v\n%s", err, sdl)
	}
	return s, sdl
}

// typeOf is the GraphQL type of typ.name, "" when the field is absent and
// "<no type>" when typ is.
func typeOf(s *ast.Schema, typ, name string) string {
	def := s.Types[typ]
	if def == nil {
		return "<no type " + typ + ">"
	}
	f := def.Fields.ForName(name)
	if f == nil {
		return ""
	}
	return f.Type.String()
}

// goField is the name argument of name's @goField, "" without one.
func goField(s *ast.Schema, typ, name string) string {
	f := s.Types[typ].Fields.ForName(name)
	if f == nil {
		return "<no field>"
	}
	d := f.Directives.ForName("goField")
	if d == nil {
		return ""
	}
	return d.Arguments.ForName("name").Value.Raw
}

// fieldsOf lists typ's fields, or an input's, as "name: Type", with " = v"
// for a default value; nil when typ is not declared.
func fieldsOf(s *ast.Schema, typ string) []string {
	def := s.Types[typ]
	if def == nil {
		return nil
	}
	out := []string{}
	for _, f := range def.Fields {
		if strings.HasPrefix(f.Name, "__") {
			continue
		}
		d := f.Name + ": " + f.Type.String()
		if f.DefaultValue != nil {
			d += " = " + f.DefaultValue.String()
		}
		out = append(out, d)
	}
	return out
}

// argsOf lists the arguments of typ.name as "name: Type".
func argsOf(s *ast.Schema, typ, name string) []string {
	out := []string{}
	for _, a := range s.Types[typ].Fields.ForName(name).Arguments {
		out = append(out, a.Name+": "+a.Type.String())
	}
	return out
}

func renderErr(t *testing.T, m *plugin.Model, wants ...string) {
	t.Helper()
	sdl, err := Render(m)
	if err == nil {
		t.Fatalf("no error; SDL:\n%s", sdl)
	}
	for _, w := range wants {
		if !strings.Contains(err.Error(), w) {
			t.Errorf("error %q does not name %q", err, w)
		}
	}
}

// ---- tests ----

// TestNodeKeyWhere: the node key of an entity with a where input has
// predicates, typed ID, by default: all eight for an ordered key, the four
// of equality for a uuid or an enum, and an explicit WhereOps wins.
func TestNodeKeyWhere(t *testing.T) {
	equality := []string{"id: ID", "idNEQ: ID", "idIn: [ID!]", "idNotIn: [ID!]"}
	ordered := append(slices.Clone(equality), "idGT: ID", "idGTE: ID", "idLT: ID", "idLTE: ID")
	// withKey is a User whose key has the type typ and the annotations as,
	// and a name field with WhereOps, which gives it a where input.
	withKey := func(typ string, as ...graphql.Annotation) *plugin.Entity {
		name := field("name", "string")
		name.Annotations = ann(t, graphql.WhereOps(graphql.OpEQ))
		u := entity("User", name)
		k := u.Fields[0]
		k.Type = typ
		if typ == "enum" {
			k.EnumValues = []string{"a", "b"}
		}
		if len(as) > 0 {
			k.Annotations = ann(t, as...)
		}
		return u
	}
	cases := []struct {
		name string
		u    *plugin.Entity
		want []string // the key's predicates
	}{
		{"int64", withKey("int64"), ordered},
		{"uint64", withKey("uint64"), ordered},
		{"string", withKey("string"), ordered},
		{"uuid", withKey("uuid"), equality},
		{"enum", withKey("enum"), equality},
		{"WhereOps()", withKey("int64", graphql.WhereOps()), ordered},
		{"WhereOps(OpsAll)", withKey("int64", graphql.WhereOps(graphql.OpsAll)), ordered},
		{"WhereOps(OpEQ)", withKey("int64", graphql.WhereOps(graphql.OpEQ)), equality[:1]},
		// A partial explicit set gives those predicates alone, in the
		// input's order.
		{"WhereOps(OpIn, OpGT)", withKey("int64", graphql.WhereOps(graphql.OpIn, graphql.OpGT)), []string{"idIn: [ID!]", "idGT: ID"}},
	}
	for _, c := range cases {
		s, sdl := load(t, model(c.u))
		want := slices.Concat(logic("UserWhereInput"), c.want, []string{"name: String"})
		if got := fieldsOf(s, "UserWhereInput"); !slices.Equal(got, want) {
			t.Errorf("%s:\n got %v\nwant %v\n%s", c.name, got, want, sdl)
		}
	}

	t.Run("skipped", func(t *testing.T) {
		s, _ := load(t, model(withKey("int64", graphql.Skip(graphql.SkipWhereInput))))
		want := append(logic("UserWhereInput"), "name: String")
		if got := fieldsOf(s, "UserWhereInput"); !slices.Equal(got, want) {
			t.Errorf("got %v\nwant %v", got, want)
		}
	})

	// The key alone does not give an entity a where input.
	t.Run("no other predicate", func(t *testing.T) {
		s, _ := load(t, model(entity("User")))
		if got := fieldsOf(s, "UserWhereInput"); got != nil {
			t.Errorf("a where input of %v", got)
		}
	})

	// An op the key's kind lacks is refused, and the error calls the key an
	// ID, not by an internal kind name.
	t.Run("refused op", func(t *testing.T) {
		for _, c := range []struct {
			typ string
			op  graphql.WhereOp
		}{{"uuid", graphql.OpGT}, {"int64", graphql.OpsString}} {
			_, err := Render(model(withKey(c.typ, graphql.WhereOps(c.op))))
			if err == nil || !strings.Contains(err.Error(), "not a predicate of a non-nullable ID field") || strings.Contains(err.Error(), "orderedID") {
				t.Errorf("%s key: %v, want a refusal that names an ID field", c.typ, err)
			}
		}
	})

	// A time key makes no Node: the entity has no id, and so no key predicates.
	t.Run("time key", func(t *testing.T) {
		u := withKey("time")
		s, sdl := load(t, model(u))
		want := append(logic("UserWhereInput"), "name: String")
		if got := fieldsOf(s, "UserWhereInput"); !slices.Equal(got, want) {
			t.Errorf("got %v\nwant %v\n%s", got, want, sdl)
		}
	})
}

func TestTypeMapping(t *testing.T) {
	cases := []struct {
		f    *plugin.Field
		want string // "" for absent
	}{
		{field("s", "string"), "String!"},
		{field("i", "int"), "Int!"},
		{field("i8", "int8"), "Int!"},
		{field("i16", "int16"), "Int!"},
		{field("i32", "int32"), "Int!"},
		{field("i64", "int64"), "Int!"},
		{field("u8", "uint8"), "Int!"},
		{field("u16", "uint16"), "Int!"},
		{field("u32", "uint32"), "Int!"},
		{field("u", "uint"), "Uint64!"},
		{field("u64", "uint64"), "Uint64!"},
		{field("f32", "float32"), "Float!"},
		{field("f64", "float64"), "Float!"},
		{field("b", "bool"), "Boolean!"},
		{field("t", "time"), "Time!"},
		{field("uid", "uuid"), "UUID!"},
		{field("raw", "bytes"), "Bytes!"},
		{field("doc", "json"), "JSON!"},
		{&plugin.Field{Name: "opt", GoName: "Opt", Type: "string", Optional: true}, "String!"}, // Optional alone: a NOT NULL column
		{&plugin.Field{Name: "nil_str", GoName: "NilStr", Type: "string", Nullable: true, Pointer: true}, "String"},
		{&plugin.Field{Name: "nil_time", GoName: "NilTime", Type: "time", Pointer: true, Nullable: true, Optional: true}, "Time"},
		{&plugin.Field{Name: "opt_doc", GoName: "OptDoc", Type: "json", Nullable: true, Optional: true}, "JSON"}, // Optional JSON: a NULL-able column, though no pointer
		{&plugin.Field{Name: "secret", GoName: "Secret", Type: "string", Sensitive: true}, ""},
		{field("org_id", "int64"), ""}, // the tenant column
		{&plugin.Field{Name: "level", GoName: "Level", Type: "enum", EnumValues: []string{"low", "high"}}, "UserLevel!"},
		{&plugin.Field{Name: "mood", GoName: "Mood", Type: "enum", EnumValues: []string{"ok"}, Nullable: true}, "UserMood"},
	}
	u := entity("User")
	for _, c := range cases {
		u.Fields = append(u.Fields, c.f)
	}
	u.Tenant = slices.IndexFunc(u.Fields, func(f *plugin.Field) bool { return f.Name == "org_id" })
	s, _ := load(t, model(u))
	if got := typeOf(s, "User", "id"); got != "ID!" {
		t.Errorf("id: %s", got)
	}
	// The id is the opaque global ID, from the method IDGQL, not the raw key
	// in the struct's ID field.
	if got := goField(s, "User", "id"); got != "IDGQL" {
		t.Errorf("id binds to %q", got)
	}
	if got := s.Types["User"].Interfaces; !slices.Equal(got, []string{"Node"}) {
		t.Errorf("User implements %v", got)
	}
	for _, c := range cases {
		name := camel(c.f.GoName)
		if got := typeOf(s, "User", name); got != c.want {
			t.Errorf("%s (%s): got %q, want %q", c.f.Name, c.f.Type, got, c.want)
		}
	}
	for _, name := range []string{"secret", "orgID", "orgId"} {
		if typeOf(s, "User", name) != "" {
			t.Errorf("%s is exposed", name)
		}
	}

	t.Run("foreign keys", func(t *testing.T) {
		post := entity("Post", field("author_id", "int64"), field("title", "string"))
		author := edge("author", plugin.M2O, "User")
		author.Required, author.Field, author.FieldOn = true, "author_id", "Post"
		post.Edges = []*plugin.Edge{author}
		cat := entity("Category", &plugin.Field{Name: "category_parent", GoName: "CategoryParent", Type: "int64", Nullable: true, Implicit: "parent"})
		parent := edge("parent", plugin.M2O, "Category")
		parent.Field, parent.FieldOn = "category_parent", "Category"
		cat.Edges = []*plugin.Edge{parent}
		user := entity("User")
		posts := edge("posts", plugin.O2M, "Post")
		posts.Field, posts.FieldOn = "author_id", "Post"
		user.Edges = []*plugin.Edge{posts}
		s, _ := load(t, model(cat, post, user))
		for typ, name := range map[string]string{"Post": "authorID", "Category": "categoryParent"} {
			if got := typeOf(s, typ, name); got != "" {
				t.Errorf("%s.%s is exposed as %s", typ, name, got)
			}
		}
		if got := typeOf(s, "Post", "author"); got != "User!" {
			t.Errorf("Post.author: %q", got)
		}
		if got := typeOf(s, "Category", "parent"); got != "Category" {
			t.Errorf("Category.parent: %q", got)
		}
		if got := typeOf(s, "Post", "title"); got != "String!" {
			t.Errorf("Post.title: %q", got)
		}
	})

	t.Run("enum values", func(t *testing.T) {
		st := &plugin.Field{Name: "status", GoName: "Status", Type: "enum", EnumValues: []string{"draft", "in_review", "published", "2fa"}}
		st.Annotations = ann(t, graphql.EnumValues(map[string]string{"published": "LIVE", "2fa": "TWO_FA"}))
		s, _ := load(t, model(entity("User", st)))
		def := s.Types["UserStatus"]
		if def == nil || def.Kind != ast.Enum {
			t.Fatalf("UserStatus: %+v", def)
		}
		var got []string
		for _, v := range def.EnumValues {
			got = append(got, v.Name)
		}
		if want := []string{"DRAFT", "IN_REVIEW", "LIVE", "TWO_FA"}; !slices.Equal(got, want) {
			t.Errorf("values %v, want %v", got, want)
		}
	})

	t.Run("enum value that is not a name", func(t *testing.T) {
		st := &plugin.Field{Name: "status", GoName: "Status", Type: "enum", EnumValues: []string{"ok", "2fa"}}
		renderErr(t, model(entity("User", st)), "User", "status", `"2fa"`, "EnumValues")
		st.EnumValues = []string{"ok", "OK"}
		renderErr(t, model(entity("User", st)), "User", "status", "OK")
		st.EnumValues = []string{"x", "y"}
		st.Annotations = ann(t, graphql.EnumValues(map[string]string{"x": "null"}))
		renderErr(t, model(entity("User", st)), "User", "status", `"null"`)
		st.Annotations = ann(t, graphql.EnumValues(map[string]string{"x": "a-b"}))
		renderErr(t, model(entity("User", st)), "User", "status", `"a-b"`)
		st.Annotations = nil
		st.EnumValues = nil
		renderErr(t, model(entity("User", st)), "User", "status", "no values")
		st.EnumValues = []string{"ok"}
		st.Annotations = ann(t, graphql.EnumValues(map[string]string{"gone": "GONE"}))
		renderErr(t, model(entity("User", st)), "User", "status", `"gone"`)
	})

	t.Run("enum skipped", func(t *testing.T) {
		st := &plugin.Field{Name: "status", GoName: "Status", Type: "enum", EnumValues: []string{"a", "b"}}
		st.Annotations = ann(t, graphql.Skip(graphql.SkipEnumField))
		s, _ := load(t, model(entity("User", st)))
		if got := typeOf(s, "User", "status"); got != "String!" {
			t.Errorf("status: %q", got)
		}
		if s.Types["UserStatus"] != nil {
			t.Error("UserStatus is declared")
		}
	})

	t.Run("other", func(t *testing.T) {
		renderErr(t, model(entity("User", field("link", "other"))), "User", "link", "graphql.Skip()")
		lk := field("link", "other")
		lk.Annotations = ann(t, graphql.Skip())
		load(t, model(entity("User", lk)))
	})

	t.Run("Type renames", func(t *testing.T) {
		user := entity("User", &plugin.Field{Name: "role", GoName: "Role", Type: "enum", Enum: "UserRole", EnumValues: []string{"admin"}})
		user.Annotations = ann(t, graphql.Type("Account"), graphql.QueryField("accounts"))
		posts := edge("posts", plugin.O2M, "Post")
		user.Edges = []*plugin.Edge{posts}
		post := entity("Post")
		author := edge("author", plugin.M2O, "User")
		author.Required = true
		post.Edges = []*plugin.Edge{author}
		group := entity("Group")
		group.Edges = []*plugin.Edge{edge("users", plugin.M2M, "User")}
		m := model(group, post, user)
		m.Enums = []*plugin.Enum{{Type: "UserRole", Values: []string{"admin"}}}
		s, sdl := load(t, m)
		if s.Types["User"] != nil || s.Types["UserConnection"] != nil || s.Types["UserEdge"] != nil {
			t.Errorf("a User type is left:\n%s", sdl)
		}
		// The enum keeps the name of velox-gen's Go enum type, which gqlgen
		// binds it to: Type renames the type, not its enums.
		if s.Types["UserRole"] == nil || s.Types["AccountRole"] != nil {
			t.Errorf("the enum is not UserRole:\n%s", sdl)
		}
		for _, c := range [][3]string{
			{"Account", "id", "ID!"},
			{"Account", "role", "UserRole!"},
			{"AccountConnection", "edges", "[AccountEdge!]!"},
			{"AccountEdge", "node", "Account!"},
			{"Post", "author", "Account!"},
			{"Group", "users", "AccountConnection!"},
			{"Query", "accounts", "AccountConnection!"},
		} {
			if got := typeOf(s, c[0], c[1]); got != c[2] {
				t.Errorf("%s.%s: %q, want %q", c[0], c[1], got, c[2])
			}
		}
	})

	t.Run("enum names", func(t *testing.T) {
		// velox-gen's enum type (Enum is in Model.Enums), and a field of the
		// application's own Go enum type (Enum names that type): both are
		// <Schema><Field>.
		role := &plugin.Field{Name: "role", GoName: "Role", Type: "enum", Enum: "UserRole", EnumValues: []string{"admin"}}
		level := &plugin.Field{Name: "level", GoName: "Level", Type: "enum", Enum: "Level", EnumValues: []string{"low"}}
		m := model(entity("User", role, level))
		m.Enums = []*plugin.Enum{{Type: "UserRole", Values: []string{"admin"}}}
		s, sdl := load(t, m)
		if typeOf(s, "User", "role") != "UserRole!" || typeOf(s, "User", "level") != "UserLevel!" || s.Types["Level"] != nil {
			t.Errorf("enum names:\n%s", sdl)
		}
	})

	t.Run("FieldName", func(t *testing.T) {
		name := field("name", "string")
		name.Annotations = ann(t, graphql.FieldName("displayName"))
		user := entity("User", name, field("created_at", "time"))
		posts := edge("posts", plugin.O2M, "User")
		posts.Annotations = ann(t, graphql.FieldName("articles"))
		user.Edges = []*plugin.Edge{posts}
		s, _ := load(t, model(user))
		if got := typeOf(s, "User", "displayName"); got != "String!" || typeOf(s, "User", "name") != "" {
			t.Errorf("displayName: %q", got)
		}
		if got := goField(s, "User", "displayName"); got != "Name" {
			t.Errorf("displayName binds to %q", got)
		}
		if got := goField(s, "User", "createdAt"); got != "" {
			t.Errorf("createdAt binds to %q", got)
		}
		if got := goField(s, "User", "articles"); got != "PostsGQL" {
			t.Errorf("articles binds to %q", got)
		}
	})
}

func TestSkip(t *testing.T) {
	blog := func(userAnn, nameAnn, postsAnn graphql.Annotation) *plugin.Model {
		name := field("name", "string")
		name.Annotations = ann(t, nameAnn)
		user := entity("User", name, field("email", "string"))
		user.Annotations = ann(t, userAnn)
		if userAnn.Skip&graphql.SkipType == 0 {
			user.Annotations = ann(t, userAnn, graphql.QueryField("users"))
		}
		posts := edge("posts", plugin.O2M, "Post")
		posts.Annotations = ann(t, postsAnn)
		user.Edges = []*plugin.Edge{posts, edge("groups", plugin.M2M, "Group")}
		post := entity("Post", field("author_id", "int64"))
		author := edge("author", plugin.M2O, "User")
		author.Field, author.FieldOn = "author_id", "Post"
		post.Edges = []*plugin.Edge{author}
		group := entity("Group")
		group.Edges = []*plugin.Edge{edge("users", plugin.M2M, "User")}
		return model(group, post, user)
	}

	t.Run("entity", func(t *testing.T) {
		s, sdl := load(t, blog(graphql.Skip(), graphql.Annotation{}, graphql.Annotation{}))
		if strings.Contains(sdl, "User") {
			t.Errorf("User is referenced:\n%s", sdl)
		}
		if typeOf(s, "Post", "author") != "" || typeOf(s, "Group", "users") != "" || typeOf(s, "Query", "users") != "" {
			t.Error("an edge or the query field to User is left")
		}
		if s.Types["Post"] == nil || s.Types["Group"] == nil {
			t.Error("Post or Group is gone")
		}
	})

	t.Run("entity, other parts", func(t *testing.T) {
		s, _ := load(t, blog(graphql.Skip(graphql.SkipWhereInput|graphql.SkipOrderField), graphql.Annotation{}, graphql.Annotation{}))
		if s.Types["User"] == nil || typeOf(s, "Post", "author") != "User" || typeOf(s, "Query", "users") != "UserConnection!" {
			t.Error("User was skipped")
		}
	})

	t.Run("field", func(t *testing.T) {
		s, _ := load(t, blog(graphql.Annotation{}, graphql.Skip(), graphql.Annotation{}))
		if typeOf(s, "User", "name") != "" || typeOf(s, "User", "email") != "String!" {
			t.Error("name is not the only field skipped")
		}
		s, _ = load(t, blog(graphql.Annotation{}, graphql.Skip(graphql.SkipOrderField), graphql.Annotation{}))
		if typeOf(s, "User", "name") != "String!" {
			t.Error("SkipOrderField skipped the field")
		}
	})

	t.Run("edge", func(t *testing.T) {
		s, _ := load(t, blog(graphql.Annotation{}, graphql.Annotation{}, graphql.Skip()))
		if typeOf(s, "User", "posts") != "" || typeOf(s, "User", "groups") != "GroupConnection!" {
			t.Error("posts is not the only edge skipped")
		}
		if typeOf(s, "Post", "author") != "User" {
			t.Error("the inverse edge was skipped")
		}
	})
}

func TestEdges(t *testing.T) {
	cat := entity("Category")
	parent := edge("parent", plugin.M2O, "Category")
	children := edge("children", plugin.O2M, "Category")
	cat.Edges = []*plugin.Edge{parent, children}
	user := entity("User")
	user.Edges = []*plugin.Edge{edge("posts", plugin.O2M, "Post"), edge("groups", plugin.M2M, "Group"), edge("profile", plugin.O2O, "Profile")}
	plain := edge("tags", plugin.M2M, "Group")
	plain.Annotations = ann(t, graphql.Connection(false))
	user.Edges = append(user.Edges, plain)
	// A Required edge whose key is NOT NULL is never empty: T!. One whose key
	// is NULL-able (a Required self edge's) is emptied by ON DELETE SET NULL:
	// T.
	post := entity("Post", field("author_id", "int64"))
	author := edge("author", plugin.M2O, "User")
	author.Required, author.Field, author.FieldOn = true, "author_id", "Post"
	post.Edges = []*plugin.Edge{author}
	// On the side that does not own the key, too: a Required Desk.chair whose
	// key Chair.desk_id is NULL-able can read null.
	chair := entity("Chair", &plugin.Field{Name: "desk_id", GoName: "DeskID", Column: "desk_id", Type: "int64", Nullable: true, Optional: true, Unique: true})
	cd := edge("desk", plugin.O2O, "Desk")
	cd.Field, cd.FieldOn, cd.Ref, cd.Inverse, cd.OwnsKey = "desk_id", "Chair", "chair", true, true
	chair.Edges = []*plugin.Edge{cd}
	desk := entity("Desk")
	dc := edge("chair", plugin.O2O, "Chair")
	dc.Field, dc.FieldOn, dc.Ref, dc.Required = "desk_id", "Chair", "desk", true
	desk.Edges = []*plugin.Edge{dc}
	s, _ := load(t, model(cat, entity("Group"), post, entity("Profile"), user, folder(t), chair, desk))

	for _, c := range [][4]string{
		{"Category", "parent", "Category", "ParentGQL"},
		{"Category", "children", "CategoryConnection!", "ChildrenGQL"},
		{"Post", "author", "User!", "AuthorGQL"},
		{"Folder", "parent", "Folder", "ParentGQL"},
		{"Folder", "prev", "Folder", "PrevGQL"},
		{"Folder", "next", "Folder", "NextGQL"},
		{"Desk", "chair", "Chair", "ChairGQL"},
		{"User", "posts", "PostConnection!", "PostsGQL"},
		{"User", "groups", "GroupConnection!", "GroupsGQL"},
		{"User", "profile", "Profile", "ProfileGQL"},
		{"User", "tags", "[Group!]!", "TagsGQL"},
	} {
		if got := typeOf(s, c[0], c[1]); got != c[2] {
			t.Errorf("%s.%s: %q, want %q", c[0], c[1], got, c[2])
		}
		if got := goField(s, c[0], c[1]); got != c[3] {
			t.Errorf("%s.%s binds to %q, want %q", c[0], c[1], got, c[3])
		}
	}
	args := func(typ, name string) []string {
		var out []string
		for _, a := range s.Types[typ].Fields.ForName(name).Arguments {
			out = append(out, a.Name+": "+a.Type.String())
		}
		return out
	}
	conn := []string{"after: Cursor", "first: Int", "before: Cursor", "last: Int"}
	for _, f := range [][2]string{{"User", "posts"}, {"User", "groups"}, {"Category", "children"}} {
		if got := args(f[0], f[1]); !slices.Equal(got, conn) {
			t.Errorf("%s.%s arguments: %v", f[0], f[1], got)
		}
	}
	for _, f := range [][2]string{{"User", "tags"}, {"User", "profile"}, {"Post", "author"}} {
		if got := args(f[0], f[1]); len(got) != 0 {
			t.Errorf("%s.%s arguments: %v", f[0], f[1], got)
		}
	}
	for _, c := range [][3]string{
		{"PostConnection", "edges", "[PostEdge!]!"},
		{"PostConnection", "pageInfo", "PageInfo!"},
		{"PostConnection", "totalCount", "Int!"},
		{"PostEdge", "node", "Post!"},
		{"PostEdge", "cursor", "Cursor!"},
	} {
		if got := typeOf(s, c[0], c[1]); got != c[2] {
			t.Errorf("%s.%s: %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

// An entity whose key is more than one column is a type without an id that
// implements nothing; its key columns are its fields, and its connection is
// paged on them. A join entity is no type at all.
func TestCompositeKey(t *testing.T) {
	like := &plugin.Entity{
		Name: "Like", Tenant: -1, PrimaryKey: []int{0, 1},
		Fields: []*plugin.Field{field("user_id", "int64"), field("day", "string"), field("weight", "int")},
	}
	user := edge("user", plugin.M2O, "User")
	user.Required, user.Field, user.FieldOn = true, "user_id", "Like"
	like.Edges = []*plugin.Edge{user}
	u := entity("User")
	likes := edge("likes", plugin.O2M, "Like")
	likes.Field, likes.FieldOn = "user_id", "Like"
	u.Edges = []*plugin.Edge{likes}
	u.Annotations = ann(t, graphql.QueryField("users"))
	like.Annotations = ann(t, graphql.QueryField("likes"))
	join := &plugin.Entity{Name: "UserGroup", Join: true, Tenant: -1, PrimaryKey: []int{0, 1},
		Fields: []*plugin.Field{field("user_id", "int64"), field("group_id", "int64")}}
	s, sdl := load(t, model(like, u, join))
	def := s.Types["Like"]
	if def == nil || len(def.Interfaces) != 0 {
		t.Fatalf("Like: %+v", def)
	}
	for name, want := range map[string]string{"id": "", "userID": "", "day": "String!", "weight": "Int!", "user": "User!"} {
		if got := typeOf(s, "Like", name); got != want {
			t.Errorf("Like.%s: %q, want %q", name, got, want)
		}
	}
	if typeOf(s, "User", "likes") != "LikeConnection!" || typeOf(s, "LikeEdge", "node") != "Like!" || typeOf(s, "Query", "likes") != "LikeConnection!" {
		t.Errorf("Like's connection:\n%s", sdl)
	}
	if strings.Contains(sdl, "UserGroup") {
		t.Errorf("the join entity is a type:\n%s", sdl)
	}

	// With its own fields skipped and its edge's target skipped, the type
	// would have no fields: an error naming the schema, not a parse error.
	for _, f := range like.Fields[1:] {
		f.Annotations = ann(t, graphql.Skip())
	}
	u.Annotations = ann(t, graphql.Skip())
	renderErr(t, model(like, u), "schema Like", "no fields", "graphql.Skip()")
}

func TestDescriptions(t *testing.T) {
	name := field("name", "string")
	name.Comment = `Says """hello""".`
	user := entity("User", name)
	user.Comment = "A user.\n\nTwo paragraphs, the second \"quoted\""
	posts := edge("posts", plugin.O2M, "User")
	posts.Comment = `The posts, in C:\`
	user.Edges = []*plugin.Edge{posts}
	s, _ := load(t, model(user))
	if got := s.Types["User"].Description; got != user.Comment {
		t.Errorf("type: %q", got)
	}
	if got := s.Types["User"].Fields.ForName("name").Description; got != name.Comment {
		t.Errorf("field: %q", got)
	}
	if got := s.Types["User"].Fields.ForName("posts").Description; got != posts.Comment {
		t.Errorf("edge: %q", got)
	}

	// A lone CR is a line break, as CRLF is: both render as the block a "\n"
	// comment does.
	for _, c := range [][2]string{{"a\rb", "a\nb"}, {"a\r\n\rb", "a\n\nb"}} {
		name.Comment = c[0]
		got, sdl := load(t, model(entity("User", name)))
		if d := got.Types["User"].Fields.ForName("name").Description; d != c[1] {
			t.Errorf("comment %q: description %q, want %q\n%s", c[0], d, c[1], sdl)
		}
		if strings.Contains(sdl, "\r") {
			t.Errorf("comment %q: the SDL has a CR:\n%q", c[0], sdl)
		}
		name.Comment = c[1]
		if _, want := load(t, model(entity("User", name))); sdl != want {
			t.Errorf("comment %q renders differently from %q:\n%s\nwant\n%s", c[0], c[1], sdl, want)
		}
	}
}

func TestDirectives(t *testing.T) {
	const decl = `directive @auth(role: String, any: [Int!], opts: Opts, off: Boolean, n: Float) on FIELD_DEFINITION
input Opts { a: Boolean, b: String }
`
	name := field("name", "string")
	name.Annotations = ann(t, graphql.Directives(graphql.Directive{Name: "auth", Args: map[string]any{
		"role": "a \"b\"\n\\c", "any": []any{1, 2}, "opts": map[string]any{"b": "x", "a": true}, "off": false, "n": nil,
	}}))
	user := entity("User", name)
	posts := edge("posts", plugin.O2M, "User")
	posts.Annotations = ann(t, graphql.Directives(graphql.Directive{Name: "auth"}))
	user.Edges = []*plugin.Edge{posts}
	s, sdl := load(t, model(user), decl)
	d := s.Types["User"].Fields.ForName("name").Directives.ForName("auth")
	if d == nil {
		t.Fatalf("field directive:\n%s", sdl)
	}
	if got := d.Arguments.ForName("role").Value.Raw; got != "a \"b\"\n\\c" {
		t.Errorf("role: %q", got)
	}
	if !strings.Contains(sdl, `@auth(any: [1, 2], n: null, off: false, opts: {a: true, b: "x"}, role: "a \"b\"\n\\c")`) {
		t.Errorf("arguments are not in name order:\n%s", sdl)
	}
	if f := s.Types["User"].Fields.ForName("posts"); f.Directives.ForName("auth") == nil || f.Directives.ForName("goField") == nil {
		t.Errorf("edge directives:\n%s", sdl)
	}

	posts.Annotations = ann(t, graphql.Directives(graphql.Directive{Name: "bad name"}))
	renderErr(t, model(user), "User", "posts", `"bad name"`)
	posts.Annotations = nil
	name.Annotations = ann(t, graphql.Directives(graphql.Directive{Name: "auth", Args: map[string]any{"1": 1}}))
	renderErr(t, model(user), "User", "name", `"1"`)
}

// The plugin validates the SDL with only the directives GraphQL defines and
// its own @goField: the application declares the rest elsewhere. A directive
// GraphQL defines, used where it does not apply or with a wrong argument,
// fails naming the element; @goField is the plugin's own.
func TestDirectiveValidation(t *testing.T) {
	build := func(ent, fld, edg []graphql.Directive) *plugin.Model {
		name := field("name", "string")
		name.Annotations = ann(t, graphql.Directives(fld...))
		user := entity("User", name)
		user.Annotations = ann(t, graphql.Directives(ent...))
		posts := edge("posts", plugin.O2M, "User")
		posts.Annotations = ann(t, graphql.Directives(edg...))
		user.Edges = []*plugin.Edge{posts}
		return model(user)
	}
	dep := func(args map[string]any) []graphql.Directive {
		return []graphql.Directive{{Name: "deprecated", Args: args}}
	}
	renderErr(t, build(dep(nil), nil, nil), "schema User:", "deprecated", "OBJECT")
	// gqlparser's schema validation checks a directive's argument names, not
	// the types of their values.
	renderErr(t, build(nil, dep(map[string]any{"why": "old"}), nil), "schema User, field name:", "why")
	renderErr(t, build(nil, []graphql.Directive{{Name: "include", Args: map[string]any{"if": true}}}, nil), "schema User, field name:", "include")
	renderErr(t, build(nil, nil, []graphql.Directive{{Name: "goField", Args: map[string]any{"name": "X"}}}), "schema User, edge posts", "@goField")
	renderErr(t, build(nil, []graphql.Directive{{Name: "goField"}}, nil), "schema User, field name", "@goField")

	// Valid: @deprecated where it applies, and the application's own
	// directives, which the plugin cannot check.
	_, sdl := load(t, build(nil, dep(map[string]any{"reason": "old"}), []graphql.Directive{{Name: "cost"}}), "directive @cost on FIELD_DEFINITION")
	if !strings.Contains(sdl, `name: String! @deprecated(reason: "old")`) || !strings.Contains(sdl, `@goField(name: "PostsGQL") @cost`) {
		t.Errorf("directives:\n%s", sdl)
	}
}

// Directive arguments are decoded as JSON numbers, so an integer above 2^53
// keeps its value.
func TestDirectiveNumbers(t *testing.T) {
	name := field("name", "string")
	name.Annotations = ann(t, graphql.Directives(graphql.Directive{Name: "limit", Args: map[string]any{
		"n": int64(9007199254740993), "f": 0.5, "e": -12,
	}}))
	_, sdl := load(t, model(entity("User", name)), "directive @limit(n: Int, f: Float, e: Int) on FIELD_DEFINITION")
	if !strings.Contains(sdl, "@limit(e: -12, f: 0.5, n: 9007199254740993)") {
		t.Errorf("numbers:\n%s", sdl)
	}
}

func TestQuery(t *testing.T) {
	a, b := entity("Alpha"), entity("Beta")
	b.Annotations = ann(t, graphql.QueryField("betas"))
	a.Annotations = ann(t, graphql.QueryField("alphas"))
	s, sdl := load(t, model(b, a))
	var got []string
	for _, f := range s.Types["Query"].Fields {
		if !strings.HasPrefix(f.Name, "__") {
			got = append(got, f.Name+": "+f.Type.String())
		}
	}
	want := []string{"node: Node", "nodes: [Node]!", "alphas: AlphaConnection!", "betas: BetaConnection!"}
	if !slices.Equal(got, want) {
		t.Errorf("Query: %v, want %v", got, want)
	}
	if !strings.Contains(sdl, "  node(id: ID!): Node\n") || !strings.Contains(sdl, "  nodes(ids: [ID!]!): [Node]!\n") ||
		!strings.Contains(sdl, "  alphas(after: Cursor, first: Int, before: Cursor, last: Int): AlphaConnection!\n") {
		t.Errorf("Query:\n%s", sdl)
	}
	if strings.Index(sdl, "# Alpha") > strings.Index(sdl, "# Beta") {
		t.Error("entities are not in name order")
	}

	b.Annotations = ann(t, graphql.QueryField("alphas"))
	renderErr(t, model(a, b), "Alpha", "Beta", "alphas")
	b.Annotations = ann(t, graphql.QueryField("node"))
	renderErr(t, model(b), "Beta", "node")
}

// The vocabulary every later plan binds Go to.
func TestVocabulary(t *testing.T) {
	_, sdl := load(t, model(entity("User")))
	for _, want := range []string{
		"scalar Time\n", "scalar UUID\n", "scalar Bytes\n", "scalar JSON\n", "scalar Uint64\n", "scalar Cursor\n",
		"directive @goField(forceResolver: Boolean, name: String, omittable: Boolean, type: String) on INPUT_FIELD_DEFINITION | FIELD_DEFINITION\n",
		"directive @goModel(model: String, models: [String!], forceGenerate: Boolean) on OBJECT | INPUT_OBJECT | SCALAR | ENUM | INTERFACE | UNION\n",
		"interface Node {\n", "  id: ID!\n",
		"type PageInfo {\n", "  hasNextPage: Boolean!\n", "  hasPreviousPage: Boolean!\n", "  startCursor: Cursor\n", "  endCursor: Cursor\n",
		"enum OrderDirection {\n  ASC\n  DESC\n}\n",
		"type User implements Node {\n",
	} {
		if !strings.Contains(sdl, want) {
			t.Errorf("no %q in\n%s", want, sdl)
		}
	}
}

func TestNames(t *testing.T) {
	article := entity("Article")
	article.Annotations = ann(t, graphql.Type("Post"))
	renderErr(t, model(article, entity("Post")), "Article", "Post")

	node := entity("Thing")
	node.Annotations = ann(t, graphql.Type("PageInfo"))
	renderErr(t, model(node), "Thing", "PageInfo")

	node.Annotations = ann(t, graphql.Type("1x"))
	renderErr(t, model(node), "Thing", `"1x"`)

	// UserEdge's connection collides with an entity named UserEdge.
	renderErr(t, model(entity("User"), entity("UserEdge")), "User", "UserEdge")

	f := field("title", "string")
	f.Annotations = ann(t, graphql.FieldName("name"))
	renderErr(t, model(entity("Doc", f, field("name", "string"))), "Doc", "name")

	f.Annotations = ann(t, graphql.FieldName("a-b"))
	renderErr(t, model(entity("Doc", f)), "Doc", "title", `"a-b"`)

	// Go identifiers may be any letters; GraphQL names are ASCII.
	renderErr(t, model(entity("Café")), "schema Café", `"Café"`, "graphql.Type")
	cafe := entity("Café")
	cafe.Annotations = ann(t, graphql.Type("Cafe"))
	load(t, model(cafe))
	renderErr(t, model(entity("Doc", field("naïve", "string"))), "schema Doc, field naïve", `"naïve"`, "graphql.FieldName")
	e := entity("Doc")
	e.Edges = []*plugin.Edge{edge("crème", plugin.O2M, "Doc")}
	renderErr(t, model(e), "schema Doc, edge crème", "graphql.FieldName")
}

func TestCamel(t *testing.T) {
	for in, want := range map[string]string{
		"ID": "id", "UserID": "userID", "URLPath": "urlPath", "CreatedAt": "createdAt", "IDs": "ids",
		"HTTPServer": "httpServer", "A": "a", "Name": "name", "OrgID": "orgID", "U8": "u8",
	} {
		if got := camel(in); got != want {
			t.Errorf("camel(%q) = %q, want %q", in, got, want)
		}
	}
}

// An enum of the application's own Go type binds with @goModel, whatever the
// entity's GraphQL type is called; velox-gen's own enum types, which gqlgen
// binds by name, have none.
func TestAppEnumGoModel(t *testing.T) {
	level := &plugin.Field{Name: "level", GoName: "Level", Type: "enum", Enum: "Level", ElemType: "levels.Level", PkgPath: "example.com/app/levels", EnumValues: []string{"low", "high"}}
	role := &plugin.Field{Name: "role", GoName: "Role", Type: "enum", Enum: "UserRole", EnumValues: []string{"admin"}}
	event := entity("Event", level)
	event.Annotations = ann(t, graphql.Type("Occurrence"))
	user := entity("User", role)
	m := model(event, user)
	m.Enums = []*plugin.Enum{{Type: "UserRole", Values: []string{"admin"}}}
	s, sdl := load(t, m)
	if want := "enum EventLevel @goModel(model: \"example.com/app/levels.Level\") {\n  LOW\n  HIGH\n}\n"; !strings.Contains(sdl, want) {
		t.Errorf("no %q in\n%s", want, sdl)
	}
	if d := s.Types["EventLevel"].Directives.ForName("goModel"); d == nil || d.Arguments.ForName("model").Value.Raw != "example.com/app/levels.Level" {
		t.Errorf("EventLevel has no @goModel:\n%s", sdl)
	}
	if want := "enum UserRole {\n"; !strings.Contains(sdl, want) {
		t.Errorf("no %q in\n%s", want, sdl)
	}
	if d := s.Types["UserRole"].Directives.ForName("goModel"); d != nil {
		t.Errorf("velox-gen's own enum UserRole has @goModel:\n%s", sdl)
	}
}

// Two entities with a field of one application enum type get an enum each,
// <Schema><Field>, both bound to that type; an application enum whose
// package the model does not name (PkgPath "") gets no @goModel, and gqlgen
// binds it by name, as velox-gen's.
func TestAppEnumShared(t *testing.T) {
	level := func() *plugin.Field {
		return &plugin.Field{Name: "level", GoName: "Level", Type: "enum", Enum: "Level", ElemType: "levels.Level", PkgPath: "example.com/app/levels", EnumValues: []string{"low", "high"}}
	}
	bare := &plugin.Field{Name: "grade", GoName: "Grade", Type: "enum", Enum: "Grade", ElemType: "Grade", EnumValues: []string{"a"}}
	s, sdl := load(t, model(entity("Event", level()), entity("Task", level(), bare)))
	for _, name := range []string{"EventLevel", "TaskLevel"} {
		if d := s.Types[name].Directives.ForName("goModel"); d == nil || d.Arguments.ForName("model").Value.Raw != "example.com/app/levels.Level" {
			t.Errorf("%s has no @goModel of levels.Level:\n%s", name, sdl)
		}
	}
	g := s.Types["TaskGrade"]
	if g == nil {
		t.Fatalf("no enum TaskGrade:\n%s", sdl)
	}
	if d := g.Directives.ForName("goModel"); d != nil {
		t.Errorf("TaskGrade, of no package, has @goModel:\n%s", sdl)
	}
}

// @goModel is velox-graphql's own: it binds the types velox generates and
// the application's enum types.
func TestGoModelRefused(t *testing.T) {
	const want = "@goModel is velox-graphql's own: it binds types to the Go code velox generates and the application's enum types"
	goModel := graphql.Directives(graphql.Directive{Name: "goModel", Args: map[string]any{"model": "x.T"}})
	f := field("name", "string")
	f.Annotations = ann(t, goModel)
	renderErr(t, model(entity("User", f)), "schema User, field name", want)
	user := entity("User")
	user.Annotations = ann(t, goModel)
	renderErr(t, model(user), "schema User", want)
}
