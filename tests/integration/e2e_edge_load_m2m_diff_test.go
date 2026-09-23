package integration_test

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/runtime"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/tag"
	schema "github.com/syssam/velox/testschema"
)

// TestMultiDialect_EdgeLoadM2MDifferential pins the many-to-many eager
// loader (runtime.M2MLoad) against the entity-level edge query: for every
// parent, the tags a WithEdgeLoad("tags", ...) loads must be exactly what
// p.QueryTags() with the same options returns — same rows, same order when
// an order is asked for, same projected columns, same nested edges, same
// policy narrowing. It runs on every dialect and, on each, through both the
// server's own capabilities and a driver that reports no window functions,
// so the window and the in-memory per-parent limit are both compared.
//
// The data shares tags between posts (the loader must hand one scanned tag
// to every parent it belongs to) and gives the posts different numbers of
// tags, some fewer than the limit.
func TestMultiDialect_EdgeLoadM2MDifferential(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		u := createUser(t, client, "differ", "differ@m2m")
		// Names interleave prefixes and do not sort like the IDs, so an
		// order by name and the ID tiebreak rank differently.
		names := []string{"b-3", "a-5", "b-1", "a-2", "a-9", "b-7", "a-0"}
		tags := make([]int, len(names))
		for i, n := range names {
			tags[i] = createTag(t, client, "diff-"+n).ID
		}
		membership := [][]int{
			{0, 1, 2, 3, 4, 5, 6},
			{1, 3, 5},
			{6, 0},
			{2},
			{},
			{4, 3, 1, 6, 5},
		}
		var posts []int
		for i, m := range membership {
			p := createPost(t, client, u, fmt.Sprintf("dp%d", i), "c")
			posts = append(posts, p.ID)
			ids := make([]int, len(m))
			for j, k := range m {
				ids[j] = tags[k]
			}
			if len(ids) > 0 {
				require.NoError(t, client.Post.UpdateOneID(p.ID).AddTagIDs(ids...).Exec(ctx))
			}
		}

		byName := func(s *sql.Selector) { s.OrderBy(sql.Desc(s.C(tag.FieldName))) }
		byID := func(s *sql.Selector) { s.OrderBy(s.C(tag.FieldID)) }
		type option struct {
			ctx   func(context.Context) context.Context
			load  []runtime.LoadOption
			query func(entity.TagQuerier) ([]*entity.Tag, error)
			// ordered compares the rows in order; otherwise as sets.
			ordered bool
		}
		all := func(ctx context.Context) func(entity.TagQuerier) ([]*entity.Tag, error) {
			return func(q entity.TagQuerier) ([]*entity.Tag, error) { return q.All(ctx) }
		}
		options := map[string]func(ctx context.Context) option{
			"plain": func(ctx context.Context) option {
				return option{query: all(ctx)}
			},
			"limit": func(ctx context.Context) option {
				return option{
					load:    []runtime.LoadOption{runtime.Limit(2)},
					query:   func(q entity.TagQuerier) ([]*entity.Tag, error) { return q.Order(byID).Limit(2).All(ctx) },
					ordered: true,
				}
			},
			"order": func(ctx context.Context) option {
				return option{
					load:    []runtime.LoadOption{runtime.OrderBy(byName)},
					query:   func(q entity.TagQuerier) ([]*entity.Tag, error) { return q.Order(byName).All(ctx) },
					ordered: true,
				}
			},
			"order and limit": func(ctx context.Context) option {
				return option{
					load:    []runtime.LoadOption{runtime.OrderBy(byName), runtime.Limit(3)},
					query:   func(q entity.TagQuerier) ([]*entity.Tag, error) { return q.Order(byName).Limit(3).All(ctx) },
					ordered: true,
				}
			},
			"projection": func(ctx context.Context) option {
				return option{
					load:  []runtime.LoadOption{runtime.Select(tag.FieldID)},
					query: func(q entity.TagQuerier) ([]*entity.Tag, error) { return q.Select(tag.FieldID).All(ctx) },
				}
			},
			"nested": func(ctx context.Context) option {
				return option{
					load: []runtime.LoadOption{runtime.WithEdge(tag.EdgePosts)},
					query: func(q entity.TagQuerier) ([]*entity.Tag, error) {
						return q.WithPosts().All(ctx)
					},
				}
			},
			"policy filter": func(ctx context.Context) option {
				return option{
					ctx:   func(ctx context.Context) context.Context { return schema.FilterTagQueryToPrefixContext(ctx, "diff-a") },
					query: all(ctx),
				}
			},
			"policy filter, order and limit": func(ctx context.Context) option {
				return option{
					ctx:     func(ctx context.Context) context.Context { return schema.FilterTagQueryToPrefixContext(ctx, "diff-a") },
					load:    []runtime.LoadOption{runtime.OrderBy(byName), runtime.Limit(2)},
					query:   func(q entity.TagQuerier) ([]*entity.Tag, error) { return q.Order(byName).Limit(2).All(ctx) },
					ordered: true,
				}
			},
		}

		// render describes loaded tags: ID, name and (when loaded) the IDs
		// of their posts.
		render := func(t *testing.T, tags []*entity.Tag, ordered bool) []string {
			t.Helper()
			out := make([]string, len(tags))
			for i, tg := range tags {
				s := fmt.Sprintf("%d:%q", tg.ID, tg.Name)
				if ps, err := tg.Edges.PostsOrErr(); err == nil {
					ids := postIDs(ps)
					slices.Sort(ids)
					s += fmt.Sprint(ids)
				}
				out[i] = s
			}
			if !ordered {
				slices.Sort(out)
			}
			return out
		}

		base := client.RuntimeConfig().Driver
		drivers := map[string]dialect.Driver{
			"server capabilities": base,
			"forced fallback":     noWindowDriver{base},
		}
		for dname, drv := range drivers {
			c := integration.NewClient(integration.Driver(drv))
			for oname, mk := range options {
				t.Run(dname+"/"+oname, func(t *testing.T) {
					ctx := context.Background()
					if o := mk(ctx); o.ctx != nil {
						ctx = o.ctx(ctx)
					}
					o := mk(ctx)
					q := c.Post.Query().Where(post.IDField.In(posts...))
					edgeLoader(t, q).WithEdgeLoad(post.EdgeTags, o.load...)
					got, err := q.All(ctx)
					require.NoError(t, err)
					require.Len(t, got, len(posts))
					compared := 0
					for _, p := range got {
						want, err := o.query(p.QueryTags())
						require.NoError(t, err)
						loaded, err := p.Edges.TagsOrErr()
						require.NoError(t, err)
						require.Equal(t, render(t, want, o.ordered), render(t, loaded, o.ordered), "post %s", p.Title)
						compared += len(want)
					}
					require.NotZero(t, compared, "the option selects no rows; the comparison proves nothing")
				})
			}
		}
	})
}
