package integration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// TestMultiDialect_CountHonorsLimitAndOffset pins that Count returns the
// number of rows the query would return. The LIMIT/OFFSET were applied to
// the single COUNT row instead: Offset(2).Count() failed with "no rows in
// result set" and Limit(2).Count() reported every row. Ent has the same
// bug; velox counts over the windowed query instead.
func TestMultiDialect_CountHonorsLimitAndOffset(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		for i := range 5 {
			_, err := c.User.Create().SetName(fmt.Sprintf("u%d", i)).SetEmail(fmt.Sprintf("u%d@cw", i)).
				SetAge(30).SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			require.NoError(t, err)
		}
		for _, tc := range []struct {
			name string
			q    func() interface {
				Count(context.Context) (int, error)
			}
			want int
		}{
			{"none", func() interface {
				Count(context.Context) (int, error)
			} {
				return c.User.Query()
			}, 5},
			{"limit", func() interface {
				Count(context.Context) (int, error)
			} {
				return c.User.Query().Limit(2)
			}, 2},
			{"limit_over", func() interface {
				Count(context.Context) (int, error)
			} {
				return c.User.Query().Limit(9)
			}, 5},
			{"offset", func() interface {
				Count(context.Context) (int, error)
			} {
				return c.User.Query().Offset(2)
			}, 3},
			{"offset_past_end", func() interface {
				Count(context.Context) (int, error)
			} {
				return c.User.Query().Offset(7)
			}, 0},
			{"both", func() interface {
				Count(context.Context) (int, error)
			} {
				return c.User.Query().Order(user.ByName()).Offset(1).Limit(3)
			}, 3},
			{"unique_with_limit", func() interface {
				Count(context.Context) (int, error)
			} {
				return c.User.Query().Unique(true).Limit(4)
			}, 4},
		} {
			n, err := tc.q().Count(ctx)
			require.NoError(t, err, tc.name)
			require.Equal(t, tc.want, n, tc.name)
		}
	})
}
