package integration_test

import (
	"context"
	"strconv"
	"testing"

	"github.com/syssam/velox/dialect"
	integration "github.com/syssam/velox/tests/integration"
	postclient "github.com/syssam/velox/tests/integration/client/post"
	userclient "github.com/syssam/velox/tests/integration/client/user"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"

	"github.com/syssam/velox/runtime"
)

// BenchmarkEdgeLoadPerParentLimit_SQLite compares the two ways an eager
// load applies a per-parent limit — ROW_NUMBER() OVER in SQL ("window")
// and reading every row and trimming in memory ("trim", the MySQL 5.7
// path) — on SQLite, over 100 parents with 100 or 1000 children each, for
// the O2M loader (users → posts) and the M2M loader (posts → tags). "full"
// loads the edge with no limit.
//
// It answers whether SQLite, which is in-process and pays no network cost
// for rows it reads and discards, should trim instead of rank for larger
// limits. Measured 2026-09-23 (M3 Max, -count=6 -benchtime=10x): with 100
// children, trim was 12-34% faster for O2M limits 20-50 and 30-45% for
// M2M, the window 7-12% faster at limit 1; with 1000 children most
// differences were within noise. But trim allocates in proportion to the
// WHOLE edge, not the limit: 9.8 MB vs 0.23 MB at 100 children and limit
// 1, 96 MB vs 1.1 MB at 1000 children and limit 10 (40-400x the
// allocations). A planner sees the limit, never the edge's size, so a
// limit threshold would pick an unbounded-memory path for large edges to
// win a few milliseconds of CPU on small ones. SQLite keeps the window.
func BenchmarkEdgeLoadPerParentLimit_SQLite(b *testing.B) {
	for _, children := range []int{100, 1000} {
		b.Run("children="+strconv.Itoa(children), func(b *testing.B) { benchEdgeLoadPerParentLimit(b, 100, children) })
	}
}

func benchEdgeLoadPerParentLimit(b *testing.B, parents, children int) {
	client := openBenchClient(b)
	defer client.Close()
	ctx := context.Background()

	names := make([]string, parents)
	for i := range names {
		names[i] = "u" + strconv.Itoa(i)
	}
	users, err := client.User.MapCreateBulk(names, func(c *userclient.UserCreate, i int) {
		c.SetName(names[i]).SetEmail(names[i] + "@bench").SetAge(30).
			SetRole(user.RoleUser).SetCreatedAt(now).SetUpdatedAt(now)
	}).Save(ctx)
	if err != nil {
		b.Fatalf("users: %v", err)
	}
	// Round-robin across users so every parent's rows interleave.
	idx := make([]int, parents*children)
	for i := range idx {
		idx[i] = i
	}
	for start := 0; start < len(idx); start += 2000 {
		chunk := idx[start:min(start+2000, len(idx))]
		if _, err := client.Post.MapCreateBulk(chunk, func(c *postclient.PostCreate, i int) {
			c.SetTitle("p" + strconv.Itoa(chunk[i])).SetContent("c").SetStatus(post.StatusPublished).
				SetViewCount(0).SetAuthorID(users[chunk[i]%parents].ID).SetCreatedAt(now).SetUpdatedAt(now)
		}).Save(ctx); err != nil {
			b.Fatalf("posts: %v", err)
		}
	}
	tagIDs := make([]int, children)
	for i := range tagIDs {
		tg, err := client.Tag.Create().SetName("bench-t" + strconv.Itoa(i)).Save(ctx)
		if err != nil {
			b.Fatalf("tag: %v", err)
		}
		tagIDs[i] = tg.ID
	}
	tagged, err := client.Post.Query().Limit(parents).IDs(ctx)
	if err != nil {
		b.Fatalf("post ids: %v", err)
	}
	for _, id := range tagged {
		if err := client.Post.UpdateOneID(id).AddTagIDs(tagIDs...).Exec(ctx); err != nil {
			b.Fatalf("tag post: %v", err)
		}
	}

	drivers := map[string]*integration.Client{
		"window": integration.NewClient(integration.Driver(windowDriver{client.RuntimeConfig().Driver})),
		"trim":   integration.NewClient(integration.Driver(noWindowDriver{client.RuntimeConfig().Driver})),
	}
	run := func(b *testing.B, c *integration.Client, limit int) {
		b.Helper()
		o2m := func() int {
			q := c.User.Query()
			var opts []runtime.LoadOption
			if limit > 0 {
				opts = append(opts, runtime.Limit(limit))
			}
			edgeLoaderB(b, q).WithEdgeLoad(user.EdgePosts, opts...)
			got, err := q.All(ctx)
			if err != nil {
				b.Fatal(err)
			}
			n := 0
			for _, u := range got {
				n += len(u.Edges.Posts)
			}
			return n
		}
		m2m := func() int {
			q := c.Post.Query().Where(post.IDField.In(tagged...))
			var opts []runtime.LoadOption
			if limit > 0 {
				opts = append(opts, runtime.Limit(limit))
			}
			edgeLoaderB(b, q).WithEdgeLoad(post.EdgeTags, opts...)
			got, err := q.All(ctx)
			if err != nil {
				b.Fatal(err)
			}
			return countTags(got)
		}
		want := parents * children
		if limit > 0 {
			want = parents * min(limit, children)
		}
		for name, load := range map[string]func() int{"o2m": o2m, "m2m": m2m} {
			b.Run(name, func(b *testing.B) {
				if got := load(); got != want {
					b.Fatalf("loaded %d rows, want %d", got, want)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					load()
				}
			})
		}
	}
	b.Run("full", func(b *testing.B) { run(b, client, 0) })
	for _, limit := range []int{1, 5, 10, 20, 50, 100} {
		for _, path := range []string{"window", "trim"} {
			b.Run("limit="+strconv.Itoa(limit)+"/"+path, func(b *testing.B) { run(b, drivers[path], limit) })
		}
	}
}

// windowDriver reports a server with window functions whatever it is
// connected to, forcing the window path.
type windowDriver struct{ dialect.Driver }

func (windowDriver) ServerCapabilities(context.Context) (dialect.Capabilities, error) {
	return dialect.VersionCapabilities(dialect.MySQL, "8.0.36"), nil
}

func countTags(posts []*entity.Post) int {
	n := 0
	for _, p := range posts {
		n += len(p.Edges.Tags)
	}
	return n
}

// edgeLoaderB is edgeLoader for benchmarks.
func edgeLoaderB(b *testing.B, q any) runtime.FieldCollectable {
	b.Helper()
	fc, ok := q.(runtime.FieldCollectable)
	if !ok {
		b.Fatal("generated queries implement runtime.FieldCollectable")
	}
	return fc
}
