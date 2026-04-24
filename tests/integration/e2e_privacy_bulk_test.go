package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/privacy"
	userclient "github.com/syssam/velox/tests/integration/client/user"
	"github.com/syssam/velox/tests/integration/user"
	schema "github.com/syssam/velox/testschema"
)

// TestPrivacy_DenyBulkCreateWithoutAuth pins that the User mutation
// policy fires on bulk create the same way it fires on single-row
// Create. The existing TestPrivacy_DenyMutationWithoutAuth covered
// only client.User.Create(); this test uses MapCreateBulk and
// asserts the policy's MutationRule runs inside the bulk mutator
// chain so the whole bulk aborts with privacy.Deny.
func TestPrivacy_DenyBulkCreateWithoutAuth(t *testing.T) {
	client := openTestClient(t)
	ctx := schema.EnforceUserPrivacyContext(context.Background())

	names := []string{"Mallory", "Oscar"}
	_, err := client.User.MapCreateBulk(names, func(c *userclient.UserCreate, i int) {
		c.SetName(names[i]).
			SetEmail(names[i] + "@test.com").
			SetAge(30).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now)
	}).Save(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, privacy.Deny),
		"bulk create under enforced privacy must surface privacy.Deny, got %v", err)

	// No rows should have been inserted.
	count, err := client.User.Query().Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 0, count, "no rows should be created when bulk mutation is denied")
}

// TestPrivacy_DenyMultiRowUpdateWithoutAuth pins the same policy
// guarantee for multi-row Update. Uses AllowWriteContext to
// pre-seed rows (because createUser goes through the enforced
// context via the helper), then flips enforcement on and tries a
// .Update().Where(...).Save(ctx) — must surface privacy.Deny.
func TestPrivacy_DenyMultiRowUpdateWithoutAuth(t *testing.T) {
	client := openTestClient(t)

	// Pre-seed without enforcement.
	_, err := client.User.Create().
		SetName("Alice").
		SetEmail("alice@test.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(context.Background())
	require.NoError(t, err)
	_, err = client.User.Create().
		SetName("Amy").
		SetEmail("amy@test.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(context.Background())
	require.NoError(t, err)

	// Enforce privacy and attempt a multi-row update.
	ctx := schema.EnforceUserPrivacyContext(context.Background())
	_, err = client.User.Update().
		Where(user.NameField.HasPrefix("A")).
		AddAge(1).
		SetUpdatedAt(now).
		Save(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, privacy.Deny),
		"multi-row update under enforced privacy must surface privacy.Deny, got %v", err)

	// Rows must be unchanged.
	alice, err := client.User.Query().Where(user.NameField.EQ("Alice")).Only(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 30, alice.Age, "denied update must not have incremented Alice's age")
}

// TestPrivacy_DenyMultiRowDeleteWithoutAuth is the delete-side
// analog. Pre-seeds two rows, flips enforcement on, calls
// .Delete().Where(...).Exec(ctx) — must surface privacy.Deny.
func TestPrivacy_DenyMultiRowDeleteWithoutAuth(t *testing.T) {
	client := openTestClient(t)

	_, err := client.User.Create().
		SetName("Alice").
		SetEmail("alice@test.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(context.Background())
	require.NoError(t, err)
	_, err = client.User.Create().
		SetName("Amy").
		SetEmail("amy@test.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(context.Background())
	require.NoError(t, err)

	ctx := schema.EnforceUserPrivacyContext(context.Background())
	_, err = client.User.Delete().
		Where(user.NameField.HasPrefix("A")).
		Exec(ctx)
	require.Error(t, err)
	assert.True(t, errors.Is(err, privacy.Deny),
		"multi-row delete under enforced privacy must surface privacy.Deny, got %v", err)

	// Both rows must still exist.
	count, err := client.User.Query().Where(user.NameField.HasPrefix("A")).Count(context.Background())
	require.NoError(t, err)
	assert.Equal(t, 2, count, "denied delete must not have removed any rows")
}

// TestPrivacy_AllowBulkCreateWithAuth is the positive counterpart:
// the same bulk create succeeds when the context carries the
// AllowWrite marker. Closes the loop on bulk + privacy coverage.
func TestPrivacy_AllowBulkCreateWithAuth(t *testing.T) {
	client := openTestClient(t)
	ctx := schema.AllowWriteContext(schema.EnforceUserPrivacyContext(context.Background()))

	names := []string{"Alice", "Bob"}
	users, err := client.User.MapCreateBulk(names, func(c *userclient.UserCreate, i int) {
		c.SetName(names[i]).
			SetEmail(names[i] + "@test.com").
			SetAge(30).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now)
	}).Save(ctx)
	require.NoError(t, err)
	require.Len(t, users, 2)
	for _, u := range users {
		assert.NotZero(t, u.ID)
	}
}
