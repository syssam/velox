package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
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
		builders := []*user.UserCreate{
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
