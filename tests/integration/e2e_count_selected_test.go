package integration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/query"
	"github.com/syssam/velox/tests/integration/user"
)

// TestMultiDialect_CountSelectedFields pins that Count counts what Select
// chose, as in Ent: Select(f).Count() counts non-NULL f and
// Select(f).Unique(true).Count() distinct f. sqlCount cleared the selected
// columns, so both counted every row. Several selected columns are counted
// from a derived table, since COUNT(a, b) is rejected by PostgreSQL and
// SQLite. A connection's totalCount clears the fields CollectFields
// selected before counting, or it would count them.
func TestMultiDialect_CountSelectedFields(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		// Nicknames: two "a", one "b", three NULL.
		nicks := []*string{ptr("a"), ptr("a"), ptr("b"), nil, nil, nil}
		for i, n := range nicks {
			_, err := c.User.Create().SetName(fmt.Sprintf("u%d", i%4)).SetEmail(fmt.Sprintf("u%d@cs", i)).
				SetAge(30).SetRole(user.RoleUser).SetNillableNickname(n).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			require.NoError(t, err)
		}
		count := func(name string, n int, err error, want int) {
			t.Helper()
			require.NoError(t, err, name)
			require.Equal(t, want, n, name)
		}
		n, err := c.User.Query().Count(ctx)
		count("all rows", n, err, 6)
		n, err = c.User.Query().Unique(true).Count(ctx)
		count("unique rows", n, err, 6)
		n, err = c.User.Query().Select(user.FieldNickname).Count(ctx)
		count("non-NULL nicknames", n, err, 3)
		n, err = c.User.Query().Unique(true).Select(user.FieldNickname).Count(ctx)
		count("distinct nicknames", n, err, 2)
		n, err = c.User.Query().Select(user.FieldName, user.FieldAge).Count(ctx)
		count("rows over two columns", n, err, 6)
		n, err = c.User.Query().Unique(true).Select(user.FieldName, user.FieldAge).Count(ctx)
		count("distinct (name, age)", n, err, 4)

		// The query survives its Count: sqlgraph qualified the selected
		// fields in place ("users"."nickname"), and the next Count or All
		// on the same query failed validation.
		sel := c.User.Query().Select(user.FieldNickname)
		for range 2 {
			n, err = sel.Count(ctx)
			count("repeated Count", n, err, 3)
		}
		us, err := sel.All(ctx)
		require.NoError(t, err, "All after Count")
		require.Len(t, us, 6)

		// A field selected on the query, as CollectFields selects them. One
		// NULL-able field: counted, it would give the non-NULL rows (3).
		q := c.User.Query().(*query.UserQuery)
		q.GetCtx().AppendFieldOnce(user.FieldNickname)
		conn, err := q.Paginate(ctx, nil, ptr(2), nil, nil)
		require.NoError(t, err)
		require.Equal(t, 6, conn.TotalCount, "totalCount counts rows, not the collected fields")
		require.Len(t, conn.Edges, 2)
	})
}

// TestMultiDialect_CountSelectedAcrossShapes pins that a Count over
// selected fields gives the same answer whatever else shapes the query:
// a window (Limit/Offset) counted every row while the plain path counted
// non-NULL values, a field selected twice repeated a column name in the
// derived table (MySQL rejects it), and traversals, edge predicates and
// modifiers must count the same way.
func TestMultiDialect_CountSelectedAcrossShapes(t *testing.T) {
	forEachDialect(t, func(t *testing.T, c *integration.Client) {
		ctx := context.Background()
		nicks := []*string{ptr("a"), ptr("a"), ptr("b"), nil, nil, nil}
		var ids []int
		for i, n := range nicks {
			u, err := c.User.Create().SetName(fmt.Sprintf("u%d", i%4)).SetEmail(fmt.Sprintf("u%d@csx", i)).
				SetAge(30).SetRole(user.RoleUser).SetNillableNickname(n).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			require.NoError(t, err)
			ids = append(ids, u.ID)
		}
		for i := range 3 {
			_, err := c.Post.Create().SetTitle(fmt.Sprintf("t%d", i%2)).SetContent("c").
				SetStatus(post.StatusPublished).SetViewCount(0).SetAuthorID(ids[0]).
				SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
			require.NoError(t, err)
		}
		check := func(name string, n int, err error, want int) {
			t.Helper()
			if !(err == nil && n == want) {
				t.Errorf("%s: got %d err=%v, want %d", name, n, err, want)
			}
		}
		n, err := c.User.Query().Select(user.FieldNickname).Count(ctx)
		check("nick", n, err, 3)
		n, err = c.User.Query().Limit(100).Select(user.FieldNickname).Count(ctx)
		check("nick+limit (same rows as nick)", n, err, 3)
		n, err = c.User.Query().Unique(true).Select(user.FieldNickname).Count(ctx)
		check("distinct nick", n, err, 2)
		n, err = c.User.Query().Unique(true).Limit(100).Select(user.FieldNickname).Count(ctx)
		check("distinct nick+limit", n, err, 2)
		n, err = c.User.Query().Select(user.FieldName, user.FieldName).Count(ctx)
		check("dup fields", n, err, 6)
		n, err = c.User.Query().Where(user.IDField.EQ(ids[0])).(*query.UserQuery).QueryPosts().Select(post.FieldTitle, post.FieldContent).Count(ctx)
		check("traversal 2 cols", n, err, 3)
		n, err = c.User.Query().Where(user.IDField.EQ(ids[0])).(*query.UserQuery).QueryPosts().Unique(true).Select(post.FieldTitle).Count(ctx)
		check("traversal distinct", n, err, 2)
		n, err = c.User.Query().Where(user.IDField.EQ(ids[0])).(*query.UserQuery).QueryPosts().Unique(true).Select(post.FieldTitle, post.FieldContent).Count(ctx)
		check("traversal distinct 2", n, err, 2)
		n, err = c.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(ids[0]))).Select(post.FieldTitle, post.FieldContent).Count(ctx)
		check("edge pred 2 cols", n, err, 3)
		n, err = c.User.Query().Modify(func(s *sql.Selector) { s.Where(sql.EQ(s.C(user.FieldName), "u0")) }).
			Select(user.FieldNickname, user.FieldName).Count(ctx)
		check("modify 2 cols", n, err, 2)
		n, err = c.User.Query().Offset(1).Select(user.FieldNickname, user.FieldName).Count(ctx)
		check("offset 2 cols", n, err, 5)
		// Select the ID alone.
		n, err = c.User.Query().Select(user.FieldID).Count(ctx)
		check("id", n, err, 6)
	})
}
