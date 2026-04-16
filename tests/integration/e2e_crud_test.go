package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/tests/integration/user"
)

// TestCreate verifies basic create + read back.
func TestCreate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Alice", "alice@example.com")
	assert.Equal(t, "Alice", u.Name)
	assert.Equal(t, "alice@example.com", u.Email)
	assert.Equal(t, 30, u.Age)
	assert.NotZero(t, u.ID)

	got, err := client.User.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "Alice", got.Name)
}

// TestQueryWithPredicate verifies filtered queries.
func TestQueryWithPredicate(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Bob", "bob@example.com")

	users, err := client.User.Query().
		Where(user.NameField.EQ("Alice")).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, alice.ID, users[0].ID)
}

// TestUpdateOne verifies updating a single entity.
func TestUpdateOne(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Bob", "bob@example.com")

	_, err := client.User.UpdateOneID(u.ID).
		SetName("Bobby").
		Save(ctx)
	require.NoError(t, err)

	got, err := client.User.Get(ctx, u.ID)
	require.NoError(t, err)
	assert.Equal(t, "Bobby", got.Name)
}

// TestDeleteOne verifies deleting a single entity.
func TestDeleteOne(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	u := createUser(t, client, "Carol", "carol@example.com")

	err := client.User.DeleteOneID(u.ID).Exec(ctx)
	require.NoError(t, err)

	_, err = client.User.Get(ctx, u.ID)
	assert.Error(t, err, "deleted user should not be found")
}

// TestCount verifies Count() with and without predicates.
func TestCount(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Bob", "bob@example.com")
	createUser(t, client, "Charlie", "charlie@example.com")

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, count)

	count, err = client.User.Query().
		Where(user.NameField.HasPrefix("A")).
		Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}
