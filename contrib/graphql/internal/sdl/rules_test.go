package sdl

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/plugin"
)

// An annotation on an element it does not apply to, or that contradicts the
// element, fails generation with the schema and the element named.
func TestAnnotationMisuse(t *testing.T) {
	// build is a User with a name, an optional nickname, a sensitive secret,
	// a status enum, an immutable handle, the tenant and a posts edge, each
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
		user.Tenant = 6
		user.Annotations = ann(t, a.user)
		posts := edge("posts", "O2M", "User")
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
		{"OrderField on an optional field", anns{nickname: graphql.OrderField("NICK")}, []string{"schema User, field nickname", "graphql.OrderField", "optional"}},
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
		{"CreateInput on an edge", anns{posts: graphql.CreateInput(false)}, []string{"schema User, edge posts", "graphql.CreateInput"}},
		{"FieldName on a schema", anns{user: graphql.FieldName("x")}, []string{"schema User:", "graphql.FieldName"}},
		{"EnumValues on a string", anns{name: graphql.EnumValues(map[string]string{"a": "A"})}, []string{"schema User, field name", "graphql.EnumValues"}},
		{"an unknown EnumValues key", anns{status: graphql.EnumValues(map[string]string{"gone": "GONE"})}, []string{"schema User, field status", `"gone"`}},
		{"WhereOps on a sensitive field", anns{secret: graphql.WhereOps()}, []string{"schema User, field secret", "graphql.WhereOps", "sensitive"}},
		{"WhereOps on the tenant", anns{org: graphql.WhereOps()}, []string{"schema User, field org_id", "graphql.WhereOps", "tenant"}},
		{"CreateInput(true) on the tenant", anns{org: graphql.CreateInput(true)}, []string{"schema User, field org_id", "graphql.CreateInput", "tenant"}},
		{"UpdateInput(true) on an immutable field", anns{handle: graphql.UpdateInput(true)}, []string{"schema User, field handle", "graphql.UpdateInput", "immutable"}},
		{"an unknown mutation", anns{user: graphql.Mutations("upsert")}, []string{"schema User", `"upsert"`}},
		{"create with its input skipped", anns{user: graphql.Annotation{Mutations: []graphql.MutationKind{"create"}, Skip: graphql.SkipMutationCreateInput}}, []string{"schema User", "create", "SkipMutationCreateInput"}},
		{"QueryField on a skipped type", anns{user: graphql.Annotation{QueryField: "users", Skip: graphql.SkipType}}, []string{"schema User", "graphql.QueryField", "Skip"}},
		{"Mutations on a skipped type", anns{user: graphql.Annotation{Mutations: []graphql.MutationKind{"create"}, Skip: graphql.SkipType}}, []string{"schema User", "graphql.Mutations", "Skip"}},
		{"OrderField on a field out of the type", anns{name: graphql.Annotation{OrderField: "NAME", Skip: graphql.SkipType}}, []string{"schema User, field name", "graphql.OrderField", "cursors"}},
		{"WhereOps on a field out of the type", anns{name: graphql.Annotation{WhereOps: graphql.OpsEquality, Skip: graphql.SkipType}}, []string{"schema User, field name", "graphql.WhereOps", "reveal"}},
		{"CreateInput(false) on the tenant", anns{org: graphql.CreateInput(false)}, []string{"schema User, field org_id", "graphql.CreateInput", "tenant"}},
		{"UpdateInput(false) on the tenant", anns{org: graphql.UpdateInput(false)}, []string{"schema User, field org_id", "graphql.UpdateInput", "tenant"}},
		{"UpdateInput(true) on the tenant", anns{org: graphql.UpdateInput(true)}, []string{"schema User, field org_id", "graphql.UpdateInput", "tenant"}},
		{"update with its input skipped", anns{user: graphql.Annotation{Mutations: []graphql.MutationKind{"update"}, Skip: graphql.SkipMutationUpdateInput}}, []string{"schema User", "update", "SkipMutationUpdateInput"}},
	} {
		t.Run(c.name, func(t *testing.T) { renderErr(t, build(c.a), c.wants...) })
	}

	// A mutation kind listed twice is harmless.
	m := build(anns{user: graphql.Annotation{Mutations: []graphql.MutationKind{"create", "create"}}})
	if s, _ := load(t, m); typeOf(s, "Mutation", "createUser") != "User!" {
		t.Error("no createUser")
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
			author := edge("author", "M2O", "User")
			author.Field, author.FieldOn = "author_id", "Post"
			post.Edges = []*plugin.Edge{author}
			renderErr(t, model(post, entity("User")), "schema Post, field author_id", "foreign key")
		}
	})

	t.Run("a required edge create cannot set", func(t *testing.T) {
		// Its target is skipped, or is not a Node: createPost could never
		// give the key.
		for _, target := range []*plugin.Entity{
			func() *plugin.Entity { u := entity("User"); u.Annotations = ann(t, graphql.Skip()); return u }(),
			{Name: "User", Tenant: -1, PrimaryKey: []int{0, 1}, Fields: []*plugin.Field{field("a", "string"), field("b", "string")}},
		} {
			post := entity("Post", field("title", "string"))
			author := edge("author", "M2O", "User")
			author.Required = true
			post.Edges = []*plugin.Edge{author}
			post.Annotations = ann(t, graphql.Mutations(graphql.Create()))
			renderErr(t, model(post, target), "schema Post, edge author", "required", "createPost")
			// The user may leave it to a hook.
			author.Annotations = ann(t, graphql.Skip(graphql.SkipMutationCreateInput))
			load(t, model(post, target))
		}

		// A required O2O whose key the target's row holds: createUser writes
		// no key of its own, so it can succeed without the edge.
		user := entity("User", field("name", "string"))
		profile := edge("profile", "O2O", "Profile")
		profile.Required, profile.Field, profile.FieldOn = true, "user_id", "Profile"
		user.Edges = []*plugin.Edge{profile}
		user.Annotations = ann(t, graphql.Mutations(graphql.Create()))
		target := entity("Profile", field("user_id", "int64"))
		target.Annotations = ann(t, graphql.Skip())
		s, _ := load(t, model(target, user))
		if got := fieldsOf(s, "CreateUserInput"); !slices.Equal(got, []string{"name: String!"}) {
			t.Errorf("CreateUserInput: %v", got)
		}
		// The same edge holding its key on User's row is refused.
		profile.OwnsKey, profile.FieldOn = true, "User"
		renderErr(t, model(target, user), "schema User, edge profile", "required", "createUser")
	})

	t.Run("a key the caller supplies", func(t *testing.T) {
		code := entity("Country", field("name", "string"))
		code.Fields[0] = &plugin.Field{Name: "id", GoName: "ID", Type: "string"}
		code.Annotations = ann(t, graphql.Mutations(graphql.Create()))
		renderErr(t, model(code), "schema Country", "create", "supplied by the caller", "v1")
		code.Fields[0].HasDefault = true
		load(t, model(code))
		// Update and delete need no new key.
		code.Fields[0].HasDefault = false
		code.Annotations = ann(t, graphql.Mutations(graphql.Update(), graphql.Delete()))
		load(t, model(code))
	})

	t.Run("QueryField on a join entity", func(t *testing.T) {
		join := &plugin.Entity{Name: "UserGroup", Join: true, Tenant: -1, PrimaryKey: []int{0, 1},
			Fields: []*plugin.Field{field("user_id", "int64"), field("group_id", "int64")}}
		join.Annotations = ann(t, graphql.QueryField("userGroups"))
		renderErr(t, model(entity("User"), join), "schema UserGroup", "graphql.QueryField", "join")
		join.Annotations = ann(t, graphql.Mutations())
		renderErr(t, model(entity("User"), join), "schema UserGroup", "graphql.Mutations", "join")
	})

	t.Run("update or delete without an ID", func(t *testing.T) {
		for _, k := range []graphql.MutationKind{graphql.Update(), graphql.Delete()} {
			like := &plugin.Entity{Name: "Like", Tenant: -1, PrimaryKey: []int{0, 1},
				Fields: []*plugin.Field{field("a", "string"), field("b", "string")}}
			like.Annotations = ann(t, graphql.Mutations(k))
			renderErr(t, model(like), "schema Like", string(k), "key")
		}
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
	if !strings.Contains(sdl, "enum UserRole {") || !strings.Contains(sdl, "type User implements Node {") {
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
