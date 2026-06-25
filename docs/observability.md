# Observability (tracing, metrics, logging)

Velox follows the same model as [Ent](https://entgo.io/docs/tracing): **the database
is instrumented at the `database/sql` layer, one level below the ORM.** Velox issues
every statement through the standard `ExecContext` / `QueryContext` methods of the
`*sql.DB` you give it, so any `database/sql` instrumentation — OpenTelemetry,
Prometheus, a custom driver wrapper — observes 100% of velox's traffic without any
ORM-specific glue.

> This routing is a guaranteed contract, pinned by
> `dialect/sql/observability_test.go::TestOpenDB_RoutesSQLThroughDatabaseSQLLayer`
> (and the transaction variant). If a refactor ever bypassed the `database/sql`
> layer, that test fails before release.

## At a glance

| Need | Use | External dep? |
|------|-----|---------------|
| Distributed tracing + DB metrics | `otelsql`-wrapped `*sql.DB` → `sql.OpenDB` | yes (`otelsql`) |
| Lightweight in-process stats (counts, latency, slow, errors) | `sql.OpenWithStats` / `sql.NewStatsDriver` | no (built-in) |
| Slow-query alerting | `sql.WithSlowQueryHook` | no (built-in) |
| Per-statement query logging | `sql.NewLogDriver` | no (built-in) |
| Debug logging (with tx ids) | `dialect.Debug` | no (built-in) |
| Semantic, ORM-level spans (per operation) | a velox `Interceptor` | yes (your tracer) |

All of these are wired the same way: build a `dialect.Driver` and hand it to your
generated client via the `Driver(...)` option.

```go
client := myapp.NewClient(myapp.Driver(drv)) // drv is any dialect.Driver
```

---

## 1. Distributed tracing & metrics with OpenTelemetry (recommended)

This is the production-grade path and is identical to Ent's: wrap the standard
`*sql.DB` with [`otelsql`](https://github.com/XSAM/otelsql), then hand it to velox
through `sql.OpenDB`.

```go
import (
	"github.com/XSAM/otelsql"
	"go.opentelemetry.io/otel/attribute"

	"github.com/syssam/velox/dialect"
	veloxsql "github.com/syssam/velox/dialect/sql"

	myapp "example.com/app/velox" // your generated client package
)

func newClient(dsn string) (*myapp.Client, error) {
	// 1. Open an OTel-instrumented *sql.DB (spans for every Exec/Query/Begin/Commit).
	db, err := otelsql.Open("postgres", dsn,
		otelsql.WithAttributes(attribute.String("db.system", "postgres")),
	)
	if err != nil {
		return nil, err
	}

	// 2. Export connection-pool metrics (open/idle/in-use conns, wait counts).
	//    RegisterDBStatsMetrics returns (metric.Registration, error) — keep the
	//    handle if you want to unregister later.
	if _, err := otelsql.RegisterDBStatsMetrics(db,
		otelsql.WithAttributes(attribute.String("db.system", "postgres")),
	); err != nil {
		return nil, err
	}

	// 3. Hand the instrumented *sql.DB to velox. Every query/exec/tx velox runs
	//    now flows through the instrumented *sql.DB.
	drv := veloxsql.OpenDB(dialect.Postgres, db)
	return myapp.NewClient(myapp.Driver(drv)), nil
}
```

> **Verified:** this exact wiring is compiled and exercised end-to-end against
> `github.com/XSAM/otelsql v0.42.0` by [`contrib/otelvelox`](../contrib/otelvelox),
> whose test asserts otelsql emits a span for every statement velox runs through
> `sql.OpenDB`. If you prefer the OpenTelemetry `semconv` constants over a plain
> `attribute.String` (e.g. `semconv.DBSystemPostgreSQL`), confirm the constant
> against the `semconv` version you import. The velox guarantee — that `sql.OpenDB`
> \+ the `Driver(...)` option route every statement through the instrumented
> `*sql.DB` — is pinned by `dialect/sql/observability_test.go`.

`db/sql` spans are created with the active span in `ctx` as their parent, so DB
calls nest correctly under your HTTP/gRPC request spans as long as you pass the
request `context.Context` into velox calls (which you already do — every velox
query/mutation takes a `ctx`).

The same recipe works for MySQL (`otelsql.Open("mysql", …)`, `dialect.MySQL`) and
SQLite (`dialect.SQLite`).

---

## 2. Built-in lightweight stats (no external dependencies)

If you don't want an OTel pipeline, velox ships an in-process stats driver that
tracks query/exec counts, total & average latency, slow-query count, and error
count using lock-free atomics.

```go
import (
	"time"

	veloxsql "github.com/syssam/velox/dialect/sql"
	myapp "example.com/app/velox"
)

statsDrv, stats, err := veloxsql.OpenWithStats("postgres", dsn,
	veloxsql.WithSlowThreshold(200*time.Millisecond),
	veloxsql.WithSlowQueryLog(), // slog.Warn on every slow query
)
if err != nil {
	return err
}
client := myapp.NewClient(myapp.Driver(statsDrv))

// Sample the counters whenever you like (e.g. push to your metrics backend):
go func() {
	for range time.Tick(time.Minute) {
		s := stats.Stats() // StatsSnapshot
		slog.Info("db stats",
			"queries", s.TotalQueries,
			"execs", s.TotalExecs,
			"avg", s.AvgQueryDuration(),
			"slow", s.SlowQueries,
			"errors", s.Errors,
		)
	}
}()
```

`StatsSnapshot` exposes `TotalQueries`, `TotalExecs`, `TotalDuration`,
`SlowQueries`, `Errors`, plus `AvgQueryDuration()` and a `String()` summary.
Already have a `*dialect/sql.Driver`? Wrap it directly with
`veloxsql.NewStatsDriver(drv, opts...)`.

> Note: these counters are **global** (not labeled by entity/operation/table). For
> per-operation breakdowns and histograms, use the OpenTelemetry path above, or add
> ORM-level spans (section 4).

---

## 3. Slow-query alerting

`WithSlowQueryHook` fires a callback for any statement slower than the threshold —
wire it to your alerting/metrics system instead of (or in addition to) logging.

```go
statsDrv, _, err := veloxsql.OpenWithStats("postgres", dsn,
	veloxsql.WithSlowThreshold(100*time.Millisecond),
	veloxsql.WithSlowQueryHook(func(ctx context.Context, query string, args []any, d time.Duration) {
		metrics.SlowQueries.Inc()
		slog.WarnContext(ctx, "slow query", "duration", d, "query", query)
	}),
)
```

## 3b. Per-statement & debug logging

```go
drv, _ := veloxsql.Open(dialect.Postgres, dsn) // *dialect/sql.Driver

// Structured per-statement logging:
logged := veloxsql.NewLogDriver(drv)

// Or debug logging that also tags transactions with an id:
debugged := dialect.Debug(drv)

client := myapp.NewClient(myapp.Driver(logged)) // or Driver(debugged)
```

---

## 4. ORM-level (semantic) spans with an interceptor

The `database/sql`-layer spans in section 1 are labeled by raw SQL. To get spans
labeled by *velox operation* (entity, op type), add a query
[interceptor](hooks-and-interceptors.md). This is how you attach ORM semantics that
the SQL layer can't see — the same place Ent recommends for semantic spans.

```go
import (
	"context"

	"go.opentelemetry.io/otel"
	myapp "example.com/app/velox"
)

tracer := otel.Tracer("velox")

client.Intercept(myapp.InterceptFunc(func(next myapp.Querier) myapp.Querier {
	return myapp.QuerierFunc(func(ctx context.Context, q myapp.Query) (myapp.Value, error) {
		ctx, span := tracer.Start(ctx, "velox.query")
		defer span.End()
		v, err := next.Query(ctx, q)
		if err != nil {
			span.RecordError(err)
		}
		return v, err
	})
}))
```

Register interceptors once at startup, before serving traffic (they share a
lock-free store — see [hooks-and-interceptors.md](hooks-and-interceptors.md)).
For write-side spans, use a mutation [hook](hooks-and-interceptors.md) the same way.

---

## Why velox has no "tracing driver" of its own

Velox deliberately does **not** ship a bespoke OpenTelemetry driver. Tracing belongs
at the `database/sql` layer where the mature, well-tested `otelsql` ecosystem already
lives — exactly where Ent puts it. A velox-specific tracing wrapper would duplicate
`otelsql`, drift from the ecosystem, and add maintenance surface for no benefit. The
built-in `StatsDriver` / `LogDriver` exist only as zero-dependency conveniences for
projects that don't run an OTel pipeline; they are not a replacement for it.
