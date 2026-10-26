package veloxotel

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/internal/testmodel"
)

// unreachable is a connector to no database: a dry run never connects.
type unreachable struct{}

func (unreachable) Connect(context.Context) (driver.Conn, error) {
	return nil, errors.New("unreachable")
}
func (unreachable) Driver() driver.Driver { return nil }

// TestRecordSkipsDisabledInstruments: with no meter to record to, record and
// txRecord build no attribute set, so they allocate nothing.
func TestRecordSkipsDisabledInstruments(t *testing.T) {
	ctx := velox.WithAllTenants(context.Background(), "record tests")
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
		a.txRecord(ctx, start, tx, velox.TxCommit)
	}); n != 0 {
		t.Errorf("txRecord: %v allocations, want 0", n)
	}
}
