package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/runtime"
	"github.com/syssam/velox/tests/integration/post"
)

// TestCreate_OwnFKEdgeIsNotAStub pins that create leaves an M2O edge
// unloaded. createSpec used to store `&User{ID: id}` in Edges and mark it
// loaded, so the GraphQL resolver — which trusts Edges.AuthorOrErr() —
// answered `createPost { author { name } }` with an empty name, and the
// stub's QueryPosts() panicked on its nil driver. Bulk create shared the
// code path.
func TestCreate_OwnFKEdgeIsNotAStub(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t)
	alice := createUser(t, client, "alice", "alice@example.com")

	single := createPost(t, client, alice, "hello", "world")
	bulk, err := client.Post.CreateBulk(
		client.Post.Create().SetTitle("t").SetContent("c").SetStatus(post.StatusPublished).
			SetViewCount(0).SetCreatedAt(now).SetUpdatedAt(now).SetAuthorID(alice.ID),
	).Save(ctx)
	require.NoError(t, err)

	for _, p := range append(bulk, single) {
		_, err := p.Edges.AuthorOrErr()
		require.True(t, runtime.IsNotLoaded(err), "create must not mark the author edge loaded")

		author, err := p.Author(ctx) // GraphQL resolver
		require.NoError(t, err)
		require.Equal(t, "alice", author.Name)
		require.Equal(t, "alice@example.com", author.Email)
	}
}

// TestSetUniqueEdgeIDTwice_LastWins pins that SetXxxID on a unique edge
// replaces the stored ID. It used to add to a map, so the second call — a
// hook overriding the caller's owner, say — won only when map iteration
// happened to put it first (4 of 30 runs on create, 2 of 30 on update).
func TestSetUniqueEdgeIDTwice_LastWins(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t)
	u1 := createUser(t, client, "u1", "u1@example.com")
	u2 := createUser(t, client, "u2", "u2@example.com")
	u3 := createUser(t, client, "u3", "u3@example.com")

	for range 20 {
		b := client.Post.Create().SetTitle("t").SetContent("c").SetStatus(post.StatusPublished).
			SetViewCount(0).SetCreatedAt(now).SetUpdatedAt(now).
			SetAuthorID(u1.ID).SetAuthorID(u2.ID)
		require.Equal(t, []int{u2.ID}, b.Mutation().AuthorIDs())
		p, err := b.Save(ctx)
		require.NoError(t, err)
		got, err := p.QueryAuthor().Only(ctx)
		require.NoError(t, err)
		require.Equal(t, u2.ID, got.ID, "create")

		_, err = client.Post.UpdateOneID(p.ID).SetAuthorID(u1.ID).SetAuthorID(u3.ID).Save(ctx)
		require.NoError(t, err)
		reread, err := client.Post.Get(ctx, p.ID)
		require.NoError(t, err)
		got, err = reread.QueryAuthor().Only(ctx)
		require.NoError(t, err)
		require.Equal(t, u3.ID, got.ID, "update")
	}
}
