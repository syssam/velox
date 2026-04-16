package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// TestError_IsNotFoundOnFirst verifies First() on an empty result produces
// an error that IsNotFound recognizes.
func TestError_IsNotFoundOnFirst(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, err := client.User.Query().First(ctx)
	require.Error(t, err)
	assert.True(t, integration.IsNotFound(err), "First() on empty must return NotFoundError")
}

// TestError_IsNotFoundOnGet verifies Get() for a missing ID produces a
// NotFoundError.
func TestError_IsNotFoundOnGet(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, err := client.User.Get(ctx, 999)
	require.Error(t, err)
	assert.True(t, integration.IsNotFound(err), "Get on missing id must return NotFoundError")
}

// TestError_MaskNotFoundMasks verifies MaskNotFound turns NotFoundError into
// nil while passing other errors through unchanged.
func TestError_MaskNotFoundMasks(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, getErr := client.User.Get(ctx, 999)
	require.Error(t, getErr)
	assert.Nil(t, integration.MaskNotFound(getErr), "MaskNotFound must swallow NotFoundError")

	// Non-NotFound errors are passed through. Create with a duplicate email
	// hits the unique constraint.
	createUser(t, client, "Alice", "dup@test.com")
	_, dupErr := client.User.Create().
		SetName("Alice2").
		SetEmail("dup@test.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.Error(t, dupErr)
	assert.NotNil(t, integration.MaskNotFound(dupErr), "MaskNotFound must pass through non-NotFound errors")
}

// TestError_IsNotSingular verifies Only() with multiple matches returns an
// error that IsNotSingular recognizes.
func TestError_IsNotSingular(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "a@test.com")
	createUser(t, client, "Bob", "b@test.com")

	_, err := client.User.Query().Only(ctx)
	require.Error(t, err)
	assert.True(t, integration.IsNotSingular(err), "Only() with >1 match must return NotSingularError")
}

// TestError_IsConstraintErrorOnDuplicate verifies that violating a UNIQUE
// constraint produces a ConstraintError.
func TestError_IsConstraintErrorOnDuplicate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "same@test.com")
	_, err := client.User.Create().
		SetName("AliceDup").
		SetEmail("same@test.com").
		SetAge(25).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.Error(t, err)
	assert.True(t, integration.IsConstraintError(err), "duplicate email must produce ConstraintError")
}
