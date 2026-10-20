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
	nickname := &plugin.Field{Name: "nickname", GoName: "Nickname", Type: "string", Nullable: true, Nillable: true, Optional: true}
	role := &plugin.Field{Name: "role", GoName: "Role", Type: "enum", Enum: "UserRole", EnumValues: []string{"admin", "viewer"}, HasDefault: true}
	created := &plugin.Field{Name: "created_at", GoName: "CreatedAt", Type: "time", Immutable: true, HasDefault: true}
	updated := &plugin.Field{Name: "updated_at", GoName: "UpdatedAt", Type: "time", HasDefault: true, UpdateDefault: true}
	editedBy := &plugin.Field{Name: "edited_by", GoName: "EditedBy", Type: "string", UpdateDefault: true}
	touched := &plugin.Field{Name: "touched", GoName: "Touched", Type: "time", Nullable: true, Nillable: true, Optional: true, UpdateDefault: true}
	org := field("org_id", "int64")
	secret := &plugin.Field{Name: "secret", GoName: "Secret", Type: "string", Sensitive: true}
	handle := &plugin.Field{Name: "handle", GoName: "Handle", Type: "string", Immutable: true}
	views := field("views", "int")
	views.HasDefault = true
	views.Annotations = ann(t, graphql.CreateInput(false))
	notes := &plugin.Field{Name: "notes", GoName: "Notes", Type: "string", Nullable: true, Nillable: true, Optional: true}
	notes.Annotations = ann(t, graphql.UpdateInput(false))
	hidden := field("hidden", "string") // out of the type, still written
	hidden.Annotations = ann(t, graphql.Skip(graphql.SkipType))
	createOnly := field("create_only", "string")
	createOnly.Annotations = ann(t, graphql.Skip(graphql.SkipMutationUpdateInput))
	skipped := field("skipped", "string")
	skipped.Annotations = ann(t, graphql.Skip())
	bestFriend := &plugin.Field{Name: "user_best_friend", GoName: "UserBestFriend", Type: "int64", Nullable: true, Nillable: true, Optional: true, Implicit: "best_friend"}
	user := entity("User", name, nickname, role, created, updated, editedBy, touched, org, secret, handle, views, notes, hidden, createOnly, skipped, bestFriend)
	user.Tenant = 8 // org_id
	user.Annotations = ann(t, graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete()))
	posts := edge("posts", "O2M", "Post")
	posts.Field, posts.FieldOn = "author_id", "Post"
	groups := edge("groups", "M2M", "Group")
	groups.Annotations = ann(t, graphql.FieldName("memberships"))
	profile := edge("profile", "O2O", "Profile")
	categories := edge("categories", "M2M", "Group")
	categories.Annotations = ann(t, graphql.Skip(graphql.SkipMutationCreateInput))
	likes := edge("likes", "O2M", "Like") // to an entity that is not a Node
	likes.Field, likes.FieldOn = "user_id", "Like"
	bf := edge("best_friend", "O2O", "User")
	bf.Field, bf.FieldOn, bf.OwnsKey = "user_best_friend", "User", true // bidirectional
	user.Edges = []*plugin.Edge{posts, groups, profile, categories, likes, bf}

	post := entity("Post", field("author_id", "int64"), field("title", "string"))
	author := edge("author", "M2O", "User")
	author.Required, author.Field, author.FieldOn = true, "author_id", "Post"
	creator := edge("creator", "M2O", "User")
	creator.Immutable = true
	post.Edges = []*plugin.Edge{author, creator}
	post.Annotations = ann(t, graphql.Mutations(graphql.Create(), graphql.Update()))

	like := &plugin.Entity{
		Name: "Like", Tenant: -1, PrimaryKey: []int{0, 1},
		Fields: []*plugin.Field{field("user_id", "int64"), field("day", "string")},
	}
	lu := edge("user", "M2O", "User")
	lu.Required, lu.Field, lu.FieldOn = true, "user_id", "Like"
	like.Edges = []*plugin.Edge{lu}
	like.Annotations = ann(t, graphql.Mutations(graphql.Create()))

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
			"postIDs: [ID!]", "membershipIDs: [ID!]", "profileID: ID", "bestFriendID: ID",
		}},
		{"UpdateUserInput", []string{
			"name: String", "nickname: String", "clearNickname: Boolean", "role: UserRole", "updatedAt: Time",
			"secret: String", "views: Int", "hidden: String",
			// Post.author_id is required: posts can be added, not removed.
			"addPostIDs: [ID!]",
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
		log.Annotations = ann(t, graphql.Mutations(graphql.Create(), graphql.Update(), graphql.Delete()))
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
		u.Annotations = ann(t, graphql.Mutations(graphql.Create()))
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
		by := &plugin.Field{Name: "edited_by", GoName: "EditedBy", Type: "string", Nullable: true, Nillable: true, Optional: true, UpdateDefault: true}
		by.Annotations = ann(t, graphql.CreateInput(true))
		u := entity("User", by)
		u.Annotations = ann(t, graphql.Mutations(graphql.Create()))
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
				u.Annotations = ann(t, graphql.Mutations(graphql.Create(), graphql.Update()))
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
		author := edge("author", "M2O", "User")
		author.Field, author.FieldOn, author.Inverse, author.Ref = "author_id", "Post", true, "posts"
		author.Required, author.Immutable = !aid.Nullable, aid.Immutable
		post.Edges = []*plugin.Edge{author}
		user := entity("User", field("name", "string"))
		posts := edge("posts", "O2M", "Post")
		posts.Field, posts.FieldOn = "author_id", "Post"
		user.Edges = []*plugin.Edge{posts}
		for _, e := range []*plugin.Entity{post, user} {
			e.Annotations = ann(t, graphql.Mutations(graphql.Create(), graphql.Update()))
		}
		return model(post, user)
	}
	for _, c := range []struct {
		name                   string
		flags                  func(f *plugin.Field)
		userCreate, userUpdate []string
		postCreate, postUpdate []string
	}{
		{"nullable", func(f *plugin.Field) { f.Nullable, f.Optional, f.Nillable = true, true, true },
			[]string{"name: String!", "postIDs: [ID!]"},
			[]string{"name: String", "addPostIDs: [ID!]", "removePostIDs: [ID!]", "clearPosts: Boolean"},
			[]string{"title: String!", "authorID: ID"},
			[]string{"title: String", "authorID: ID", "clearAuthor: Boolean"}},
		{"required", func(f *plugin.Field) {},
			[]string{"name: String!", "postIDs: [ID!]"},
			[]string{"name: String", "addPostIDs: [ID!]"},
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
		owner := edge("user", "O2O", "User")
		owner.Field, owner.FieldOn, owner.Inverse, owner.Ref, owner.OwnsKey = "user_id", "Profile", true, "profile", true
		owner.Required, owner.Immutable = !uid.Nullable, uid.Immutable
		profile.Edges = []*plugin.Edge{owner}
		user := entity("User", field("name", "string"))
		pe := edge("profile", "O2O", "Profile")
		pe.Field, pe.FieldOn = "user_id", "Profile"
		pe.Required = required
		user.Edges = []*plugin.Edge{pe}
		user.Annotations = ann(t, graphql.Mutations(graphql.Create(), graphql.Update()))
		return model(profile, user)
	}
	for _, c := range []struct {
		name           string
		flags          func(f *plugin.Field)
		create, update []string
	}{
		{"O2O nullable", func(f *plugin.Field) { f.Nullable, f.Optional, f.Nillable = true, true, true },
			[]string{"name: String!", "profileID: ID"}, []string{"name: String", "profileID: ID", "clearProfile: Boolean"}},
		{"O2O required", func(f *plugin.Field) {},
			[]string{"name: String!", "profileID: ID"}, []string{"name: String", "profileID: ID"}},
		{"O2O immutable", func(f *plugin.Field) { f.Immutable = true },
			[]string{"name: String!"}, []string{"name: String"}},
		// A required edge whose key is on the target's row: create must give
		// the target, and update may move it but not clear it, whatever the
		// key's nullability.
		{"O2O required edge, nullable key", func(f *plugin.Field) {
			f.Nullable, f.Optional, f.Nillable = true, true, true
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
}

// An Optional field is optional on create, and a Nillable one too; only the
// Nillable one, whose column holds NULL, can be cleared on update.
func TestInputsOptionalAndNillable(t *testing.T) {
	motto := &plugin.Field{Name: "motto", GoName: "Motto", Type: "string", Optional: true}
	bio := &plugin.Field{Name: "bio", GoName: "Bio", Type: "string", Nullable: true, Nillable: true, Pointer: true}
	u := entity("User", field("name", "string"), motto, bio)
	u.Annotations = ann(t, graphql.Mutations(graphql.Create(), graphql.Update()))
	s, sdl := load(t, model(u))
	for typ, want := range map[string][]string{
		"CreateUserInput": {"name: String!", "motto: String", "bio: String"},
		"UpdateUserInput": {"name: String", "motto: String", "bio: String", "clearBio: Boolean"},
	} {
		if got := fieldsOf(s, typ); !slices.Equal(got, want) {
			t.Errorf("%s:\n got %v\nwant %v\n%s", typ, got, want, sdl)
		}
	}
}
