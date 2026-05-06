package integration_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
	"github.com/syssam/velox/dialect"

	_ "modernc.org/sqlite"
)

// TestPaginate_CursorRoundTrip reproduces the second-page-empty bug:
// the cursor goes through MarshalGQL → string → UnmarshalGQL, changing the
// ID's Go type from int to int8/int16/etc. If the SQL predicate doesn't
// handle the type change, page 2 returns 0 rows.
func TestPaginate_CursorRoundTrip(t *testing.T) {
	client, err := integration.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	defer client.Close()
	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))

	// Create 10 users.
	for i := 1; i <= 10; i++ {
		_, err := client.User.Create().
			SetName(fmt.Sprintf("User%02d", i)).
			SetEmail(fmt.Sprintf("user%02d@test.com", i)).
			SetAge(20+i).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	first := 5

	// Page 1: no cursor.
	page1, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil)
	require.NoError(t, err)
	require.Len(t, page1.Edges, 5, "page 1 must return exactly 5 users")
	require.True(t, page1.PageInfo.HasNextPage, "more users exist")
	require.NotNil(t, page1.PageInfo.EndCursor)

	// Simulate GQL transport: serialize cursor to string, then deserialize.
	// This is what the client receives and sends back in the next request.
	var buf bytes.Buffer
	page1.PageInfo.EndCursor.MarshalGQL(&buf)
	encoded := buf.String()
	// Strip surrounding quotes added by MarshalGQL.
	encoded = encoded[1 : len(encoded)-1]

	var afterCursor gqlrelay.Cursor
	require.NoError(t, afterCursor.UnmarshalGQL(encoded), "cursor must survive GQL round-trip")
	t.Logf("original ID type=%T value=%v", page1.PageInfo.EndCursor.ID, page1.PageInfo.EndCursor.ID)
	t.Logf("decoded   ID type=%T value=%v", afterCursor.ID, afterCursor.ID)

	// Page 2: with after cursor that went through msgpack round-trip.
	page2, err := client.User.Query().Paginate(ctx, &afterCursor, &first, nil, nil)
	require.NoError(t, err)
	require.Len(t, page2.Edges, 5,
		"page 2 MUST return 5 users — if empty, the cursor ID type mismatch "+
			"after msgpack decode (int → int8) is causing the WHERE predicate to fail")
	require.False(t, page2.PageInfo.HasNextPage, "no more users after page 2")

	// IDs on page 1 and page 2 must not overlap.
	page1IDs := make(map[int]bool)
	for _, e := range page1.Edges {
		page1IDs[e.Node.ID] = true
	}
	for _, e := range page2.Edges {
		require.False(t, page1IDs[e.Node.ID], "page 2 node ID %d appeared on page 1 — cursor predicate is wrong", e.Node.ID)
	}
}
