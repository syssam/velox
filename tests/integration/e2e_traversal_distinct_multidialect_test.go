package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/query"
	"github.com/syssam/velox/tests/integration/user"
)

// TestMultiDialect_TraversalDistinctWithOrder pins that a query-level
// traversal returns each target once on every dialect, together with the
// orderings and shapes users combine it with. A JOIN-based traversal
// returned the author of two posts twice; a default DISTINCT fixed that but
// made Postgres and MySQL reject ORDER BY on an unselected expression
// (ByPostsCount) — which SQLite accepts, so only a live-DB run shows it.
// sqlgraph.SetNeighbors now uses a semi-join, needing no DISTINCT.
func TestMultiDialect_TraversalDistinctWithOrder(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		mk := func(name string) int {
			u, err := c.User.Create().SetName(name).SetEmail(name + "@td").SetAge(30).
				SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			require.NoError(t, err)
			return u.ID
		}
		alice, bob := mk("alice"), mk("bob")
		for i, a := range []int{alice, alice, bob} {
			_, err := c.Post.Create().SetTitle(string(rune('a' + i))).SetContent("c").
				SetStatus(post.StatusPublished).SetViewCount(i).SetLabels([]string{"x"}).
				SetAuthorID(a).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			require.NoError(t, err)
		}
		authors := func() *query.UserQuery {
			return c.Post.Query().(*query.PostQuery).QueryAuthor().(*query.UserQuery)
		}
		posts := func() *query.PostQuery {
			return c.User.Query().(*query.UserQuery).QueryPosts().(*query.PostQuery)
		}

		got, err := authors().Order(user.ByName()).All(ctx)
		require.NoError(t, err)
		require.Len(t, got, 2, "plain column order")

		got, err = authors().Order(user.ByPostsCount(sql.OrderDesc())).All(ctx)
		require.NoError(t, err, "edge-count order")
		require.Len(t, got, 2)
		require.Equal(t, alice, got[0].ID)

		ps, err := posts().Order(post.ByAuthorField(user.FieldName)).All(ctx)
		require.NoError(t, err, "neighbor-field order")
		require.Len(t, ps, 3)

		ps, err = posts().Order(post.ByViewCount()).All(ctx)
		require.NoError(t, err, "json column under DISTINCT")
		require.Len(t, ps, 3)

		n, err := authors().Count(ctx)
		require.NoError(t, err)
		require.Equal(t, 2, n)

		ids, err := authors().Order(user.ByPostsCount(sql.OrderDesc())).IDs(ctx)
		require.NoError(t, err, "IDs with edge-count order")
		require.Equal(t, []int{alice, bob}, ids)

		only, err := c.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(alice))).(*query.PostQuery).QueryAuthor().Only(ctx)
		require.NoError(t, err, "two posts, one author: Only must succeed")
		require.Equal(t, alice, only.ID)

		// A limited source: MySQL rejects LIMIT directly inside IN (…).
		limited, err := c.Post.Query().Order(post.ByID()).Limit(2).(*query.PostQuery).QueryAuthor().All(ctx)
		require.NoError(t, err, "limited source")
		require.Len(t, limited, 1, "the first two posts are both alice's")

		// Chained traversals: users -> posts -> author.
		back, err := posts().QueryAuthor().(*query.UserQuery).Order(user.ByName()).All(ctx)
		require.NoError(t, err, "chained traversal")
		require.Len(t, back, 2)

		// M2M with a target shared by several sources.
		tag, err := c.Tag.Create().SetName("shared").Save(ctx)
		require.NoError(t, err)
		_, err = c.Post.Update().AddTagIDs(tag.ID).Save(ctx)
		require.NoError(t, err)
		tags, err := posts().QueryTags().All(ctx)
		require.NoError(t, err)
		require.Len(t, tags, 1, "one tag shared by three posts")
	})
}
