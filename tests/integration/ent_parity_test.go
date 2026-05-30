package integration_test

// Ent-parity behavioral tests. Each test pins a contract that was verified
// by comparing velox's generated output against the Ent reference implementation.
// Structural guards live in compiler/gen/sql/wiring_test.go; these tests verify
// the end-to-end runtime behavior of those structures.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"

	_ "modernc.org/sqlite"
)

// TestCreate_M2OEdgePrePopulated verifies that after PostCreate.Save(), the
// returned entity has the M2O owner edge pre-populated via Edges.SetAuthor —
// so callers can navigate to the author without an extra DB round-trip.
//
// Before this fix (P1, 2026-05-06), post.Edges.AuthorOrErr() returned an
// error after create; the edge was only available after a separate query with
// WithAuthor(). Ent's equivalent is `_node.todo_children = &nodes[0]`.
func TestCreate_M2OEdgePrePopulated(t *testing.T) {
	client, err := integration.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))

	author, err := client.User.Create().
		SetName("Alice").
		SetEmail("alice@x.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	p, err := client.Post.Create().
		SetTitle("hello").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(author.ID).
		SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	// The returned post must have the author edge pre-populated.
	got, edgeErr := p.Edges.AuthorOrErr()
	require.NoError(t, edgeErr,
		"Edges.AuthorOrErr() must succeed after Create.Save — "+
			"edge should be pre-populated with the author ID (no re-query needed)")
	assert.Equal(t, author.ID, got.ID,
		"pre-populated author must carry the correct ID")
	// Only the ID is set — the rest of the fields are zero-values because
	// the entity was constructed from the FK value, not loaded from the DB.
	assert.Empty(t, got.Name,
		"pre-populated author has only ID — Name is zero-value until a full query runs")
}

// TestCreate_M2OEdgePrePopulated_BulkCreate verifies the same M2O edge
// pre-population invariant for CreateBulk (the inner mutator chain path).
func TestCreate_M2OEdgePrePopulated_BulkCreate(t *testing.T) {
	client, err := integration.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))

	author, err := client.User.Create().
		SetName("Bob").
		SetEmail("bob@x.com").
		SetAge(25).
		SetRole(user.RoleUser).
		SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	posts, err := client.Post.CreateBulk(
		client.Post.Create().SetTitle("p1").SetStatus(post.StatusDraft).
			SetViewCount(0).SetAuthorID(author.ID).
			SetCreatedAt(now).SetUpdatedAt(now),
		client.Post.Create().SetTitle("p2").SetStatus(post.StatusDraft).
			SetViewCount(0).SetAuthorID(author.ID).
			SetCreatedAt(now).SetUpdatedAt(now),
	).Save(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 2)

	for _, p := range posts {
		got, edgeErr := p.Edges.AuthorOrErr()
		require.NoError(t, edgeErr,
			"Edges.AuthorOrErr() must succeed after CreateBulk.Save for post %q", p.Title)
		assert.Equal(t, author.ID, got.ID,
			"bulk-created post %q must have author ID pre-populated", p.Title)
	}
}
