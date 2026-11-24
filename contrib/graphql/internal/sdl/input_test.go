package sdl

import (
	"math"
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/plugin"
)

// blogInputs is a model whose User and Post have every kind of input field.
func blogInputs(t *testing.T) *plugin.Model {
	t.Helper()
	name := field("name", "string")
	name.Comment = "The display name."
	nickname := &plugin.Field{Name: "nickname", GoName: "Nickname", Type: "string", Nullable: true, Optional: true}
	role := &plugin.Field{Name: "role", GoName: "Role", Type: "enum", Enum: "UserRole", EnumValues: []string{"admin", "viewer"}, HasDefault: true}
	created := &plugin.Field{Name: "created_at", GoName: "CreatedAt", Type: "time", Immutable: true, HasDefault: true}
	updated := &plugin.Field{Name: "updated_at", GoName: "UpdatedAt", Type: "time", HasDefault: true, UpdateDefault: true}
	editedBy := &plugin.Field{Name: "edited_by", GoName: "EditedBy", Type: "string", UpdateDefault: true}
	touched := &plugin.Field{Name: "touched", GoName: "Touched", Type: "time", Nullable: true, Optional: true, UpdateDefault: true}
	org := field("org_id", "int64")
	org.Annotations = ann(t, graphql.Skip()) // out of everything
	secret := &plugin.Field{Name: "secret", GoName: "Secret", Type: "string", Sensitive: true}
	handle := &plugin.Field{Name: "handle", GoName: "Handle", Type: "string", Immutable: true}
	views := field("views", "int") // a hook fills it on create
	views.Annotations = ann(t, graphql.CreateInput(false))
	notes := &plugin.Field{Name: "notes", GoName: "Notes", Type: "string", Nullable: true, Optional: true}
	notes.Annotations = ann(t, graphql.UpdateInput(false))
	hidden := field("hidden", "string") // out of the type, still written
	hidden.Annotations = ann(t, graphql.Skip(graphql.SkipType))
	createOnly := field("create_only", "string")
	createOnly.Annotations = ann(t, graphql.Skip(graphql.SkipMutationUpdateInput))
	skipped := field("skipped", "string")
	skipped.Annotations = ann(t, graphql.Skip())
	bestFriend := &plugin.Field{Name: "user_best_friend", GoName: "UserBestFriend", Type: "int64", Nullable: true, Optional: true, Implicit: "best_friend"}
	user := entity("User", name, nickname, role, created, updated, editedBy, touched, org, secret, handle, views, notes, hidden, createOnly, skipped, bestFriend)
	user.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
	posts := edge("posts", plugin.O2M, "Post")
	posts.Field, posts.FieldOn = "author_id", "Post"
	groups := edge("groups", plugin.M2M, "Group")
	groups.Annotations = ann(t, graphql.FieldName("memberships"))
	profile := edge("profile", plugin.O2O, "Profile")
	categories := edge("categories", plugin.M2M, "Group")
	categories.Annotations = ann(t, graphql.Skip(graphql.SkipMutationCreateInput))
	likes := edge("likes", plugin.O2M, "Like") // to an entity that has no ID
	likes.Field, likes.FieldOn = "user_id", "Like"
	bf := edge("best_friend", plugin.O2O, "User")
	bf.Field, bf.FieldOn, bf.OwnsKey = "user_best_friend", "User", true // bidirectional
	user.Edges = []*plugin.Edge{posts, groups, profile, categories, likes, bf}

	rev := &plugin.Field{Name: "rev", GoName: "Rev", Type: "int64", Version: true} // velox sets it
	post := entity("Post", field("author_id", "int64"), field("title", "string"), rev)
	author := edge("author", plugin.M2O, "User")
	author.Required, author.Field, author.FieldOn = true, "author_id", "Post"
	creator := edge("creator", plugin.M2O, "User")
	creator.Immutable = true
	post.Edges = []*plugin.Edge{author, creator}
	post.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))

	like := &plugin.Entity{
		Name: "Like", PrimaryKey: []int{0, 1},
		Fields: []*plugin.Field{field("user_id", "int64"), field("day", "string")},
	}
	lu := edge("user", plugin.M2O, "User")
	lu.Required, lu.Field, lu.FieldOn = true, "user_id", "Like"
	like.Edges = []*plugin.Edge{lu}
	like.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))

	m := model(entity("Group"), like, post, entity("Profile"), user)
	m.Enums = []*plugin.Enum{{Type: "UserRole", Values: []string{"admin", "viewer"}}}
	return m
}

func TestInputs(t *testing.T) {
	s, sdl := load(t, blogInputs(t))
	for _, c := range []struct {
		typ  string
		want []string
	}{
		{"CreateUserInput", []string{
			"name: String!", "nickname: String", "role: UserRole", "createdAt: Time", "updatedAt: Time",
			// editedBy has only an UpdateDefault and is not nullable: nothing
			// fills it on insert, so it is required. touched is nullable: out.
			"editedBy: String!",
			"secret: String!", "handle: String!", "notes: String", "hidden: String!", "createOnly: String!",
			// Post.author_id is required: a post always has an author, so
			// User has no input that takes one; it is linked by its authorID.
			"membershipIDs: [ID!]", "profileID: ID", "bestFriendID: ID",
		}},
		{"UpdateUserInput", []string{
			"name: String", "nickname: String", "clearNickname: Boolean", "role: UserRole", "updatedAt: Time",
			"secret: String", "views: Int", "hidden: String",
			"addMembershipIDs: [ID!]", "removeMembershipIDs: [ID!]", "clearMemberships: Boolean",
			"profileID: ID", "clearProfile: Boolean",
			"addCategoryIDs: [ID!]", "removeCategoryIDs: [ID!]", "clearCategories: Boolean",
			"bestFriendID: ID", "clearBestFriend: Boolean",
		}},
		// author_id is the author edge's: authorID, required on create; the
		// immutable creator edge is set on create only.
		{"CreatePostInput", []string{"title: String!", "authorID: ID!", "creatorID: ID"}},
		{"UpdatePostInput", []string{"title: String", "authorID: ID"}},
		// A composite key's foreign keys are its edges' IDs.
		{"CreateLikeInput", []string{"day: String!", "userID: ID!"}},
	} {
		if got := fieldsOf(s, c.typ); !slices.Equal(got, c.want) {
			t.Errorf("%s:\n got %v\nwant %v", c.typ, got, c.want)
		}
	}
	if !strings.Contains(sdl, "input CreateUserInput {\n  \"\"\"The display name.\"\"\"\n  name: String!\n") {
		t.Errorf("the field's description:\n%s", sdl)
	}

	// The inputs are those of the kinds each schema asks for, and no
	// Mutation: the application declares its own.
	if s.Types["Mutation"] != nil || strings.Contains(sdl, "type Mutation") {
		t.Errorf("a Mutation:\n%s", sdl)
	}
	for _, in := range []string{"CreateLikeInput", "CreatePostInput", "UpdatePostInput", "CreateUserInput", "UpdateUserInput"} {
		if s.Types[in] == nil {
			t.Errorf("no %s:\n%s", in, sdl)
		}
	}
	for _, absent := range []string{"UpdateLikeInput", "CreateGroupInput"} {
		if s.Types[absent] != nil {
			t.Errorf("%s is declared", absent)
		}
	}

	t.Run("no mutations", func(t *testing.T) {
		s, sdl := load(t, model(entity("User", field("name", "string"))))
		if s.Types["Mutation"] != nil || strings.Contains(sdl, "Mutation") || strings.Contains(sdl, "Input") {
			t.Errorf("a Mutation:\n%s", sdl)
		}
	})

	t.Run("no update fields", func(t *testing.T) {
		msg := &plugin.Field{Name: "msg", GoName: "Msg", Type: "string", Immutable: true}
		log := entity("Log", msg)
		log.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
		s, sdl := load(t, model(log))
		if s.Types["UpdateLogInput"] != nil {
			t.Errorf("an update:\n%s", sdl)
		}
		if s.Types["CreateLogInput"] == nil {
			t.Errorf("create is missing:\n%s", sdl)
		}
	})

	t.Run("no create fields", func(t *testing.T) {
		// An input type cannot be empty.
		only := field("only", "int64")
		only.Annotations = ann(t, graphql.Skip(graphql.SkipMutationCreateInput))
		u := entity("User", only)
		u.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
		renderErr(t, model(u), "schema User", "create input", "no fields")
	})

	t.Run("Type renames", func(t *testing.T) {
		u := entity("User", field("name", "string"))
		u.Annotations = ann(t, graphql.Type("Account"), graphql.Mutations())
		s, sdl := load(t, model(u))
		if s.Types["CreateAccountInput"] == nil || s.Types["UpdateAccountInput"] == nil || strings.Contains(sdl, "CreateUser") {
			t.Errorf("renamed:\n%s", sdl)
		}
	})

	t.Run("explicit inputs", func(t *testing.T) {
		// CreateInput(true) brings back a field the rules leave out.
		by := &plugin.Field{Name: "edited_by", GoName: "EditedBy", Type: "string", Nullable: true, Optional: true, UpdateDefault: true}
		by.Annotations = ann(t, graphql.CreateInput(true))
		u := entity("User", by)
		u.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate))
		s, _ := load(t, model(u))
		if got := fieldsOf(s, "CreateUserInput"); !slices.Equal(got, []string{"editedBy: String"}) {
			t.Errorf("CreateUserInput: %v", got)
		}
	})

	// An UpdateDefault fills a field on update, and only a field that is not
	// nullable has nothing to fill it on insert: that one is required on
	// create and stays out of update; UpdateInput(true) brings it in.
	t.Run("update default", func(t *testing.T) {
		for _, c := range []struct {
			name           string
			nullable       bool
			ann            []graphql.Annotation
			create, update []string
		}{
			{"required", false, nil, []string{"title: String!", "editedBy: String!"}, []string{"title: String"}},
			{"nullable", true, nil, []string{"title: String!"}, []string{"title: String"}},
			{"required, update input", false, []graphql.Annotation{graphql.UpdateInput(true)}, []string{"title: String!", "editedBy: String!"}, []string{"title: String", "editedBy: String"}},
			{"required, no create input", false, []graphql.Annotation{graphql.CreateInput(false)}, []string{"title: String!"}, []string{"title: String"}},
		} {
			t.Run(c.name, func(t *testing.T) {
				by := &plugin.Field{Name: "edited_by", GoName: "EditedBy", Type: "string", Nullable: c.nullable, Optional: c.nullable, UpdateDefault: true}
				by.Annotations = ann(t, c.ann...)
				u := entity("Post", field("title", "string"), by)
				u.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
				s, sdl := load(t, model(u))
				if got := fieldsOf(s, "CreatePostInput"); !slices.Equal(got, c.create) {
					t.Errorf("CreatePostInput:\n got %v\nwant %v\n%s", got, c.create, sdl)
				}
				if got := fieldsOf(s, "UpdatePostInput"); !slices.Equal(got, c.update) {
					t.Errorf("UpdatePostInput:\n got %v\nwant %v\n%s", got, c.update, sdl)
				}
			})
		}
	})
}

// An edge's inputs offer only what a write of its foreign key may do, on
// whichever entity holds the key.
func TestEdgeInputsFollowTheKey(t *testing.T) {
	// blog is User and Post, both with create and update, Post.author_id as
	// key; flags sets the key's flags.
	blog := func(flags func(f *plugin.Field)) *plugin.Model {
		aid := field("author_id", "int64")
		flags(aid)
		post := entity("Post", aid, field("title", "string"))
		author := edge("author", plugin.M2O, "User")
		author.Field, author.FieldOn, author.Inverse, author.Ref = "author_id", "Post", true, "posts"
		author.Required, author.Immutable = !aid.Nullable, aid.Immutable
		post.Edges = []*plugin.Edge{author}
		user := entity("User", field("name", "string"))
		posts := edge("posts", plugin.O2M, "Post")
		posts.Field, posts.FieldOn = "author_id", "Post"
		user.Edges = []*plugin.Edge{posts}
		for _, e := range []*plugin.Entity{post, user} {
			e.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
		}
		return model(post, user)
	}
	for _, c := range []struct {
		name                   string
		flags                  func(f *plugin.Field)
		userCreate, userUpdate []string
		postCreate, postUpdate []string
	}{
		{"nullable", func(f *plugin.Field) { f.Nullable, f.Optional = true, true },
			[]string{"name: String!", "postIDs: [ID!]"},
			[]string{"name: String", "addPostIDs: [ID!]", "removePostIDs: [ID!]", "clearPosts: Boolean"},
			[]string{"title: String!", "authorID: ID"},
			[]string{"title: String", "authorID: ID", "clearAuthor: Boolean"}},
		// A required author_id: a post always has an author, so User has no
		// input that would take one from it; Post sets it.
		{"required", func(f *plugin.Field) {},
			[]string{"name: String!"},
			[]string{"name: String"},
			[]string{"title: String!", "authorID: ID!"},
			[]string{"title: String", "authorID: ID"}},
		// The reviewer's example: an immutable author_id moves on no write,
		// so User's posts edge has no input; Post sets it on create only.
		{"immutable", func(f *plugin.Field) { f.Immutable = true },
			[]string{"name: String!"},
			[]string{"name: String"},
			[]string{"title: String!", "authorID: ID!"},
			[]string{"title: String"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, sdl := load(t, blog(c.flags))
			for typ, want := range map[string][]string{
				"CreateUserInput": c.userCreate, "UpdateUserInput": c.userUpdate,
				"CreatePostInput": c.postCreate, "UpdatePostInput": c.postUpdate,
			} {
				if got := fieldsOf(s, typ); !slices.Equal(got, want) {
					t.Errorf("%s:\n got %v\nwant %v\n%s", typ, got, want, sdl)
				}
			}
		})
	}

	// An O2O whose key the other entity holds: User.profile, with the key
	// Profile.user_id.
	required := false // User.profile is a required edge
	o2o := func(flags func(f *plugin.Field)) *plugin.Model {
		uid := field("user_id", "int64")
		flags(uid)
		profile := entity("Profile", uid, field("bio", "string"))
		owner := edge("user", plugin.O2O, "User")
		owner.Field, owner.FieldOn, owner.Inverse, owner.Ref, owner.OwnsKey = "user_id", "Profile", true, "profile", true
		owner.Required, owner.Immutable = !uid.Nullable, uid.Immutable
		profile.Edges = []*plugin.Edge{owner}
		user := entity("User", field("name", "string"))
		pe := edge("profile", plugin.O2O, "Profile")
		pe.Field, pe.FieldOn = "user_id", "Profile"
		pe.Required = required
		user.Edges = []*plugin.Edge{pe}
		user.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
		return model(profile, user)
	}
	for _, c := range []struct {
		name           string
		flags          func(f *plugin.Field)
		create, update []string
	}{
		{"O2O nullable", func(f *plugin.Field) { f.Nullable, f.Optional = true, true },
			[]string{"name: String!", "profileID: ID"}, []string{"name: String", "profileID: ID", "clearProfile: Boolean"}},
		// A required key always holds a user, whom profileID could only take
		// the profile from: no input; the profile is linked by its userID.
		{"O2O required", func(f *plugin.Field) {},
			[]string{"name: String!"}, []string{"name: String"}},
		{"O2O immutable", func(f *plugin.Field) { f.Immutable = true },
			[]string{"name: String!"}, []string{"name: String"}},
		// A required edge whose key is on the target's row: create must give
		// the target, and update may move it but not clear it, whatever the
		// key's nullability.
		{"O2O required edge, nullable key", func(f *plugin.Field) {
			f.Nullable, f.Optional = true, true
			required = true
		}, []string{"name: String!", "profileID: ID!"}, []string{"name: String", "profileID: ID"}},
	} {
		required = false
		t.Run(c.name, func(t *testing.T) {
			s, sdl := load(t, o2o(c.flags))
			if got := fieldsOf(s, "CreateUserInput"); !slices.Equal(got, c.create) {
				t.Errorf("CreateUserInput:\n got %v\nwant %v\n%s", got, c.create, sdl)
			}
			if got := fieldsOf(s, "UpdateUserInput"); !slices.Equal(got, c.update) {
				t.Errorf("UpdateUserInput:\n got %v\nwant %v\n%s", got, c.update, sdl)
			}
		})
	}

	// A Required edge whose key is NULL-able: create sets it from both sides,
	// and the owning side's update moves it; the other side's update only adds,
	// since a remove, a clear or a one-to-one re-point would leave the old
	// child's Required key NULL, which velox refuses.
	t.Run("required self edges", func(t *testing.T) {
		s, sdl := load(t, model(folder(t)))
		for typ, want := range map[string][]string{
			"CreateFolderInput": {"name: String!", "childIDs: [ID!]", "parentID: ID!", "nextID: ID", "prevID: ID!"},
			"UpdateFolderInput": {"name: String", "addChildIDs: [ID!]", "parentID: ID", "prevID: ID"},
		} {
			if got := fieldsOf(s, typ); !slices.Equal(got, want) {
				t.Errorf("%s:\n got %v\nwant %v\n%s", typ, got, want, sdl)
			}
		}

		// A self edge that is not Required keeps the other side's update inputs.
		cat := entity("Category", &plugin.Field{Name: "category_children", GoName: "CategoryChildren", Column: "category_children", Type: "int64", Nullable: true, Optional: true, Implicit: "parent"})
		ch := edge("children", plugin.O2M, "Category")
		ch.Field, ch.FieldOn, ch.Ref = "category_children", "Category", "parent"
		parent := edge("parent", plugin.M2O, "Category")
		parent.Field, parent.FieldOn, parent.Ref, parent.Inverse = "category_children", "Category", "children", true
		cat.Edges = []*plugin.Edge{ch, parent}
		cat.Annotations = ann(t, graphql.Mutations(graphql.MutationUpdate))
		s, sdl = load(t, model(cat))
		if got, want := fieldsOf(s, "UpdateCategoryInput"), []string{"addChildIDs: [ID!]", "removeChildIDs: [ID!]", "clearChildren: Boolean", "parentID: ID", "clearParent: Boolean"}; !slices.Equal(got, want) {
			t.Errorf("UpdateCategoryInput:\n got %v\nwant %v\n%s", got, want, sdl)
		}
	})
}

// An Optional field is optional on create, and a Nillable one too; only a
// nullable one, whose column holds NULL, can be cleared on update: a Nillable
// field, or an Optional JSON one, which is no pointer.
func TestInputsOptionalAndNullable(t *testing.T) {
	motto := &plugin.Field{Name: "motto", GoName: "Motto", Type: "string", Optional: true}
	bio := &plugin.Field{Name: "bio", GoName: "Bio", Type: "string", Nullable: true, Pointer: true}
	doc := &plugin.Field{Name: "doc", GoName: "Doc", Type: "json", Nullable: true, Optional: true}
	u := entity("User", field("name", "string"), motto, bio, doc)
	u.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
	s, sdl := load(t, model(u))
	for typ, want := range map[string][]string{
		"CreateUserInput": {"name: String!", "motto: String", "bio: String", "doc: JSON"},
		"UpdateUserInput": {"name: String", "motto: String", "bio: String", "clearBio: Boolean", "doc: JSON", "clearDoc: Boolean"},
	} {
		if got := fieldsOf(s, typ); !slices.Equal(got, want) {
			t.Errorf("%s:\n got %v\nwant %v\n%s", typ, got, want, sdl)
		}
	}
}

// graphql.CreateDefault makes a bool or number with no Default optional in the
// create input, with its default in the SDL, while the type keeps it non-null:
// what entgql gives Default(true), which velox-gen refuses on a field that is
// not Nillable.
func TestCreateDefault(t *testing.T) {
	build := func(fs ...*plugin.Field) *plugin.Entity {
		u := entity("User", fs...)
		u.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
		return u
	}
	with := func(f *plugin.Field, a graphql.Annotation) *plugin.Field {
		f.Annotations = ann(t, a)
		return f
	}
	u := build(with(field("active", "bool"), graphql.CreateDefault(true)),
		with(field("views", "int64"), graphql.CreateDefault(int64(-7))),
		with(field("big", "uint64"), graphql.CreateDefault(uint64(5))),
		with(field("score", "float32"), graphql.CreateDefault(1.5)))
	s, sdl := load(t, model(u))
	for _, want := range []string{"active: Boolean! = true", "views: Int! = -7", `big: Uint64! = "5"`, "score: Float! = 1.5"} {
		if !strings.Contains(sdl, want) {
			t.Errorf("no %q in\n%s", want, sdl)
		}
	}
	for typ, want := range map[string][]string{
		"User":            {"id: ID!", "active: Boolean!", "views: Int!", "big: Uint64!", "score: Float!"},
		"UpdateUserInput": {"active: Boolean", "views: Int", "big: Uint64", "score: Float"},
	} {
		if got := fieldsOf(s, typ); !slices.Equal(got, want) {
			t.Errorf("%s:\n got %v\nwant %v", typ, got, want)
		}
	}
	if d := s.Types["CreateUserInput"].Fields.ForName("active"); d == nil || d.DefaultValue == nil || d.DefaultValue.Raw != "true" {
		t.Errorf("active has no default true: %+v", d)
	}

	nullable := field("n", "int")
	nullable.Nullable, nullable.Pointer = true, true
	defaulted := field("n", "int")
	defaulted.HasDefault, defaulted.Default = true, []byte("0")
	noCreate := entity("User", with(field("n", "int"), graphql.CreateDefault(1)))
	noCreate.Annotations = ann(t, graphql.Mutations(graphql.MutationUpdate))
	for name, c := range map[string]struct {
		m    *plugin.Model
		want string
	}{
		"a string":       {model(build(with(field("n", "string"), graphql.CreateDefault(1)))), "graphql.CreateDefault on a field of type string"},
		"nullable":       {model(build(with(nullable, graphql.CreateDefault(1)))), "graphql.CreateDefault on a nullable field"},
		"with a Default": {model(build(with(defaulted, graphql.CreateDefault(1)))), "graphql.CreateDefault on a field with a Default"},
		"not a bool":     {model(build(with(field("n", "bool"), graphql.CreateDefault(1)))), "graphql.CreateDefault(1) on a bool field"},
		"not an integer": {model(build(with(field("n", "int8"), graphql.CreateDefault(1.5)))), "graphql.CreateDefault(1.5) is not a value of the int8 field"},
		"out of range":   {model(build(with(field("n", "int8"), graphql.CreateDefault(300)))), "graphql.CreateDefault(300) is not a value of the int8 field"},
		"negative uint":  {model(build(with(field("n", "uint16"), graphql.CreateDefault(-1)))), "graphql.CreateDefault(-1) is not a value of the uint16 field"},
		"NaN":            {model(build(with(field("n", "float64"), graphql.CreateDefault(math.NaN())))), `graphql.CreateDefault("NaN") is not a value of the float64 field`},
		"high bit":       {model(build(with(field("n", "uint64"), graphql.CreateDefault(uint64(math.MaxUint64))))), "database/sql cannot send a uint64 with the high bit set"},
		"left out":       {model(build(with(field("n", "int"), graphql.Annotation{CreateInput: new(bool), CreateDefault: []byte("1")}))), "graphql.CreateDefault on a field the create input leaves out"},
		"skipped":        {model(build(with(field("n", "int"), graphql.Annotation{Skip: graphql.SkipMutationCreateInput, CreateDefault: []byte("1")}))), "graphql.CreateDefault on a field the create input leaves out"},
		"no create":      {model(noCreate), "schema User, field n: graphql.CreateDefault, and graphql.Mutations does not list create"},
		"on an edge": {model(func() *plugin.Entity {
			u := build()
			e := edge("e", plugin.M2M, "User")
			e.Annotations = ann(t, graphql.CreateDefault(1))
			u.Edges = []*plugin.Edge{e}
			return u
		}()), "graphql.CreateDefault does not apply to an edge"},
	} {
		t.Run(name, func(t *testing.T) { renderErr(t, c.m, c.want) })
	}
}

// A JSON field of []string, []int, []int64, []float64 or []bool is a list of
// the element's non-null scalar, in the type and in the inputs, as entgql
// gives field.Strings; one of any other Go type stays JSON.
func TestJSONLists(t *testing.T) {
	list := func(name, elem string, nullable bool) *plugin.Field {
		return &plugin.Field{Name: name, GoName: goName(name), Column: name, Type: "json", GoType: elem, ElemType: elem, Kind: "slice", Nullable: nullable, Optional: nullable}
	}
	named := list("named", "schema.Tags", false)
	named.PkgPath = "example.com/app/schema"
	m := list("m", "map[string]any", true)
	m.Kind = "map"
	u := entity("User", list("ss", "[]string", false), list("is", "[]int", true), list("ls", "[]int64", false),
		list("fs", "[]float64", false), list("bs", "[]bool", false), list("i8", "[]int8", false), named, m)
	u.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
	s, sdl := load(t, model(u))
	for typ, want := range map[string][]string{
		"User":            {"id: ID!", "ss: [String!]!", "is: [Int!]", "ls: [Int!]!", "fs: [Float!]!", "bs: [Boolean!]!", "i8: JSON!", "named: JSON!", "m: JSON"},
		"CreateUserInput": {"ss: [String!]!", "is: [Int!]", "ls: [Int!]!", "fs: [Float!]!", "bs: [Boolean!]!", "i8: JSON!", "named: JSON!", "m: JSON"},
		"UpdateUserInput": {"ss: [String!]", "is: [Int!]", "clearIs: Boolean", "ls: [Int!]", "fs: [Float!]", "bs: [Boolean!]", "i8: JSON", "named: JSON", "m: JSON", "clearM: Boolean"},
	} {
		if got := fieldsOf(s, typ); !slices.Equal(got, want) {
			t.Errorf("%s:\n got %v\nwant %v\n%s", typ, got, want, sdl)
		}
	}
	// A list binds to the Go field, as gqlgen binds []string to [String!];
	// JSON to its getter.
	if strings.Contains(sdl, "SsGQL") || !strings.Contains(sdl, "NamedGQL") {
		t.Errorf("getters:\n%s", sdl)
	}
	surf, err := Build(model(u))
	if err != nil {
		t.Fatal(err)
	}
	for _, ty := range surf.Types {
		for _, fi := range ty.Create {
			if fi.Name == "ss" && fi.GoType != "[]string" || fi.Name == "named" && fi.GoType != "veloxgql.JSON" {
				t.Errorf("CreateUserInput.%s: Go type %s", fi.Name, fi.GoType)
			}
		}
	}
}

// A mutation input's fields and a where input's carry no directive, as in
// entgql: a directive declared for FIELD_DEFINITION alone would make the
// SDL invalid. The type's fields and edges keep theirs, a declared foreign
// key's included, with their arguments.
func TestInputsCarryNoDirectives(t *testing.T) {
	admin := graphql.Directives(graphql.Directive{Name: "admin"})
	audit := graphql.Directives(graphql.Directive{Name: "audit", Args: map[string]any{"level": 2}})
	name := &plugin.Field{Name: "name", GoName: "Name", Type: "string", Pointer: true, Nullable: true, Optional: true}
	name.Annotations = ann(t, admin, graphql.WhereInput())
	title := field("title", "string")
	title.Annotations = ann(t, audit)
	user := entity("User", name, title)
	user.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
	groups := edge("groups", plugin.M2M, "Group")
	groups.Annotations = ann(t, admin, graphql.WhereInput())
	best := edge("best", plugin.M2O, "User")
	best.Field, best.FieldOn = "best_id", "User"
	best.Annotations = ann(t, admin, audit)
	bestID := &plugin.Field{Name: "best_id", GoName: "BestID", Type: "int64", Pointer: true, Nullable: true, Optional: true}
	bestID.Annotations = ann(t, graphql.Directives(graphql.Directive{Name: "deprecated", Args: map[string]any{"reason": "old"}}), graphql.WhereInput())
	user.Fields = append(user.Fields, bestID)
	user.Edges = []*plugin.Edge{groups, best}
	s, sdl := load(t, model(entity("Group"), user), appDirectives)

	for _, c := range []struct {
		typ, field string
		want       []string
	}{
		{"User", "name", []string{"@admin"}},
		{"User", "title", []string{"@audit"}},
		{"User", "bestID", []string{"@deprecated"}},
		{"User", "best", []string{"@admin", "@audit"}},
	} {
		if got := dirsOf(s, c.typ, c.field); !slices.Equal(got, c.want) {
			t.Errorf("%s.%s: %v, want %v\n%s", c.typ, c.field, got, c.want, sdl)
		}
	}
	for _, in := range []string{"CreateUserInput", "UpdateUserInput", "UserWhereInput"} {
		for _, f := range s.Types[in].Fields {
			if len(f.Directives) > 0 {
				t.Errorf("%s.%s carries %v", in, f.Name, dirsOf(s, in, f.Name))
			}
		}
	}
	if !strings.Contains(sdl, "  best: User @admin @audit(level: 2)\n") {
		t.Errorf("the arguments are lost:\n%s", sdl)
	}
}

// An edge's ID inputs are named after ent's singular of its lowercase name,
// in Go and GraphQL alike: an edge people, or an edge renamed to people,
// gives addPersonIDs and AddPersonIDs.
func TestEdgeInputsSingular(t *testing.T) {
	team := entity("Team")
	team.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
	team.Edges = []*plugin.Edge{edge("people", plugin.M2M, "Person")}
	club := entity("Club")
	club.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
	members := edge("members", plugin.M2M, "Person")
	members.Annotations = ann(t, graphql.FieldName("people"))
	club.Edges = []*plugin.Edge{members}
	m := model(club, entity("Person"), team)
	s, err := Build(m)
	if err != nil {
		t.Fatal(err)
	}
	load(t, m)
	names := func(fs []InputField) []string {
		var out []string
		for _, f := range fs {
			out = append(out, f.Name+" "+f.GoName)
		}
		return out
	}
	for _, typ := range []string{"Team", "Club"} {
		ty := typeNamed(t, s, typ)
		if got, want := names(ty.Create), []string{"personIDs PersonIDs"}; !slices.Equal(got, want) {
			t.Errorf("Create%sInput: %q, want %q", typ, got, want)
		}
		if got, want := names(ty.Update), []string{"addPersonIDs AddPersonIDs", "removePersonIDs RemovePersonIDs", "clearPeople ClearPeople"}; !slices.Equal(got, want) {
			t.Errorf("Update%sInput: %q, want %q", typ, got, want)
		}
	}
}

// folder is an entity with two Required self edges whose keys are NULL-able,
// as velox-gen gives them: parent (M2O) of children (O2M) on folder_children,
// and prev (O2O, owning) of next on folder_next. Both have create and update.
func folder(t *testing.T) *plugin.Entity {
	t.Helper()
	children := &plugin.Field{Name: "folder_children", GoName: "FolderChildren", Column: "folder_children", Type: "int64", Nullable: true, Optional: true, Implicit: "parent"}
	next := &plugin.Field{Name: "folder_next", GoName: "FolderNext", Column: "folder_next", Type: "int64", Nullable: true, Optional: true, Unique: true, Implicit: "prev"}
	f := entity("Folder", field("name", "string"), children, next)
	ch := edge("children", plugin.O2M, "Folder")
	ch.Field, ch.FieldOn, ch.Ref = "folder_children", "Folder", "parent"
	parent := edge("parent", plugin.M2O, "Folder")
	parent.Field, parent.FieldOn, parent.Ref, parent.Inverse, parent.Required = "folder_children", "Folder", "children", true, true
	nx := edge("next", plugin.O2O, "Folder")
	nx.Field, nx.FieldOn, nx.Ref = "folder_next", "Folder", "prev"
	prev := edge("prev", plugin.O2O, "Folder")
	prev.Field, prev.FieldOn, prev.Ref, prev.Inverse, prev.Required, prev.OwnsKey = "folder_next", "Folder", "next", true, true, true
	f.Edges = []*plugin.Edge{ch, parent, nx, prev}
	f.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate, graphql.MutationUpdate))
	return f
}
