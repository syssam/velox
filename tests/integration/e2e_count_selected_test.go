package integration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
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
