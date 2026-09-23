package sql

import (
	"context"
	stdsql "database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
)

// txScopedDriver mirrors the generated txDriver: statements run on the open
// transaction, and BaseDriver exposes the driver the transaction came from.
type txScopedDriver struct {
	tx   dialect.Tx
	base *Driver
}

func (d txScopedDriver) BaseDriver() dialect.Driver             { return d.base }
func (d txScopedDriver) Tx(context.Context) (dialect.Tx, error) { return dialect.NopTx(d), nil }
func (d txScopedDriver) Close() error                           { return nil }
func (d txScopedDriver) Dialect() string                        { return d.base.Dialect() }
func (d txScopedDriver) Query(ctx context.Context, q string, a, v any) error {
	return d.tx.Query(ctx, q, a, v)
}

func (d txScopedDriver) Exec(ctx context.Context, q string, a, v any) error {
	return d.tx.Exec(ctx, q, a, v)
}

// TestDriverCapabilities_RealMySQL_ProbesThroughOpenTx pins that the first
// capability probe made inside a transaction runs on the transaction's own
// connection. Probing through the base driver needs a second pooled
// connection, so on a pool of one — held by the transaction — it blocked
// until the context expired.
func TestDriverCapabilities_RealMySQL_ProbesThroughOpenTx(t *testing.T) {
	dsn := os.Getenv("VELOX_TEST_MYSQL")
	if dsn == "" {
		t.Skip("VELOX_TEST_MYSQL not set — skipping real MySQL test")
	}
	db, err := stdsql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	drv := OpenDB(dialect.MySQL, db)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := drv.Tx(ctx)
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	caps, err := dialect.DriverCapabilities(ctx, txScopedDriver{tx: tx, base: drv})
	require.NoError(t, err, "probe must not wait for a second pooled connection")
	// The answer is cached on the base driver for later callers.
	cached, err := drv.ServerCapabilities(ctx)
	require.NoError(t, err)
	require.Equal(t, caps, cached)
}

// TestDriverCapabilities_RealMySQL_TxProbeDoesNotWaitOnPoolProbe pins the
// reverse order of the deadlock above: a non-transactional caller starts the
// cold probe first and needs a pooled connection, while every connection is
// held by a transaction that then asks for capabilities too. The transaction
// must answer from its own connection instead of waiting for that probe.
func TestDriverCapabilities_RealMySQL_TxProbeDoesNotWaitOnPoolProbe(t *testing.T) {
	dsn := os.Getenv("VELOX_TEST_MYSQL")
	if dsn == "" {
		t.Skip("VELOX_TEST_MYSQL not set — skipping real MySQL test")
	}
	db, err := stdsql.Open("mysql", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	db.SetMaxOpenConns(1)
	drv := OpenDB(dialect.MySQL, db)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := drv.Tx(ctx)
	require.NoError(t, err)

	poolDone := make(chan error, 1)
	go func() {
		_, err := dialect.DriverCapabilities(ctx, drv)
		poolDone <- err
	}()
	time.Sleep(200 * time.Millisecond) // let the pool probe start and wait for a connection

	caps, err := dialect.DriverCapabilities(ctx, txScopedDriver{tx: tx, base: drv})
	require.NoError(t, err, "the transaction must not wait on a probe that needs its connection")
	require.NoError(t, tx.Rollback())
	require.NoError(t, <-poolDone, "the pool probe completes once the transaction releases its connection")
	cached, err := drv.ServerCapabilities(ctx)
	require.NoError(t, err)
	require.Equal(t, caps, cached)
}
