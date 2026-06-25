package sql

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"
)

// These tests pin velox's production observability contract: an externally
// instrumented *sql.DB (e.g. wrapped with otelsql) handed to OpenDB sees every
// statement velox issues, because velox routes all SQL through the standard
// database/sql ExecContext/QueryContext methods. This is the same interposition
// point Ent's documented tracing path relies on. If a future refactor bypassed
// the database/sql layer, the documented "wrap your *sql.DB with otelsql"
// recipe would silently stop tracing — these tests fail first.

// callRecorder records the SQL statements that reach the database/sql driver
// layer — the exact point where otelsql and any other database/sql
// instrumentation interposes.
type callRecorder struct {
	mu      sync.Mutex
	queries []string
	execs   []string
}

func (r *callRecorder) recordQuery(q string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.queries = append(r.queries, q)
}

func (r *callRecorder) recordExec(q string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.execs = append(r.execs, q)
}

func (r *callRecorder) snapshot() (queries, execs []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.queries...), append([]string(nil), r.execs...)
}

// instrumentDriver is a minimal database/sql driver that records every
// statement routed through it, modeling how otelsql wraps the underlying driver.
type instrumentDriver struct{ rec *callRecorder }

func (d *instrumentDriver) Open(string) (driver.Conn, error) {
	return &instrumentConn{rec: d.rec}, nil
}

// instrumentConn implements driver.Conn plus the *Context fast paths, so
// database/sql dispatches Exec/Query straight to them (the otelsql seam).
type instrumentConn struct{ rec *callRecorder }

func (c *instrumentConn) Prepare(string) (driver.Stmt, error) {
	return nil, fmt.Errorf("instrumentConn: Prepare not supported (use *Context paths)")
}
func (c *instrumentConn) Close() error              { return nil }
func (c *instrumentConn) Begin() (driver.Tx, error) { return instrumentTx{}, nil }

func (c *instrumentConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	c.rec.recordQuery(query)
	return &emptyRows{}, nil
}

func (c *instrumentConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	c.rec.recordExec(query)
	return driver.RowsAffected(0), nil
}

type instrumentTx struct{}

func (instrumentTx) Commit() error   { return nil }
func (instrumentTx) Rollback() error { return nil }

type emptyRows struct{}

func (*emptyRows) Columns() []string         { return nil }
func (*emptyRows) Close() error              { return nil }
func (*emptyRows) Next([]driver.Value) error { return io.EOF }

// drvCounter gives each registered driver a unique name, since
// database/sql.Register panics on a duplicate name.
var drvCounter atomic.Int64

// newInstrumentedDriver registers a fresh recording driver and returns a velox
// Driver built from it via OpenDB — the exact production wiring.
func newInstrumentedDriver(t *testing.T) (*callRecorder, *Driver) {
	t.Helper()
	rec := &callRecorder{}
	name := fmt.Sprintf("velox-observe-%d", drvCounter.Add(1))
	sql.Register(name, &instrumentDriver{rec: rec})
	db, err := sql.Open(name, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return rec, OpenDB(dialect.SQLite, db)
}

func TestOpenDB_RoutesSQLThroughDatabaseSQLLayer(t *testing.T) {
	rec, drv := newInstrumentedDriver(t)
	ctx := context.Background()

	require.NoError(t, drv.Query(ctx, "SELECT name FROM users", []any{}, &Rows{}))
	require.NoError(t, drv.Exec(ctx, "UPDATE users SET name = 'x'", []any{}, nil))

	queries, execs := rec.snapshot()
	assert.Contains(t, queries, "SELECT name FROM users",
		"velox must route SELECTs through the *sql.DB QueryContext so otelsql can observe them")
	assert.Contains(t, execs, "UPDATE users SET name = 'x'",
		"velox must route writes through the *sql.DB ExecContext so otelsql can observe them")
}

func TestOpenDB_RoutesTxStatementsThroughDatabaseSQLLayer(t *testing.T) {
	rec, drv := newInstrumentedDriver(t)
	ctx := context.Background()

	tx, err := drv.Tx(ctx)
	require.NoError(t, err)
	require.NoError(t, tx.Query(ctx, "SELECT id FROM accounts", []any{}, &Rows{}))
	require.NoError(t, tx.Exec(ctx, "INSERT INTO accounts DEFAULT VALUES", []any{}, nil))
	require.NoError(t, tx.Commit())

	queries, execs := rec.snapshot()
	assert.Contains(t, queries, "SELECT id FROM accounts",
		"velox must route in-transaction SELECTs through the database/sql layer")
	assert.Contains(t, execs, "INSERT INTO accounts DEFAULT VALUES",
		"velox must route in-transaction writes through the database/sql layer")
}
