package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// TestCreateBulk verifies MapCreateBulk with a slice callback.
func TestCreateBulk(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	names := []string{"Alice", "Bob", "Charlie"}
	emails := []string{"a@test.com", "b@test.com", "c@test.com"}
	ages := []int{30, 25, 40}

	users, err := client.User.MapCreateBulk(names, func(c *user.UserCreate, i int) {
		c.SetName(names[i]).
			SetEmail(emails[i]).
			SetAge(ages[i]).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now)
	}).Save(ctx)
	require.NoError(t, err)
	assert.Len(t, users, 3)
	for _, u := range users {
		assert.NotZero(t, u.ID)
	}

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)
}

// TestUpdate_AddNumericField verifies numeric increment on update.
func TestUpdate_AddNumericField(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")
	assert.Equal(t, 30, alice.Age)

	_, err := client.User.UpdateOneID(alice.ID).
		AddAge(5).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	got, err := client.User.Get(ctx, alice.ID)
	require.NoError(t, err)
	assert.Equal(t, 35, got.Age)
}

// TestUpdate_BulkByPredicate verifies bulk update with where predicate.
func TestUpdate_BulkByPredicate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Amy", "amy@example.com")
	createUser(t, client, "Bob", "bob@example.com")

	affected, err := client.User.Update().
		Where(user.NameField.HasPrefix("A")).
		AddAge(1).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, affected)
}

// TestDelete_ByPredicate verifies bulk delete with where predicate.
func TestDelete_ByPredicate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Bob", "bob@example.com")
	createUser(t, client, "Charlie", "charlie@example.com")

	affected, err := client.User.Delete().
		Where(user.NameField.HasPrefix("B")).
		Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, affected)

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, count)
}

// TestMutation_OldValueOnUpdate verifies the Mutation interface in a hook.
func TestMutation_OldValueOnUpdate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")

	var newName string
	client.User.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			if m.Op() == integration.OpUpdateOne {
				if v, ok := m.Field("name"); ok {
					newName, _ = v.(string)
				}
			}
			return next.Mutate(ctx, m)
		})
	})

	_, err := client.User.UpdateOneID(alice.ID).
		SetName("AliceUpdated").
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, "AliceUpdated", newName)
}

// TestUpdateOne_ReturnsFullyPopulatedEntity pins the post-2026-04-15
// contract: UpdateOne.Save returns an entity with ALL columns populated,
// not just the ones passed to SetXxx and not zero values for untouched
// fields. Guards against regressions if the RETURNING single-path refactor
// drops a column from the callbacks.
func TestUpdateOne_ReturnsFullyPopulatedEntity(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	original := createUser(t, client, "alice", "alice@example.com")
	require.NotZero(t, original.ID)

	updated, err := client.User.UpdateOneID(original.ID).
		SetName("alice-v2").
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	assert.Equal(t, original.ID, updated.ID, "ID must survive update")
	assert.Equal(t, "alice-v2", updated.Name, "SetXxx field must be in returned entity")
	assert.Equal(t, "alice@example.com", updated.Email, "untouched field must survive update")
	assert.Equal(t, original.Age, updated.Age, "untouched Age must survive update")
	assert.Equal(t, original.Role, updated.Role, "untouched Role must survive update")
	// Config.Driver must be set so edge resolvers work.
	assert.NotNil(t, updated.Config().Driver, "returned entity must carry Config.Driver")
}
