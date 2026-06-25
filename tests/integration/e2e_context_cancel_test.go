package integration_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
	integration "github.com/syssam/velox/tests/integration"
)

// Production deadlines must abort in-flight velox work and surface a detectable
// context error. velox must NOT swallow ctx cancellation (e.g. by substituting
// context.Background() on any execution path) — if it did, a query the caller
// already gave up on would keep running and tie up a connection.
//
// These tests pin that the caller's context.Canceled / DeadlineExceeded
// propagates through reads, writes, and transaction begin as an errors.Is-matchable
// error.
func TestContextCancellation_Propagates(t *testing.T) {
	t.Parallel()

	open := func(t *testing.T) *integration.Client {
		t.Helper()
		client, err := integration.Open(dialect.SQLite, ":memory:?_pragma=foreign_keys(1)")
		require.NoError(t, err)
		t.Cleanup(func() { _ = client.Close() })
		require.NoError(t, client.Schema.Create(context.Background()))
		return client
	}

	t.Run("read path: canceled context surfaces as context.Canceled", func(t *testing.T) {
		t.Parallel()
		client := open(t)

		// Non-vacuous: the same query succeeds under a live context, so a
		// failure below can only be the cancellation.
		_, err := client.User.Query().All(context.Background())
		require.NoError(t, err)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = client.User.Query().All(ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled,
			"a canceled context must surface as context.Canceled, not be swallowed")
	})

	t.Run("read path: expired deadline surfaces as context.DeadlineExceeded", func(t *testing.T) {
		t.Parallel()
		client := open(t)

		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		_, err := client.User.Query().All(ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("write path: canceled context surfaces as context.Canceled", func(t *testing.T) {
		t.Parallel()
		client := open(t)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := client.User.Create().SetName("x").SetEmail("x@example.com").SetAge(1).Save(ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})

	t.Run("transaction begin: canceled context surfaces as context.Canceled", func(t *testing.T) {
		t.Parallel()
		client := open(t)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := client.Tx(ctx)
		require.Error(t, err)
		require.ErrorIs(t, err, context.Canceled)
	})
}
