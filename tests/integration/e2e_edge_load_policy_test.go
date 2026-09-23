package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/runtime"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/query"
	"github.com/syssam/velox/tests/integration/user"
	schema "github.com/syssam/velox/testschema"
)

// seedPostWithTag creates a user with one post carrying one tag.
func seedPostWithTag(t *testing.T, c *integration.Client) {
	t.Helper()
	u := createUser(t, c, "author", "author@eagerpolicy")
	p := createPost(t, c, u, "p", "c")
	tg := createTag(t, c, "t")
	require.NoError(t, c.Post.UpdateOneID(p.ID).AddTagIDs(tg.ID).Exec(context.Background()))
}

// TestAuthz_EagerLoadDenyFailsTheParentQuery pins that a Deny from the
// TARGET's policy on an eager-loaded edge fails the whole parent query, on
// every eager-load path: to-one, many-to-many (whose loader scans its join
// rows itself), named edges, WithEdgeLoad with a per-parent limit, and a
// many-to-many edge nested under another edge. This is Ent's behavior: the
// loader returns the policy error and the parent's All returns it.
func TestAuthz_EagerLoadDenyFailsTheParentQuery(t *testing.T) {
	c := openTestClient(t)
	seedPostWithTag(t, c)
	denyUser := schema.EnforceUserPrivacyContext(context.Background())
	denyTag := schema.DenyTagQueryContext(context.Background())

	for name, run := range map[string]func() error{
		"to-one WithAuthor": func() error {
			_, err := c.Post.Query().WithAuthor().All(denyUser)
			return err
		},
		"to-one WithEdgeLoad": func() error {
			q := c.Post.Query()
			edgeLoader(t, q).WithEdgeLoad(post.EdgeAuthor)
			_, err := q.All(denyUser)
			return err
		},
		"M2M WithTags": func() error {
			_, err := c.Post.Query().WithTags().All(denyTag)
			return err
		},
		"M2M WithEdgeLoad with a limit": func() error {
			q := c.Post.Query()
			edgeLoader(t, q).WithEdgeLoad(post.EdgeTags, runtime.Limit(1))
			_, err := q.All(denyTag)
			return err
		},
		"named M2M": func() error {
			_, err := c.Post.Query().(*query.PostQuery).WithNamedTags("first").All(denyTag)
			return err
		},
		"M2M nested under O2M": func() error {
			_, err := c.User.Query().WithPosts(func(q entity.PostQuerier) { q.WithTags() }).All(denyTag)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := run()
			require.Error(t, err)
			assert.True(t, errors.Is(err, privacy.Deny), "want privacy.Deny, got %v", err)
		})
	}

	t.Run("without the deny marker every path loads", func(t *testing.T) {
		posts, err := c.Post.Query().WithAuthor().WithTags().(*query.PostQuery).WithNamedTags("first").All(context.Background())
		require.NoError(t, err)
		require.Len(t, posts, 1)
		assert.NotNil(t, posts[0].Edges.Author)
		assert.Len(t, posts[0].Edges.Tags, 1)
		named, err := posts[0].NamedTags("first")
		require.NoError(t, err)
		assert.Len(t, named, 1)
	})
}

// TestAuthz_EagerLoadReadsTheLivePolicy pins that an eager load takes the
// target's policy from the same variable the target's client does
// (user.RuntimePolicy), read when the load runs. It used to read a copy the
// registry took at init, so replacing user.RuntimePolicy — what the tenant
// filter tests do — changed direct queries but not eager loads.
//
// Swaps a package global: must not run in parallel.
func TestAuthz_EagerLoadReadsTheLivePolicy(t *testing.T) {
	c := openTestClient(t)
	seedPostWithTag(t, c)
	ctx := context.Background()

	prev := user.RuntimePolicy
	t.Cleanup(func() { user.RuntimePolicy = prev })
	user.RuntimePolicy = privacy.Policy{
		Query: privacy.QueryPolicy{privacy.AlwaysDenyRule()},
	}

	_, err := c.Post.Query().WithAuthor().All(ctx)
	require.Error(t, err, "the eager load must see the replaced policy")
	assert.True(t, errors.Is(err, privacy.Deny), "got %v", err)

	posts, err := c.Post.Query().All(ctx)
	require.NoError(t, err)
	_, err = posts[0].QueryAuthor().Only(ctx)
	assert.True(t, errors.Is(err, privacy.Deny), "the entity edge query must see it too, got %v", err)
}
