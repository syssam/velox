package integration_test

import (
	"context"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/tag"
)

// TestMultiDialect_EdgeLoadM2MFollowsInterceptorOrder pins that a many-to-many
// eager load assigns targets in the order the target's interceptor chain
// returns them, as the direct edge query does (and Ent's loader does). A
// loader that always assigned in scan-row order silently ignored an
// interceptor that re-sorts results, so eager and direct loads disagreed.
func TestMultiDialect_EdgeLoadM2MFollowsInterceptorOrder(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		u := createUser(t, client, "inter", "inter@m2m")
		p := createPost(t, client, u, "p", "c")
		var ids []int
		for _, name := range []string{"inter-a", "inter-b", "inter-c", "inter-d"} {
			ids = append(ids, createTag(t, client, name).ID)
		}
		require.NoError(t, client.Post.UpdateOneID(p.ID).AddTagIDs(ids...).Exec(ctx))

		c := integration.NewClient(integration.Driver(client.RuntimeConfig().Driver))
		c.Tag.Intercept(velox.InterceptFunc(func(next velox.Querier) velox.Querier {
			return velox.QuerierFunc(func(ctx context.Context, q velox.Query) (velox.Value, error) {
				v, err := next.Query(ctx, q)
				if err != nil {
					return nil, err
				}
				tags := slices.Clone(v.([]*entity.Tag))
				slices.SortFunc(tags, func(a, b *entity.Tag) int { return b.ID - a.ID })
				return tags, nil
			})
		}))

		pp, err := c.Post.Get(ctx, p.ID)
		require.NoError(t, err)
		direct, err := pp.QueryTags().Order(tag.ByID()).All(ctx)
		require.NoError(t, err)
		eager, err := c.Post.Query().Where(post.IDField.EQ(p.ID)).WithTags(func(q entity.TagQuerier) { q.Order(tag.ByID()) }).Only(ctx)
		require.NoError(t, err)
		want := slices.Clone(ids)
		slices.Reverse(want)
		assert.Equal(t, want, tagIDs(direct), "the interceptor's order reaches the direct query")
		assert.Equal(t, want, tagIDs(eager.Edges.Tags), "and the eager load")
	})
}

func tagIDs(tags []*entity.Tag) []int {
	out := make([]int, len(tags))
	for i, tg := range tags {
		out[i] = tg.ID
	}
	return out
}
