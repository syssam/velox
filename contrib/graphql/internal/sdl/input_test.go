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
	org := field("org_id", "int64")
	secret := &plugin.Field{Name: "secret", GoName: "Secret", Type: "string", Sensitive: true}
	handle := &plugin.Field{Name: "handle", GoName: "Handle", Type: "string", Immutable: true}
	views := field("views", "int")
	views.HasDefault = true
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
	user := entity("User", name, nickname, role, created, updated, editedBy, org, secret, handle, views, notes, hidden, createOnly, skipped, bestFriend)
	user.Tenant = 7
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
	bf.Field, bf.FieldOn = "user_best_friend", "User"
	user.Edges = []*plugin.Edge{posts, groups, profile, categories, likes, bf}

	post := entity("Post", field("author_id", "int64"), field("title", "string"))
	author := edge("author", "M2O", "User")
	author.Required, author.Field, author.FieldOn = true, "author_id", "Post"
	creator := edge("creator", "M2O", "User")
	creator.Immutable = true
	post.Edges = []*plugin.Edge{author, creator}
	post.Annotations = ann(t, graphql.Mutations(graphql.Create()))

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
			"secret: String!", "handle: String!", "notes: String", "hidden: String!", "createOnly: String!",
			"postIDs: [ID!]", "membershipIDs: [ID!]", "profileID: ID", "bestFriendID: ID",
		}},
		{"UpdateUserInput", []string{
			"name: String", "nickname: String", "clearNickname: Boolean", "role: UserRole", "updatedAt: Time",
			"secret: String", "views: Int", "hidden: String",
			"addPostIDs: [ID!]", "removePostIDs: [ID!]", "clearPosts: Boolean",
			"addMembershipIDs: [ID!]", "removeMembershipIDs: [ID!]", "clearMemberships: Boolean",
			"profileID: ID", "clearProfile: Boolean",
			"addCategoryIDs: [ID!]", "removeCategoryIDs: [ID!]", "clearCategories: Boolean",
			"bestFriendID: ID", "clearBestFriend: Boolean",
		}},
		// author_id is the author edge's: authorID, required on create; the
		// immutable creator edge is set on create only.
		{"CreatePostInput", []string{"title: String!", "authorID: ID!", "creatorID: ID"}},
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
		"createUser(input: CreateUserInput!): User!",
		"updateUser(id: ID!, input: UpdateUserInput!): User!",
		"deleteUser(id: ID!): ID!",
	}
	if !slices.Equal(got, want) {
		t.Errorf("Mutation:\n got %v\nwant %v", got, want)
	}
	for _, absent := range []string{"UpdatePostInput", "UpdateLikeInput", "CreateGroupInput"} {
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
		by := &plugin.Field{Name: "edited_by", GoName: "EditedBy", Type: "string", UpdateDefault: true}
		by.Annotations = ann(t, graphql.CreateInput(true))
		u := entity("User", by)
		u.Annotations = ann(t, graphql.Mutations(graphql.Create()))
		s, _ := load(t, model(u))
		if got := fieldsOf(s, "CreateUserInput"); !slices.Equal(got, []string{"editedBy: String"}) {
			t.Errorf("CreateUserInput: %v", got)
		}
	})
}
