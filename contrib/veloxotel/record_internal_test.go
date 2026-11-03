package veloxotel

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
)

// unreachable is a connector to no database: a dry run never connects.
type unreachable struct{}

func (unreachable) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("unreachable")
}
func (unreachable) Driver() driver.Driver { return nil }

// TestRecordSkipsDisabledInstruments: with no meter to record to, record and
// txRecord build no attribute set, so they allocate nothing. The Statement
// is captured by a Rewrite hook during a dry run (SQL): record reads its
// Entity, an *EntityInfo only velox builds, and a dry run makes the
// statement without a database to send it to.
func TestRecordSkipsDisabledInstruments(t *testing.T) {
	ctx := velox.WithoutPolicy(context.Background(), "record tests")
	var st *velox.Statement
	db := velox.Open(velox.Postgres, sql.OpenDB(unreachable{}), velox.WithRewrite(func(_ context.Context, s *velox.Statement) error {
		st = s
		return nil
	}))
	if _, _, err := db.Query[testmodel.Invoice]().Limit(1).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	if st == nil || st.Op != velox.OpSelect || st.Entity == nil {
		t.Fatalf("statement %+v", st)
	}
	a := newAdapter(Config{TracerProvider: tracenoop.NewTracerProvider(), MeterProvider: metricnoop.NewMeterProvider()})
	if n := testing.AllocsPerRun(100, func() {
		a.record(ctx, st, "invoices", "SELECT invoices", nil, 0.001, failure{}, false)
	}); n != 0 {
		t.Errorf("record: %v allocations, want 0", n)
	}
	start := time.Now()
	tx := &velox.TxInfo{Attempt: 1}
	if n := testing.AllocsPerRun(100, func() {
		a.txRecord(ctx, start, tx, velox.TxCommit, nil)
	}); n != 0 {
		t.Errorf("txRecord: %v allocations, want 0", n)
	}
}

// TestRecordSkipsInstrumentsAMeterDisables: with a meter whose views drop
// the duration instruments, and keep the returned rows and the fallbacks,
// record of a write and txRecord still build no attribute set: each checks
// the instruments it would record to.
func TestRecordSkipsInstrumentsAMeterDisables(t *testing.T) {
	ctx := velox.WithoutPolicy(context.Background(), "record tests")
	var st *velox.Statement
	db := velox.Open(velox.Postgres, sql.OpenDB(unreachable{}), velox.WithRewrite(func(_ context.Context, s *velox.Statement) error {
		st = s
		return nil
	}))
	if _, _, err := db.Delete[testmodel.Invoice]().Where(testmodel.Invoices.ID.EQ(1)).SQL(ctx); err != nil {
		t.Fatal(err)
	}
	drop := func(name string) sdkmetric.View {
		return sdkmetric.NewView(sdkmetric.Instrument{Name: name}, sdkmetric.Stream{Aggregation: sdkmetric.AggregationDrop{}})
	}
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(sdkmetric.NewManualReader()),
		sdkmetric.WithView(drop("db.client.operation.duration"), drop("velox.tx.duration")))
	t.Cleanup(func() { _ = mp.Shutdown(context.Background()) })
	a := newAdapter(Config{TracerProvider: tracenoop.NewTracerProvider(), MeterProvider: mp})
	if !a.returned.Enabled(ctx) || a.duration.Enabled(ctx) || a.txDuration.Enabled(ctx) {
		t.Fatalf("enabled: returned rows %v, duration %v, velox.tx.duration %v; want the first alone",
			a.returned.Enabled(ctx), a.duration.Enabled(ctx), a.txDuration.Enabled(ctx))
	}
	if n := testing.AllocsPerRun(100, func() {
		a.record(ctx, st, "invoices", "DELETE invoices", nil, 0.001, failure{}, false)
	}); n != 0 {
		t.Errorf("record of a write: %v allocations, want 0", n)
	}
	start := time.Now()
	tx := &velox.TxInfo{Attempt: 1}
	if n := testing.AllocsPerRun(100, func() {
		a.txRecord(ctx, start, tx, velox.TxCommit, nil)
	}); n != 0 {
		t.Errorf("txRecord: %v allocations, want 0", n)
	}
}
