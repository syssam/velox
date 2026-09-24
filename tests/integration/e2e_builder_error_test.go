package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect/sql"
	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// TestQuery_BuilderErrorsAreReturned pins that an error recorded while the
// SQL is built — an unknown column in an aggregate or an order term — is
// returned, not executed around. Nothing read selector.Err(), so
// GroupBy(...).Aggregate(Sum("bogus")) returned zeros with a nil error and
// Aggregate(Sum("bogus")).Int() fell back to selecting every column. Ent
// checks selector.Err() before running the query.
func TestQuery_BuilderErrorsAreReturned(t *testing.T) {
	ctx := context.Background()
	c := openTestClient(t)
	createUser(t, c, "alice", "alice@x")

	var rows []struct {
		Role string
		Sum  int
	}
	err := c.User.Query().GroupBy(user.FieldRole).Aggregate(integration.Sum("bogus")).Scan(ctx, &rows)
	require.ErrorContains(t, err, "bogus", "GroupBy aggregate: got rows %v", rows)

	_, err = c.User.Query().Aggregate(integration.Sum("bogus")).Int(ctx)
	require.ErrorContains(t, err, "bogus", "Aggregate")

	_, err = c.User.Query().Order(sql.OrderByField("bogus").ToFunc()).All(ctx)
	require.ErrorContains(t, err, "bogus", "Order on an unknown column")

	sum, err := c.User.Query().Aggregate(integration.Sum(user.FieldAge)).Int(ctx)
	require.NoError(t, err)
	require.Equal(t, 30, sum)
}
