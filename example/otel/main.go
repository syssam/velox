// Command otel traces velox with contrib/otel. A request pays an invoice in
// a transaction; the program prints the spans that made, with each
// statement's fingerprint, Go call site and the SQL comment sent, and the
// metric points recorded. It exports to memory, so it needs no collector.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	veloxotel "github.com/syssam/velox2/contrib/otel"
	"github.com/syssam/velox2/example/otel/gen"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// ddl creates and fills the table by hand: planning migrations from the
// schemas is a later velox2 sub-project.
const ddl = `
CREATE TABLE invoices (
	id          INTEGER PRIMARY KEY,
	number      TEXT NOT NULL UNIQUE,
	customer    TEXT NOT NULL,
	status      TEXT NOT NULL,
	total_cents INTEGER NOT NULL
);
INSERT INTO invoices (number, customer, status, total_cents) VALUES
	('INV-003', 'Acme', 'paid', 12000),
	('INV-004', 'Globex', 'sent', 4500)`

// pay marks an invoice paid: the handler of POST /invoices/{number}/pay.
// Nothing in it knows about tracing.
func pay(ctx context.Context, db *velox.DB, number string) error {
	I := gen.Invoices
	return db.Tx(ctx, func(ctx context.Context, tx *velox.DB) error {
		inv, err := tx.Query[gen.Invoice]().Where(I.Number.EQ(number)).One(ctx)
		if err != nil {
			return err
		}
		inv.Status = gen.InvoiceStatusPaid
		return tx.Save(inv, I.Status).Exec(ctx)
	})
}

func run(ctx context.Context, w io.Writer) error {
	sqlDB, err := sql.Open("sqlite", "file::memory:")
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1) // an in-memory database lives as long as its one connection
	if _, err := sqlDB.ExecContext(ctx, ddl); err != nil {
		return err
	}

	// A service passes the providers it exports to a collector with; these
	// keep every span and metric point in memory, to print them.
	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	// Options turns on call sites, a span per statement and per transaction,
	// the metrics and, because App is set, the SQL comment, which carries the
	// route tag.
	db := velox.Open(velox.SQLite, sqlDB, veloxotel.Options(veloxotel.Config{
		TracerProvider: tp,
		MeterProvider:  mp,
		App:            "billing",
		CommentTags:    []string{"route"},
	})...)

	// Middleware would set the route tag and start the request's span.
	req := velox.WithTags(ctx, "route", "POST /invoices/{number}/pay")
	req, span := tp.Tracer("example").Start(req, "POST /invoices/{number}/pay")
	err = pay(req, db, "INV-004")
	span.End()
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(w, "== spans")
	printSpans(w, spans.GetSpans())
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &rm); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(w, "\n== metrics")
	printMetrics(w, rm)
	return nil
}
