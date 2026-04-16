package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	"github.com/syssam/velox/tests/integration/user"
)

// TestSelect_StringsSingleField verifies Select(field).Strings(ctx) returns
// the raw column values projected into a []string.
func TestSelect_StringsSingleField(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "a@test.com")
	createUser(t, client, "Bob", "b@test.com")
	createUser(t, client, "Charlie", "c@test.com")

	names, err := client.User.Query().
		Order(user.ByName(sql.OrderAsc())).
		Select(user.FieldName).
		Strings(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"Alice", "Bob", "Charlie"}, names)
}

// TestSelect_IntsSingleField verifies Select(field).Ints(ctx) returns
// numeric column values projected into a []int.
func TestSelect_IntsSingleField(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	// Helper defaults to age=30; override explicitly for deterministic ordering.
	for i, age := range []int{10, 20, 30} {
		_, err := client.User.Create().
			SetName("u" + string(rune('A'+i))).
			SetEmail("u" + string(rune('a'+i)) + "@test.com").
			SetAge(age).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		require.NoError(t, err)
	}

	ages, err := client.User.Query().
		Order(user.ByAge(sql.OrderAsc())).
		Select(user.FieldAge).
		Ints(ctx)
	require.NoError(t, err)
	assert.Equal(t, []int{10, 20, 30}, ages)
}

// TestSelect_StringSingular verifies Select(field).String(ctx) on a single-row
// query returns the single value.
func TestSelect_StringSingular(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "Alice", "a@test.com")

	name, err := client.User.Query().Select(user.FieldName).String(ctx)
	require.NoError(t, err)
	assert.Equal(t, "Alice", name)
}

// TestSelect_StringSingularNotFound verifies Select(field).String(ctx) on an
// empty query returns a NotFoundError.
func TestSelect_StringSingularNotFound(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	_, err := client.User.Query().Select(user.FieldName).String(ctx)
	require.Error(t, err)
}

// TestSelect_StringSingularNotSingular verifies Select(field).String(ctx) on a
// multi-row query returns a NotSingularError.
func TestSelect_StringSingularNotSingular(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "Alice", "a@test.com")
	createUser(t, client, "Bob", "b@test.com")

	_, err := client.User.Query().Select(user.FieldName).String(ctx)
	require.Error(t, err)
}

// TestModify_Query verifies Modify() applies a raw *sql.Selector modifier to
// the query pipeline.
func TestModify_Query(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "a@test.com")
	createUser(t, client, "Bob", "b@test.com")
	createUser(t, client, "Charlie", "c@test.com")

	// Modify by applying a raw LIMIT via the selector — exercises the Modify
	// path without depending on any vendor-specific SQL syntax.
	users, err := client.User.Query().
		Order(user.ByName(sql.OrderAsc())).
		Modify(func(s *sql.Selector) {
			s.Limit(1)
		}).
		All(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	assert.Equal(t, "Alice", users[0].Name)
}
