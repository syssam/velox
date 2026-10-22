# otel

Tracing and metrics with `contrib/veloxotel`, the OpenTelemetry adapter, a module
of its own (`github.com/syssam/velox2/contrib/veloxotel`) so that velox itself
depends on no OpenTelemetry package. A request pays an invoice in a
transaction; the program prints the spans and metric points that made.

```sh
cd example
go run ./otel
```

```go
db := velox.Open(velox.SQLite, sqlDB, veloxotel.Options(veloxotel.Config{
	TracerProvider: tp,                  // default otel.GetTracerProvider()
	MeterProvider:  mp,                  // default otel.GetMeterProvider()
	App:            "billing",           // turns on the SQL comment
	CommentTags:    []string{"route"},   // the velox.WithTags keys it carries
})...)

ctx = velox.WithTags(ctx, "route", "POST /invoices/{number}/pay") // in middleware
```

- **Spans.** One per statement, named `SELECT invoices` and the like, of kind
  client, a child of the span on the statement's context. Inside `db.Tx`,
  fn's `ctx` carries the transaction's `velox.tx` span, so a statement run on
  it (or on a context derived from it) nests under `velox.tx`; `velox.tx`
  holds the isolation, the attempt count and the outcome (`commit`,
  `rollback`, `error`).
- **Fingerprint.** `velox.fingerprint` names the statement's shape: the same
  for every argument value, in every process running the same velox version.
- **Call site.** `code.file.path` and `code.line.number` are the line of the
  application that ran the statement, here inside `pay`, relative to the
  module root. `Options` turns on `velox.WithCallers` (`OmitCallers: true`
  leaves it out); packages of your own that wrap velox are skipped with
  `velox.WithCallerSkip`.
- **SQL comment.** The text sent ends with a sqlcommenter comment holding the
  app, the call site and the listed tags, so `pg_stat_activity` and the
  slow-query log show where a statement came from. List only tags whose
  values are stable per code path, such as a route template: the comment is
  part of the text pgx caches prepared statements by. Every tag, listed or
  not, is a `velox.tag.<key>` span attribute.
- **Metrics.** `db.client.operation.duration`, `db.client.response.returned_rows`
  (reads only) and `velox.tx.duration`. Their attributes are bounded: no SQL,
  tag, tenant or, by default, fingerprint. With `Iter`, the duration is the
  database's time: it leaves out the loop body (`velox.Result.LoopBody`),
  which the span still covers.
- **Server and errors.** The adapter sees no connection string, so
  `Config.Target` (or `Config.TargetFor`, per shard and replica) names
  `db.namespace`, `server.address` and `server.port`; this example sets
  neither. A failed statement carries `error.type` and, when the driver gave
  one, its code as `db.response.status_code` (`velox.DriverCode`).
- **Order.** Give an `Around` hook of your own that retries before
  `veloxotel.Options`, as `velox.WithAround` makes the first one outermost:
  the adapter then makes one span and one duration point per attempt.

The printed output leaves out span IDs, times and durations, which change
between runs. A service exports to a collector instead, through its own
`TracerProvider` and `MeterProvider`; this example keeps everything in
memory with the OpenTelemetry SDK's `tracetest` exporter and a manual metric
reader.

The output is in `testdata/output.golden`.
