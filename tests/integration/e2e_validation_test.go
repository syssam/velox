package integration_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// TestValidationError_StructuredFields verifies that a ValidationError returned
// by a generated check() method carries Entity and Field alongside the legacy
// Name field.
func TestValidationError_StructuredFields(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Omit the required "name" field — SetEmail and SetRole are provided so
	// the first check failure is on "name".
	_, err := client.User.Create().
		SetEmail("alice@example.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.Error(t, err)

	var ve *integration.ValidationError
	require.True(t, errors.As(err, &ve), "error must be a *ValidationError")
	assert.Equal(t, "User", ve.Entity, "Entity must be the entity type name")
	assert.Equal(t, "name", ve.Field, "Field must be the missing field name")
	assert.Equal(t, "name", ve.Name, "Name (legacy) must match Field")
}

// TestValidationError_EdgeMissing verifies that a required-edge
// ValidationError also carries Entity and Field.
func TestValidationError_EdgeMissing(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Comment requires author and post edges. Provide content but omit both
	// edges — first failure should be on "author".
	_, err := client.Comment.Create().
		SetContent("hello").
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.Error(t, err)

	var ve *integration.ValidationError
	require.True(t, errors.As(err, &ve), "error must be a *ValidationError")
	assert.Equal(t, "Comment", ve.Entity)
	assert.Equal(t, "post", ve.Field, "Field must be the first missing required edge")
	assert.Equal(t, "post", ve.Name, "Name (legacy) must match Field")
}
