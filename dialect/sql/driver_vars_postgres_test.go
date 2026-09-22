package sql

import (
	"context"
	stdsql "database/sql"
	"os"
	"testing"

	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
)

// TestWithVars_RealPostgres exercises WithVar against a real Postgres server.
// go-sqlmock accepts any statement text, so it cannot tell that Postgres
// rejects bind parameters in a SET statement, nor that a plain SET inside a
// transaction outlives COMMIT on the pooled connection. Both are pinned here.
func TestWithVars_RealPostgres(t *testing.T) {
	dsn := os.Getenv("VELOX_TEST_POSTGRES")
	if dsn == "" {
		t.Skip("VELOX_TEST_POSTGRES not set — skipping real Postgres test")
	}
	db, err := stdsql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	// One pooled connection, so a leaked setting is guaranteed to be observed.
	db.SetMaxOpenConns(1)
	drv := OpenDB(dialect.Postgres, db)
	ctx := context.Background()

	current := func(t *testing.T, ctx context.Context, q dialect.ExecQuerier) string {
		t.Helper()
		rows := &Rows{}
		require.NoError(t, q.Query(ctx, "SELECT COALESCE(current_setting('velox.tenant', true), '')", []any{}, rows))
		require.True(t, rows.Next())
		var v string
		require.NoError(t, rows.Scan(&v))
		require.NoError(t, rows.Close())
		return v
	}

	t.Run("Session", func(t *testing.T) {
		require.Equal(t, "t-1", current(t, WithVar(ctx, "velox.tenant", "t-1"), drv))
		require.Empty(t, current(t, ctx, drv), "session variable must be reset before the connection returns to the pool")
	})

	t.Run("TxScoped", func(t *testing.T) {
		tx, err := drv.Tx(ctx)
		require.NoError(t, err)
		require.Equal(t, "t-2", current(t, WithVar(ctx, "velox.tenant", "t-2"), tx))
		require.NoError(t, tx.Exec(WithVar(ctx, "velox.tenant", "t-2"), "SELECT 1", []any{}, nil))
		require.NoError(t, tx.Commit())
		require.Empty(t, current(t, ctx, drv), "a variable set inside a transaction must not survive COMMIT")
	})
}
