package sql

import (
	"bytes"
	"context"
	"log"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/syssam/velox/dialect"

	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

// The driver must surface query errors to the caller (wrapped, via %w) but must
// NOT write the failing statement or its args to the process-wide default
// logger. Doing so leaks query arguments (often PII) to stderr and spams logs on
// normal, expected errors such as context cancellation. Opt-in query logging is
// available via LogDriver / DebugDriver / an Observer — it is the caller's
// choice, not an unconditional side effect of every error.
//
// Not parallel: it swaps the global log output.
func TestConn_Query_DoesNotLogStatementToDefaultLogger(t *testing.T) {
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(old) })

	drv, err := Open(dialect.SQLite, ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = drv.Close() })

	// A query that errors at the database (no such table).
	qErr := drv.Query(context.Background(), "SELECT secret_col FROM nonexistent_table", []any{}, &Rows{})
	require.Error(t, qErr, "the error must still be surfaced to the caller")

	logged := buf.String()
	assert.NotContains(t, logged, "nonexistent_table",
		"driver must not write SQL statements to the default logger (PII/log-spam); the error is returned to the caller instead")
	assert.NotContains(t, logged, "SQL-ERROR",
		"leftover debug logging must not reach the default logger")
}
