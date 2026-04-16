package main

import (
	"context"
	"testing"

	"example.com/integration-test/velox"
	_ "example.com/integration-test/velox/query"
	"example.com/integration-test/velox/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql/schema"

	_ "modernc.org/sqlite"
)

func openTestClient(t *testing.T) *velox.Client {
	t.Helper()
	client, err := velox.Open("sqlite", "file:test.db?mode=memory&_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	err = client.Schema.Create(ctx, schema.WithDropIndex(true), schema.WithDropColumn(true))
	require.NoError(t, err)
	return client
}

// TestCreateAndQueryUser tests basic CRUD.
func TestCreateAndQueryUser(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u, err := client.User.Create().
		SetName("Alice").
		SetEmail("alice@example.com").
		SetActive(true).
		Save(ctx)
	require.NoError(t, err)
	require.NotNil(t, u)
	assert.Equal(t, "Alice", u.Name)
	assert.Equal(t, "alice@example.com", u.Email)
	assert.True(t, u.Active)
	assert.NotZero(t, u.ID)

	// Query back
	users, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "Alice", users[0].Name)

	// Get by ID
	found, err := client.User.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, u.ID, found.ID)

	// Count
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestO2MEdge tests One-to-Many: User → Posts.
func TestO2MEdge(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u, err := client.User.Create().SetName("Bob").SetEmail("bob@test.com").Save(ctx)
	require.NoError(t, err)

	_, err = client.Post.Create().SetTitle("Post 1").SetAuthorID(u.ID).Save(ctx)
	require.NoError(t, err)
	_, err = client.Post.Create().SetTitle("Post 2").SetAuthorID(u.ID).Save(ctx)
	require.NoError(t, err)

	// Query posts for user (O2M via client)
	posts, err := client.User.QueryPosts(u).All(ctx)
	require.NoError(t, err)
	assert.Len(t, posts, 2)

	// Eager load
	users, err := client.User.Query().WithPosts().All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	loaded, err := users[0].Edges.PostsOrErr()
	require.NoError(t, err)
	assert.Len(t, loaded, 2)
}

// TestM2OEdge tests Many-to-One: Post → Author.
func TestM2OEdge(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u, err := client.User.Create().SetName("Carol").SetEmail("carol@test.com").Save(ctx)
	require.NoError(t, err)
	p, err := client.Post.Create().SetTitle("My Post").SetAuthorID(u.ID).Save(ctx)
	require.NoError(t, err)

	// Query author for post (M2O)
	author, err := client.Post.QueryAuthor(p).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Carol", author.Name)

	// Eager load
	posts, err := client.Post.Query().WithAuthor().All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 1)
	loaded, err := posts[0].Edges.AuthorOrErr()
	require.NoError(t, err)
	assert.Equal(t, "Carol", loaded.Name)
}

// TestM2MEdge tests Many-to-Many: Post ↔ Tags.
func TestM2MEdge(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u, err := client.User.Create().SetName("Dave").SetEmail("dave@test.com").Save(ctx)
	require.NoError(t, err)

	t1, err := client.Tag.Create().SetName("golang").Save(ctx)
	require.NoError(t, err)
	t2, err := client.Tag.Create().SetName("testing").Save(ctx)
	require.NoError(t, err)

	// Create post with M2M tags
	p, err := client.Post.Create().
		SetTitle("Go Testing").
		SetAuthorID(u.ID).
		AddTagIDs(t1.ID, t2.ID).
		Save(ctx)
	require.NoError(t, err)

	// Query tags for post
	tags, err := client.Post.QueryTags(p).All(ctx)
	require.NoError(t, err)
	assert.Len(t, tags, 2)

	// Query posts for tag (M2M inverse)
	posts, err := client.Tag.QueryPosts(t1).All(ctx)
	require.NoError(t, err)
	assert.Len(t, posts, 1)
	assert.Equal(t, "Go Testing", posts[0].Title)

	// Eager load M2M
	allPosts, err := client.Post.Query().WithTags().All(ctx)
	require.NoError(t, err)
	require.Len(t, allPosts, 1)
	loadedTags, err := allPosts[0].Edges.TagsOrErr()
	require.NoError(t, err)
	assert.Len(t, loadedTags, 2)
}

// TestO2OEdge tests One-to-One: User → Profile.
func TestO2OEdge(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u, err := client.User.Create().SetName("Eve").SetEmail("eve@test.com").Save(ctx)
	require.NoError(t, err)

	bio := "Hello world"
	_, err = client.Profile.Create().SetBio(bio).SetUserID(u.ID).Save(ctx)
	require.NoError(t, err)

	// Query profile (O2O)
	prof, err := client.User.QueryProfile(u).Only(ctx)
	require.NoError(t, err)
	require.NotNil(t, prof.Bio)
	assert.Equal(t, "Hello world", *prof.Bio)

	// Eager load O2O
	users, err := client.User.Query().WithProfile().All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	loadedProf, err := users[0].Edges.ProfileOrErr()
	require.NoError(t, err)
	require.NotNil(t, loadedProf)
	assert.Equal(t, "Hello world", *loadedProf.Bio)
}

// TestUpdateAndDelete tests update and delete operations.
func TestUpdateAndDelete(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u, err := client.User.Create().SetName("Frank").SetEmail("frank@test.com").Save(ctx)
	require.NoError(t, err)

	// Update
	u, err = client.User.UpdateOneID(u.ID).SetName("Franklin").Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Franklin", u.Name)

	// Delete
	err = client.User.DeleteOneID(u.ID).Exec(ctx)
	require.NoError(t, err)

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// TestEdgePredicates tests edge predicate filtering.
func TestEdgePredicates(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u1, err := client.User.Create().SetName("WithPosts").SetEmail("wp@test.com").Save(ctx)
	require.NoError(t, err)
	_, err = client.User.Create().SetName("NoPosts").SetEmail("np@test.com").Save(ctx)
	require.NoError(t, err)

	_, err = client.Post.Create().SetTitle("Test").SetAuthorID(u1.ID).Save(ctx)
	require.NoError(t, err)

	// HasPosts predicate
	withPosts, err := client.User.Query().Where(user.HasPosts()).All(ctx)
	require.NoError(t, err)
	assert.Len(t, withPosts, 1)
	assert.Equal(t, "WithPosts", withPosts[0].Name)

	// Not(HasPosts)
	withoutPosts, err := client.User.Query().Where(user.Not(user.HasPosts())).All(ctx)
	require.NoError(t, err)
	assert.Len(t, withoutPosts, 1)
	assert.Equal(t, "NoPosts", withoutPosts[0].Name)
}

// TestCommentEdges tests Comment M2O edges.
func TestCommentEdges(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u, err := client.User.Create().SetName("Commenter").SetEmail("comm@test.com").Save(ctx)
	require.NoError(t, err)
	p, err := client.Post.Create().SetTitle("Commented").SetAuthorID(u.ID).Save(ctx)
	require.NoError(t, err)

	c, err := client.Comment.Create().
		SetBody("Great post!").
		SetAuthorID(u.ID).
		SetPostID(p.ID).
		Save(ctx)
	require.NoError(t, err)

	// M2O: comment → author
	author, err := client.Comment.QueryAuthor(c).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Commenter", author.Name)

	// M2O: comment → post
	post, err := client.Comment.QueryPost(c).Only(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Commented", post.Title)

	// Eager load both
	comments, err := client.Comment.Query().WithAuthor().WithPost().All(ctx)
	require.NoError(t, err)
	require.Len(t, comments, 1)

	loadedAuthor, err := comments[0].Edges.AuthorOrErr()
	require.NoError(t, err)
	assert.Equal(t, "Commenter", loadedAuthor.Name)

	loadedPost, err := comments[0].Edges.PostOrErr()
	require.NoError(t, err)
	assert.Equal(t, "Commented", loadedPost.Title)
}
