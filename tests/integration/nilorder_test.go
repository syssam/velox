package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/dialect"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/user"

	_ "modernc.org/sqlite"
)

// TestPaginate_NilOrderBy_SecondPage is the minimal reproducer for the
// "second page empty" bug. When the caller passes orderBy=nil,
// WithUserOrder(nil) falls back to DefaultUserOrder which sets
// cfg.Order.Field.Column="id". That makes CursorsPredicate use the composite
// path (id,id) > (nil, cursor_id), but after.Value is nil because
// DefaultUserOrder.ToCursor only stores ID. The resulting SQL is
// WHERE ("id","id") > (NULL, 50) which SQL evaluates to NULL → 0 rows.
func TestPaginate_NilOrderBy_SecondPage(t *testing.T) {
	client, err := integration.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer client.Close()
	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))

	for i := 1; i <= 10; i++ {
		_, err := client.User.Create().
			SetName(fmt.Sprintf("U%02d", i)).
			SetEmail(fmt.Sprintf("u%02d@x.com", i)).
			SetAge(20 + i).SetRole(user.RoleUser).
			SetCreatedAt(now).SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	first := 5

	// Page 1 with nil orderBy — exactly what the gqlgen resolver does.
	page1, err := client.User.Query().Paginate(
		ctx, nil, &first, nil, nil,
		entity.WithUserOrder(nil), // orderBy=nil, falls back to DefaultUserOrder
	)
	require.NoError(t, err)
	require.Len(t, page1.Edges, 5, "page 1 should return 5 users")
	require.True(t, page1.PageInfo.HasNextPage)

	// Simulate the GQL round-trip (cursor is serialized, then deserialized).
	var buf bytes.Buffer
	page1.PageInfo.EndCursor.MarshalGQL(&buf)
	encoded := buf.String()[1 : buf.Len()-1]
	var afterCursor gqlrelay.Cursor
	require.NoError(t, afterCursor.UnmarshalGQL(encoded))
	t.Logf("cursor: ID=%v(%T) Value=%v(%T)", afterCursor.ID, afterCursor.ID, afterCursor.Value, afterCursor.Value)

	// Page 2 — this is what the gqlgen resolver does on the next request.
	page2, err := client.User.Query().Paginate(
		ctx, &afterCursor, &first, nil, nil,
		entity.WithUserOrder(nil), // still nil orderBy
	)
	require.NoError(t, err)
	require.Len(t, page2.Edges, 5,
		"page 2 MUST return 5 users — if 0, the bug is confirmed: "+
			"CompositeGT([\"id\",\"id\"], nil, cursor_id) produces WHERE (id,id) > (NULL, 5) "+
			"which SQL evaluates to NULL/false, returning 0 rows")
}
