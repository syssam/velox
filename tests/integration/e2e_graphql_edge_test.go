package integration_test

// GraphQL edge integration tests — exercises the contrib/graphql generator's
// runtime output inside velox's own module, not only via examples/fullgql.
// These tests live here so a `go test ./...` at the repo root catches
// graphql regressions without waiting for the examples matrix CI job.
//
// What's covered:
//  - entity edge method fast path: reloaded.Posts(...) reuses eager-loaded
//    Edges.Posts via entity.BuildPostConnection — no DB round trip.
//  - Slow-path Paginate with WithPostFilter: mimics the hand-written
//    resolver body for edge-with-where (SDL emits @goField(forceResolver:
//    true); the body calls Paginate with WithPostFilter(where.Filter)).
//    Verifies the filter actually reaches SQL and reduces the result.

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/gqlfilter"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"
)

// TestGraphQLEdge_FastPath_UsesEagerLoadedPosts pins the fast-path branch
// in entity/gql_edge_user.go::(*User).Posts — when the parent query has
// eagerly loaded Posts via .WithPosts(), the subsequent call to the edge
// method MUST reuse that slice and skip the SQL query.
//
// Test shape: eager-load edges, then close the DB. If the fast path is
// taken, no DB call happens and the test passes. If the slow path runs,
// Paginate errors on the closed connection and the test fails — giving
// a precise regression signal.
func TestGraphQLEdge_FastPath_UsesEagerLoadedPosts(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice-fp", "alice-fp@example.com")
	p1 := createPost(t, client, alice, "A", "content-a")
	p2 := createPost(t, client, alice, "B", "content-b")
	p3 := createPost(t, client, alice, "C", "content-c")

	// Eager-load posts on the reloaded user so Edges.Posts is populated.
	reloaded, err := client.User.Query().
		Where(user.IDField.EQ(alice.ID)).
		WithPosts().
		Only(ctx)
	require.NoError(t, err)

	// Shut the DB. Any fresh query will now fail. The fast path avoids
	// touching the driver, so it must succeed; the slow path fails here.
	require.NoError(t, client.Close())

	first := 10
	conn, err := reloaded.Posts(ctx, nil, &first, nil, nil, nil)
	require.NoError(t, err,
		"entity edge method MUST use eager-loaded edges — a closed-DB error "+
			"here means the fast path in gen_entity_edge.go::genConnectionEdgeMethod "+
			"regressed and the method is hitting the DB despite WithPosts()")
	require.NotNil(t, conn)
	require.Equal(t, 3, conn.TotalCount, "fast path returned wrong totalCount")
	require.Len(t, conn.Edges, 3, "fast path returned wrong edge count")

	gotIDs := make([]int, 0, len(conn.Edges))
	for _, e := range conn.Edges {
		require.NotNil(t, e.Node)
		gotIDs = append(gotIDs, e.Node.ID)
	}
	assert.ElementsMatch(t, []int{p1.ID, p2.ID, p3.ID}, gotIDs,
		"fast path must return the eager-loaded posts exactly, not a coincidental subset")
}

// TestGraphQLEdge_SlowPath_WithFilter pins the hand-written-resolver
// pattern for edge-connection-with-where. Since the entity method cannot
// accept *gqlfilter.PostWhereInput (entity -> gqlfilter -> post -> entity
// cycle), @goField(forceResolver: true) forces a user-written resolver.
// The canonical body shape is:
//
//	q := r.Client.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(u.ID)))
//	return q.Paginate(ctx, ..., entity.WithPostOrder(orderBy),
//	                            entity.WithPostFilter(where.Filter))
//
// This test drives that exact Go code path directly (no gqlgen) to pin the
// whole chain: PostWhereInput.Filter → WithPostFilter → cfg.Filter → Paginate
// applies to SQL → result set is filtered. If any link drops `where`
// silently, the returned count will equal the unfiltered count and the
// test fails.
func TestGraphQLEdge_SlowPath_WithFilter(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice-sp", "alice-sp@example.com")

	// 3 published, 1 draft. Filter for published must keep 3, drop 1.
	published := []string{"pub-1", "pub-2", "pub-3"}
	for _, title := range published {
		createPost(t, client, alice, title, "c")
	}
	// createPost defaults to StatusPublished; override one to Draft.
	draftPost, err := client.Post.Create().
		SetTitle("draft-1").
		SetContent("c").
		SetStatus(post.StatusDraft).
		SetViewCount(0).
		SetAuthorID(alice.ID).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	require.Equal(t, post.StatusDraft, draftPost.Status)

	// Mimic the hand-written resolver body. No shortcuts — this is
	// literally what schema.resolvers.go::(*userResolver).Posts would do,
	// minus gqlgen's arg-unpacking.
	publishedStatus := post.StatusPublished
	where := &gqlfilter.PostWhereInput{
		Status: &publishedStatus,
	}

	q := client.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(alice.ID)))
	first := 10
	conn, err := q.Paginate(ctx, nil, &first, nil, nil, entity.WithPostFilter(where.Filter))
	require.NoError(t, err, "Paginate with WithPostFilter must succeed")
	require.NotNil(t, conn)

	// Sanity: the unfiltered set would be 4. Filtered must be exactly 3.
	// If this is 4, the filter was silently dropped somewhere between
	// where.Filter → WithPostFilter → cfg.Filter → Paginate.
	require.Equal(t, 3, conn.TotalCount,
		"WithPostFilter(where.Filter) must reduce totalCount from 4 to 3 — "+
			"a totalCount of 4 here means the filter dropped silently, which is "+
			"the exact regression class that @goField(forceResolver: true) + "+
			"the hand-written resolver body are designed to prevent")
	require.Len(t, conn.Edges, 3)

	for _, e := range conn.Edges {
		require.NotNil(t, e.Node)
		assert.Equal(t, post.StatusPublished, e.Node.Status,
			"all returned posts must be Published — any Draft here proves the "+
				"filter didn't reach the SQL WHERE clause")
	}
}

// TestGraphQLEdge_BuildPostConnection_DirectCall pins the shared in-memory
// helper used by BOTH the slow path (query.PostQuery.Paginate delegates
// to it) AND the fast path (edge resolver reuses it with eager-loaded
// nodes). Ensures pure in-memory assembly of cursor+pageInfo is correct
// without involving any query layer. If Paginate ever regresses and
// disagrees with the fast path, THIS test catches the helper itself.
func TestGraphQLEdge_BuildPostConnection_DirectCall(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice-dc", "alice-dc@example.com")
	for _, title := range []string{"a", "b", "c", "d", "e"} {
		createPost(t, client, alice, title, "c")
	}

	posts, err := client.Post.Query().
		Where(post.HasAuthorWith(user.IDField.EQ(alice.ID))).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 5)

	// Caller knows total count (5): pass it explicitly. first=3 trims to
	// 3 edges + HasNextPage=true. TotalCount stays 5 because caller passed
	// it — this is what the slow path does (it runs COUNT(*) beforehand)
	// and what the fast path SHOULD do when totalCount is known from
	// eager-load. Passing 0 is the "totalCount unknown" shorthand and
	// defaults to len(edges), which is typically wrong when first/last
	// trimmed rows — callers in the fast path should prefer the explicit
	// totalCount when available.
	first := 3
	conn := entity.BuildPostConnection(posts, 5, nil, nil, &first, nil, nil)
	require.Equal(t, 5, conn.TotalCount, "explicit totalCount must be preserved")
	assert.True(t, conn.PageInfo.HasNextPage,
		"BuildPostConnection must set HasNextPage when input exceeds first")
	require.Len(t, conn.Edges, 3,
		"first=3 with 5 input nodes must yield exactly 3 edges")
	// StartCursor and EndCursor must point into Edges[0] and Edges[last].
	require.NotNil(t, conn.PageInfo.StartCursor)
	require.NotNil(t, conn.PageInfo.EndCursor)
	assert.Equal(t, conn.Edges[0].Cursor, *conn.PageInfo.StartCursor)
	assert.Equal(t, conn.Edges[2].Cursor, *conn.PageInfo.EndCursor)

	// totalCount=0 fallback: defaults to len(edges) AFTER probe-trim, which
	// is the edge count, not the input count. This matches Ent's build()
	// semantics; callers who want true totalCount must pass it explicitly.
	fallbackConn := entity.BuildPostConnection(posts, 0, nil, nil, &first, nil, nil)
	assert.Equal(t, 3, fallbackConn.TotalCount,
		"totalCount=0 defaults to len(trimmed edges) — matches Ent's build() semantics")

	// Empty input corner case: HasNextPage stays false, Edges is empty,
	// cursors stay nil, totalCount defaults to 0. Matches gqlrelay spec.
	emptyConn := entity.BuildPostConnection(nil, 0, nil, nil, nil, nil, nil)
	assert.Equal(t, 0, emptyConn.TotalCount)
	assert.Empty(t, emptyConn.Edges)
	assert.Nil(t, emptyConn.PageInfo.StartCursor)
	assert.Nil(t, emptyConn.PageInfo.EndCursor)
}

// TestGraphQLEdge_BuildPostConnection_ReversePagination pins `last`-cursor
// semantics in the in-memory helper:
//
//	last=N with len(nodes) > N → probe-trim the first row (not the last),
//	set HasPreviousPage=true, then reverse the remaining slice so the
//	client sees nodes in forward order.
//
// Ent's `CategoryConnection.build` has the exact same behavior; this test
// locks it in so future refactors can't subtly swap trim-direction or
// drop the reverse — both are easy to get wrong and produce visually
// plausible but wrong pagination.
func TestGraphQLEdge_BuildPostConnection_ReversePagination(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice-rev", "alice-rev@example.com")
	// Create in order a, b, c, d, e — default sort by ID means this is
	// the canonical forward order of the returned slice.
	for _, title := range []string{"a", "b", "c", "d", "e"} {
		createPost(t, client, alice, title, "c")
	}
	posts, err := client.Post.Query().
		Where(post.HasAuthorWith(user.IDField.EQ(alice.ID))).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 5)

	// last=2 with 5 nodes: drop first 3, keep [d, e], reverse → [e, d].
	// Reversal is the behavior the gqlrelay cursor protocol expects when
	// the server fetches in ASC order but the client paginated backward.
	last := 2
	conn := entity.BuildPostConnection(posts, 5, nil, nil, nil, nil, &last)
	require.Len(t, conn.Edges, 2,
		"last=2 with 5 input nodes must yield exactly 2 edges")
	assert.True(t, conn.PageInfo.HasPreviousPage,
		"last=N with len > N must set HasPreviousPage=true (there's more data behind the window)")
	assert.False(t, conn.PageInfo.HasNextPage,
		"HasNextPage must remain false in reverse pagination — no probe row at the tail")

	// Titles should appear in REVERSE of input order: input was a,b,c,d,e →
	// last 2 = d,e → reversed = e,d
	require.Equal(t, "e", conn.Edges[0].Node.Title,
		"reverse pagination must emit the last-fetched node first")
	require.Equal(t, "d", conn.Edges[1].Node.Title,
		"reverse pagination ordering regression — the slice was not reversed after trim")
}

// TestGraphQLEdge_BuildPostConnection_CustomOrderCursor pins that
// BuildPostConnection uses order.Field.ToCursor when provided, NOT the
// fallback node.ID cursor. When a caller passes a specific *PostOrder,
// the emitted cursor must reflect that order's cursor-generation function
// (which typically embeds both ID and the order value, so gqlrelay's
// cursor-comparator works). Regression here would silently break
// cursor-based pagination whenever a client sorts by anything other than
// default ID.
func TestGraphQLEdge_BuildPostConnection_CustomOrderCursor(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice-ord", "alice-ord@example.com")
	createPost(t, client, alice, "alpha", "c")
	createPost(t, client, alice, "bravo", "c")

	posts, err := client.Post.Query().
		Where(post.HasAuthorWith(user.IDField.EQ(alice.ID))).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 2)

	// Build a PostOrder that uses the "title" field. The generated
	// order.Field.ToCursor for title embeds both ID and Value — if the
	// helper uses order.Field.ToCursor correctly, we should see non-nil
	// Cursor.Value; if it falls back to the default ID-only cursor,
	// Value would be empty.
	order := &entity.PostOrder{
		Direction: "ASC",
		Field: &entity.PostOrderField{
			Column: "title",
			ToCursor: func(p *entity.Post) gqlrelay.Cursor {
				return gqlrelay.Cursor{ID: p.ID, Value: p.Title}
			},
		},
	}

	conn := entity.BuildPostConnection(posts, 2, order, nil, nil, nil, nil)
	require.Len(t, conn.Edges, 2)

	// First edge's cursor should carry the title as Value — proof that
	// order.Field.ToCursor was invoked (not the default-ID fallback,
	// which would leave Value empty).
	first := conn.Edges[0]
	require.NotNil(t, first.Node)
	assert.Equal(t, first.Node.Title, first.Cursor.Value,
		"BuildPostConnection must use order.Field.ToCursor for cursor "+
			"generation when order is provided — a nil/empty Cursor.Value "+
			"here means it fell through to the default ID cursor")
	// Same for the second edge — defensive against the "only first edge
	// went through ToCursor" class of regression.
	second := conn.Edges[1]
	require.NotNil(t, second.Node)
	assert.Equal(t, second.Node.Title, second.Cursor.Value)
}

// TestGraphQLEdge_FilterWithCursorPagination pins the most common real-world
// pattern: client filters AND paginates through results — `todos(where:
// {status: PUBLISHED}, first: 2, after: cursor)`. Requires BOTH filter AND
// cursor to apply at SQL level.
//
// Filter and cursor handling sit in different layers of Paginate: the filter
// goes through the optional callback in cfg.Filter, the cursor goes through
// CursorsPredicate → q.Where. A regression in either layer independently
// would pass simpler tests and fail here.
func TestGraphQLEdge_FilterWithCursorPagination(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice-fcp", "alice-fcp@example.com")
	// 5 published posts (p-1..p-5) + 3 drafts (d-1..d-3). Filter for
	// published + paginate 2-at-a-time should walk through 3 pages of
	// (2, 2, 1) published posts, never seeing drafts.
	for i := 1; i <= 5; i++ {
		createPost(t, client, alice, fmt.Sprintf("p-%d", i), "c")
	}
	for i := 1; i <= 3; i++ {
		_, err := client.Post.Create().
			SetTitle(fmt.Sprintf("d-%d", i)).
			SetContent("c").
			SetStatus(post.StatusDraft).
			SetViewCount(0).
			SetAuthorID(alice.ID).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	publishedStatus := post.StatusPublished
	where := &gqlfilter.PostWhereInput{Status: &publishedStatus}
	first := 2

	// Page 1: expect 2 published posts, HasNextPage=true.
	q := client.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(alice.ID)))
	page1, err := q.Paginate(ctx, nil, &first, nil, nil, entity.WithPostFilter(where.Filter))
	require.NoError(t, err)
	require.Equal(t, 5, page1.TotalCount, "totalCount must reflect filter: 5 published (not 8 total)")
	require.Len(t, page1.Edges, 2, "first=2 must yield 2 edges on page 1")
	require.True(t, page1.PageInfo.HasNextPage, "3 more published posts remain")
	for _, e := range page1.Edges {
		require.Equal(t, post.StatusPublished, e.Node.Status,
			"filter must keep drafts off every page, not just page 1")
	}
	endCursor := page1.PageInfo.EndCursor
	require.NotNil(t, endCursor)

	// Page 2: use after=endCursor, same filter. Expect 2 more published.
	q2 := client.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(alice.ID)))
	page2, err := q2.Paginate(ctx, endCursor, &first, nil, nil, entity.WithPostFilter(where.Filter))
	require.NoError(t, err)
	require.Len(t, page2.Edges, 2)
	for _, e := range page2.Edges {
		require.Equal(t, post.StatusPublished, e.Node.Status,
			"page 2 must ALSO carry filter — a bug where cursor pagination dropped "+
				"the filter on subsequent pages would pass previous tests and fail here")
	}

	// Page 3: last published post + cursor exhausted.
	page3, err := client.Post.Query().
		Where(post.HasAuthorWith(user.IDField.EQ(alice.ID))).
		Paginate(ctx, page2.PageInfo.EndCursor, &first, nil, nil, entity.WithPostFilter(where.Filter))
	require.NoError(t, err)
	require.Len(t, page3.Edges, 1, "page 3 should have the remaining 1 published post")
	assert.False(t, page3.PageInfo.HasNextPage, "no more pages after page 3")
}

// TestGraphQLEdge_WhereNilSafety_FastPathBranching pins the fast-path
// guard's negative side: when the hand-written resolver follows the
// recipe `if where == nil && after == nil && before == nil { fast path
// } else { slow path }`, a non-nil `where` MUST take the slow path even
// when the edge is already eager-loaded. Skipping the slow path would
// return UNFILTERED eager-loaded nodes as if they matched the filter —
// silent data-correctness bug / potential tenant-isolation leak.
//
// Simulates the user-resolver body (schema.resolvers.go) as a local
// function so the branch decision is visible and tested, not hidden
// behind a generator.
func TestGraphQLEdge_WhereNilSafety_FastPathBranching(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice-nil", "alice-nil@example.com")
	createPost(t, client, alice, "pub-a", "c")
	createPost(t, client, alice, "pub-b", "c")
	_, err := client.Post.Create().
		SetTitle("draft-a").SetContent("c").
		SetStatus(post.StatusDraft).SetViewCount(0).
		SetAuthorID(alice.ID).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	reloaded, err := client.User.Query().
		Where(user.IDField.EQ(alice.ID)).
		WithPosts().
		Only(ctx)
	require.NoError(t, err)
	require.Len(t, reloaded.Edges.Posts, 3, "sanity: 3 posts eager-loaded (2 published + 1 draft)")

	// Simulate the hand-written resolver body verbatim. Copy-paste from
	// the recipe in contrib/graphql/doc.go — this is what every user
	// would write for a where-bearing edge resolver.
	userResolverPosts := func(
		where *gqlfilter.PostWhereInput,
		after, before *gqlrelay.Cursor,
		first, last *int,
	) (*entity.PostConnection, error) {
		// Fast-path guard (the thing under test):
		if where == nil && after == nil && before == nil {
			if nodes, err := reloaded.Edges.PostsOrErr(); err == nil {
				return entity.BuildPostConnection(nodes, 0, nil, after, first, before, last), nil
			}
		}
		// Slow path:
		q := client.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(reloaded.ID)))
		return q.Paginate(ctx, after, first, before, last,
			entity.WithPostOrder(nil),
			entity.WithPostFilter(where.Filter))
	}

	// Branch A: where == nil → fast path must fire → 3 posts (including
	// draft) returned in memory.
	first := 10
	connFast, err := userResolverPosts(nil, nil, nil, &first, nil)
	require.NoError(t, err)
	require.Equal(t, 3, connFast.TotalCount,
		"fast path with where=nil must return all eager-loaded posts (including draft)")

	// Branch B: where != nil → MUST skip fast path → slow path filters
	// → 2 posts (drafts excluded). If this returns 3, someone removed the
	// `where == nil` guard and silent-wrong-data is back.
	publishedStatus := post.StatusPublished
	where := &gqlfilter.PostWhereInput{Status: &publishedStatus}
	connSlow, err := userResolverPosts(where, nil, nil, &first, nil)
	require.NoError(t, err)
	require.Equal(t, 2, connSlow.TotalCount,
		"where != nil MUST bypass the fast path and run the filtered slow "+
			"path — a totalCount of 3 here means the guard was removed and "+
			"the resolver is returning eager-loaded unfiltered nodes (silent "+
			"data-correctness bug)")
	for _, e := range connSlow.Edges {
		assert.Equal(t, post.StatusPublished, e.Node.Status,
			"every edge returned by the slow path must carry the filtered status")
	}
}

// TestGraphQLEdge_FilterEmptyResult pins the zero-result corner case:
// a filter that matches nothing must return a connection with empty
// Edges, TotalCount=0, and nil start/end cursors — NOT panic, not
// return a nil connection, not silently fail.
func TestGraphQLEdge_FilterEmptyResult(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice-empty", "alice-empty@example.com")
	// Only drafts — no published posts.
	for i := 1; i <= 3; i++ {
		_, err := client.Post.Create().
			SetTitle(fmt.Sprintf("d-%d", i)).
			SetContent("c").
			SetStatus(post.StatusDraft).
			SetViewCount(0).
			SetAuthorID(alice.ID).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	publishedStatus := post.StatusPublished
	where := &gqlfilter.PostWhereInput{Status: &publishedStatus}

	q := client.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(alice.ID)))
	first := 10
	conn, err := q.Paginate(ctx, nil, &first, nil, nil, entity.WithPostFilter(where.Filter))
	require.NoError(t, err, "filter-yields-empty must succeed — NOT return an error")
	require.NotNil(t, conn, "conn must never be nil on a successful Paginate call")

	assert.Equal(t, 0, conn.TotalCount, "totalCount must be 0 when filter matches nothing")
	assert.Empty(t, conn.Edges, "edges must be empty, not nil")
	assert.False(t, conn.PageInfo.HasNextPage, "no more pages when none exist at all")
	assert.False(t, conn.PageInfo.HasPreviousPage)
	assert.Nil(t, conn.PageInfo.StartCursor, "no start cursor for empty result")
	assert.Nil(t, conn.PageInfo.EndCursor, "no end cursor for empty result")
}

// TestGraphQLEdge_Tx_FastPath pins a subtle semantic: the fast path
// on the entity edge method must work inside a transaction. obj.QueryXxx()
// is never called on the fast path (that's the whole point — no DB hit),
// so the `obj.config.Driver = *txDriver` concern that Unwrap() addresses
// doesn't apply to reads via eager-loaded edges. This test confirms by
// eager-loading inside a Tx, committing, then reading the edge method on
// the Tx-returned entity — no "transaction already committed" error.
func TestGraphQLEdge_Tx_FastPath(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	var loaded *entity.User
	err := integration.WithTx(ctx, client, func(tx *integration.Tx) error {
		alice, err := tx.User.Create().
			SetName("Alice-tx").SetEmail("alice-tx@example.com").
			SetAge(30).SetRole(user.RoleUser).
			SetCreatedAt(now).SetUpdatedAt(now).
			Save(ctx)
		if err != nil {
			return err
		}
		for _, title := range []string{"tx-p1", "tx-p2"} {
			if _, err := tx.Post.Create().
				SetTitle(title).SetContent("c").
				SetStatus(post.StatusPublished).SetViewCount(0).
				SetAuthorID(alice.ID).SetCreatedAt(now).SetUpdatedAt(now).
				Save(ctx); err != nil {
				return err
			}
		}

		// Eager-load posts via the tx client. The returned User carries
		// config.Driver = *txDriver — after commit, any fresh query via
		// that driver would fail.
		u, err := tx.User.Query().
			Where(user.IDField.EQ(alice.ID)).
			WithPosts().
			Only(ctx)
		if err != nil {
			return err
		}
		loaded = u
		return nil
	})
	require.NoError(t, err)
	// DO NOT call loaded.Unwrap() — the fast path shouldn't need it
	// because it doesn't go through QueryPosts(), which is where the
	// *txDriver check matters.
	require.Len(t, loaded.Edges.Posts, 2, "sanity: eager load must succeed inside tx")

	// Fast path: no DB call. Must work despite loaded.config.Driver being
	// a (now committed) *txDriver.
	first := 10
	conn, err := loaded.Posts(ctx, nil, &first, nil, nil, nil)
	require.NoError(t, err,
		"entity edge method fast path must work post-commit on a tx-created "+
			"entity WITHOUT Unwrap() — if this fails, the fast path is falling "+
			"through to QueryPosts() somewhere (which would need Unwrap)")
	require.Equal(t, 2, conn.TotalCount)
	require.Len(t, conn.Edges, 2)
}

// TestGraphQLEdge_BuildPostConnection_DoesNotMutateInput pins a subtle
// correctness invariant: BuildXxxConnection must NOT mutate the caller's
// node slice. The fast path hands it `obj.Edges.Posts` directly (pointer-
// shared with the entity's cached eager-loaded edges). If the helper
// reverses in place for `last != nil` — which a naive reverse loop does —
// the NEXT read of obj.Edges.Posts sees corrupted order.
//
// This test drove finding a real correctness bug in the initial version
// of my BuildXxxConnection: I used `nodes[i], nodes[j] = nodes[j], nodes[i]`
// which mutates the underlying array. Ent avoids this by using a nodeAt()
// index function that reads backwards without reordering the slice.
func TestGraphQLEdge_BuildPostConnection_DoesNotMutateInput(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice-mut", "alice-mut@example.com")
	for _, title := range []string{"A", "B", "C", "D", "E"} {
		createPost(t, client, alice, title, "c")
	}
	posts, err := client.Post.Query().
		Where(post.HasAuthorWith(user.IDField.EQ(alice.ID))).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 5)

	// Snapshot of ORIGINAL order before BuildPostConnection runs.
	originalOrder := make([]string, len(posts))
	for i, p := range posts {
		originalOrder[i] = p.Title
	}

	// last=3 with 5 nodes exercises the reverse logic: trim to
	// posts[2..4] = [C, D, E], then produce reversed output [E, D, C].
	// If reverse is in-place, `posts` becomes [A, B, E, D, C] — broken.
	last := 3
	conn := entity.BuildPostConnection(posts, 5, nil, nil, nil, nil, &last)
	require.Len(t, conn.Edges, 3)
	assert.Equal(t, "E", conn.Edges[0].Node.Title)
	assert.Equal(t, "D", conn.Edges[1].Node.Title)
	assert.Equal(t, "C", conn.Edges[2].Node.Title)

	// AFTER BuildPostConnection, posts must still be in [A, B, C, D, E]
	// order. Any corruption here means subsequent reads of
	// obj.Edges.Posts via the fast path would see wrong order.
	postMutateOrder := make([]string, len(posts))
	for i, p := range posts {
		postMutateOrder[i] = p.Title
	}
	assert.Equal(t, originalOrder, postMutateOrder,
		"BuildPostConnection MUST NOT mutate the input slice — in-place "+
			"reverse would corrupt obj.Edges.Posts when called from the fast "+
			"path, silently breaking the eager-loaded cache on the parent "+
			"*entity.User")
}

// TestGraphQLEdge_NestedWhereLogic pins the common "filter with logical
// operators" pattern:
//
//	where: {
//	  and: [
//	    { status: PUBLISHED },
//	    { not: { titleContains: "draft" } },
//	  ]
//	}
//
// Exercises TodoWhereInput.P() walking a tree of Not/And/Or children
// and composing the SQL predicate correctly. A regression that fails
// to traverse children would pass trivial filter tests but fail here.
func TestGraphQLEdge_NestedWhereLogic(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice-nest", "alice-nest@example.com")
	// Match: published + title doesn't contain "draft"
	createPost(t, client, alice, "real-article", "c")  // match
	createPost(t, client, alice, "final-article", "c") // match
	createPost(t, client, alice, "draft-version", "c") // fail: title contains draft
	_, err := client.Post.Create().
		SetTitle("archived-article").SetContent("c").
		SetStatus(post.StatusArchived).SetViewCount(0).
		SetAuthorID(alice.ID).SetCreatedAt(now).SetUpdatedAt(now).
		Save(ctx) // fail: status != published
	require.NoError(t, err)

	publishedStatus := post.StatusPublished
	draftPrefix := "draft"
	where := &gqlfilter.PostWhereInput{
		And: []*gqlfilter.PostWhereInput{
			{Status: &publishedStatus},
			{Not: &gqlfilter.PostWhereInput{TitleContains: &draftPrefix}},
		},
	}

	q := client.Post.Query().Where(post.HasAuthorWith(user.IDField.EQ(alice.ID)))
	first := 10
	conn, err := q.Paginate(ctx, nil, &first, nil, nil, entity.WithPostFilter(where.Filter))
	require.NoError(t, err, "nested where (AND + NOT + contains) must compose correctly")
	require.Equal(t, 2, conn.TotalCount,
		"expected exactly 2 matches (real-article, final-article) — "+
			"any other count means the P() method's tree walk regressed")
	titles := make([]string, 0, len(conn.Edges))
	for _, e := range conn.Edges {
		titles = append(titles, e.Node.Title)
	}
	assert.ElementsMatch(t, []string{"real-article", "final-article"}, titles,
		"filtered posts must match the logical condition exactly")
}
