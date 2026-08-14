package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
	"github.com/syssam/velox/tests/integration/intercept"
	"github.com/syssam/velox/tests/integration/user"
	testschema "github.com/syssam/velox/testschema"
)

// This file is the authorization-coverage matrix for velox's two
// row-scoping mechanisms. It exists because "which surfaces does my
// tenant filter actually reach?" is not answerable by reading the
// generator — the answer is spread across prepareQuery, the mutation
// spec builder, sqlgraph, and the entity/ edge methods.
//
// The matrix it pins:
//
//	surface                     interceptor   Policy()
//	--------------------------------------------------
//	All/Count/IDs/Exist/Only         yes         yes
//	Get(ctx, id)                     yes         yes
//	Select/GroupBy                   yes         yes
//	eager load (WithPosts)           yes         yes
//	entity edge query                yes         yes
//	inside a transaction             yes         yes
//	Noder(ctx, id)                   yes         yes
//	Update()/Delete() by predicate   NO          yes
//	HasXxxWith(...) subquery         NO          NO   <- see e2e_authz_known_gaps_test.go
//
// The load-bearing conclusion: Policy() is a strict superset of
// interceptors for authorization. Interceptors do not reach writes.
// Do NOT recommend interceptors as a row-level security boundary.

// countingTraverser registers one generic traverser on the root client
// and returns a per-entity-type invocation counter.
func countingTraverser(t *testing.T, client *integration.Client) map[string]int {
	t.Helper()
	seen := map[string]int{}
	client.Intercept(intercept.TraverseFunc(func(_ context.Context, q intercept.Query) error {
		seen[q.Type()]++
		return nil
	}))
	return seen
}

// TestAuthzSurface_Interceptor_ReadPaths pins that a single generic
// traverser registered on the ROOT client reaches every read terminal.
// Each of these calls prepareQuery, which is where RunTraversers runs;
// a generator change that skips prepareQuery on any terminal silently
// removes that terminal from every tenant filter in every velox app.
func TestAuthzSurface_Interceptor_ReadPaths(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		name string
		run  func(t *testing.T, c *integration.Client, u *entity.User)
	}{
		{"All", func(t *testing.T, c *integration.Client, _ *entity.User) {
			_, err := c.User.Query().All(ctx)
			require.NoError(t, err)
		}},
		{"Count", func(t *testing.T, c *integration.Client, _ *entity.User) {
			_, err := c.User.Query().Count(ctx)
			require.NoError(t, err)
		}},
		{"IDs", func(t *testing.T, c *integration.Client, _ *entity.User) {
			_, err := c.User.Query().IDs(ctx)
			require.NoError(t, err)
		}},
		{"Exist", func(t *testing.T, c *integration.Client, _ *entity.User) {
			_, err := c.User.Query().Exist(ctx)
			require.NoError(t, err)
		}},
		{"Only", func(t *testing.T, c *integration.Client, _ *entity.User) {
			_, _ = c.User.Query().Limit(1).Only(ctx)
		}},
		{"First", func(t *testing.T, c *integration.Client, _ *entity.User) {
			_, _ = c.User.Query().First(ctx)
		}},
		{"Get_byID", func(t *testing.T, c *integration.Client, u *entity.User) {
			_, err := c.User.Get(ctx, u.ID)
			require.NoError(t, err)
		}},
		{"Select", func(t *testing.T, c *integration.Client, _ *entity.User) {
			var out []string
			require.NoError(t, c.User.Query().Select(user.FieldName).Scan(ctx, &out))
		}},
		{"GroupBy", func(t *testing.T, c *integration.Client, _ *entity.User) {
			var out []struct {
				Name string `json:"name"`
			}
			require.NoError(t, c.User.Query().GroupBy(user.FieldName).Scan(ctx, &out))
		}},
		{"Where_rawSelector", func(t *testing.T, c *integration.Client, _ *entity.User) {
			_, err := c.User.Query().Where(func(s *sql.Selector) {}).All(ctx)
			require.NoError(t, err)
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := openTestClient(t)
			u := createUser(t, c, "alice", "alice@x")
			seen := countingTraverser(t, c)
			tc.run(t, c, u)
			assert.Positive(t, seen["User"],
				"%s never reached the traverser — every interceptor-based row filter "+
					"silently stops covering this terminal", tc.name)
		})
	}
}

// TestAuthzSurface_Interceptor_EagerLoadAndEdges pins that the child
// SELECTs velox issues on the user's behalf are scoped too. These are
// the paths a caller never writes explicitly, so a gap here is invisible
// in review.
func TestAuthzSurface_Interceptor_EagerLoadAndEdges(t *testing.T) {
	ctx := context.Background()

	t.Run("eager_load_WithPosts", func(t *testing.T) {
		c := openTestClient(t)
		u := createUser(t, c, "alice", "alice@x")
		createPost(t, c, u, "p1", "c")
		seen := countingTraverser(t, c)

		_, err := c.User.Query().WithPosts().All(ctx)
		require.NoError(t, err)
		assert.Positive(t, seen["Post"], "eager-loaded child SELECT must be scoped")
	})

	t.Run("m2m_edge_query", func(t *testing.T) {
		c := openTestClient(t)
		u := createUser(t, c, "alice", "alice@x")
		tg := createTag(t, c, "go")
		p, err := c.Post.Create().
			SetTitle("p1").SetContent("c").SetStatus("published").SetViewCount(0).
			SetAuthorID(u.ID).AddTags(tg).SetCreatedAt(now).SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
		seen := countingTraverser(t, c)

		_, err = c.Post.QueryTags(p).All(ctx)
		require.NoError(t, err)
		assert.Positive(t, seen["Tag"], "M2M edge query must be scoped")
	})

	// Three provenances for the entity handle, because they populate
	// config.InterStore by different routes. entity/edge methods fall
	// back to an EMPTY store when config.InterStore is nil, which would
	// drop the filter silently rather than erroring.
	t.Run("entity_edge_query_from_Query", func(t *testing.T) {
		c := openTestClient(t)
		u := createUser(t, c, "alice", "alice@x")
		createPost(t, c, u, "p1", "c")
		users, err := c.User.Query().All(ctx)
		require.NoError(t, err)
		seen := countingTraverser(t, c)

		_, err = users[0].QueryPosts().All(ctx)
		require.NoError(t, err)
		assert.Positive(t, seen["Post"], "edge query off a Query-loaded entity must be scoped")
	})

	t.Run("entity_edge_query_from_Create", func(t *testing.T) {
		c := openTestClient(t)
		u := createUser(t, c, "bob", "bob@x")
		createPost(t, c, u, "p1", "c")
		seen := countingTraverser(t, c)

		_, err := u.QueryPosts().All(ctx)
		require.NoError(t, err)
		assert.Positive(t, seen["Post"], "edge query off a Create-returned entity must be scoped")
	})

	t.Run("entity_edge_query_after_tx_Unwrap", func(t *testing.T) {
		c := openTestClient(t)
		var u *entity.User
		require.NoError(t, integration.WithTx(ctx, c, func(tx *integration.Tx) error {
			var err error
			u, err = tx.User.Create().
				SetName("carol").SetEmail("carol@x").SetAge(30).
				SetCreatedAt(now).SetUpdatedAt(now).
				Save(ctx)
			return err
		}))
		u = u.Unwrap()
		seen := countingTraverser(t, c)

		_, err := u.QueryPosts().All(ctx)
		require.NoError(t, err)
		assert.Positive(t, seen["Post"],
			"Unwrap() swaps the driver — it must not drop the interceptor store")
	})

	t.Run("inside_transaction", func(t *testing.T) {
		c := openTestClient(t)
		createUser(t, c, "alice", "alice@x")
		seen := countingTraverser(t, c)

		require.NoError(t, integration.WithTx(ctx, c, func(tx *integration.Tx) error {
			_, err := tx.User.Query().All(ctx)
			return err
		}))
		assert.Positive(t, seen["User"], "tx-scoped queries must stay scoped")
	})

	t.Run("noder_by_global_id", func(t *testing.T) {
		c := openTestClient(t)
		u := createUser(t, c, "alice", "alice@x")
		seen := countingTraverser(t, c)

		_, err := c.Noder(ctx, u.ID)
		require.NoError(t, err)
		assert.Positive(t, seen["User"],
			"Noder is the GraphQL node(id:) entry point — the classic IDOR vector")
	})
}

// TestAuthzSurface_Interceptor_DoesNotReachWrites pins the GAP: query
// interceptors never run on mutations. A tenant filter built only from
// interceptors can UPDATE and DELETE other tenants' rows.
//
// This asserts zero deliberately. If velox ever grows mutation
// interceptors, this test failing is the signal to update the guidance
// in CLAUDE.md that says Policy() is required for writes.
func TestAuthzSurface_Interceptor_DoesNotReachWrites(t *testing.T) {
	ctx := context.Background()
	c := openTestClient(t)
	seen := countingTraverser(t, c)

	u, err := c.User.Create().
		SetName("alice").SetEmail("a@x").SetAge(30).
		SetCreatedAt(now).SetUpdatedAt(now).Save(ctx)
	require.NoError(t, err)
	assert.Empty(t, seen, "Create must not reach a query interceptor")

	_, err = c.User.UpdateOne(u).SetName("alice2").Save(ctx)
	require.NoError(t, err)
	assert.Empty(t, seen, "UpdateOne must not reach a query interceptor")

	_, err = c.User.Update().Where(user.NameField.EQ("alice2")).SetName("alice3").Save(ctx)
	require.NoError(t, err)
	assert.Empty(t, seen, "bulk Update must not reach a query interceptor")

	_, err = c.User.Delete().Where(user.NameField.EQ("alice3")).Exec(ctx)
	require.NoError(t, err)
	assert.Empty(t, seen,
		"bulk Delete must not reach a query interceptor — this is why row-level "+
			"authorization must be expressed as Policy(), not Intercept()")
}

// TestAuthzSurface_Policy_ScopesReads pins that a Policy FilterFunc
// reaches the same read terminals the interceptor does. testschema's
// User policy injects WHERE name = <value> when the context carries
// FilterUserQueryToNameContext, so the row count is the observable.
func TestAuthzSurface_Policy_ScopesReads(t *testing.T) {
	base := context.Background()
	scoped := testschema.FilterUserQueryToNameContext(base, "alice")

	c := openTestClient(t)
	createUser(t, c, "alice", "alice@x")
	createUser(t, c, "bob", "bob@x")

	all, err := c.User.Query().All(base)
	require.NoError(t, err)
	require.Len(t, all, 2, "unscoped baseline")

	t.Run("All", func(t *testing.T) {
		got, err := c.User.Query().All(scoped)
		require.NoError(t, err)
		assert.Len(t, got, 1, "policy filter must scope All")
	})

	t.Run("Count", func(t *testing.T) {
		got, err := c.User.Query().Count(scoped)
		require.NoError(t, err)
		assert.Equal(t, 1, got, "policy filter must scope Count")
	})

	t.Run("IDs", func(t *testing.T) {
		got, err := c.User.Query().IDs(scoped)
		require.NoError(t, err)
		assert.Len(t, got, 1, "policy filter must scope IDs")
	})

	t.Run("Select", func(t *testing.T) {
		var out []string
		require.NoError(t, c.User.Query().Select(user.FieldName).Scan(scoped, &out))
		assert.Equal(t, []string{"alice"}, out, "policy filter must scope Select")
	})

	t.Run("GroupBy", func(t *testing.T) {
		var out []struct {
			Name string `json:"name"`
		}
		require.NoError(t, c.User.Query().GroupBy(user.FieldName).Scan(scoped, &out))
		assert.Len(t, out, 1, "policy filter must scope GroupBy")
	})

	t.Run("inside_transaction", func(t *testing.T) {
		require.NoError(t, integration.WithTx(base, c, func(tx *integration.Tx) error {
			got, err := tx.User.Query().All(scoped)
			require.NoError(t, err)
			assert.Len(t, got, 1, "policy filter must survive into a tx")
			return nil
		}))
	})
}

// TestAuthzSurface_Policy_ScopesWrites is the load-bearing counterpart
// to TestAuthzSurface_Interceptor_DoesNotReachWrites: the same row
// filter, expressed as a Policy, DOES constrain bulk writes.
//
// The mechanism is privacy.FilterFunc -> (*UserMutation).AddPredicate ->
// PredicatesFuncs() -> spec.Predicate -> the UPDATE/DELETE WHERE clause.
// Breaking any link in that chain turns a scoped bulk write into a
// cross-tenant one, which is why this asserts on surviving rows rather
// than on affected counts.
func TestAuthzSurface_Policy_ScopesWrites(t *testing.T) {
	base := context.Background()

	t.Run("bulk_Update_is_scoped", func(t *testing.T) {
		c := openTestClient(t)
		createUser(t, c, "alice", "alice@x")
		createUser(t, c, "bob", "bob@x")

		// No predicate on the builder at all: without the policy this
		// would rewrite every row in the table.
		scoped := testschema.FilterUserMutationToNameContext(base, "alice")
		affected, err := c.User.Update().SetNickname("touched").Save(scoped)
		require.NoError(t, err)
		assert.Equal(t, 1, affected, "policy filter must narrow the bulk UPDATE to one row")

		bobRows, err := c.User.Query().Where(user.NameField.EQ("bob")).All(base)
		require.NoError(t, err)
		require.Len(t, bobRows, 1)
		assert.Nil(t, bobRows[0].Nickname,
			"bob is outside the policy scope and must not have been updated")
	})

	t.Run("bulk_Delete_is_scoped", func(t *testing.T) {
		c := openTestClient(t)
		createUser(t, c, "alice", "alice@x")
		createUser(t, c, "bob", "bob@x")

		scoped := testschema.FilterUserMutationToNameContext(base, "alice")
		affected, err := c.User.Delete().Exec(scoped)
		require.NoError(t, err)
		assert.Equal(t, 1, affected, "policy filter must narrow the bulk DELETE to one row")

		remaining, err := c.User.Query().All(base)
		require.NoError(t, err)
		require.Len(t, remaining, 1)
		assert.Equal(t, "bob", remaining[0].Name,
			"bob is outside the policy scope and must have survived the DELETE")
	})
}
