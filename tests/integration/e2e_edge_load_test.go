package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/runtime"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/comment"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"
	schema "github.com/syssam/velox/testschema"
)

// edgeLoader is the by-name eager-load entry point every generated query
// implements; the GraphQL field collector drives it.
func edgeLoader(t *testing.T, q any) runtime.FieldCollectable {
	t.Helper()
	fc, ok := q.(runtime.FieldCollectable)
	require.True(t, ok, "generated queries implement runtime.FieldCollectable")
	return fc
}

func postIDs(posts []*entity.Post) []int {
	ids := make([]int, len(posts))
	for i, p := range posts {
		ids[i] = p.ID
	}
	return ids
}

// TestMultiDialect_EdgeLoadLimitPerParent pins that runtime.Limit through
// WithEdgeLoad caps the rows of EACH parent in one query — a plain LIMIT
// would cap the total across parents, which is what made the option unsafe
// to honor before. Posts are created round-robin so every parent's rows
// interleave with the others' and a global cap would starve later parents.
func TestMultiDialect_EdgeLoadLimitPerParent(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		users := []*entity.User{
			createUser(t, client, "u0", "u0@limit"),
			createUser(t, client, "u1", "u1@limit"),
			createUser(t, client, "u2", "u2@limit"),
		}
		want := map[int][]int{}
		for round := range 4 {
			for i, u := range users {
				if i == 2 && round > 0 {
					continue // u2 has a single post: fewer rows than the limit
				}
				p := createPost(t, client, u, "p", "c")
				want[u.ID] = append(want[u.ID], p.ID)
			}
		}

		t.Run("first n by id", func(t *testing.T) {
			q := client.User.Query().Where(user.IDField.In(users[0].ID, users[1].ID, users[2].ID))
			edgeLoader(t, q).WithEdgeLoad(user.EdgePosts, runtime.Limit(2))
			got, err := q.All(ctx)
			require.NoError(t, err)
			require.Len(t, got, 3)
			for _, u := range got {
				exp := want[u.ID]
				if len(exp) > 2 {
					exp = exp[:2]
				}
				assert.Equal(t, exp, postIDs(u.Edges.Posts), "user %d", u.ID)
			}
		})

		t.Run("ranked by the edge order", func(t *testing.T) {
			q := client.User.Query().Where(user.IDField.In(users[0].ID, users[1].ID))
			edgeLoader(t, q).WithEdgeLoad(user.EdgePosts,
				runtime.Limit(2),
				runtime.OrderBy(sql.OrderByField(post.FieldID, sql.OrderDesc()).ToFunc()),
			)
			got, err := q.All(ctx)
			require.NoError(t, err)
			for _, u := range got {
				exp := want[u.ID]
				assert.Equal(t, []int{exp[3], exp[2]}, postIDs(u.Edges.Posts), "user %d keeps its two newest, newest first", u.ID)
			}
		})

		t.Run("projection keeps the parent key", func(t *testing.T) {
			q := client.User.Query().Where(user.IDField.EQ(users[0].ID))
			edgeLoader(t, q).WithEdgeLoad(user.EdgePosts, runtime.Select(post.FieldTitle), runtime.Limit(3))
			got, err := q.All(ctx)
			require.NoError(t, err)
			require.Len(t, got, 1)
			require.Len(t, got[0].Edges.Posts, 3)
			for _, p := range got[0].Edges.Posts {
				assert.Equal(t, "p", p.Title)
				assert.Empty(t, p.Content, "an unselected column must not be read")
			}
		})
	})
}

// TestMultiDialect_EdgeLoadLimitPerParent_M2M is the join-table variant:
// the partition key is the join table's parent column.
func TestMultiDialect_EdgeLoadLimitPerParent_M2M(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		u := createUser(t, client, "tagger", "tagger@limit")
		tags := make([]int, 4)
		for i := range tags {
			tags[i] = createTag(t, client, "t"+string(rune('a'+i))).ID
		}
		p0 := createPost(t, client, u, "p0", "c")
		p1 := createPost(t, client, u, "p1", "c")
		p2 := createPost(t, client, u, "p2", "c")
		require.NoError(t, client.Post.UpdateOneID(p0.ID).AddTagIDs(tags...).Exec(ctx))
		require.NoError(t, client.Post.UpdateOneID(p1.ID).AddTagIDs(tags[1], tags[3]).Exec(ctx))
		require.NoError(t, client.Post.UpdateOneID(p2.ID).AddTagIDs(tags[2]).Exec(ctx))

		q := client.Post.Query().Where(post.IDField.In(p0.ID, p1.ID, p2.ID))
		edgeLoader(t, q).WithEdgeLoad(post.EdgeTags, runtime.Limit(2))
		got, err := q.All(ctx)
		require.NoError(t, err)
		byID := map[int][]int{}
		for _, p := range got {
			for _, tg := range p.Edges.Tags {
				byID[p.ID] = append(byID[p.ID], tg.ID)
			}
		}
		assert.ElementsMatch(t, []int{tags[0], tags[1]}, byID[p0.ID])
		assert.ElementsMatch(t, []int{tags[1], tags[3]}, byID[p1.ID])
		assert.ElementsMatch(t, []int{tags[2]}, byID[p2.ID])
	})
}

// TestMultiDialect_EdgeLoadLimitOrderWithArgs ranks each parent's rows by an
// ORDER BY expression that binds an argument, alongside a WHERE argument on
// the child query. The window's ORDER BY is rendered before the WHERE, so
// the placeholders must be numbered in rendering order — on Postgres a
// misnumbered $N binds the wrong value or fails outright.
func TestMultiDialect_EdgeLoadLimitOrderWithArgs(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		u0 := createUser(t, client, "u0", "u0@orderargs")
		u1 := createUser(t, client, "u1", "u1@orderargs")
		for _, u := range []*entity.User{u0, u1} {
			createPost(t, client, u, "other", "c")
			createPost(t, client, u, "keep", "c")
			createPost(t, client, u, "other", "skip")
		}
		q := client.User.Query()
		posts := edgeLoader(t, q).WithEdgeLoad(user.EdgePosts,
			runtime.Limit(1),
			runtime.OrderBy(func(s *sql.Selector) {
				s.OrderExprFunc(func(b *sql.Builder) {
					b.WriteString("CASE WHEN ").Ident(s.C(post.FieldTitle)).WriteOp(sql.OpEQ).Arg("keep").WriteString(" THEN 0 ELSE 1 END")
				})
			}),
		)
		posts.(entity.PostQuerier).Where(post.ContentField.NEQ("skip"))
		got, err := q.All(ctx)
		require.NoError(t, err)
		require.Len(t, got, 2)
		for _, u := range got {
			require.Len(t, u.Edges.Posts, 1, u.Name)
			assert.Equal(t, "keep", u.Edges.Posts[0].Title, u.Name)
		}
	})
}

// TestMultiDialect_EdgeLoadRerun pins that executing the same query twice
// (and a clone of it) loads the same edges. The loaders used to mutate the
// stored child query: each run appended another LimitPerPartition modifier
// (the second run wrapped the ranked query twice) and another IN over the
// parent keys (a parent that appeared between runs lost its rows).
func TestMultiDialect_EdgeLoadRerun(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		u0 := createUser(t, client, "u0", "u0@rerun")
		for range 3 {
			createPost(t, client, u0, "p", "c")
		}
		q := client.User.Query()
		edgeLoader(t, q).WithEdgeLoad(user.EdgePosts, runtime.Limit(2))
		first, err := q.All(ctx)
		require.NoError(t, err)
		require.Len(t, first, 1)
		assert.Len(t, first[0].Edges.Posts, 2)

		u1 := createUser(t, client, "u1", "u1@rerun")
		for range 3 {
			createPost(t, client, u1, "p", "c")
		}
		check := func(t *testing.T, got []*entity.User) {
			t.Helper()
			require.Len(t, got, 2)
			for _, u := range got {
				assert.Len(t, u.Edges.Posts, 2, "user %s", u.Name)
			}
		}
		t.Run("second run", func(t *testing.T) {
			got, err := q.All(ctx)
			require.NoError(t, err)
			check(t, got)
		})
		t.Run("clone", func(t *testing.T) {
			got, err := q.Clone().All(ctx)
			require.NoError(t, err)
			check(t, got)
		})
	})
}

// TestEdgeLoad_UnlimitedAndToOne pins the other WithEdgeLoad options: no
// limit loads every row, a to-one edge ignores Limit, and the returned
// edge query accepts nested loads.
func TestEdgeLoad_UnlimitedAndToOne(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	u := createUser(t, client, "nested", "nested@x")
	p := createPost(t, client, u, "p", "c")
	createComment(t, client, u, p, "one")
	createComment(t, client, u, p, "two")

	q := client.Post.Query()
	author := edgeLoader(t, q).WithEdgeLoad(post.EdgeAuthor, runtime.Limit(0))
	require.NotNil(t, author)
	comments := edgeLoader(t, q).WithEdgeLoad(post.EdgeComments, runtime.Select(comment.FieldContent))
	require.NotNil(t, comments)
	comments.WithEdgeLoad("author")
	assert.Nil(t, edgeLoader(t, q).WithEdgeLoad("no_such_edge"))

	got, err := q.All(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.NotNil(t, got[0].Edges.Author, "Limit is ignored on a to-one edge")
	require.Len(t, got[0].Edges.Comments, 2)
	for _, c := range got[0].Edges.Comments {
		assert.NotEmpty(t, c.Content)
		require.NotNil(t, c.Edges.Author, "nested load through the returned edge query")
		assert.Equal(t, u.ID, c.Edges.Author.ID)
	}
}

// TestAuthz_EagerLoadEnforcesTargetPolicy pins that an eager-loaded edge
// query carries the TARGET's privacy policy, like the entity-level edge
// query does. It did not: WithAuthor built the child with the parent's
// interceptors only, so testschema's User policy (name filter from ctx)
// hid bob from p.QueryAuthor() but not from WithAuthor(). GraphQL field
// collection turns every nested edge into such an eager load.
func TestAuthz_EagerLoadEnforcesTargetPolicy(t *testing.T) {
	c := openTestClient(t)
	alice := createUser(t, c, "alice", "alice@policy")
	bob := createUser(t, c, "bob", "bob@policy")
	createPost(t, c, alice, "by-alice", "x")
	createPost(t, c, bob, "by-bob", "x")
	scoped := schema.FilterUserQueryToNameContext(context.Background(), "alice")

	authors := func(posts []*entity.Post) map[string]bool {
		out := map[string]bool{}
		for _, p := range posts {
			out[p.Title] = p.Edges.Author != nil
		}
		return out
	}
	want := map[string]bool{"by-alice": true, "by-bob": false}

	t.Run("WithAuthor", func(t *testing.T) {
		posts, err := c.Post.Query().WithAuthor().All(scoped)
		require.NoError(t, err)
		assert.Equal(t, want, authors(posts))
	})
	t.Run("WithEdgeLoad", func(t *testing.T) {
		q := c.Post.Query()
		edgeLoader(t, q).WithEdgeLoad(post.EdgeAuthor)
		posts, err := q.All(scoped)
		require.NoError(t, err)
		assert.Equal(t, want, authors(posts))
	})
	t.Run("entity edge query agrees", func(t *testing.T) {
		posts, err := c.Post.Query().All(scoped)
		require.NoError(t, err)
		for _, p := range posts {
			_, err := p.QueryAuthor().Only(scoped)
			assert.Equal(t, want[p.Title], err == nil, p.Title)
		}
	})
}
