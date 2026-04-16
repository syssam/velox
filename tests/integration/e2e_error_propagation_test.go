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

// TestUpdate_BulkByPredicate_FiresHook pins the hook contract on
// multi-row Update. The adjacent-paths audit claimed that
// .Update().Where(...).Save(ctx) goes through velox.WithHooks
// cleanly, but the claim was not covered by a test — only UpdateOne
// had a hook test. If a future refactor separates the multi-row
// sqlSave from the hook wrap, this test fails loudly.
func TestUpdate_BulkByPredicate_FiresHook(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Amy", "amy@example.com")
	createUser(t, client, "Bob", "bob@example.com")

	var hookCalls int
	client.User.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			hookCalls++
			return next.Mutate(ctx, m)
		})
	})

	affected, err := client.User.Update().
		Where(user.NameField.HasPrefix("A")).
		AddAge(1).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, affected)
	assert.Equal(t, 1, hookCalls,
		"multi-row Update.Save must fire the hook once for the whole operation, not once per row")
}

// TestDelete_ByPredicate_FiresHook is the delete-side analog. Same
// rationale as TestUpdate_BulkByPredicate_FiresHook: the audit
// cleared the path but there was no test pinning the hook wrap.
func TestDelete_ByPredicate_FiresHook(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	createUser(t, client, "Alice", "alice@example.com")
	createUser(t, client, "Amy", "amy@example.com")
	createUser(t, client, "Bob", "bob@example.com")

	var hookCalls int
	client.User.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			hookCalls++
			return next.Mutate(ctx, m)
		})
	})

	affected, err := client.User.Delete().
		Where(user.NameField.HasPrefix("A")).
		Exec(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, affected)
	assert.Equal(t, 1, hookCalls,
		"multi-row Delete.Exec must fire the hook once for the whole operation")
}

// TestUpdate_HookErrorAbortsMutation pins that a hook returning an
// error aborts the mutation — no partial writes to the main row.
// The mechanism is velox.WithHooks propagating the error back
// through the chain; the SQL executes inside the innermost exec
// which the hook wraps. If a hook errors before exec, no SQL runs.
func TestUpdate_HookErrorAbortsMutation(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	alice := createUser(t, client, "Alice", "alice@example.com")
	originalAge := alice.Age

	sentinel := errors.New("hook aborted mutation")
	client.User.Use(func(next integration.Mutator) integration.Mutator {
		return integration.MutateFunc(func(ctx context.Context, m integration.Mutation) (integration.Value, error) {
			return nil, sentinel
		})
	})

	_, err := client.User.UpdateOneID(alice.ID).
		AddAge(10).
		SetUpdatedAt(now).
		Save(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel, "hook error must propagate verbatim through WithHooks")

	// Row in the DB must be unchanged.
	got, err := client.User.Get(context.Background(), alice.ID)
	require.NoError(t, err)
	assert.Equal(t, originalAge, got.Age,
		"hook-aborted UpdateOne must not have written the new age")
}

// TestInterceptor_ErrorAborts pins that an interceptor returning an
// error aborts the query and propagates the error verbatim. The fix
// is WithInterceptors unwrapping chain errors. The test fails loudly
// if error propagation silently swallows a non-nil Intercept return.
func TestInterceptor_ErrorAborts(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()
	createUser(t, client, "Alice", "alice@example.com")

	sentinel := errors.New("interceptor aborted query")
	client.Intercept(integration.InterceptFunc(func(next integration.Querier) integration.Querier {
		return integration.QuerierFunc(func(ctx context.Context, q integration.Query) (integration.Value, error) {
			return nil, sentinel
		})
	}))

	_, err := client.User.Query().All(ctx)
	require.Error(t, err)
	assert.ErrorIs(t, err, sentinel, "interceptor error must propagate to the caller")
}
