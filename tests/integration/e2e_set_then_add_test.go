package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"
)

// TestMultiDialect_SetThenAddOrAppend pins that Set followed by Add or
// Append on one builder applies both, in order. Set+Add rendered two
// assignments of one column: PostgreSQL rejected the statement ("multiple
// assignments to same column") and SQLite dropped the Set, adding to the
// stored value. Set+Append appended to the stored value and lost the Set.
// (Ent generates the same statements.)
func TestMultiDialect_SetThenAddOrAppend(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		u := createUser(t, c, "a", "a@sta") // age 30

		got, err := c.User.UpdateOne(u).SetAge(5).AddAge(1).Save(ctx)
		require.NoError(t, err)
		require.Equal(t, 6, got.Age, "SetAge(5).AddAge(1)")

		got, err = c.User.UpdateOne(u).AddAge(1).SetAge(5).Save(ctx)
		require.NoError(t, err)
		require.Equal(t, 5, got.Age, "a later Set overrides an Add")

		n, err := c.User.Update().Where(user.IDField.EQ(u.ID)).SetAge(10).AddAge(2).Save(ctx)
		require.NoError(t, err)
		require.Equal(t, 1, n)
		got, err = c.User.Get(ctx, u.ID)
		require.NoError(t, err)
		require.Equal(t, 12, got.Age, "bulk Update: SetAge(10).AddAge(2)")

		p := createPost(t, c, u, "t", "c")
		gp, err := c.Post.UpdateOne(p).SetLabels([]string{"a"}).AppendLabels([]string{"b"}).Save(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a", "b"}, gp.Labels, "SetLabels then AppendLabels")

		gp, err = c.Post.UpdateOne(p).AppendLabels([]string{"c"}).Save(ctx)
		require.NoError(t, err)
		require.Equal(t, []string{"a", "b", "c"}, gp.Labels, "an append alone still appends to the stored value")

		_, err = c.Post.Update().Where(post.IDField.EQ(p.ID)).SetLabels([]string{"x"}).AppendLabels([]string{"y"}).Save(ctx)
		require.NoError(t, err)
		gp, err = c.Post.Get(ctx, p.ID)
		require.NoError(t, err)
		require.Equal(t, []string{"x", "y"}, gp.Labels, "bulk Update: SetLabels then AppendLabels")
	})
}
