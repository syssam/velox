package integration_test

// Pagination end-to-end tests: covers every page-case combination through the
// full stack (schema → generate → Paginate → SQL → result). All tests use an
// in-memory SQLite database, so no external services are required.
//
// Cases covered:
//   - Forward pagination (first + after cursor), default order
//   - Forward pagination, explicit ASC order field
//   - Forward pagination, explicit DESC order field
//   - Backward pagination (last + before cursor), default order
//   - Backward pagination, explicit ASC order field
//   - Backward pagination, explicit DESC order field
//   - last only (no before cursor): fetches truly last N rows
//   - GQL cursor round-trip: int → int8 type coercion after msgpack encode/decode
//   - Nil orderBy (WithUserOrder(nil)) degrades to DefaultOrder without breaking page 2
//   - Filter + cursor: WhereInput predicate combined with cursor pagination

import (
	"bytes"
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/dialect"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/user"

	_ "modernc.org/sqlite"
)

// roundTripCursor serializes a Cursor through MarshalGQL → UnmarshalGQL, simulating
// the JSON/msgpack transport that happens between a GQL response and the next request.
func roundTripCursor(t *testing.T, c *gqlrelay.Cursor) gqlrelay.Cursor {
	t.Helper()
	var buf bytes.Buffer
	c.MarshalGQL(&buf)
	encoded := buf.String()
	encoded = encoded[1 : len(encoded)-1] // strip surrounding quotes
	var out gqlrelay.Cursor
	require.NoError(t, out.UnmarshalGQL(encoded), "cursor must survive GQL round-trip")
	return out
}

// setupUsers creates n users (IDs 1..n) and returns the client.
func setupUsers(t *testing.T, n int) *integration.Client {
	t.Helper()
	client, err := integration.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })
	ctx := context.Background()
	require.NoError(t, client.Schema.Create(ctx))
	for i := 1; i <= n; i++ {
		_, err := client.User.Create().
			SetName(fmt.Sprintf("User%02d", i)).
			SetEmail(fmt.Sprintf("user%02d@test.com", i)).
			SetAge(20 + i).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}
	return client
}

// ids extracts the node IDs from a connection's edges.
func ids(conn *entity.UserConnection) []int {
	out := make([]int, len(conn.Edges))
	for i, e := range conn.Edges {
		out[i] = e.Node.ID
	}
	return out
}

// userOrderBy constructs a *entity.UserOrder using UnmarshalGQL, since the
// per-field vars (userByName, etc.) are unexported from the entity package.
func userOrderBy(t *testing.T, gqlField string, dir entity.OrderDirection) *entity.UserOrder {
	t.Helper()
	var field entity.UserOrderField
	require.NoError(t, field.UnmarshalGQL(gqlField))
	return &entity.UserOrder{Direction: dir, Field: &field}
}

// commentOrderBy builds a *entity.CommentOrder for the multi-order Comment
// entity (testschema/comment.go carries graphql.MultiOrder()). Comment is an
// edge-connection target (Post.comments, User.comments), so it guards the
// edge-method multi-order path.
func commentOrderBy(t *testing.T, gqlField string, dir entity.OrderDirection) *entity.CommentOrder {
	t.Helper()
	var field entity.CommentOrderField
	require.NoError(t, field.UnmarshalGQL(gqlField))
	return &entity.CommentOrder{Direction: dir, Field: &field}
}

// commentIDs extracts node IDs from a CommentConnection's edges.
func commentIDs(conn *entity.CommentConnection) []int {
	out := make([]int, len(conn.Edges))
	for i, e := range conn.Edges {
		out[i] = e.Node.ID
	}
	return out
}

// withUserOrder wraps the multi-order WithUserOrder([]*UserOrder) (opt, error)
// signature back into a single inline expression for use in Paginate's
// variadic opts. Passing no orders falls back to DefaultUserOrder. User is a
// MultiOrder entity (testschema/user.go), so every Paginate in this file runs
// through the composite gqlrelay.MultiCursorsPredicate path.
func withUserOrder(t *testing.T, orders ...*entity.UserOrder) entity.UserPaginateOption {
	t.Helper()
	opt, err := entity.WithUserOrder(orders)
	require.NoError(t, err)
	return opt
}

// TestPaginate_Forward_DefaultOrder pages forward through 10 users with the
// default (ID ASC) order, verifying IDs, HasNextPage, and non-overlapping pages.
func TestPaginate_Forward_DefaultOrder(t *testing.T) {
	client := setupUsers(t, 10)
	ctx := context.Background()
	first := 5

	page1, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil)
	require.NoError(t, err)
	require.Len(t, page1.Edges, 5, "page 1 must return 5 users")
	assert.True(t, page1.PageInfo.HasNextPage)
	assert.False(t, page1.PageInfo.HasPreviousPage)
	assert.Equal(t, []int{1, 2, 3, 4, 5}, ids(page1))

	after := roundTripCursor(t, page1.PageInfo.EndCursor)
	page2, err := client.User.Query().Paginate(ctx, &after, &first, nil, nil)
	require.NoError(t, err)
	require.Len(t, page2.Edges, 5, "page 2 must return 5 users")
	assert.False(t, page2.PageInfo.HasNextPage, "no more data after page 2")
	assert.True(t, page2.PageInfo.HasPreviousPage)
	assert.Equal(t, []int{6, 7, 8, 9, 10}, ids(page2))
}

// TestPaginate_Forward_NilOrder exercises the common resolver pattern where
// orderBy is nil (WithUserOrder(nil) falls back to DefaultOrder). The bug this
// pins: cursor.Value is nil in DefaultOrder.ToCursor, so page 2 was returning
// 0 rows before the fix (composite NULL comparison in SQL).
func TestPaginate_Forward_NilOrder(t *testing.T) {
	client := setupUsers(t, 10)
	ctx := context.Background()
	first := 5

	page1, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil, withUserOrder(t))
	require.NoError(t, err)
	require.Len(t, page1.Edges, 5)
	assert.Equal(t, []int{1, 2, 3, 4, 5}, ids(page1))

	after := roundTripCursor(t, page1.PageInfo.EndCursor)
	t.Logf("cursor after round-trip: ID=%v(%T) Value=%v(%T)",
		after.ID, after.ID, after.Value, after.Value)

	page2, err := client.User.Query().Paginate(ctx, &after, &first, nil, nil, withUserOrder(t))
	require.NoError(t, err)
	require.Len(t, page2.Edges, 5,
		"page 2 must return 5 users — 0 rows means composite NULL comparison bug is back")
	assert.Equal(t, []int{6, 7, 8, 9, 10}, ids(page2))
}

// TestPaginate_Forward_ExplicitASC pages forward ordered by name ASC.
// Cursor.Value carries the name string; composite (name, id) comparison handles ties.
func TestPaginate_Forward_ExplicitASC(t *testing.T) {
	client := setupUsers(t, 6)
	ctx := context.Background()
	first := 3

	orderBy := userOrderBy(t, "NAME", entity.OrderDirectionAsc)
	page1, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil, withUserOrder(t, orderBy))
	require.NoError(t, err)
	require.Len(t, page1.Edges, 3)
	// Lexicographic ASC: User01 < User02 < User03
	assert.Equal(t, []int{1, 2, 3}, ids(page1))

	after := roundTripCursor(t, page1.PageInfo.EndCursor)
	t.Logf("cursor: ID=%v Value=%v(%T)", after.ID, after.Value, after.Value)

	page2, err := client.User.Query().Paginate(ctx, &after, &first, nil, nil, withUserOrder(t, orderBy))
	require.NoError(t, err)
	require.Len(t, page2.Edges, 3, "page 2 must return 3 users with explicit name-ASC order")
	assert.Equal(t, []int{4, 5, 6}, ids(page2))
}

// TestPaginate_Forward_ExplicitDESC pages forward ordered by name DESC.
func TestPaginate_Forward_ExplicitDESC(t *testing.T) {
	client := setupUsers(t, 6)
	ctx := context.Background()
	first := 3

	orderBy := userOrderBy(t, "NAME", entity.OrderDirectionDesc)
	page1, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil, withUserOrder(t, orderBy))
	require.NoError(t, err)
	require.Len(t, page1.Edges, 3)
	// Lexicographic DESC: User06 > User05 > User04
	assert.Equal(t, []int{6, 5, 4}, ids(page1))

	after := roundTripCursor(t, page1.PageInfo.EndCursor)
	page2, err := client.User.Query().Paginate(ctx, &after, &first, nil, nil, withUserOrder(t, orderBy))
	require.NoError(t, err)
	require.Len(t, page2.Edges, 3, "page 2 DESC must return last 3 users in reverse order")
	assert.Equal(t, []int{3, 2, 1}, ids(page2))
}

// TestPaginate_LastOnly fetches the truly last N rows without a before cursor.
// Before the reverse-order fix, last=3 with ORDER BY id ASC fetched the FIRST 3
// rows (IDs 1,2,3) and returned them reversed, not the actual last 3 (8,9,10).
func TestPaginate_LastOnly(t *testing.T) {
	client := setupUsers(t, 10)
	ctx := context.Background()
	last := 3

	conn, err := client.User.Query().Paginate(ctx, nil, nil, nil, &last)
	require.NoError(t, err)
	require.Len(t, conn.Edges, 3, "last=3 must return exactly 3 users")
	assert.True(t, conn.PageInfo.HasPreviousPage, "rows exist before these 3")
	assert.False(t, conn.PageInfo.HasNextPage)
	// last 3 IDs in user-visible (ascending) order:
	assert.Equal(t, []int{8, 9, 10}, ids(conn))
}

// TestPaginate_Backward_DefaultOrder pages backward using last+before cursors.
// Verifies that page 2 backward returns the correct rows and that HasPreviousPage
// is set correctly when more rows precede the window.
func TestPaginate_Backward_DefaultOrder(t *testing.T) {
	client := setupUsers(t, 10)
	ctx := context.Background()
	last := 3

	// First, grab a cursor from the "end" of the forward list.
	first5 := 5
	fwd, err := client.User.Query().Paginate(ctx, nil, &first5, nil, nil)
	require.NoError(t, err)
	// cursor at ID=5 (end of first forward page)
	startCursor := roundTripCursor(t, fwd.PageInfo.EndCursor)

	// Backward from that cursor: expect IDs 3,4,5 in ascending order.
	conn, err := client.User.Query().Paginate(ctx, nil, nil, &startCursor, &last)
	require.NoError(t, err)
	require.Len(t, conn.Edges, 3, "last=3 before cursor(5) must return 3 users")
	assert.Equal(t, []int{2, 3, 4}, ids(conn), "rows before cursor(5) in ascending display order")
	assert.True(t, conn.PageInfo.HasPreviousPage, "rows exist before these 3")
}

// TestPaginate_Backward_ExplicitASC backward-paginates with an explicit name-ASC order.
func TestPaginate_Backward_ExplicitASC(t *testing.T) {
	client := setupUsers(t, 6)
	ctx := context.Background()

	orderBy := userOrderBy(t, "NAME", entity.OrderDirectionAsc)

	// Forward page to get a cursor.
	first3 := 3
	fwd, err := client.User.Query().Paginate(ctx, nil, &first3, nil, nil, withUserOrder(t, orderBy))
	require.NoError(t, err)
	require.Len(t, fwd.Edges, 3)
	// page 1 ASC: User01, User02, User03 (IDs 1,2,3)

	beforeCursor := roundTripCursor(t, fwd.PageInfo.EndCursor)
	last2 := 2
	// Backward from cursor at User03: expect User01, User02 (IDs 1,2).
	bwd, err := client.User.Query().Paginate(ctx, nil, nil, &beforeCursor, &last2, withUserOrder(t, orderBy))
	require.NoError(t, err)
	require.Len(t, bwd.Edges, 2, "last=2 before cursor(User03) must return 2 users")
	assert.Equal(t, []int{1, 2}, ids(bwd))
}

// TestPaginate_Backward_ExplicitDESC backward-paginates with an explicit name-DESC order.
func TestPaginate_Backward_ExplicitDESC(t *testing.T) {
	client := setupUsers(t, 6)
	ctx := context.Background()

	orderBy := userOrderBy(t, "NAME", entity.OrderDirectionDesc)

	first3 := 3
	fwd, err := client.User.Query().Paginate(ctx, nil, &first3, nil, nil, withUserOrder(t, orderBy))
	require.NoError(t, err)
	require.Len(t, fwd.Edges, 3)
	// page 1 DESC: User06, User05, User04 (IDs 6,5,4)

	beforeCursor := roundTripCursor(t, fwd.PageInfo.EndCursor)
	last2 := 2
	// Backward from cursor at User04: expect User05, User06 (IDs 5,6) in DESC display order.
	bwd, err := client.User.Query().Paginate(ctx, nil, nil, &beforeCursor, &last2, withUserOrder(t, orderBy))
	require.NoError(t, err)
	require.Len(t, bwd.Edges, 2, "last=2 before cursor(User04) DESC must return 2 users")
	assert.Equal(t, []int{6, 5}, ids(bwd))
}

// TestPaginate_MultiField_Backward is the dedicated multi-column guard. It
// orders by [CREATED_AT ASC, NAME ASC]: every seeded user shares created_at
// (now), so the first sort column is fully tied and the composite row-value
// cursor (created_at, name, id) carries the entire ordering. Backward
// pagination (before cursor) must therefore exercise the mirrored direction
// across all three cursor columns — the exact path that returned the wrong
// side of the dataset before the MultiCursorsPredicate before-cursor fix.
func TestPaginate_MultiField_Backward(t *testing.T) {
	client := setupUsers(t, 6) // names User01..User06 (ids 1..6), all created_at=now
	ctx := context.Background()

	order := []*entity.UserOrder{
		userOrderBy(t, "CREATED_AT", entity.OrderDirectionAsc),
		userOrderBy(t, "NAME", entity.OrderDirectionAsc),
	}
	mkOpt := func() entity.UserPaginateOption { return withUserOrder(t, order...) }

	// Forward page 1: created_at tied ⇒ NAME ASC ⇒ User01..User04 (ids 1..4).
	first := 4
	fwd, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil, mkOpt())
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3, 4}, ids(fwd), "multi-field forward page 1")

	// Backward from the page-1 end cursor (User04), last=2: a MIDDLE window
	// with rows on both sides. The two rows immediately before User04 in
	// display order are User02, User03 (ids 2,3) — User01 still precedes them
	// (HasPreviousPage), nothing follows within this window. With the
	// before-cursor bug this returned rows AFTER User04 instead.
	beforeCursor := roundTripCursor(t, fwd.PageInfo.EndCursor)
	last := 2
	bwd, err := client.User.Query().Paginate(ctx, nil, nil, &beforeCursor, &last, mkOpt())
	require.NoError(t, err)
	assert.Equal(t, []int{2, 3}, ids(bwd),
		"multi-field backward must return the rows BEFORE the cursor (composite mirror), not after it")
	assert.True(t, bwd.PageInfo.HasPreviousPage, "User01 precedes the [User02,User03] window")
}

// TestPaginate_EdgeConnection_MultiOrder_Backward exercises the multi-order
// path through an EDGE-connection method (Post.Comments) rather than a
// top-level query. Comment is graphql.MultiOrder() AND an edge-connection
// target, so the generated (*Post).Comments method must accept []*CommentOrder
// and thread it through WithCommentOrder (which returns an error for
// multi-order). Before the generator fix, the entity/ package did not even
// compile for this combination. This pins both that it compiles and that the
// before-cursor mirror is correct when reached via an edge method.
func TestPaginate_EdgeConnection_MultiOrder_Backward(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	author := createUser(t, client, "EdgeAuthor", "edge@mo.com")
	p := createPost(t, client, author, "p", "body")

	// Six comments with distinct created_at ⇒ CREATED_AT ASC = insertion order
	// (comment ids 1..6, since Comment is a fresh table on this client).
	for i := 1; i <= 6; i++ {
		_, err := client.Comment.Create().
			SetContent(fmt.Sprintf("c%02d", i)).
			SetPostID(p.ID).
			SetAuthorID(author.ID).
			SetCreatedAt(now.Add(time.Duration(i) * time.Second)).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	order := []*entity.CommentOrder{commentOrderBy(t, "CREATED_AT", entity.OrderDirectionAsc)}

	// Forward first=4 via the edge method (slice orderBy param).
	first := 4
	fwd, err := p.Comments(ctx, nil, &first, nil, nil, order)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3, 4}, commentIDs(fwd), "edge-connection multi-order forward")

	// Backward before=endCursor(comment 4), last=2 ⇒ comments 2,3 (the mirror).
	before := roundTripCursor(t, fwd.PageInfo.EndCursor)
	last := 2
	bwd, err := p.Comments(ctx, nil, nil, &before, &last, order)
	require.NoError(t, err)
	assert.Equal(t, []int{2, 3}, commentIDs(bwd),
		"edge-connection multi-order backward must return rows BEFORE the cursor")
}

// TestPaginate_TotalCount verifies that TotalCount reflects the full dataset
// size, not the page size, and does not change across pages.
func TestPaginate_TotalCount(t *testing.T) {
	client := setupUsers(t, 10)
	ctx := context.Background()
	first := 3

	page1, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 10, page1.TotalCount, "TotalCount must reflect full dataset")

	after := roundTripCursor(t, page1.PageInfo.EndCursor)
	page2, err := client.User.Query().Paginate(ctx, &after, &first, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 10, page2.TotalCount, "TotalCount must not change between pages")
}

// TestPaginate_NoOverlapBetweenPages verifies that IDs from consecutive pages never overlap.
func TestPaginate_NoOverlapBetweenPages(t *testing.T) {
	client := setupUsers(t, 12)
	ctx := context.Background()
	first := 4

	seen := map[int]bool{}
	var after *gqlrelay.Cursor
	for page := 1; ; page++ {
		conn, err := client.User.Query().Paginate(ctx, after, &first, nil, nil)
		require.NoError(t, err)
		for _, e := range conn.Edges {
			require.False(t, seen[e.Node.ID], "ID %d appeared on page %d AND a previous page", e.Node.ID, page)
			seen[e.Node.ID] = true
		}
		if !conn.PageInfo.HasNextPage {
			break
		}
		c := roundTripCursor(t, conn.PageInfo.EndCursor)
		after = &c
	}
	assert.Len(t, seen, 12, "all 12 users must appear exactly once across all pages")
}
