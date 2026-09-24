package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/runtime"
	"github.com/syssam/velox/tests/integration/user"
)

// TestSelectAndGroupBy_RejectUnknownFields pins that Select and GroupBy
// validate field names before any SQL is built. prepareQuery never checked
// them, so a name reached the SELECT list verbatim: an unknown field failed
// only at the driver, and an expression ran as written —
// Select("(SELECT group_concat(email) FROM users)") returned every user's
// email. Callers that pass a client-chosen field name (a GraphQL or REST
// "fields" parameter) turned that into SQL injection. Ent rejects unknown
// fields in prepareQuery.
func TestSelectAndGroupBy_RejectUnknownFields(t *testing.T) {
	ctx := context.Background()
	c := openTestClient(t)
	createUser(t, c, "alice", "alice@x")
	const injected = "(SELECT group_concat(email) FROM users)"

	_, err := c.User.Query().Select("bogus").All(ctx)
	require.True(t, runtime.IsValidationError(err), "unknown field: got %v", err)

	got, err := c.User.Query().Select(injected).Strings(ctx)
	require.True(t, runtime.IsValidationError(err), "expression as field: got %v %v", got, err)

	_, err = c.User.Query().Select(user.FieldName, injected).All(ctx)
	require.True(t, runtime.IsValidationError(err), "expression after a valid field: got %v", err)

	var rows []struct{ Role string }
	err = c.User.Query().GroupBy(injected).Scan(ctx, &rows)
	require.True(t, runtime.IsValidationError(err), "GroupBy expression: got %v", err)

	names, err := c.User.Query().Select(user.FieldName).Strings(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"alice"}, names)
}
