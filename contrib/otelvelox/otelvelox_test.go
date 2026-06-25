package otelvelox_test

import (
	"context"
	"testing"

	"github.com/XSAM/otelsql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver

	"github.com/syssam/velox/dialect"
	veloxsql "github.com/syssam/velox/dialect/sql"
)

// TestOTelTracing_EndToEnd is the proof behind docs/observability.md: an
// otelsql-instrumented *sql.DB handed to velox via sql.OpenDB emits an
// OpenTelemetry span for every statement velox runs. It compiles the exact
// otelsql calls the docs recommend against a real otelsql release, and asserts
// spans actually flow.
//
// Non-vacuous by construction: the in-memory recorder only sees spans otelsql
// emits, and otelsql only emits them if velox routes its SQL through the
// instrumented driver. An empty recording would mean velox bypassed the
// database/sql layer (the otelsql/Ent interposition point) — and would fail here.
func TestOTelTracing_EndToEnd(t *testing.T) {
	// In-memory span recorder, installed as the global tracer provider that
	// otelsql reads from by default.
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	// The documented wiring, compiled against real github.com/XSAM/otelsql:
	db, err := otelsql.Open("sqlite", ":memory:",
		otelsql.WithAttributes(attribute.String("db.system", "sqlite")),
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	// RegisterDBStatsMetrics returns (metric.Registration, error); the
	// registration handle can be used to unregister later.
	_, err = otelsql.RegisterDBStatsMetrics(db,
		otelsql.WithAttributes(attribute.String("db.system", "sqlite")),
	)
	require.NoError(t, err)

	// Hand the instrumented *sql.DB to velox — this is the whole integration.
	drv := veloxsql.OpenDB(dialect.SQLite, db)

	ctx := context.Background()
	require.NoError(t, drv.Exec(ctx, "CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)", []any{}, nil))
	require.NoError(t, drv.Exec(ctx, "INSERT INTO t (name) VALUES ('alice')", []any{}, nil))

	rows := &veloxsql.Rows{}
	require.NoError(t, drv.Query(ctx, "SELECT name FROM t", []any{}, rows))
	require.NoError(t, rows.Close())

	spans := rec.Ended()
	require.NotEmpty(t, spans,
		"otelsql must emit spans for the SQL velox runs through sql.OpenDB; "+
			"an empty recording means velox bypassed the database/sql tracing seam")

	names := make([]string, 0, len(spans))
	for _, s := range spans {
		names = append(names, s.Name())
	}
	t.Logf("otelsql emitted %d spans for velox's statements: %v", len(spans), names)

	// We issued 2 execs + 1 query, so otelsql should have produced at least one
	// span per statement.
	assert.GreaterOrEqual(t, len(spans), 3,
		"expected at least one span per statement velox executed")
}
