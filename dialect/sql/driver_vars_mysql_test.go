package sql

import (
	"context"
	stdsql "database/sql"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
)

// TestWithVars_RealMySQL exercises WithVar against a real MySQL server.
// MySQL user variables are session-scoped and NOT transactional: neither
// COMMIT nor ROLLBACK clears them, so a value set inside a transaction stays
// on the pooled connection unless velox resets it. go-sqlmock cannot observe
// that, so it is pinned here.
func TestWithVars_RealMySQL(t *testing.T) {
	dsn := os.Getenv("VELOX_TEST_MYSQL")
	if dsn == "" {
		t.Skip("VELOX_TEST_MYSQL not set — skipping real MySQL test")
	}
	db, err := stdsql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	// One pooled connection, so a leaked variable is guaranteed to be observed.
	db.SetMaxOpenConns(1)
	drv := OpenDB(dialect.MySQL, db)
	ctx := context.Background()

	// current reads @<name>; ok is false when the variable is NULL (unset).
	current := func(t *testing.T, ctx context.Context, q dialect.ExecQuerier, name string) (string, bool) {
		t.Helper()
		rows := &Rows{}
		require.NoError(t, q.Query(ctx, "SELECT @"+name, []any{}, rows))
		require.True(t, rows.Next())
		var v stdsql.NullString
		require.NoError(t, rows.Scan(&v))
		require.NoError(t, rows.Close())
		return v.String, v.Valid
	}
	requireUnset := func(t *testing.T, name, msg string) {
		t.Helper()
		v, ok := current(t, ctx, drv, name)
		require.False(t, ok, "%s: @%s = %q", msg, name, v)
	}

	t.Run("Session", func(t *testing.T) {
		v, ok := current(t, WithVar(ctx, "velox_tenant", "t-1"), drv, "velox_tenant")
		require.True(t, ok)
		require.Equal(t, "t-1", v)
		requireUnset(t, "velox_tenant", "session variable must be reset before the connection returns to the pool")
	})

	t.Run("SessionExec", func(t *testing.T) {
		require.NoError(t, drv.Exec(WithVar(ctx, "velox_tenant", "t-1"), "DO 1", []any{}, nil))
		requireUnset(t, "velox_tenant", "Exec must reset the variable too")
	})

	t.Run("DottedName", func(t *testing.T) {
		v, ok := current(t, WithVar(ctx, "velox.tenant", "t-dot"), drv, "velox.tenant")
		require.True(t, ok)
		require.Equal(t, "t-dot", v)
		requireUnset(t, "velox.tenant", "a dotted variable must be reset too")
	})

	t.Run("ValueIsParameter", func(t *testing.T) {
		const tricky = `it's a \ "value"; DROP TABLE x --`
		v, ok := current(t, WithVar(ctx, "velox_tenant", tricky), drv, "velox_tenant")
		require.True(t, ok)
		require.Equal(t, tricky, v)
		requireUnset(t, "velox_tenant", "variable must be reset")
	})

	t.Run("FailedStatementStillResets", func(t *testing.T) {
		err := drv.Exec(WithVar(ctx, "velox_tenant", "t-err"), "SELECT * FROM velox_no_such_table", []any{}, nil)
		require.Error(t, err)
		rows := &Rows{}
		err = drv.Query(WithVar(ctx, "velox_tenant", "t-err"), "SELECT * FROM velox_no_such_table", []any{}, rows)
		require.Error(t, err)
		requireUnset(t, "velox_tenant", "a failed statement must still reset the variable")
	})

	for _, end := range []struct {
		name string
		fn   func(dialect.Tx) error
	}{
		{"Commit", func(tx dialect.Tx) error { return tx.Commit() }},
		{"Rollback", func(tx dialect.Tx) error { return tx.Rollback() }},
	} {
		t.Run("Tx"+end.name, func(t *testing.T) {
			tx, err := drv.Tx(ctx)
			require.NoError(t, err)
			txCtx := WithVar(ctx, "velox_tenant", "t-2")
			v, ok := current(t, txCtx, tx, "velox_tenant")
			require.True(t, ok)
			require.Equal(t, "t-2", v)
			require.NoError(t, tx.Exec(txCtx, "DO 1", []any{}, nil))
			v, ok = current(t, txCtx, tx, "velox_tenant")
			require.True(t, ok, "a variable set via WithVar must be visible to every statement in the tx that carries it")
			require.Equal(t, "t-2", v)
			require.NoError(t, end.fn(tx))
			requireUnset(t, "velox_tenant", "a variable set inside a transaction must not survive "+end.name)
		})
	}

	t.Run("TxFailedStatementThenRollback", func(t *testing.T) {
		tx, err := drv.Tx(ctx)
		require.NoError(t, err)
		err = tx.Exec(WithVar(ctx, "velox_tenant", "t-3"), "SELECT * FROM velox_no_such_table", []any{}, nil)
		require.Error(t, err)
		require.NoError(t, tx.Rollback())
		requireUnset(t, "velox_tenant", "a failed statement inside a rolled-back tx must not leak the variable")
	})

	t.Run("InvalidIdentifier", func(t *testing.T) {
		for _, name := range []string{"x = 1; DROP TABLE users; --", "x`y", "tenant id", ""} {
			rows := &Rows{}
			err := drv.Query(WithVar(ctx, name, "v"), "SELECT 1", []any{}, rows)
			require.ErrorContains(t, err, "invalid session variable name", "name=%q", name)
		}
		// The rejected call must not have poisoned the single pooled connection.
		v, ok := current(t, WithVar(ctx, "velox_tenant", "t-4"), drv, "velox_tenant")
		require.True(t, ok)
		require.Equal(t, "t-4", v)
		requireUnset(t, "velox_tenant", "variable must be reset")
	})
}
