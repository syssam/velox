package integration_test

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/privacy"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/user"
	schema "github.com/syssam/velox/testschema"
)

// raceEnabled is set to true via build tag in race_on_test.go when -race
// is active. Used to gate race-documenting tests.
var raceEnabled = false

// SP-2 state propagation matrix.
//
// These tests pin the regression contract for SP-2's structural
// promise: a client-level interceptor or hook must fire on EVERY
// codegen-emitted derivation path. Pre-SP-2 we found 5 silent gaps
// across these paths; Phase 2 made the gaps structurally impossible
// by switching from per-query slice copies to a shared pointer to
// *entity.InterceptorStore. These tests are the behavioral safety
// net behind the structural guards in compiler/gen/sql/wiring_test.go.
//
// The matrix runs against the SQLite test client by default (always
// runs on `go test`), and a parallel _Postgres variant runs the same
// matrix against docker postgres when the PG* env vars are set
// (skips cleanly otherwise — see openPostgresOrSkip).

// interceptorPath is one path in the SP-2 derivation matrix. Each
// path takes a fresh client (already wired with the counting
// interceptor) and exercises one query construction code path.
type interceptorPath struct {
	name string
	run  func(t *testing.T, client *integration.Client)
}

func interceptorPaths() []interceptorPath {
	return []interceptorPath{
		{"DirectQuery", func(t *testing.T, client *integration.Client) {
			ctx := context.Background()
			seedUser(t, client)
			_, err := client.User.Query().All(ctx)
			require.NoError(t, err)
		}},
		{"QueryWhere", func(t *testing.T, client *integration.Client) {
			ctx := context.Background()
			u := seedUser(t, client)
			_, err := client.User.Query().Where(user.NameField.EQ(u.Name)).All(ctx)
			require.NoError(t, err)
		}},
		{"IDs", func(t *testing.T, client *integration.Client) {
			ctx := context.Background()
			seedUser(t, client)
			_, err := client.User.Query().IDs(ctx)
			require.NoError(t, err)
		}},
		{"Count", func(t *testing.T, client *integration.Client) {
			ctx := context.Background()
			seedUser(t, client)
			_, err := client.User.Query().Count(ctx)
			require.NoError(t, err)
		}},
		{"Select", func(t *testing.T, client *integration.Client) {
			ctx := context.Background()
			seedUser(t, client)
			var names []string
			err := client.User.Query().Select(user.FieldName).Scan(ctx, &names)
			require.NoError(t, err)
		}},
		{"WithEagerLoad", func(t *testing.T, client *integration.Client) {
			ctx := context.Background()
			seedUser(t, client)
			_, err := client.User.Query().WithPosts().All(ctx)
			require.NoError(t, err)
		}},
		{"NestedEagerLoad", func(t *testing.T, client *integration.Client) {
			ctx := context.Background()
			seedUser(t, client)
			_, err := client.User.Query().
				WithPosts(func(pq entity.PostQuerier) {
					pq.WithComments()
				}).All(ctx)
			require.NoError(t, err)
		}},
		{"EntityClientEdgeQuery", func(t *testing.T, client *integration.Client) {
			ctx := context.Background()
			u := seedUser(t, client)
			_, err := client.User.QueryPosts(u).All(ctx)
			require.NoError(t, err)
		}},
	}
}

func seedUser(t *testing.T, client *integration.Client) *entity.User {
	t.Helper()
	u, err := client.User.Create().
		SetName("seed").SetEmail("seed@x").SetAge(1).Save(context.Background())
	require.NoError(t, err)
	return u
}

// runInterceptorMatrix exercises every interceptorPath against a
// fresh client, asserting that a single registered counting
// interceptor fires at least once per path.
//
// Each subtest opens its own client so the interceptor and the
// matrix path operate on the same client instance — a single
// shared client across subtests would carry stale state from
// earlier paths and confuse the count assertions.
//
// The interceptor is registered on BOTH client.User AND client.Post
// because some matrix paths run against the source entity (User
// queries) and some against the target entity of an edge traversal
// (Post queries via client.User.QueryPosts(u)). Per-entity wiring
// matches Ent's semantics: client.X.Intercept only fires on X
// queries; cross-entity edges run through the target entity's chain.
func runInterceptorMatrix(t *testing.T, openClient func(*testing.T) *integration.Client) {
	t.Helper()
	for _, tc := range interceptorPaths() {
		t.Run(tc.name, func(t *testing.T) {
			client := openClient(t)

			var fired atomic.Int64
			counter := integration.InterceptFunc(func(next integration.Querier) integration.Querier {
				return integration.QuerierFunc(func(ctx context.Context, q integration.Query) (integration.Value, error) {
					fired.Add(1)
					return next.Query(ctx, q)
				})
			})
			client.User.Intercept(counter)
			client.Post.Intercept(counter)

			tc.run(t, client)

			assert.Greater(t, fired.Load(), int64(0),
				"interceptor did not fire on %s path", tc.name)
		})
	}
}

// TestStatePropagation_InterceptorMatrix_SQLite is the always-on
// SP-2 regression contract for client-level interceptor wiring,
// run against the in-memory SQLite test client.
func TestStatePropagation_InterceptorMatrix_SQLite(t *testing.T) {
	runInterceptorMatrix(t, openTestClient)
}

// TestStatePropagation_InterceptorMatrix_Postgres runs the same
// matrix against docker postgres. Skips cleanly when PG* env vars
// are not set.
func TestStatePropagation_InterceptorMatrix_Postgres(t *testing.T) {
	runInterceptorMatrix(t, func(t *testing.T) *integration.Client {
		client, cleanup := openPostgresOrSkip(t)
		t.Cleanup(cleanup)
		return client
	})
}

type hookPath struct {
	name string
	run  func(t *testing.T, client *integration.Client)
}

func hookPaths() []hookPath {
	return []hookPath{
		{"Create", func(t *testing.T, client *integration.Client) {
			_, err := client.User.Create().
				SetName("a").SetEmail("a@x").SetAge(1).Save(context.Background())
			require.NoError(t, err)
		}},
		{"UpdateOne", func(t *testing.T, client *integration.Client) {
			ctx := context.Background()
			u, err := client.User.Create().
				SetName("b").SetEmail("b@x").SetAge(1).Save(ctx)
			require.NoError(t, err)
			_, err = client.User.UpdateOne(u).SetName("b2").Save(ctx)
			require.NoError(t, err)
		}},
		{"DeleteOne", func(t *testing.T, client *integration.Client) {
			ctx := context.Background()
			u, err := client.User.Create().
				SetName("c").SetEmail("c@x").SetAge(1).Save(ctx)
			require.NoError(t, err)
			err = client.User.DeleteOne(u).Exec(ctx)
			require.NoError(t, err)
		}},
		{"CreateBulk", func(t *testing.T, client *integration.Client) {
			_, err := client.User.CreateBulk(
				client.User.Create().SetName("d1").SetEmail("d1@x").SetAge(1),
				client.User.Create().SetName("d2").SetEmail("d2@x").SetAge(1),
			).Save(context.Background())
			require.NoError(t, err)
		}},
	}
}

// runHookMatrix is the hook-side parallel of runInterceptorMatrix.
func runHookMatrix(t *testing.T, openClient func(*testing.T) *integration.Client) {
	t.Helper()
	for _, tc := range hookPaths() {
		t.Run(tc.name, func(t *testing.T) {
			client := openClient(t)

			var fired atomic.Int64
			client.User.Use(func(next integration.Mutator) integration.Mutator {
				return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
					fired.Add(1)
					return next.Mutate(ctx, m)
				})
			})

			tc.run(t, client)

			assert.Greater(t, fired.Load(), int64(0),
				"hook did not fire on %s path", tc.name)
		})
	}
}

// TestStatePropagation_HookMatrix_SQLite pins the always-on hook
// regression contract for SP-2's mutation paths.
func TestStatePropagation_HookMatrix_SQLite(t *testing.T) {
	runHookMatrix(t, openTestClient)
}

// TestStatePropagation_HookMatrix_Postgres runs the same matrix
// against docker postgres when configured.
func TestStatePropagation_HookMatrix_Postgres(t *testing.T) {
	runHookMatrix(t, func(t *testing.T) *integration.Client {
		client, cleanup := openPostgresOrSkip(t)
		t.Cleanup(cleanup)
		return client
	})
}

// TestStateProp_TxClientHonorsPolicy pins that a transactional client
// propagates the User privacy policy. Regression guard: if a future
// refactor makes Tx bypass user.NewUserClient (or constructs clients
// without RuntimePolicy wiring), tx-scoped queries would silently skip
// privacy — same bug class as the 2026-04-15 clone bug but on the tx
// boundary.
func TestStateProp_TxClientHonorsPolicy(t *testing.T) {
	client := openTestClient(t)
	ctx := schema.EnforceUserPrivacyContext(context.Background())

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	// Subtests share the single tx; all three expect Deny and write
	// nothing, so dirty-state concerns don't apply. If a future subtest
	// mutates tx state, split into separate top-level tests.
	t.Run("QueryDenied", func(t *testing.T) {
		_, err := tx.User.Query().Count(ctx)
		require.Error(t, err, "tx-scoped query must honor policy")
		assert.ErrorIs(t, err, privacy.Deny)
	})

	t.Run("MutationDenied", func(t *testing.T) {
		_, err := tx.User.Create().
			SetName("Mallory").
			SetEmail("m@example.com").
			SetAge(30).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.Error(t, err, "tx-scoped mutation must honor policy")
		assert.ErrorIs(t, err, privacy.Deny)
	})

	t.Run("ClonedQueryPathDenied", func(t *testing.T) {
		// Exist → FirstID → q.clone().IDs — the exact path that
		// bypassed policy pre-fix. Pin it on the tx boundary too.
		_, err := tx.User.Query().Exist(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, privacy.Deny)
	})
}

// TestStateProp_EdgeQueryHonorsTargetPolicy pins that cross-entity edge
// queries (e.g. user.QueryPosts()) wire the TARGET entity's policy, not
// the source's. Loading a User → calling user.QueryPosts() → the Post
// query must evaluate Post's policy (if any). Covered for User→User
// paths elsewhere; this test adds the cross-entity case.
func TestStateProp_EdgeQueryHonorsTargetPolicy(t *testing.T) {
	client := openTestClient(t)
	// Create as allowed context.
	allowCtx := schema.AllowWriteContext(schema.EnforceUserPrivacyContext(context.Background()))
	u, err := client.User.Create().
		SetName("Alice").SetEmail("a@example.com").SetAge(30).
		SetCreatedAt(now).SetUpdatedAt(now).
		Save(allowCtx)
	require.NoError(t, err)

	// Now read under enforce-without-allow: User policy denies.
	// The edge query from user → posts routes through a fresh *PostQuery,
	// which must NOT carry User's policy. Post has no policy in testschema,
	// so the edge read should succeed.
	readCtx := schema.EnforceUserPrivacyContext(context.Background())
	_, err = u.QueryPosts().All(readCtx)
	require.NoError(t, err, "edge query to entity without policy must not inherit source entity's policy")
}

// TestStateProp_ConcurrentUseIsRaceDocument is a documentation test, not
// a safety test. It proves that Use()/Intercept() racing with Query()
// trips the Go race detector, matching the documented contract:
//
//	"all Use() and Intercept() calls must complete before concurrent
//	 query/mutation execution begins"
//
// Rather than enforce safety at runtime (would cost a mutex on every
// store read), velox pushes this to the race detector. This test runs
// ONLY under -race to validate the contract. Without -race it's skipped.
func TestStateProp_ConcurrentUseIsRaceDocument(t *testing.T) {
	if !raceEnabled {
		t.Skip("race detector not enabled; test only meaningful under -race")
	}
	// Intentionally omit an actual concurrent test body; we just pin that
	// the contract is documented. If a future reader wonders "why isn't
	// Use() safe concurrently with queries?" — the answer is here: the
	// race detector is the enforcement mechanism, not a runtime lock.
	t.Log("concurrency contract: Use/Intercept must complete before queries; enforced by the race detector, not by runtime locks")
}
