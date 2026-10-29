// Command otel traces velox with contrib/veloxotel. A request pays an invoice in
// a transaction; the program prints the spans that made, with each
// statement's fingerprint, Go call site and the SQL comment sent, and the
// metric points recorded. It exports to memory, so it needs no collector.
package main

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	_ "modernc.org/sqlite"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/veloxotel"
	"github.com/syssam/velox2/example/otel/gen"
	"github.com/syssam/velox2/migrate"
)

func main() {
	if err := run(context.Background(), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// The tables come from migrations/, made, in the example module, by
//
//	go tool velox-gen migrate diff -dialect sqlite -name init -dir ./otel/migrations ./otel/schema
//
// and applied with migrate.Up. The temporary SQLite database is this demo's;
// a real application opens its own.
//
//go:embed migrations
var migrations embed.FS

// seedSQL fills the table the migration made.
const seedSQL = `INSERT INTO invoices (number, customer, status, total_cents) VALUES
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
	dir, err := os.MkdirTemp("", "velox-otel-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sqlDB, err := sql.Open("sqlite", filepath.Join(dir, "otel.db"))
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	sqlDB.SetMaxOpenConns(1) // statements and savepoints run on one connection
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	if _, err := migrate.Up(ctx, sqlDB, sub); err != nil {
		return err
	}
	if _, err := sqlDB.ExecContext(ctx, seedSQL); err != nil {
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
