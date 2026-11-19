package sdl

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// An annotation on an element it does not apply to, or that contradicts the
// element, fails generation with the schema and the element named.
func TestAnnotationMisuse(t *testing.T) {
	// build is a User with a name, an optional nickname, a sensitive secret,
	// a status enum, an immutable handle, an org id and a posts edge, each
	// with the annotations given.
	type anns struct{ user, name, nickname, secret, status, handle, org, posts graphql.Annotation }
	build := func(a anns) *plugin.Model {
		name := field("name", "string")
		name.Annotations = ann(t, a.name)
		nickname := &plugin.Field{Name: "nickname", GoName: "Nickname", Type: "string", Nullable: true, Optional: true}
		nickname.Annotations = ann(t, a.nickname)
		secret := &plugin.Field{Name: "secret", GoName: "Secret", Type: "string", Sensitive: true}
		secret.Annotations = ann(t, a.secret)
		status := &plugin.Field{Name: "status", GoName: "Status", Type: "enum", EnumValues: []string{"ok"}}
		status.Annotations = ann(t, a.status)
		handle := &plugin.Field{Name: "handle", GoName: "Handle", Type: "string", Immutable: true}
		handle.Annotations = ann(t, a.handle)
		org := field("org_id", "int64")
		org.Annotations = ann(t, a.org)
		user := entity("User", name, nickname, secret, status, handle, org)
		user.Annotations = ann(t, a.user)
		posts := edge("posts", plugin.O2M, "User")
		posts.Annotations = ann(t, a.posts)
		user.Edges = []*plugin.Edge{posts}
		return model(user)
	}
	load(t, build(anns{})) // the model is fine without them

	for _, c := range []struct {
		name  string
		a     anns
		wants []string
	}{
		{"OrderField on a sensitive field", anns{secret: graphql.OrderField("SECRET")}, []string{"schema User, field secret", "graphql.OrderField", "sensitive"}},
		{"OrderField on an edge", anns{posts: graphql.OrderField("POSTS")}, []string{"schema User, edge posts", "graphql.OrderField"}},
		{"OrderField on a schema", anns{user: graphql.OrderField("X")}, []string{"schema User:", "graphql.OrderField"}},
		{"Mutations on a field", anns{name: graphql.Mutations()}, []string{"schema User, field name", "graphql.Mutations"}},
		{"Mutations on an edge", anns{posts: graphql.Mutations()}, []string{"schema User, edge posts", "graphql.Mutations"}},
		{"MultiOrder on a field", anns{name: graphql.MultiOrder()}, []string{"schema User, field name", "graphql.MultiOrder"}},
		{"QueryField on a field", anns{name: graphql.QueryField("names")}, []string{"schema User, field name", "graphql.QueryField"}},
		{"Type on an edge", anns{posts: graphql.Type("Writings")}, []string{"schema User, edge posts", "graphql.Type"}},
		{"WhereOps on an edge", anns{posts: graphql.WhereOps()}, []string{"schema User, edge posts", "graphql.WhereOps"}},
		{"WhereEdge on a field", anns{name: graphql.WhereEdge()}, []string{"schema User, field name", "graphql.WhereEdge"}},
		{"Connection on a field", anns{name: graphql.Connection(false)}, []string{"schema User, field name", "graphql.Connection"}},
		{"MaxList on a field", anns{name: graphql.MaxList(5)}, []string{"schema User, field name", "graphql.MaxList"}},
		{"MaxList on a connection", anns{posts: graphql.MaxList(5)}, []string{"schema User, edge posts", "graphql.MaxList", "graphql.Connection(false)"}},
		{"MaxList under 1", anns{posts: graphql.Annotation{Connection: new(false), MaxList: -1}}, []string{"schema User, edge posts", "graphql.MaxList(-1)"}},
		{"CreateInput on an edge", anns{posts: graphql.CreateInput(false)}, []string{"schema User, edge posts", "graphql.CreateInput"}},
		{"FieldName on a schema", anns{user: graphql.FieldName("x")}, []string{"schema User:", "graphql.FieldName"}},
		{"EnumValues on a string", anns{name: graphql.EnumValues(map[string]string{"a": "A"})}, []string{"schema User, field name", "graphql.EnumValues"}},
		{"an unknown EnumValues key", anns{status: graphql.EnumValues(map[string]string{"gone": "GONE"})}, []string{"schema User, field status", `"gone"`}},
		{"WhereOps on a sensitive field", anns{secret: graphql.WhereOps()}, []string{"schema User, field secret", "graphql.WhereOps", "sensitive"}},
		{"UpdateInput(true) on an immutable field", anns{handle: graphql.UpdateInput(true)}, []string{"schema User, field handle", "graphql.UpdateInput", "immutable"}},
		{"an unknown mutation", anns{user: graphql.Mutations("upsert")}, []string{"schema User", `"upsert"`}},
		{"create with its input skipped", anns{user: graphql.Annotation{Mutations: []graphql.MutationKind{"create"}, Skip: graphql.SkipMutationCreateInput}}, []string{"schema User", "create", "SkipMutationCreateInput"}},
		{"QueryField on a skipped type", anns{user: graphql.Annotation{QueryField: "users", Skip: graphql.SkipType}}, []string{"schema User", "graphql.QueryField", "Skip"}},
		{"Mutations on a skipped type", anns{user: graphql.Annotation{Mutations: []graphql.MutationKind{"create"}, Skip: graphql.SkipType}}, []string{"schema User", "graphql.Mutations", "Skip"}},
		{"OrderField on a field out of the type", anns{name: graphql.Annotation{OrderField: "NAME", Skip: graphql.SkipType}}, []string{"schema User, field name", "graphql.OrderField", "cursors"}},
		{"WhereOps on a field out of the type", anns{name: graphql.Annotation{WhereOps: graphql.OpsEquality, Skip: graphql.SkipType}}, []string{"schema User, field name", "graphql.WhereOps", "reveal"}},
		{"update with its input skipped", anns{user: graphql.Annotation{Mutations: []graphql.MutationKind{"update"}, Skip: graphql.SkipMutationUpdateInput}}, []string{"schema User", "update", "SkipMutationUpdateInput"}},
	} {
		t.Run(c.name, func(t *testing.T) { renderErr(t, build(c.a), c.wants...) })
	}

	// A mutation kind listed twice is harmless.
	m := build(anns{user: graphql.Annotation{Mutations: []graphql.MutationKind{"create", "create"}}})
	if s, _ := load(t, m); s.Types["CreateUserInput"] == nil {
		t.Error("no CreateUserInput")
	}

	// OrderField with the order skipped too, on a hidden field: no order, no
	// cursor, no leak.
	load(t, build(anns{name: graphql.Annotation{OrderField: "NAME", Skip: graphql.SkipType | graphql.SkipOrderField}}))
	// And a filter likewise, with the where input skipped too.
	load(t, build(anns{name: graphql.Annotation{WhereOps: graphql.OpsEquality, Skip: graphql.SkipType | graphql.SkipWhereInput}}))

	t.Run("CreateInput or UpdateInput on a foreign key", func(t *testing.T) {
		for _, a := range []graphql.Annotation{graphql.CreateInput(false), graphql.CreateInput(true), graphql.UpdateInput(false), graphql.UpdateInput(true)} {
			aid := field("author_id", "int64")
			aid.Annotations = ann(t, a)
			post := entity("Post", aid)
			author := edge("author", plugin.M2O, "User")
			author.Field, author.FieldOn = "author_id", "Post"
			post.Edges = []*plugin.Edge{author}
			renderErr(t, model(post, entity("User")), "schema Post, field author_id", "foreign key")
		}
	})

	t.Run("a required edge create cannot set", func(t *testing.T) {
		// Its target is skipped, or has no ID: createPost could never
		// give the key.
		for _, target := range []*plugin.Entity{
			func() *plugin.Entity { u := entity("User"); u.Annotations = ann(t, graphql.Skip()); return u }(),
			{Name: "User", PrimaryKey: []int{0, 1}, Fields: []*plugin.Field{field("a", "string"), field("b", "string")}},
		} {
			post := entity("Post", field("title", "string"))
			author := edge("author", plugin.M2O, "User")
			author.Required = true
			post.Edges = []*plugin.Edge{author}
			post.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
			renderErr(t, model(post, target), "schema Post, edge author", "required", "CreatePostInput")
			// The user may leave it to a hook.
			author.Annotations = ann(t, graphql.Skip(graphql.SkipMutationCreateInput))
			load(t, model(post, target))
		}

		// A required O2O whose key the target's row holds: createUser writes
		// no key of its own, so it can succeed without the edge.
		user := entity("User", field("name", "string"))
		profile := edge("profile", plugin.O2O, "Profile")
		profile.Required, profile.Field, profile.FieldOn = true, "user_id", "Profile"
		user.Edges = []*plugin.Edge{profile}
		user.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
		target := entity("Profile", field("user_id", "int64"))
		target.Annotations = ann(t, graphql.Skip())
		s, _ := load(t, model(target, user))
		if got := fieldsOf(s, "CreateUserInput"); !slices.Equal(got, []string{"name: String!"}) {
			t.Errorf("CreateUserInput: %v", got)
		}
		// The same edge holding its key on User's row is refused.
		profile.OwnsKey, profile.FieldOn = true, "User"
		renderErr(t, model(target, user), "schema User, edge profile", "required", "CreateUserInput")
	})

	t.Run("a key the caller supplies", func(t *testing.T) {
		code := entity("Country", field("name", "string"))
		code.Fields[0] = &plugin.Field{Name: "id", GoName: "ID", Type: "string"}
		code.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
		renderErr(t, model(code), "schema Country", "create", "supplied by the caller", "no create input")
		code.Fields[0].HasDefault = true
		load(t, model(code))
		// Update needs no new key.
		code.Fields[0].HasDefault = false
		code.Annotations = ann(t, graphql.Mutations(graphql.MutationUpdate))
		load(t, model(code))
	})

	t.Run("QueryField on a join entity", func(t *testing.T) {
		join := &plugin.Entity{Name: "UserGroup", Join: true, PrimaryKey: []int{0, 1},
			Fields: []*plugin.Field{field("user_id", "int64"), field("group_id", "int64")}}
		join.Annotations = ann(t, graphql.QueryField("userGroups"))
		renderErr(t, model(entity("User"), join), "schema UserGroup", "graphql.QueryField", "join")
		join.Annotations = ann(t, graphql.Mutations())
		renderErr(t, model(entity("User"), join), "schema UserGroup", "graphql.Mutations", "join")
	})

	t.Run("update without an ID", func(t *testing.T) {
		like := &plugin.Entity{Name: "Like", PrimaryKey: []int{0, 1},
			Fields: []*plugin.Field{field("a", "string"), field("b", "string")}}
		like.Annotations = ann(t, graphql.Mutations(graphql.MutationUpdate))
		renderErr(t, model(like), "schema Like", "update", "key")
	})

	t.Run("delete is no input", func(t *testing.T) {
		// velox-graphql writes no delete: the application's resolver deletes.
		like := entity("Like", field("a", "string"))
		like.Annotations = ann(t, graphql.Mutations("delete"))
		renderErr(t, model(like), "schema Like", `"delete" is not a mutation input`)
	})
}

// Two elements whose generated GraphQL names collide fail with both named.
func TestNameCollisions(t *testing.T) {
	ordered := func(name string) *plugin.Entity {
		title := field("title", "string")
		title.Annotations = ann(t, graphql.OrderField("TITLE"), graphql.WhereOps())
		e := entity(name, title)
		e.Annotations = ann(t, graphql.Mutations())
		return e
	}
	renamed := func(name, typ string) *plugin.Entity {
		e := entity(name)
		e.Annotations = ann(t, graphql.Type(typ))
		return e
	}
	for _, c := range []struct {
		name  string
		m     *plugin.Model
		wants []string
	}{
		{"types", model(renamed("Article", "Post"), entity("Post")), []string{"schema Article", "schema Post", "Post"}},
		{"connection", model(entity("Post"), entity("PostConnection")), []string{"schema Post", "schema PostConnection", "PostConnection"}},
		{"order", model(ordered("Post"), entity("PostOrder")), []string{"schema Post", "schema PostOrder", "PostOrder"}},
		{"order field", model(ordered("Post"), renamed("Article", "PostOrderField")), []string{"schema Post", "schema Article", "PostOrderField"}},
		{"where input", model(ordered("Post"), renamed("Article", "PostWhereInput")), []string{"schema Post", "schema Article", "PostWhereInput"}},
		{"create input", model(ordered("Post"), renamed("Article", "CreatePostInput")), []string{"schema Post", "schema Article", "CreatePostInput"}},
		{"update input", model(ordered("Post"), renamed("Article", "UpdatePostInput")), []string{"schema Post", "schema Article", "UpdatePostInput"}},
		{"enum and order", model(func() *plugin.Entity {
			e := ordered("Post")
			e.Fields = append(e.Fields, &plugin.Field{Name: "order_field", GoName: "OrderField", Type: "enum", EnumValues: []string{"a"}})
			return e
		}()), []string{"schema Post", "field order_field", "PostOrderField"}},
		{"enum and entity", model(entity("User", &plugin.Field{Name: "role", GoName: "Role", Type: "enum", EnumValues: []string{"a"}}), entity("UserRole")), []string{"schema User, field role", "schema UserRole", "UserRole"}},
	} {
		t.Run(c.name, func(t *testing.T) { renderErr(t, c.m, c.wants...) })
	}

	// A type may not take a Go name velox-gen declares in the generated
	// package, where gqlgen's autobind would bind the type to it.
	handled := func(e *plugin.Entity, handles string) *plugin.Entity {
		e.Handles = handles
		return e
	}
	join := handled(entity("UserGroup"), "UserGroups")
	join.Join = true
	role := func(skip ...graphql.SkipMode) *plugin.Field {
		f := &plugin.Field{Name: "role", GoName: "Role", Type: "enum", Enum: "UserRole", EnumValues: []string{"a"}}
		if len(skip) > 0 {
			f.Annotations = ann(t, graphql.Skip(skip...))
		}
		return f
	}
	withEnum := func(m *plugin.Model) *plugin.Model {
		m.Enums = []*plugin.Enum{{Type: "UserRole", Values: []string{"a"}}}
		return m
	}
	hidden := handled(entity("Hidden"), "Hiddens")
	hidden.Annotations = ann(t, graphql.Skip(graphql.SkipType))
	for _, c := range []struct {
		name  string
		m     *plugin.Model
		wants []string
	}{
		{"handles", model(handled(entity("User"), "Users"), renamed("Article", "Users")), []string{"schema Article", "schema User", "Users", "handles"}},
		{"join entity", model(handled(entity("User"), "Users"), join, renamed("Article", "UserGroup")), []string{"schema Article", "join entity UserGroup"}},
		{"a name the schema gave up", model(renamed("User", "Person"), renamed("Article", "User")), []string{"schema Article", "schema User", "User"}},
		{"a schema without a type", model(hidden, renamed("Article", "Hidden")), []string{"schema Article", "schema Hidden", "Hidden"}},
		{"enum type", withEnum(model(handled(entity("User", role()), "Users"), renamed("Article", "UserRole"))), []string{"schema Article", "schema User, field role", "UserRole"}},
		{"enum type of a skipped enum", withEnum(model(handled(entity("User", role(graphql.SkipEnumField)), "Users"), renamed("Article", "UserRole"))), []string{"schema Article", "schema User, field role", "UserRole"}},
	} {
		t.Run("Go name, "+c.name, func(t *testing.T) { renderErr(t, c.m, c.wants...) })
	}
	// A type and an enum take their own Go names.
	_, sdl := load(t, withEnum(model(handled(entity("User", role()), "Users"), join)))
	if !strings.Contains(sdl, "enum UserRole {") || !strings.Contains(sdl, "type User {") {
		t.Errorf("own Go names:\n%s", sdl)
	}

	// Input fields that collide: a field named like another's predicate.
	n, nIn := field("name", "string"), field("name_in", "string")
	n.Annotations = ann(t, graphql.WhereOps(graphql.OpIn))
	nIn.GoName = "NameIn"
	nIn.Annotations = ann(t, graphql.WhereOps(graphql.OpEQ))
	renderErr(t, model(entity("User", n, nIn)), "schema User", "field name", "field name_in", "nameIn")
}

// Keyset paging cannot continue after a bytes value: velox's After refuses a
// slice column, which a nil []byte stores as NULL.
func TestOrderFieldOnBytes(t *testing.T) {
	blob := field("blob", "bytes")
	blob.Annotations = ann(t, graphql.OrderField("BLOB"))
	renderErr(t, model(entity("Thing", blob)), "schema Thing, field blob", "graphql.OrderField", "bytes")
}

// A field an application directive guards cannot be ordered unless
// OrderUnguarded says so: its values would be in the cursors, and its order
// would reveal them, which no directive guards.
func TestOrderFieldUnderDirective(t *testing.T) {
	user := func(as ...graphql.Annotation) *plugin.Model {
		salary := field("salary", "int")
		salary.Annotations = ann(t, as...)
		return model(entity("User", salary))
	}
	admin := graphql.Directives(graphql.Directive{Name: "admin"})
	_, err := Render(user(admin, graphql.OrderField("SALARY")))
	want := "schema User, field salary: graphql.OrderField on a field with the directive @admin: its values would be in the cursors and its order would reveal them, which no directive guards; add graphql.OrderUnguarded() to allow it"
	if err == nil || err.Error() != want {
		t.Errorf("got  %v\nwant %s", err, want)
	}
	load(t, user(admin, graphql.OrderField("SALARY"), graphql.OrderUnguarded()), appDirectives)
	// GraphQL's own directives guard nothing; one is copied onto the
	// predicates like any other.
	s, _ := load(t, user(graphql.Directives(graphql.Directive{Name: "deprecated"}), graphql.OrderField("SALARY"), graphql.WhereOps(graphql.OpEQ, graphql.OpGT)))
	for _, name := range []string{"salary", "salaryGT"} {
		if d := dirsOf(s, "UserWhereInput", name); !slices.Equal(d, []string{"@deprecated"}) {
			t.Errorf("%s: %v, want [@deprecated]", name, d)
		}
	}

	_, err = Render(user(graphql.OrderUnguarded()))
	want = "schema User, field salary: graphql.OrderUnguarded without graphql.OrderField says nothing"
	if err == nil || err.Error() != want {
		t.Errorf("got  %v\nwant %s", err, want)
	}
	// It applies to fields only.
	u := entity("User")
	posts := edge("posts", plugin.O2M, "User")
	posts.Annotations = ann(t, graphql.OrderUnguarded())
	u.Edges = []*plugin.Edge{posts}
	renderErr(t, model(u), "schema User, edge posts", "graphql.OrderUnguarded")
}

// An application directive on a key field would guard nothing: a foreign
// key's edge stands for it, and a type's id is readable through the cursors
// and every edge to the type. GraphQL's own directives are allowed on both.
func TestDirectiveOnKey(t *testing.T) {
	admin := graphql.Directives(graphql.Directive{Name: "admin"})
	dep := graphql.Directives(graphql.Directive{Name: "deprecated"})
	post := func(a graphql.Annotation) *plugin.Model {
		aid := field("author_id", "int64")
		aid.Annotations = ann(t, a, graphql.WhereOps())
		p := entity("Post", aid)
		author := edge("author", plugin.M2O, "User")
		author.Field, author.FieldOn = "author_id", "Post"
		p.Edges = []*plugin.Edge{author}
		return model(p, entity("User"))
	}
	_, err := Render(post(admin))
	want := "schema Post, field author_id: graphql.Directives with the directive @admin on a foreign key, which its edge stands for in GraphQL; put the directive on the edge, which guards the edge's field, its has and hasWith filters and the key's predicates"
	if err == nil || err.Error() != want {
		t.Errorf("foreign key:\n got  %v\nwant %s", err, want)
	}
	load(t, post(dep))

	user := func(a graphql.Annotation) *plugin.Model {
		u := entity("User")
		u.Fields[0].Annotations = ann(t, a, graphql.WhereOps())
		return model(u)
	}
	_, err = Render(user(admin))
	want = "schema User, field id: graphql.Directives with the directive @admin on the primary key: the id is readable through the cursors and every edge to the type, so the directive would guard nothing"
	if err == nil || err.Error() != want {
		t.Errorf("primary key:\n got  %v\nwant %s", err, want)
	}
	load(t, user(dep))
}

// An application directive on an entity would guard its objects only: gqlgen
// runs a type's directives on the fields that return the type, so the rows'
// values stay readable through the cursors, the where inputs, the order and
// totalCount. It is refused, on a join entity too; GraphQL's own directives
// are not, and reach the SDL's validation.
func TestDirectiveOnEntity(t *testing.T) {
	admin := graphql.Directives(graphql.Directive{Name: "admin"})
	u := entity("User")
	u.Annotations = ann(t, admin)
	_, err := Render(model(u))
	want := "schema User: graphql.Directives with the directive @admin on a schema, which GraphQL runs on the type's objects only: its rows' values stay readable through the cursors, the filters, the order and totalCount; put the directive on the fields and edges it guards, or use a policy for row access"
	if err == nil || err.Error() != want {
		t.Errorf("entity:\n got  %v\nwant %s", err, want)
	}
	// The first application directive is named, after GraphQL's own.
	u.Annotations = ann(t, graphql.Directives(graphql.Directive{Name: "deprecated"}, graphql.Directive{Name: "key", Args: map[string]any{"fields": "id"}}))
	renderErr(t, model(u), "schema User:", "@key on a schema")

	join := entity("UserGroup")
	join.Join = true
	join.Annotations = ann(t, admin)
	renderErr(t, model(entity("User"), join), "schema UserGroup:", "@admin on a schema")

	// GraphQL's own directive passes this rule; @deprecated does not apply
	// to an object, which the SDL's validation says.
	u.Annotations = ann(t, graphql.Directives(graphql.Directive{Name: "deprecated"}))
	_, err = Render(model(u))
	if err == nil || strings.Contains(err.Error(), "on a schema") || !strings.Contains(err.Error(), "OBJECT") {
		t.Errorf("GraphQL's own directive: %v", err)
	}
}

// Any Default on a bool or number is allowed, as in velox-gen: an insert
// fills it before its setters, so a create input that gives false or 0
// stores it, and one that leaves the field out takes the Default (ADR 0082).
// The field stays non-null in the type.
func TestDefaultOnBoolOrNumber(t *testing.T) {
	for _, typ := range []string{"int", "uint64", "float32", "bool"} {
		for _, def := range []string{"", "0", "1"} {
			n := field("n", typ)
			n.HasDefault = true
			switch {
			case def != "" && typ == "bool":
				n.Default = json.RawMessage(map[string]string{"0": "false", "1": "true"}[def])
			case def != "":
				n.Default = json.RawMessage(def)
			}
			if _, out := load(t, model(entity("Thing", n))); !strings.Contains(out, "n: ") {
				t.Errorf("%s, Default %q: no n in\n%s", typ, def, out)
			}
		}
	}
	// A string's empty value is unset, so Insert writes the Default.
	s := field("s", "string")
	s.HasDefault, s.Optional = true, true
	load(t, model(entity("Thing", s)))
}

// zeroIsUnset's kinds, which graphql.CreateDefault takes, are bool and the
// types of velox-gen's numeric set, read from its source so that the two cannot drift.
func TestZeroIsUnsetMatchesVeloxGen(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "internal", "codegen", "build.go")
	f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"bool"}
	ast.Inspect(f, func(n ast.Node) bool {
		vs, ok := n.(*ast.ValueSpec)
		if !ok || len(vs.Names) != 1 || vs.Names[0].Name != "numeric" {
			return true
		}
		for _, el := range vs.Values[0].(*ast.CompositeLit).Elts {
			k, err := strconv.Unquote(el.(*ast.KeyValueExpr).Key.(*ast.BasicLit).Value)
			if err != nil {
				t.Fatal(err)
			}
			want = append(want, k)
		}
		return false
	})
	if len(want) == 1 {
		t.Fatalf("no numeric set in %s", path)
	}
	types := []string{"bool", "string", "time", "uuid", "bytes", "json", "enum", "other",
		"int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "float32", "float64"}
	var got []string
	for _, typ := range types {
		if zeroIsUnset(typ) {
			got = append(got, typ)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("zeroIsUnset is true of %v; velox-gen's rule 36 covers %v", got, want)
	}
	for _, typ := range want {
		if !slices.Contains(types, typ) {
			t.Errorf("velox-gen's rule 36 covers %s, which this test does not try", typ)
		}
	}
}

// @goField and @goModel are velox-graphql's own: graphql.Directives refuses
// them on every element, including the ones whose directives reach no SDL
// field (a skipped entity, a join entity, a primary key, a foreign key),
// where dropping them would fail open.
func TestOwnDirectivesRefused(t *testing.T) {
	for _, name := range []string{"goField", "goModel"} {
		own := graphql.Directives(graphql.Directive{Name: name})
		for _, c := range []struct {
			placement, where string
			model            func() *plugin.Model
		}{
			{"entity", "schema User:", func() *plugin.Model {
				u := entity("User")
				u.Annotations = ann(t, own)
				return model(u)
			}},
			{"skipped entity", "schema User:", func() *plugin.Model {
				u := entity("User")
				u.Annotations = ann(t, own, graphql.Skip())
				return model(u)
			}},
			{"join entity", "schema UserGroup:", func() *plugin.Model {
				join := entity("UserGroup")
				join.Join = true
				join.Annotations = ann(t, own)
				return model(entity("User"), join)
			}},
			{"field of a skipped entity", "schema User, field name:", func() *plugin.Model {
				f := field("name", "string")
				f.Annotations = ann(t, own)
				u := entity("User", f)
				u.Annotations = ann(t, graphql.Skip())
				return model(u)
			}},
			{"edge of a skipped entity", "schema User, edge posts:", func() *plugin.Model {
				u := entity("User")
				u.Annotations = ann(t, graphql.Skip())
				posts := edge("posts", plugin.O2M, "User")
				posts.Annotations = ann(t, own)
				u.Edges = []*plugin.Edge{posts}
				return model(u)
			}},
			{"field of a join entity", "schema UserGroup, field user_id:", func() *plugin.Model {
				f := field("user_id", "int64")
				f.Annotations = ann(t, own)
				join := entity("UserGroup", f)
				join.Join = true
				return model(entity("User"), join)
			}},
			{"edge of a join entity", "schema UserGroup, edge user:", func() *plugin.Model {
				join := entity("UserGroup")
				join.Join = true
				user := edge("user", plugin.M2O, "User")
				user.Annotations = ann(t, own)
				join.Edges = []*plugin.Edge{user}
				return model(entity("User"), join)
			}},
			{"plain field", "schema User, field name:", func() *plugin.Model {
				f := field("name", "string")
				f.Annotations = ann(t, own)
				return model(entity("User", f))
			}},
			{"primary key", "schema User, field id:", func() *plugin.Model {
				u := entity("User")
				u.Fields[0].Annotations = ann(t, own)
				return model(u)
			}},
			{"foreign key", "schema Post, field author_id:", func() *plugin.Model {
				aid := field("author_id", "int64")
				aid.Annotations = ann(t, own)
				p := entity("Post", aid)
				author := edge("author", plugin.M2O, "User")
				author.Field, author.FieldOn = "author_id", "Post"
				p.Edges = []*plugin.Edge{author}
				return model(p, entity("User"))
			}},
			{"OrderField", "schema User, field name:", func() *plugin.Model {
				f := field("name", "string")
				f.Annotations = ann(t, own, graphql.OrderField("NAME"))
				return model(entity("User", f))
			}},
			{"edge", "schema User, edge posts:", func() *plugin.Model {
				u := entity("User")
				posts := edge("posts", plugin.O2M, "User")
				posts.Annotations = ann(t, own)
				u.Edges = []*plugin.Edge{posts}
				return model(u)
			}},
		} {
			t.Run(name+"/"+c.placement, func(t *testing.T) {
				renderErr(t, c.model(), c.where, "@"+name+" is velox-graphql's own: it binds ")
			})
		}
	}
}
