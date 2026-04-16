package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/user"
)

// TestTransaction_Commit verifies a committed transaction persists data.
func TestTransaction_Commit(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	_, err = tx.User.Create().
		SetName("InTx").
		SetEmail("tx@test.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count)
}

// TestTransaction_Rollback verifies a rolled-back transaction discards changes.
func TestTransaction_Rollback(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	_, err = tx.User.Create().
		SetName("WillRollback").
		SetEmail("rollback@test.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Rollback())

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

// TestTransaction_BeginTxWithOptions verifies BeginTx accepts sql.TxOptions
// and returns a usable transaction.
func TestTransaction_BeginTxWithOptions(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.BeginTx(ctx, &sql.TxOptions{ReadOnly: false})
	require.NoError(t, err)

	_, err = tx.User.Create().
		SetName("BeginTx").
		SetEmail("begintx@test.com").
		SetAge(25).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	exists, err := client.User.Query().Where(user.EmailField.EQ("begintx@test.com")).Exist(ctx)
	require.NoError(t, err)
	assert.True(t, exists)
}

// TestTransaction_NestedTxRejected verifies that calling Tx() on a txDriver
// returns an error (no nested transactions).
func TestTransaction_NestedTxRejected(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)
	defer tx.Rollback()

	// Using Tx.Client() binds a Client to the tx driver; attempting to
	// start another Tx on it should fail.
	inner := tx.Client()
	_, err = inner.Tx(ctx)
	assert.Error(t, err, "nested Tx must be rejected")
}

// TestTransaction_OnCommitHook verifies OnCommit hooks fire on successful commit.
func TestTransaction_OnCommitHook(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	var hookFired bool
	tx.OnCommit(func(next integration.Committer) integration.Committer {
		return integration.CommitFunc(func(ctx context.Context, tx *integration.Tx) error {
			hookFired = true
			return next.Commit(ctx, tx)
		})
	})

	_, err = tx.User.Create().
		SetName("HookCommit").
		SetEmail("hookcommit@test.com").
		SetAge(30).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())
	assert.True(t, hookFired, "OnCommit hook must fire on Commit()")
}

// TestTransaction_OnRollbackHook verifies OnRollback hooks fire on Rollback.
func TestTransaction_OnRollbackHook(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	var hookFired bool
	tx.OnRollback(func(next integration.Rollbacker) integration.Rollbacker {
		return integration.RollbackFunc(func(ctx context.Context, tx *integration.Tx) error {
			hookFired = true
			return next.Rollback(ctx, tx)
		})
	})

	require.NoError(t, tx.Rollback())
	assert.True(t, hookFired, "OnRollback hook must fire on Rollback()")
}

// TestTransaction_WithTxCommits verifies WithTx commits on successful callback.
func TestTransaction_WithTxCommits(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	err := integration.WithTx(ctx, client, func(tx *integration.Tx) error {
		_, err := tx.User.Create().
			SetName("WithTxUser").
			SetEmail("withtx@test.com").
			SetAge(30).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		return err
	})
	require.NoError(t, err)

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, count, "WithTx must commit on success")
}

// TestTransaction_WithTxRollbackOnError verifies WithTx rolls back when the
// callback returns an error.
func TestTransaction_WithTxRollbackOnError(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	sentinel := errors.New("callback failed")
	err := integration.WithTx(ctx, client, func(tx *integration.Tx) error {
		_, err := tx.User.Create().
			SetName("WillVanish").
			SetEmail("vanish@test.com").
			SetAge(30).
			SetRole(user.RoleUser).
			SetCreatedAt(now).
			SetUpdatedAt(now).
			Save(ctx)
		if err != nil {
			return err
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "WithTx must rollback when callback errors")
}

// TestTransaction_WithTxRollbackOnPanic verifies WithTx rolls back and
// re-raises the panic when the callback panics.
func TestTransaction_WithTxRollbackOnPanic(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	assert.Panics(t, func() {
		_ = integration.WithTx(ctx, client, func(tx *integration.Tx) error {
			_, _ = tx.User.Create().
				SetName("PanicUser").
				SetEmail("panic@test.com").
				SetAge(30).
				SetRole(user.RoleUser).
				SetCreatedAt(now).
				SetUpdatedAt(now).
				Save(ctx)
			panic("boom")
		})
	})

	count, err := client.User.Query().Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, count, "WithTx must rollback when callback panics")
}
