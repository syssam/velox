package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/privacy"
	schema "github.com/syssam/velox/testschema"
)

// TestPrivacy_DenyMutationWithoutAuth verifies the policy chain refuses
// User mutations when the request opts into enforcement but does not
// authorize the write.
func TestPrivacy_DenyMutationWithoutAuth(t *testing.T) {
	client := openTestClient(t)
	ctx := schema.EnforceUserPrivacyContext(context.Background())

	_, err := client.User.Create().
		SetName("Mallory").
		SetEmail("mallory@example.com").
		SetAge(30).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, privacy.Deny), "expected privacy.Deny, got %v", err)
}

// TestPrivacy_AllowMutationWithAuth verifies that the same enforced
// request succeeds when the auth marker is present.
func TestPrivacy_AllowMutationWithAuth(t *testing.T) {
	client := openTestClient(t)
	ctx := schema.AllowWriteContext(schema.EnforceUserPrivacyContext(context.Background()))

	u, err := client.User.Create().
		SetName("Alice").
		SetEmail("alice@example.com").
		SetAge(30).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	assert.NotZero(t, u.ID)
}

// TestPrivacy_QueryAllowedWithoutEnforce confirms that reads pass
// through without any effect when the request context does NOT opt
// into privacy enforcement. The query policy rule short-circuits to
// Skip on a bare context so existing tests (which use plain
// context.Background()) are unaffected.
func TestPrivacy_QueryAllowedWithoutEnforce(t *testing.T) {
	client := openTestClient(t)
	createUser(t, client, "Bob", "bob@example.com")

	count, err := client.User.Query().Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestPrivacy_QueryDeniedUnderEnforce pins that the QueryPolicy
// mirror of the MutationPolicy fires: under EnforceUserPrivacyContext
// without AllowWriteContext, a read returns privacy.Deny.
func TestPrivacy_QueryDeniedUnderEnforce(t *testing.T) {
	client := openTestClient(t)
	createUser(t, client, "Bob", "bob@example.com")

	ctx := schema.EnforceUserPrivacyContext(context.Background())
	_, err := client.User.Query().Count(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, privacy.Deny, "QueryPolicy should deny reads under enforce without allow")
}

// TestPrivacy_QueryAllowedUnderEnforceWithAllow pins that the
// combined EnforceUserPrivacyContext + AllowWriteContext markers
// pass the query policy chain and the read succeeds. This is the
// counterpart of TestPrivacy_AllowMutationWithAuth for the query
// side — same pattern, same contract.
func TestPrivacy_QueryAllowedUnderEnforceWithAllow(t *testing.T) {
	client := openTestClient(t)
	createUser(t, client, "Bob", "bob@example.com")

	ctx := schema.AllowWriteContext(schema.EnforceUserPrivacyContext(context.Background()))
	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestPrivacy_QueryFilterInjectsPredicate pins the row-level filter
// pattern from the original plan ("Query filter allowing only
// current user's posts"). The QueryPolicy's FilterFunc rule reads a
// context-provided name and injects a WHERE name = <value> into the
// query via the generated Filter() method — velox's
// privacy.Filter / privacy.Filterable machinery.
//
// This demonstrates that QueryRules can mutate queries (not just
// deny them), closing the last literal sub-item of the original
// session plan.
func TestPrivacy_QueryFilterInjectsPredicate(t *testing.T) {
	client := openTestClient(t)
	createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Bob", "bob@example.com")
	createUser(t, client, "Charlie", "charlie@example.com")

	// Without the filter context, all three users are visible.
	all, err := client.User.Query().All(context.Background())
	require.NoError(t, err)
	assert.Len(t, all, 3)

	// With the filter context set to "Alice", the QueryPolicy rule
	// injects a WHERE name = 'Alice' predicate.
	ctx := schema.FilterUserQueryToNameContext(context.Background(), "Alice")
	filtered, err := client.User.Query().All(ctx)
	require.NoError(t, err)
	require.Len(t, filtered, 1, "filter should have narrowed the result to Alice only")
	assert.Equal(t, "Alice", filtered[0].Name)
}

// TestPrivacy_MutationFilterInjectsPredicate pins the row-level filter
// pattern on mutations. It exercises the generated *UserMutation.Filter()
// + AddPredicate methods end-to-end via privacy.FilterFunc: the rule
// reads a context-provided name and injects WHERE name = <value> into
// an Update, limiting the set of affected rows.
//
// This is the mutation-side counterpart of
// TestPrivacy_QueryFilterInjectsPredicate. Without the generated
// Filter()/AddPredicate methods on mutations, privacy.FilterFunc would
// return Deny on every mutation because *UserMutation would not
// implement privacy.Filterable — regression guard for the gap closed
// in 2026-04-14.
func TestPrivacy_MutationFilterInjectsPredicate(t *testing.T) {
	client := openTestClient(t)
	createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Bob", "bob@example.com")
	createUser(t, client, "Charlie", "charlie@example.com")

	// Sanity: all three start at the default age (30 from createUser).
	all, err := client.User.Query().All(context.Background())
	require.NoError(t, err)
	require.Len(t, all, 3)

	// Under the mutation filter context, an Update that sets age=99
	// should only touch Alice — the FilterFunc rule narrows the
	// UPDATE's WHERE clause via the generated Filter() method.
	ctx := schema.FilterUserMutationToNameContext(context.Background(), "Alice")
	n, err := client.User.Update().SetAge(99).Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n, "filter should have narrowed the update to Alice only")

	after, err := client.User.Query().All(context.Background())
	require.NoError(t, err)
	for _, u := range after {
		if u.Name == "Alice" {
			assert.Equal(t, 99, u.Age, "Alice's age should have been updated")
		} else {
			assert.Equal(t, 30, u.Age, "%s's age should be unchanged", u.Name)
		}
	}
}

// TestPrivacy_ClonedQueryPathsRespectPolicy pins that every query entry
// point that internally routes through q.clone() still evaluates the
// privacy policy. Before the 2026-04-15 fix, clone() dropped the
// policy field, so First/Only/FirstID/OnlyID/Exist silently bypassed
// tenant filters while All/Count honored them. Any generator change
// that drops state from clone() trips this test at runtime.
func TestPrivacy_ClonedQueryPathsRespectPolicy(t *testing.T) {
	client := openTestClient(t)
	createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Bob", "bob@example.com")

	ctx := schema.EnforceUserPrivacyContext(context.Background())

	t.Run("First", func(t *testing.T) {
		_, err := client.User.Query().First(ctx)
		require.Error(t, err, "First must be denied under enforce-without-allow")
		assert.ErrorIs(t, err, privacy.Deny)
	})
	t.Run("Only", func(t *testing.T) {
		_, err := client.User.Query().Where().Only(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, privacy.Deny)
	})
	t.Run("FirstID", func(t *testing.T) {
		_, err := client.User.Query().FirstID(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, privacy.Deny)
	})
	t.Run("OnlyID", func(t *testing.T) {
		_, err := client.User.Query().OnlyID(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, privacy.Deny)
	})
	t.Run("Exist", func(t *testing.T) {
		_, err := client.User.Query().Exist(ctx)
		require.Error(t, err, "Exist routes through FirstID→clone; must surface Deny")
		assert.ErrorIs(t, err, privacy.Deny)
	})
}
