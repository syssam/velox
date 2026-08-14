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
	testschema "github.com/syssam/velox/testschema"
)

// KNOWN GAP — row scoping does not reach edge-predicate subqueries.
//
// user.HasPostsWith(...) compiles to an EXISTS/IN subquery over the posts
// table (dialect/sql/sqlgraph/graph.go::HasNeighborsWith). That subquery is
// built directly on a *sql.Selector; no PostQuery object is ever
// constructed, so neither the Post interceptor nor the Post Policy runs
// for it.
//
// Consequence: with a row filter on Post, a caller can still learn whether
// out-of-scope Post rows exist by filtering Users through the edge. It is
// an existence oracle, not row exfiltration — but it is binary-searchable.
//
// These tests assert the CURRENT (leaky) behavior on purpose, so the gap
// cannot change silently. The fix, when it lands, belongs at the point
// where HasNeighborsWith constructs `matches := builder.Select().From(to)`
// — the target table object is in hand there, so the alias is correct and
// the selector already carries a context (matches.WithContext(q.Context())).
// When that lands, flip both assertions and update CLAUDE.md.

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

func TestAuthzKnownGap_Policy_DoesNotReachEdgeSubquery(t *testing.T) {
	base := context.Background()
	client := openTestClient(t)

	alice := createUser(t, client, "alice", "alice@x")
	bob := createUser(t, client, "bob", "bob@x")
	createPost(t, client, alice, "visible", "c")
	createPost(t, client, bob, "hidden", "c")

	// testschema's User policy scopes User rows to a single name. Scoping
	// the *outer* entity works; the point here is that an edge predicate
	// still reaches rows the policy would exclude on the other side.
	scoped := testschema.FilterUserQueryToNameContext(base, "alice")

	all, err := client.User.Query().All(scoped)
	require.NoError(t, err)
	require.Len(t, all, 1, "baseline: the User policy filter applies to a plain query")

	// The same policy is inert inside another entity's edge subquery: a
	// Post query filtered by HasAuthorWith sees both authors.
	posts, err := client.Post.Query().
		Where(post.HasAuthorWith(user.IDField.GT(0))).
		All(scoped)
	require.NoError(t, err)

	assert.Len(t, posts, 2,
		"GAP: the User policy does not constrain the HasAuthorWith subquery, so "+
			"posts by out-of-scope authors still match. When edge subqueries are "+
			"scoped this becomes 1 — flip this assertion then.")

	_ = bob
}
