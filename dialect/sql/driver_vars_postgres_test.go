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

	currentOf := func(t *testing.T, ctx context.Context, q dialect.ExecQuerier, name string) string {
		t.Helper()
		rows := &Rows{}
		require.NoError(t, q.Query(ctx, "SELECT COALESCE(current_setting($1, true), '')", []any{name}, rows))
		require.True(t, rows.Next())
		var v string
		require.NoError(t, rows.Scan(&v))
		require.NoError(t, rows.Close())
		return v
	}
	current := func(t *testing.T, ctx context.Context, q dialect.ExecQuerier) string {
		t.Helper()
		return currentOf(t, ctx, q, "velox.tenant")
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

	t.Run("TxRollback", func(t *testing.T) {
		tx, err := drv.Tx(ctx)
		require.NoError(t, err)
		require.Equal(t, "t-3", current(t, WithVar(ctx, "velox.tenant", "t-3"), tx))
		require.Equal(t, "t-3", current(t, ctx, tx), "set_config(..., true) lasts until the end of the transaction")
		require.NoError(t, tx.Rollback())
		require.Empty(t, current(t, ctx, drv), "a variable set inside a transaction must not survive ROLLBACK")
	})

	// Custom settings must be dotted (e.g. app.tenant_id), and set_config
	// accepts any component, including SQL keywords. A reset spelled as SQL
	// text (RESET app.user) is a syntax error for those, which would leave
	// the value on the pooled connection for its next borrower.
	t.Run("KeywordComponent", func(t *testing.T) {
		for _, name := range []string{"app.user", "user.tenant_id", "select.from", "app.tenant.id"} {
			require.Equal(t, "v-1", currentOf(t, WithVar(ctx, name, "v-1"), drv, name), "name=%q", name)
			require.Empty(t, currentOf(t, ctx, drv, name), "name=%q must be reset before the connection returns to the pool", name)
		}
	})

	t.Run("InvalidIdentifier", func(t *testing.T) {
		rows := &Rows{}
		err := drv.Query(WithVar(ctx, "velox.tenant'; DROP TABLE x; --", "v"), "SELECT 1", []any{}, rows)
		require.ErrorContains(t, err, "invalid session variable name")
		require.Equal(t, "t-5", current(t, WithVar(ctx, "velox.tenant", "t-5"), drv))
		require.Empty(t, current(t, ctx, drv))
	})
}
