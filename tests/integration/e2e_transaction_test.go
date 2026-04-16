package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	integration "github.com/syssam/velox/tests/integration"
	"github.com/syssam/velox/tests/integration/entity"
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

// TestEntityUnwrap_PanicsOnNonTx pins the Ent-parity contract: Unwrap only
// makes sense for entities produced inside a transaction. Calling it on a
// bare entity (zero config.Driver, no *txDriver to swap) panics.
func TestEntityUnwrap_PanicsOnNonTx(t *testing.T) {
	u := &entity.User{ID: 1, Name: "Alice"}
	assert.Panics(t, func() { u.Unwrap() })

	tg := &entity.Tag{ID: 1, Name: "golang"}
	assert.Panics(t, func() { tg.Unwrap() })
}

// TestTransaction_UnwrapAllowsPostCommitEdgeRead pins the core Unwrap
// contract: after Unwrap(), a tx-returned entity can be used for edge
// reads without "sql: transaction has already been committed". Unwrap
// swaps entity.config.Driver from the committed *txDriver to the base
// driver. Without this, edges fail silently at read time in code paths
// far from the tx boundary (e.g. GraphQL resolvers).
func TestTransaction_UnwrapAllowsPostCommitEdgeRead(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	u, err := tx.User.Create().
		SetName("TxUnwrap").
		SetEmail("txu@test.com").
		SetAge(31).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	posts, err := u.Unwrap().QueryPosts().All(ctx)
	require.NoError(t, err)
	assert.Empty(t, posts, "freshly-created user has no posts")
}

// TestTransaction_WithoutUnwrapFailsAfterCommit is the inverse guardrail
// for Unwrap: without it, reading edges through a tx-returned entity
// after Commit must surface a clear error rather than succeed against
// stale driver state. If this test ever starts passing silently, the
// committed-*txDriver read path is unsafe.
func TestTransaction_WithoutUnwrapFailsAfterCommit(t *testing.T) {
	client := openTestClient(t)
	ctx := context.Background()

	tx, err := client.Tx(ctx)
	require.NoError(t, err)

	u, err := tx.User.Create().
		SetName("NoUnwrap").
		SetEmail("nou@test.com").
		SetAge(32).
		SetRole(user.RoleUser).
		SetCreatedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	require.NoError(t, err)

	require.NoError(t, tx.Commit())

	_, err = u.QueryPosts().All(ctx)
	require.Error(t, err, "reading via committed tx driver must fail without Unwrap")
}
