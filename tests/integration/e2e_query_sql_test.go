package integration_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/tests/integration/user"
)

// TestQuerySQL_WithWhereAndLimit verifies SQL() returns the generated query
// string and args without executing.
func TestQuerySQL_WithWhereAndLimit(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	query, args, err := client.User.Query().
		Where(user.NameField.EQ("Alice")).
		Limit(10).
		SQL(ctx)
	require.NoError(t, err)

	// The SQL string should contain SELECT, the table name, and LIMIT.
	assert.True(t, strings.Contains(query, "SELECT"), "SQL should contain SELECT")
	assert.True(t, strings.Contains(query, "users"), "SQL should contain table name")
	assert.True(t, strings.Contains(query, "LIMIT"), "SQL should contain LIMIT")

	// Args should contain the predicate value.
	require.NotEmpty(t, args, "should have at least one arg for predicate value")
	assert.Equal(t, "Alice", args[0], "first arg should be predicate value")
}

// TestQuerySQL_EmptyQuery verifies SQL() on a bare query returns valid SQL
// with no predicate args.
func TestQuerySQL_EmptyQuery(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	query, args, err := client.User.Query().SQL(ctx)
	require.NoError(t, err)

	assert.True(t, strings.Contains(query, "SELECT"), "SQL should contain SELECT")
	assert.True(t, strings.Contains(query, "users"), "SQL should contain table name")
	assert.Empty(t, args, "empty query should have no args")
}
