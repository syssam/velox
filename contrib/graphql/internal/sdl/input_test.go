package sdl

import (
	"slices"
	"strings"
	"testing"

	"github.com/syssam/velox2/contrib/graphql"
	"github.com/syssam/velox2/plugin"
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
	user.Tenant = 8 // org_id
	user.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate(), graphql.MutationDelete()))
	posts := edge("posts", plugin.O2M, "Post")
	posts.Field, posts.FieldOn = "author_id", "Post"
	groups := edge("groups", plugin.M2M, "Group")
	groups.Annotations = ann(t, graphql.FieldName("memberships"))
	profile := edge("profile", plugin.O2O, "Profile")
	categories := edge("categories", plugin.M2M, "Group")
	categories.Annotations = ann(t, graphql.Skip(graphql.SkipMutationCreateInput))
	likes := edge("likes", plugin.O2M, "Like") // to an entity that is not a Node
	likes.Field, likes.FieldOn = "user_id", "Like"
	bf := edge("best_friend", plugin.O2O, "User")
	bf.Field, bf.FieldOn, bf.OwnsKey = "user_best_friend", "User", true // bidirectional
	user.Edges = []*plugin.Edge{posts, groups, profile, categories, likes, bf}

	post := entity("Post", field("author_id", "int64"), field("title", "string"))
	author := edge("author", plugin.M2O, "User")
	author.Required, author.Field, author.FieldOn = true, "author_id", "Post"
	creator := edge("creator", plugin.M2O, "User")
	creator.Immutable = true
	post.Edges = []*plugin.Edge{author, creator}
	post.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()))

	like := &plugin.Entity{
		Name: "Like", Tenant: -1, PrimaryKey: []int{0, 1},
		Fields: []*plugin.Field{field("user_id", "int64"), field("day", "string")},
	}
	lu := edge("user", plugin.M2O, "User")
	lu.Required, lu.Field, lu.FieldOn = true, "user_id", "Like"
	like.Edges = []*plugin.Edge{lu}
	like.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate()))

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

	// Mutation lists the kinds each schema asks for.
	if s.Types["Mutation"] == nil {
		t.Fatalf("no Mutation:\n%s", sdl)
	}
	var got []string
	for _, f := range s.Types["Mutation"].Fields {
		got = append(got, f.Name+"("+strings.Join(argsOf(s, "Mutation", f.Name), ", ")+"): "+f.Type.String())
	}
	want := []string{
		"createLike(input: CreateLikeInput!): Like!",
		"createPost(input: CreatePostInput!): Post!",
		"updatePost(id: ID!, input: UpdatePostInput!): Post!",
		"createUser(input: CreateUserInput!): User!",
		"updateUser(id: ID!, input: UpdateUserInput!): User!",
		"deleteUser(id: ID!): ID!",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Mutation:\n got %v\nwant %v", got, want)
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
		log.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate(), graphql.MutationDelete()))
		s, sdl := load(t, model(log))
		if s.Types["UpdateLogInput"] != nil || typeOf(s, "Mutation", "updateLog") != "" {
			t.Errorf("an update:\n%s", sdl)
		}
		if typeOf(s, "Mutation", "createLog") != "Log!" || typeOf(s, "Mutation", "deleteLog") != "ID!" {
			t.Errorf("create or delete is missing:\n%s", sdl)
		}
	})

	t.Run("no create fields", func(t *testing.T) {
		// An input type cannot be empty, and createX takes its input.
		u := entity("User", field("org_id", "int64"))
		u.Tenant = 1
		u.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate()))
		renderErr(t, model(u), "schema User", "create input", "no fields")
	})

	t.Run("Type renames", func(t *testing.T) {
		u := entity("User", field("name", "string"))
		u.Annotations = ann(t, graphql.Type("Account"), graphql.Mutations())
		s, sdl := load(t, model(u))
		if typeOf(s, "Mutation", "createAccount") != "Account!" || typeOf(s, "Mutation", "updateAccount") != "Account!" ||
			s.Types["CreateAccountInput"] == nil || s.Types["UpdateAccountInput"] == nil || strings.Contains(sdl, "CreateUser") {
			t.Errorf("renamed:\n%s", sdl)
		}
	})

	t.Run("explicit inputs", func(t *testing.T) {
		// CreateInput(true) brings back a field the rules leave out.
		by := &plugin.Field{Name: "edited_by", GoName: "EditedBy", Type: "string", Nullable: true, Optional: true, UpdateDefault: true}
		by.Annotations = ann(t, graphql.CreateInput(true))
		u := entity("User", by)
		u.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate()))
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
				u.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()))
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
			e.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()))
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
		user.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()))
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
		cat.Annotations = ann(t, graphql.Mutations(graphql.MutationUpdate()))
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
	u.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()))
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

// A mutation input's fields carry the directives of the field or edge they
// set, as the where input's do: a directive that guards a field's value
// guards its writes too.
func TestInputsCarryDirectives(t *testing.T) {
	admin := graphql.Directives(graphql.Directive{Name: "admin"})
	audit := graphql.Directives(graphql.Directive{Name: "audit", Args: map[string]any{"level": 2}})
	name := &plugin.Field{Name: "name", GoName: "Name", Type: "string", Pointer: true, Nullable: true, Optional: true}
	name.Annotations = ann(t, admin)
	title := field("title", "string")
	title.Annotations = ann(t, audit)
	plain := field("plain", "string")
	user := entity("User", name, title, plain)
	user.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()))
	groups := edge("groups", plugin.M2M, "Group")
	groups.Annotations = ann(t, admin)
	best := edge("best", plugin.M2O, "User")
	best.Field, best.FieldOn = "best_id", "User"
	best.Annotations = ann(t, admin, audit)
	// The key's own directives come first, as on its where predicates;
	// GraphQL's own only, since an application directive on a foreign key is
	// refused.
	bestID := &plugin.Field{Name: "best_id", GoName: "BestID", Type: "int64", Pointer: true, Nullable: true, Optional: true}
	bestID.Annotations = ann(t, graphql.Directives(graphql.Directive{Name: "deprecated", Args: map[string]any{"reason": "old"}}))
	user.Fields = append(user.Fields, bestID)
	user.Edges = []*plugin.Edge{groups, best}
	s, sdl := load(t, model(entity("Group"), user), appDirectives)

	for _, c := range []struct {
		typ, field string
		want       []string
	}{
		{"CreateUserInput", "name", []string{"@admin"}},
		{"CreateUserInput", "title", []string{"@audit"}},
		{"CreateUserInput", "plain", nil},
		{"CreateUserInput", "groupIDs", []string{"@admin"}},
		{"CreateUserInput", "bestID", []string{"@deprecated", "@admin", "@audit"}},
		{"UpdateUserInput", "name", []string{"@admin"}},
		{"UpdateUserInput", "clearName", []string{"@admin"}},
		{"UpdateUserInput", "title", []string{"@audit"}},
		{"UpdateUserInput", "plain", nil},
		{"UpdateUserInput", "addGroupIDs", []string{"@admin"}},
		{"UpdateUserInput", "removeGroupIDs", []string{"@admin"}},
		{"UpdateUserInput", "clearGroups", []string{"@admin"}},
		{"UpdateUserInput", "bestID", []string{"@deprecated", "@admin", "@audit"}},
		{"UpdateUserInput", "clearBest", []string{"@deprecated", "@admin", "@audit"}},
	} {
		if got := dirsOf(s, c.typ, c.field); !slices.Equal(got, c.want) && (len(got) > 0 || len(c.want) > 0) {
			t.Errorf("%s.%s: %v, want %v\n%s", c.typ, c.field, got, c.want, sdl)
		}
	}
	if !strings.Contains(sdl, "  bestID: ID @deprecated(reason: \"old\") @admin @audit(level: 2)\n") {
		t.Errorf("the arguments are lost:\n%s", sdl)
	}
	if strings.Contains(sdl[strings.Index(sdl, "input CreateUserInput"):], "@goField") {
		t.Errorf("an input field has @goField:\n%s", sdl)
	}
}

// GraphQL forbids @deprecated on a required input field: a client could not
// stop sending it. A required input leaves it out; the type's field, an
// optional input and a clear keep it, and so do the other directives.
func TestNoDeprecatedOnRequiredInput(t *testing.T) {
	dep := graphql.Directive{Name: "deprecated", Args: map[string]any{"reason": "old"}}
	title := field("title", "string")
	title.Annotations = ann(t, graphql.Directives(dep, graphql.Directive{Name: "admin"}))
	note := &plugin.Field{Name: "note", GoName: "Note", Type: "string", Pointer: true, Nullable: true, Optional: true}
	note.Annotations = ann(t, graphql.Directives(dep))
	post := entity("Post", field("author_id", "int64"), title, note)
	author := edge("author", plugin.M2O, "User")
	author.Required, author.Field, author.FieldOn = true, "author_id", "Post"
	author.Annotations = ann(t, graphql.Directives(dep))
	post.Edges = []*plugin.Edge{author}
	post.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()))
	s, sdl := load(t, model(post, entity("User")), appDirectives)
	for _, c := range []struct {
		typ, field string
		want       []string
	}{
		{"Post", "title", []string{"@deprecated", "@admin"}},
		{"CreatePostInput", "title", []string{"@admin"}},
		{"CreatePostInput", "authorID", []string{}},
		{"CreatePostInput", "note", []string{"@deprecated"}},
		{"UpdatePostInput", "title", []string{"@deprecated", "@admin"}},
		{"UpdatePostInput", "authorID", []string{"@deprecated"}},
		{"UpdatePostInput", "note", []string{"@deprecated"}},
		{"UpdatePostInput", "clearNote", []string{"@deprecated"}},
	} {
		if got := dirsOf(s, c.typ, c.field); !slices.Equal(got, c.want) {
			t.Errorf("%s.%s: %v, want %v\n%s", c.typ, c.field, got, c.want, sdl)
		}
	}
}

// An edge's ID inputs are named after ent's singular of its lowercase name,
// in Go and GraphQL alike: an edge people, or an edge renamed to people,
// gives addPersonIDs and AddPersonIDs.
func TestEdgeInputsSingular(t *testing.T) {
	team := entity("Team")
	team.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()))
	team.Edges = []*plugin.Edge{edge("people", plugin.M2M, "Person")}
	club := entity("Club")
	club.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()))
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
	f.Annotations = ann(t, graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()))
	return f
}
