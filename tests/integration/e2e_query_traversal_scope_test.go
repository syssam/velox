package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/tests/integration/query"
	testschema "github.com/syssam/velox/testschema"
)

// TestQueryTraversal_ScopesTheSourceQuery pins that a query-level edge
// traversal — client.User.Query().QueryPosts() — runs the SOURCE query's
// privacy policy and traversers before building the path. The generated path
// closure called buildQuery without prepareQuery, so a User query that the
// policy denied still returned those users' posts, and a row filter on User
// leaked other users' posts through the edge. Ent calls prepareQuery on the
// parent inside the path closure (builder/query.tmpl).
func TestQueryTraversal_ScopesTheSourceQuery(t *testing.T) {
	ctx := context.Background()
	c := openTestClient(t)
	alice := createUser(t, c, "alice", "alice@x")
	bob := createUser(t, c, "bob", "bob@x")
	createPost(t, c, alice, "alice-post", "c")
	createPost(t, c, bob, "bob-post", "c")

	traverse := func() *query.UserQuery { return c.User.Query().(*query.UserQuery) }

	t.Run("policy_deny", func(t *testing.T) {
		ectx := testschema.EnforceUserPrivacyContext(ctx)
		_, err := c.User.Query().All(ectx)
		require.Error(t, err, "fixture: the policy must deny the source query")

		posts, err := traverse().QueryPosts().All(ectx)
		require.Error(t, err, "a denied source query must deny the traversal, got %d posts", len(posts))
	})

	t.Run("policy_filter", func(t *testing.T) {
		fctx := testschema.FilterUserQueryToNameContext(ctx, "alice")
		users, err := c.User.Query().All(fctx)
		require.NoError(t, err)
		require.Len(t, users, 1, "fixture: the policy must narrow the source query")

		posts, err := traverse().QueryPosts().All(fctx)
		require.NoError(t, err)
		require.Len(t, posts, 1, "bob's post leaked through the edge")
		assert.Equal(t, "alice-post", posts[0].Title)
	})

	t.Run("traverser", func(t *testing.T) {
		seen := countingTraverser(t, c)
		_, err := traverse().QueryPosts().All(ctx)
		require.NoError(t, err)
		assert.Positive(t, seen["User"], "the source query's traversers must run")
		assert.Positive(t, seen["Post"])
	})
}
