package integration_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/contrib/graphql/gqlrelay"
	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	userclient "github.com/syssam/velox/tests/integration/client/user"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/user"
)

// Lifts the load-bearing behavioral tests from the SQLite-only suite
// into a dialect-parameterized form. Each subtest opens a fresh client
// for SQLite (always) and for Postgres/MySQL when their env vars are
// set (VELOX_TEST_POSTGRES / VELOX_TEST_MYSQL or the conventional
// PG*/MYSQL_* fallbacks — see postgres_helper_test.go and
// mysql_helper_test.go). Dialects without configured env vars SKIP
// so `go test ./...` keeps working without docker. CI lanes that
// export the env vars exercise the cross-dialect surface.

type multidialectCase struct {
	name string
	open func(testing.TB) (*integration.Client, func())
}

// sqliteMemoryOpen returns a fresh in-memory SQLite client with the
// schema migrated. Matches the Postgres/MySQL helper signature so
// forEachDialect can dispatch without special-casing SQLite.
func sqliteMemoryOpen(t testing.TB) (*integration.Client, func()) {
	t.Helper()
	client, err := integration.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	require.NoError(t, err)
	if err := client.Schema.Create(context.Background()); err != nil {
		_ = client.Close()
		t.Fatalf("sqlite migrate: %v", err)
	}
	return client, func() { _ = client.Close() }
}

var multidialectCases = []multidialectCase{
	{"sqlite", sqliteMemoryOpen},
	{"postgres", openPostgresOrSkip},
	{"mysql", openMySQLOrSkip},
}

// forEachDialect runs body against every configured dialect. Dialects
// without env-var configuration SKIP per the helper convention.
func forEachDialect(t *testing.T, body func(t *testing.T, client *integration.Client)) {
	t.Helper()
	for _, dc := range multidialectCases {
		t.Run(dc.name, func(t *testing.T) {
			client, cleanup := dc.open(t)
			defer cleanup()
			body(t, client)
		})
	}
}

// TestMultiDialect_CRUD pins basic Create/Get/Update/Delete behavior
// across dialects. Failures here indicate column-type, ID-sequence, or
// scan-assign divergence between SQLite and the target dialect.
func TestMultiDialect_CRUD(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		u, err := client.User.Create().
			SetName("Alice").
			SetEmail("alice@multi.com").
			SetAge(30).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
		require.NotZero(t, u.ID, "autoincrement ID must be non-zero on every dialect")

		got, err := client.User.Get(ctx, u.ID)
		require.NoError(t, err)
		assert.Equal(t, "Alice", got.Name)
		assert.Equal(t, "alice@multi.com", got.Email)
		assert.Equal(t, 30, got.Age)

		updated, err := client.User.UpdateOneID(u.ID).SetName("Bob").Save(ctx)
		require.NoError(t, err)
		assert.Equal(t, "Bob", updated.Name)

		err = client.User.DeleteOneID(u.ID).Exec(ctx)
		require.NoError(t, err)

		_, err = client.User.Get(ctx, u.ID)
		require.Error(t, err, "Get after Delete must error on every dialect")
	})
}

// TestMultiDialect_EdgeLoad pins cross-entity join behavior. Each
// dialect emits its own quoting + JOIN syntax via sqlgraph; the test
// asserts the eager-load graph (WithPosts) produces identical row
// counts regardless of dialect.
func TestMultiDialect_EdgeLoad(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		u := createUser(t, client, "EdgeOwner", "edge@multi.com")
		_ = createPost(t, client, u, "first", "p1")
		_ = createPost(t, client, u, "second", "p2")

		users, err := client.User.Query().
			Where(user.IDField.EQ(u.ID)).
			WithPosts().
			All(ctx)
		require.NoError(t, err)
		require.Len(t, users, 1)
		assert.Len(t, users[0].Edges.Posts, 2, "eager-loaded posts must round-trip on every dialect")
	})
}

// TestMultiDialect_TransactionCommitRollback pins tx-driver semantics.
// Each dialect's database/sql driver handles Commit/Rollback slightly
// differently (Postgres uses explicit BEGIN, MySQL has implicit commits
// on DDL, SQLite has no real isolation). The test verifies velox's
// client-level tx abstraction hides those differences.
func TestMultiDialect_TransactionCommitRollback(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		// Commit path: user persists.
		tx, err := client.Tx(ctx)
		require.NoError(t, err)
		_, err = tx.User.Create().
			SetName("Committed").
			SetEmail("committed@multi.com").
			SetAge(25).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
		require.NoError(t, tx.Commit())

		count, err := client.User.Query().Where(user.EmailField.EQ("committed@multi.com")).Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, count)

		// Rollback path: user does not persist.
		tx2, err := client.Tx(ctx)
		require.NoError(t, err)
		_, err = tx2.User.Create().
			SetName("RolledBack").
			SetEmail("rollback@multi.com").
			SetAge(26).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
		require.NoError(t, tx2.Rollback())

		count, err = client.User.Query().Where(user.EmailField.EQ("rollback@multi.com")).Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, count, "rolled-back rows must not persist on any dialect")
	})
}

// TestMultiDialect_BulkOnConflictUpdate pins the MOST dialect-divergent
// code path: upsert emission. SQLite → INSERT ... ON CONFLICT DO UPDATE,
// Postgres → INSERT ... ON CONFLICT DO UPDATE, MySQL → INSERT ... ON
// DUPLICATE KEY UPDATE. Velox's ResolveWithNewValues() normalizes the
// three; if a dialect drifts, this test catches it.
func TestMultiDialect_BulkOnConflictUpdate(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		// Seed a user whose email will be the conflict key.
		_, err := client.User.Create().
			SetName("Original").
			SetEmail("conflict@multi.com").
			SetAge(20).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)

		// Bulk-create with one duplicate email (Original) and one new
		// row (Fresh). With ResolveWithNewValues, the duplicate should
		// update in place and the new row should insert.
		builders := []*userclient.UserCreate{
			client.User.Create().
				SetName("Updated").
				SetEmail("conflict@multi.com").
				SetAge(21).
				SetRole(user.RoleUser).
				SetCreatedAt(now).
				SetUpdatedAt(now),
			client.User.Create().
				SetName("Fresh").
				SetEmail("fresh@multi.com").
				SetAge(22).
				SetRole(user.RoleUser).
				SetCreatedAt(now).
				SetUpdatedAt(now),
		}
		err = client.User.CreateBulk(builders...).
			OnConflict(
				sql.ConflictColumns(user.FieldEmail),
				sql.ResolveWithNewValues(),
			).
			Exec(ctx)
		require.NoError(t, err)

		// Total rows = 2 (original updated, fresh inserted, no 3rd).
		total, err := client.User.Query().Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, 2, total, "upsert must update-in-place, not insert-duplicate")

		// The conflicting row now has the updated name + age.
		got, err := client.User.Query().Where(user.EmailField.EQ("conflict@multi.com")).Only(ctx)
		require.NoError(t, err)
		assert.Equal(t, "Updated", got.Name, "ResolveWithNewValues must overwrite name on every dialect")
		assert.Equal(t, 21, got.Age, "ResolveWithNewValues must overwrite age on every dialect")
	})
}

// TestMultiDialect_AggregateSum pins aggregate SQL emission. Each
// dialect handles SUM over an empty result set differently (NULL vs
// 0) — velox's aggregate scan must normalize this.
func TestMultiDialect_AggregateSum(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		createUser(t, client, "SumA", "suma@multi.com")
		createUser(t, client, "SumB", "sumb@multi.com")
		createUser(t, client, "SumC", "sumc@multi.com")

		// All three createUser calls use Age=30, so sum = 90.
		var results []struct {
			Sum int `json:"sum"`
		}
		err := client.User.Query().
			Aggregate(integration.As(integration.Sum(user.FieldAge), "sum")).
			Scan(ctx, &results)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, 90, results[0].Sum, "SUM(age) must equal sum of seeded ages on every dialect")
	})
}

// TestMultiDialect_OnDeleteCascade pins ON DELETE CASCADE behavior across
// dialects. Comment.post carries sqlschema.OnDelete(Cascade) (testschema), so
// deleting a Post must cascade-delete its Comments at the DB level on every
// engine. This is both the behavioral guard for cascade and — because the
// generated migrate FK must compile — the guard that an explicit OnDelete
// annotation renders to valid Go (schema.Cascade, not schema.CASCADE).
func TestMultiDialect_OnDeleteCascade(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		author := createUser(t, client, "CascadeOwner", "cascade@multi.com")
		p := createPost(t, client, author, "to-delete", "body")
		_ = createComment(t, client, author, p, "c1")
		_ = createComment(t, client, author, p, "c2")

		before, err := client.Comment.Query().Count(ctx)
		require.NoError(t, err)
		require.Equal(t, 2, before, "two comments exist before the post delete")

		// Deleting the post must cascade-delete its comments (FK ON DELETE CASCADE).
		require.NoError(t, client.Post.DeleteOneID(p.ID).Exec(ctx))

		after, err := client.Comment.Query().Count(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, after,
			"deleting a post must cascade-delete its comments on every dialect")
	})
}

// seedUsers inserts n users named User01..UserNN (so lexicographic name order
// matches insertion/ID order) on a freshly-migrated client. Because each
// dialect lane opens a fresh client whose tables were just (re)created,
// autoincrement starts at 1, so the inserted rows have IDs 1..n on every
// dialect. The per-client analog of pagination_test.go's setupUsers, which is
// hardcoded to SQLite.
func seedUsers(t *testing.T, client *integration.Client, n int) {
	t.Helper()
	ctx := context.Background()
	for i := 1; i <= n; i++ {
		_, err := client.User.Create().
			SetName(fmt.Sprintf("User%02d", i)).
			SetEmail(fmt.Sprintf("user%02d@multi.com", i)).
			SetAge(20 + i).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}
}

// TestMultiDialect_Pagination pins Relay cursor pagination across dialects —
// the MOST dialect-divergent read path. Composite cursors emit row-value
// comparisons like (name, id) > (?, ?) whose SQL, quoting, and NULL ordering
// differ per engine; all of velox's pagination work (backward direction,
// stable TotalCount, the nil-order NULL-comparison fix) was validated against
// SQLite only. This lifts the load-bearing cases from pagination_test.go into
// the dialect-parameterized form so a divergence on Postgres/MySQL is caught.
func TestMultiDialect_Pagination(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()
		seedUsers(t, client, 10)

		// Forward, default (ID ASC) order: two clean pages of 5.
		t.Run("forward_default", func(t *testing.T) {
			first := 5
			page1, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil)
			require.NoError(t, err)
			assert.Equal(t, []int{1, 2, 3, 4, 5}, ids(page1))
			assert.True(t, page1.PageInfo.HasNextPage)
			assert.False(t, page1.PageInfo.HasPreviousPage)
			assert.Equal(t, 10, page1.TotalCount, "TotalCount must reflect full dataset on every dialect")

			after := roundTripCursor(t, page1.PageInfo.EndCursor)
			page2, err := client.User.Query().Paginate(ctx, &after, &first, nil, nil)
			require.NoError(t, err)
			assert.Equal(t, []int{6, 7, 8, 9, 10}, ids(page2))
			assert.False(t, page2.PageInfo.HasNextPage)
			assert.Equal(t, 10, page2.TotalCount, "TotalCount must not change between pages")
		})

		// Nil orderBy degrades to DefaultOrder. The cursor's Value is nil, so
		// page 2 exercises the composite NULL-comparison SQL — the exact path
		// that returned 0 rows before the fix. Most likely to diverge on
		// Postgres/MySQL, which order NULLs differently from SQLite.
		t.Run("nil_order_page2", func(t *testing.T) {
			first := 5
			p1, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil, withUserOrder(t))
			require.NoError(t, err)
			require.Len(t, p1.Edges, 5)

			after := roundTripCursor(t, p1.PageInfo.EndCursor)
			p2, err := client.User.Query().Paginate(ctx, &after, &first, nil, nil, withUserOrder(t))
			require.NoError(t, err)
			require.Len(t, p2.Edges, 5,
				"nil-order page 2 must return 5 — 0 rows means the composite NULL comparison diverges on this dialect")
			assert.Equal(t, []int{6, 7, 8, 9, 10}, ids(p2))
		})

		// Explicit name-ASC order: composite (name, id) cursor across two pages.
		t.Run("explicit_asc_composite_cursor", func(t *testing.T) {
			orderBy := userOrderBy(t, "NAME", entity.OrderDirectionAsc)
			first := 3
			fa, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil, withUserOrder(t, orderBy))
			require.NoError(t, err)
			assert.Equal(t, []int{1, 2, 3}, ids(fa))

			after := roundTripCursor(t, fa.PageInfo.EndCursor)
			fb, err := client.User.Query().Paginate(ctx, &after, &first, nil, nil, withUserOrder(t, orderBy))
			require.NoError(t, err)
			assert.Equal(t, []int{4, 5, 6}, ids(fb),
				"composite name-ASC cursor must page correctly on every dialect")
		})

		// last only (no before cursor): must fetch the truly last 3 rows, not
		// the first 3 reversed. Exercises the reverse-order SQL per dialect.
		t.Run("last_only", func(t *testing.T) {
			last := 3
			conn, err := client.User.Query().Paginate(ctx, nil, nil, nil, &last)
			require.NoError(t, err)
			assert.Equal(t, []int{8, 9, 10}, ids(conn))
			assert.True(t, conn.PageInfo.HasPreviousPage)
			assert.False(t, conn.PageInfo.HasNextPage)
		})

		// Backward (last + before cursor): rows before cursor(5), ascending display.
		t.Run("backward_default", func(t *testing.T) {
			first := 5
			fwd, err := client.User.Query().Paginate(ctx, nil, &first, nil, nil)
			require.NoError(t, err)
			start := roundTripCursor(t, fwd.PageInfo.EndCursor) // cursor at ID 5

			last := 3
			bwd, err := client.User.Query().Paginate(ctx, nil, nil, &start, &last)
			require.NoError(t, err)
			assert.Equal(t, []int{2, 3, 4}, ids(bwd),
				"backward pagination must return rows before the cursor in ascending display order")
			assert.True(t, bwd.PageInfo.HasPreviousPage)
		})

		// Full forward sweep: every row appears exactly once, no overlap.
		t.Run("no_overlap_full_sweep", func(t *testing.T) {
			seen := map[int]bool{}
			var cursor *gqlrelay.Cursor
			first := 4
			for page := 1; ; page++ {
				conn, err := client.User.Query().Paginate(ctx, cursor, &first, nil, nil)
				require.NoError(t, err)
				for _, e := range conn.Edges {
					require.False(t, seen[e.Node.ID], "ID %d appeared on page %d and a previous page", e.Node.ID, page)
					seen[e.Node.ID] = true
				}
				if !conn.PageInfo.HasNextPage {
					break
				}
				c := roundTripCursor(t, conn.PageInfo.EndCursor)
				cursor = &c
			}
			assert.Len(t, seen, 10, "every user must appear exactly once across all pages on every dialect")
		})
	})
}

// TestMultiDialect_JSON pins JSON column behavior across dialects — set, read
// back, and the divergent append path. Each engine stores and mutates JSON
// differently (Postgres jsonb, MySQL JSON_ARRAY_APPEND, SQLite json_insert),
// so AppendLabels is the highest-risk JSON operation. Labels lives on Post.
func TestMultiDialect_JSON(t *testing.T) {
	forEachDialect(t, func(t *testing.T, client *integration.Client) {
		ctx := context.Background()

		author := createUser(t, client, "JSONOwner", "json@multi.com")

		// Set + read back a []string JSON array.
		p, err := client.Post.Create().
			SetTitle("json").
			SetContent("body").
			SetAuthorID(author.ID).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			SetLabels([]string{"go"}).
			Save(ctx)
		require.NoError(t, err)
		assert.Equal(t, []string{"go"}, p.Labels, "JSON array must round-trip on insert for every dialect")

		got, err := client.Post.Get(ctx, p.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"go"}, got.Labels, "JSON array must round-trip on read for every dialect")

		// Append: the dialect-divergent mutation path.
		_, err = client.Post.UpdateOneID(p.ID).
			AppendLabels([]string{"orm", "multi"}).
			Save(ctx)
		require.NoError(t, err)

		got, err = client.Post.Get(ctx, p.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{"go", "orm", "multi"}, got.Labels,
			"AppendLabels must concatenate JSON arrays identically on every dialect")
	})
}
