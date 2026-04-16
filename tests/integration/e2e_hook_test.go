package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// TestHooks_RootClientSharedWithEntity verifies that hooks registered on the
// root client (client.Use) reach entity builders via the shared HookStore.
// This validates the Ent-style shared pointer design.
func TestHooks_RootClientSharedWithEntity(t *testing.T) {
	client := openTestClient(t)

	var hookCalled bool
	client.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			hookCalled = true
			return next.Mutate(ctx, m)
		})
	})

	ctx := context.Background()
	_, err := client.User.Create().
		SetName("HookTest").
		SetEmail("hook@test.com").
		SetAge(25).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	assert.True(t, hookCalled, "root hook should fire through entity client")
}
