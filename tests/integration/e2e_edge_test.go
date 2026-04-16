package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/tag"
	"github.com/syssam/velox/tests/integration/user"
)

// TestEdgeLoading_O2M_WithPosts verifies O2M eager loading.
func TestEdgeLoading_O2M_WithPosts(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")
	createPost(t, client, alice, "Post1", "Content1")
	createPost(t, client, alice, "Post2", "Content2")

	got, err := client.User.Query().
		Where(user.NameField.EQ("Alice")).
		WithPosts().
		Only(ctx)
	require.NoError(t, err)
	assert.Len(t, got.Edges.Posts, 2)
}

// TestEdgeLoading_M2O_WithAuthor verifies M2O eager loading.
func TestEdgeLoading_M2O_WithAuthor(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	bob := createUser(t, client, "Bob", "bob@example.com")
	createPost(t, client, bob, "HisPost", "Content")

	posts, err := client.Post.Query().
		Where(post.TitleField.EQ("HisPost")).
		WithAuthor().
		All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 1)
	require.NotNil(t, posts[0].Edges.Author)
	assert.Equal(t, "Bob", posts[0].Edges.Author.Name)
}

// TestEdgeLoading_Nested verifies nested WithPosts + WithComments.
func TestEdgeLoading_Nested(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")
	p := createPost(t, client, alice, "P1", "C1")
	createComment(t, client, alice, p, "Great post!")
	createComment(t, client, alice, p, "Agreed!")

	got, err := client.User.Query().
		Where(user.NameField.EQ("Alice")).
		WithPosts(func(pq entity.PostQuerier) {
			pq.WithComments()
		}).
		Only(ctx)
	require.NoError(t, err)
	require.Len(t, got.Edges.Posts, 1)
	assert.Len(t, got.Edges.Posts[0].Edges.Comments, 2)
}

// TestM2M_PostTags verifies M2M edges (post ↔ tag junction).
func TestM2M_PostTags(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")
	tagGo := createTag(t, client, "golang")
	tagOrm := createTag(t, client, "orm")

	p, err := client.Post.Create().
		SetTitle("Go ORM").
		SetContent("A post about ORMs").
		SetStatus(post.StatusPublished).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		AddTagIDs(tagGo.ID, tagOrm.ID).
		Save(ctx)
	require.NoError(t, err)

	got, err := client.Post.Query().
		Where(post.TitleField.EQ("Go ORM")).
		WithTags().
		Only(ctx)
	require.NoError(t, err)
	assert.Len(t, got.Edges.Tags, 2)

	// Reverse direction.
	tags, err := client.Tag.Query().
		Where(tag.NameField.EQ("golang")).
		WithPosts().
		All(ctx)
	require.NoError(t, err)
	require.Len(t, tags, 1)
	assert.Len(t, tags[0].Edges.Posts, 1)
	assert.Equal(t, p.ID, tags[0].Edges.Posts[0].ID)
}

// TestQueryEdge_FromClient verifies client.User.QueryPosts(u) traversal.
func TestQueryEdge_FromClient(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")
	createPost(t, client, alice, "P1", "Content1")
	createPost(t, client, alice, "P2", "Content2")

	posts, err := client.User.QueryPosts(alice).All(ctx)
	require.NoError(t, err)
	assert.Len(t, posts, 2)
}

// TestUpdate_AddEdgeIDs verifies adding edges on update.
func TestUpdate_AddEdgeIDs(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")
	tagA := createTag(t, client, "tagA")
	tagB := createTag(t, client, "tagB")
	p := createPost(t, client, alice, "P1", "C1")

	_, err := client.Post.UpdateOneID(p.ID).
		AddTagIDs(tagA.ID, tagB.ID).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	got, err := client.Post.Query().
		Where(post.IDField.EQ(p.ID)).
		WithTags().
		Only(ctx)
	require.NoError(t, err)
	assert.Len(t, got.Edges.Tags, 2)
}

// TestUpdate_EdgeMutation_UnderHook pins that edge add/remove
// operations performed during an UpdateOne run INSIDE the hook
// chain's scope. A Post hook observing an UpdateOne that also
// calls AddTagIDs must fire exactly once for the whole mutation
// (not twice, not zero, not separately for the edge SQL). The
// Save path wraps sqlSave (which runs the main UPDATE plus the
// M2M join-table writes) in velox.WithHooks, so the hook sees a
// single logical mutation even though the SQL is multiple
// statements under the hood.
func TestUpdate_EdgeMutation_UnderHook(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")
	tagA := createTag(t, client, "edgehookA")
	tagB := createTag(t, client, "edgehookB")
	p := createPost(t, client, alice, "P1", "C1")

	var hookCalls int
	client.Post.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			hookCalls++
			return next.Mutate(ctx, m)
		})
	})

	_, err := client.Post.UpdateOneID(p.ID).
		AddTagIDs(tagA.ID, tagB.ID).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, hookCalls,
		"hook should fire exactly once for the whole UpdateOne+AddTagIDs mutation, got %d", hookCalls)

	// Verify the edge SQL actually ran — 2 tags linked.
	got, err := client.Post.Query().
		Where(post.IDField.EQ(p.ID)).
		WithTags().
		Only(ctx)
	require.NoError(t, err)
	assert.Len(t, got.Edges.Tags, 2)
}

// TestUpdate_RemoveEdgeIDs verifies removing edges on update.
func TestUpdate_RemoveEdgeIDs(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")
	tagA := createTag(t, client, "tagA")
	tagB := createTag(t, client, "tagB")

	p, err := client.Post.Create().
		SetTitle("P1").SetContent("C1").SetStatus(post.StatusPublished).SetViewCount(0).
		SetAuthorID(alice.ID).SetCreatedAt(now).SetUpdatedAt(now).
		AddTagIDs(tagA.ID, tagB.ID).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.Post.UpdateOneID(p.ID).
		RemoveTagIDs(tagA.ID).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	got, err := client.Post.Query().
		Where(post.IDField.EQ(p.ID)).
		WithTags().
		Only(ctx)
	require.NoError(t, err)
	assert.Len(t, got.Edges.Tags, 1)
}
