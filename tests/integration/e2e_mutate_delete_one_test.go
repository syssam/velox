package integration_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/runtime"
	userclient "github.com/syssam/velox/tests/integration/client/user"
)

// TestMutate_DeleteOneDeletesOnlyThatRow pins that client.Mutate with an
// OpDeleteOne mutation deletes the row its ID names. mutate sent OpDelete
// and OpDeleteOne to the same bulk delete, which reads only predicates, so
// a mutation built with NewUserMutation(cfg, OpDeleteOne) + SetID — both
// exported — deleted every row in the table. DeleteOneID adds the ID
// predicate itself; mutate now does the same, rejects a DeleteOne without
// an ID, and reports NotFound when the row does not exist.
func TestMutate_DeleteOneDeletesOnlyThatRow(t *testing.T) {
	ctx := context.Background()
	c := openTestClient(t)
	alice := createUser(t, c, "alice", "alice@x")
	createUser(t, c, "bob", "bob@x")

	m := userclient.NewUserMutation(c.RuntimeConfig(), runtime.OpDeleteOne)
	m.SetID(alice.ID)
	_, err := c.Mutate(ctx, m)
	require.NoError(t, err)

	names, err := c.User.Query().Select("name").Strings(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"bob"}, names, "DeleteOne removed more than its row")

	missing := userclient.NewUserMutation(c.RuntimeConfig(), runtime.OpDeleteOne)
	missing.SetID(alice.ID)
	_, err = c.Mutate(ctx, missing)
	require.True(t, runtime.IsNotFound(err), "deleting an absent row: got %v", err)

	noID := userclient.NewUserMutation(c.RuntimeConfig(), runtime.OpDeleteOne)
	_, err = c.Mutate(ctx, noID)
	require.Error(t, err, "a DeleteOne without an ID must not run")
	n, err := c.User.Query().Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, n)
}
