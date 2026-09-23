package integration_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/runtime"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"
)

// queryLog records every statement a client sends.
type queryLog struct {
	mu      sync.Mutex
	queries []string
}

func (l *queryLog) log(_ context.Context, v ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queries = append(l.queries, fmt.Sprint(v...))
}

// windowed reports whether any recorded statement ranked rows with a
// window function.
func (l *queryLog) windowed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, q := range l.queries {
		if strings.Contains(q, "ROW_NUMBER() OVER") {
			return true
		}
	}
	return false
}

// noWindowDriver reports a server without window functions whatever it is
// connected to, forcing the eager loaders' in-memory fallback on every
// dialect — the path a MySQL 5.7 server takes.
type noWindowDriver struct{ dialect.Driver }

func (noWindowDriver) ServerCapabilities(context.Context) (dialect.Capabilities, error) {
	return dialect.VersionCapabilities(dialect.MySQL, "5.7.44"), nil
}

// TestMultiDialect_EdgeLoadWindowOrFallback pins both halves of the
// per-parent eager-load limit: a server with window functions ranks the
// rows in SQL (ROW_NUMBER() OVER), one without reads every row and keeps
// each parent's first n in memory, and the two load the same edges row for
// row. On MySQL 5.7 the automatic path IS the fallback; on the other
// dialects the fallback is forced through a driver that reports no window
// functions, so it runs in every CI leg.
func TestMultiDialect_EdgeLoadWindowOrFallback(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		users := []*entity.User{
			createUser(t, client, "u0", "u0@fallback"),
			createUser(t, client, "u1", "u1@fallback"),
			createUser(t, client, "u2", "u2@fallback"),
		}
		posts := map[int][]int{}
		for round := range 4 {
			for i, u := range users {
				if i == 2 && round > 0 {
					continue // fewer rows than the limit
				}
				p := createPost(t, client, u, fmt.Sprintf("p%d", round), "c")
				posts[u.ID] = append(posts[u.ID], p.ID)
			}
		}
		tags := make([]int, 4)
		for i := range tags {
			tags[i] = createTag(t, client, fmt.Sprintf("fallback-t%d", i)).ID
		}
		p0, p1 := posts[users[0].ID][0], posts[users[1].ID][0]
		require.NoError(t, client.Post.UpdateOneID(p0).AddTagIDs(tags...).Exec(ctx))
		require.NoError(t, client.Post.UpdateOneID(p1).AddTagIDs(tags[3], tags[1]).Exec(ctx))

		// load runs a per-parent-limited O2M load (newest first) and M2M load
		// (lowest tag id first) and returns the loaded ids per parent.
		load := func(t *testing.T, c *integration.Client) (o2m, m2m map[int][]int) {
			t.Helper()
			q := c.User.Query().Where(user.IDField.In(users[0].ID, users[1].ID, users[2].ID))
			edgeLoader(t, q).WithEdgeLoad(user.EdgePosts,
				runtime.Limit(2),
				runtime.OrderBy(sql.OrderByField(post.FieldID, sql.OrderDesc()).ToFunc()),
			)
			got, err := q.All(ctx)
			require.NoError(t, err)
			o2m = map[int][]int{}
			for _, u := range got {
				o2m[u.ID] = postIDs(u.Edges.Posts)
			}
			pq := c.Post.Query().Where(post.IDField.In(p0, p1))
			edgeLoader(t, pq).WithEdgeLoad(post.EdgeTags, runtime.Limit(2))
			gotPosts, err := pq.All(ctx)
			require.NoError(t, err)
			m2m = map[int][]int{}
			for _, p := range gotPosts {
				for _, tg := range p.Edges.Tags {
					m2m[p.ID] = append(m2m[p.ID], tg.ID)
				}
			}
			return o2m, m2m
		}
		wantO2M := map[int][]int{}
		for id, ids := range posts {
			newest := make([]int, 0, 2)
			for i := len(ids) - 1; i >= 0 && len(newest) < 2; i-- {
				newest = append(newest, ids[i])
			}
			wantO2M[id] = newest
		}
		wantM2M := map[int][]int{p0: {tags[0], tags[1]}, p1: {tags[1], tags[3]}}

		base := client.RuntimeConfig().Driver
		t.Run("server capabilities", func(t *testing.T) {
			var ql queryLog
			o2m, m2m := load(t, integration.NewClient(integration.Driver(dialect.DebugWithContext(base, ql.log))))
			assert.Equal(t, supportsWindowFunctions(t, client), ql.windowed(),
				"the loader ranks rows with a window function exactly when the server has them")
			assert.Equal(t, wantO2M, o2m)
			for id, ids := range wantM2M {
				assert.ElementsMatch(t, ids, m2m[id], "post %d", id)
			}
		})
		t.Run("forced fallback", func(t *testing.T) {
			var ql queryLog
			o2m, m2m := load(t, integration.NewClient(integration.Driver(noWindowDriver{dialect.DebugWithContext(base, ql.log)})))
			assert.False(t, ql.windowed(), "a server without window functions must never see ROW_NUMBER() OVER")
			assert.Equal(t, wantO2M, o2m)
			for id, ids := range wantM2M {
				assert.ElementsMatch(t, ids, m2m[id], "post %d", id)
			}
		})
	})
}

// TestMultiDialect_EdgeLoadPartitionLimitRejectsQueryLimit pins that a
// per-parent limit combined with a Limit or Offset on the edge query itself
// is rejected on every server. The window path applied that Limit after
// ranking and the in-memory fallback before it, so the same load returned
// different rows on MySQL 5.7 than on 8.x; an error is the only answer both
// paths can agree on.
func TestMultiDialect_EdgeLoadPartitionLimitRejectsQueryLimit(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		u := createUser(t, client, "lim", "lim@fallback")
		createPost(t, client, u, "p", "c")
		base := client.RuntimeConfig().Driver
		for name, c := range map[string]*integration.Client{
			"server capabilities": client,
			"forced fallback":     integration.NewClient(integration.Driver(noWindowDriver{base})),
		} {
			t.Run(name, func(t *testing.T) {
				for _, limitEdge := range []func(entity.PostQuerier){
					func(pq entity.PostQuerier) { pq.Limit(3) },
					func(pq entity.PostQuerier) { pq.Offset(1) },
				} {
					q := c.User.Query().Where(user.IDField.EQ(u.ID)).WithPosts(limitEdge)
					edgeLoader(t, q).WithEdgeLoad(user.EdgePosts, runtime.Limit(2))
					_, err := q.All(ctx)
					require.Error(t, err)
					assert.Contains(t, err.Error(), "per-parent limit")
				}
			})
		}
	})
}
