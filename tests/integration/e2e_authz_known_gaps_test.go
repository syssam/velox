package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/tests/integration/intercept"
	"github.com/syssam/velox/tests/integration/post"
	"github.com/syssam/velox/tests/integration/user"
)

// KNOWN GAP — interceptors do not reach edge-predicate subqueries.
//
// user.HasPostsWith(...) compiles to an EXISTS/IN subquery over the posts
// table (dialect/sql/sqlgraph/graph.go::HasNeighborsWith). That subquery is
// built directly on a *sql.Selector; no PostQuery object is ever
// constructed, so the Post interceptors (traversers) do not run for it.
//
// The Post *Policy* does run there — runtime.ApplyEntityPolicy, pinned by
// e2e_edge_predicate_policy_test.go — which is why row-level authorization
// belongs in Policy(), not in interceptors (a query-shaping layer that also
// never reaches writes). An interceptor-only tenant filter still leaves an
// existence oracle through the edge; this test asserts that on purpose so
// it cannot change silently.

func TestAuthzKnownGap_Interceptor_DoesNotReachEdgeSubquery(t *testing.T) {
	ctx := context.Background()
	client := openTestClient(t)

	alice := createUser(t, client, "alice", "alice@x")
	bob := createUser(t, client, "bob", "bob@x")
	createPost(t, client, alice, "visible", "c")
	createPost(t, client, bob, "hidden", "c")

	seen := map[string]int{}
	client.Intercept(intercept.TraverseFunc(func(_ context.Context, q intercept.Query) error {
		seen[q.Type()]++
		if q.Type() == "Post" {
			// Row scope: only "visible" posts may be seen.
			q.WhereP(func(s *sql.Selector) { s.Where(sql.EQ(s.C(post.FieldTitle), "visible")) })
		}
		return nil
	}))

	// Baseline: a direct Post query IS scoped.
	posts, err := client.Post.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, posts, 1, "direct Post query must be scoped")
	require.Equal(t, "visible", posts[0].Title)

	before := seen["Post"]
	users, err := client.User.Query().Where(user.HasPostsWith(post.IDField.GT(0))).All(ctx)
	require.NoError(t, err)

	assert.Equal(t, before, seen["Post"],
		"GAP: the Post traverser is not invoked for the HasPostsWith subquery")

	names := make([]string, 0, len(users))
	for _, u := range users {
		names = append(names, u.Name)
	}
	assert.ElementsMatch(t, []string{"alice", "bob"}, names,
		"GAP: bob matches via his out-of-scope post, leaking its existence. "+
			"When edge subqueries are scoped this becomes [alice] — flip this assertion then.")

	_ = bob
}
