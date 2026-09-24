package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/like"
)

// TestEdgeSchema_ThroughDefaultsAndEdgeTable pins the two edge-schema paths
// through testschema's Like (User.liked_posts / Post.likers Through likes):
//
//   - adding the M2M edge fills the join row's defaults — liked_at is
//     Default(time.Now) and NOT NULL, and every AddLikedPostIDs failed with a
//     NOT NULL error because the join row carried only the two keys;
//   - the generated through edge (User.likes) addresses the join table — its
//     edge spec said M2O, so Add/Clear built SQL against users.user_id.
func TestEdgeSchema_ThroughDefaultsAndEdgeTable(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		a := createUser(t, c, "a", "a@es")
		b := createUser(t, c, "b", "b@es")
		p1 := createPost(t, c, a, "p1", "c")
		p2 := createPost(t, c, a, "p2", "c")
		before := time.Now().Add(-time.Minute)

		_, err := c.User.UpdateOne(b).AddLikedPostIDs(p1.ID).Save(ctx)
		require.NoError(t, err, "update through the M2M edge")
		_, err = c.Post.UpdateOne(p2).AddLikerIDs(b.ID).Save(ctx)
		require.NoError(t, err, "update from the inverse side")
		liker, err := c.User.Create().SetName("c").SetEmail("c@es").SetAge(30).
			SetCreatedAt(now).SetUpdatedAt(now).AddLikedPostIDs(p1.ID).Save(ctx)
		require.NoError(t, err, "create through the M2M edge")

		likes, err := c.Like.Query().All(ctx)
		require.NoError(t, err)
		require.Len(t, likes, 3)
		for _, l := range likes {
			require.True(t, l.LikedAt.After(before), "liked_at default applied: %v", l.LikedAt)
		}

		l, err := c.Like.Query().Where(like.UserIDField.EQ(liker.ID)).Only(ctx)
		require.NoError(t, err)
		_, err = c.User.UpdateOne(b).AddLikeIDs(l.ID).Save(ctx)
		require.Error(t, err, "moving another user's like")
		require.NotContains(t, err.Error(), "no such column", "SQL must target the likes table")
		require.True(t, integration.IsConstraintError(err), "got %v", err)
	})
}
