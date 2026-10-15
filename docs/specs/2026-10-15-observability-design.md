# velox2 Observability — design

Status: draft for review · 2026-10-15 · Go 1.27+

Sub-project 3 of five. The table is at the top of the Core design, `2026-10-14-core-design.md`.

It adds:
- a query fingerprint;
- the Go call site of each statement;
- sqlcommenter SQL comments;
- per-request tags;
- statement results for hooks;
- a transaction hook.

Core gains these with no new dependency. A separate module, `contrib/otel`, turns them into OpenTelemetry spans and metrics. It plugs into Core's pipeline (Core §5) and into the policy design's statement fields (`2026-10-14-policy-design.md` §2.3), and nothing else.

## Decisions

Made by the author during design (2026-10-15):

- **The OpenTelemetry adapter lives in `./contrib/otel`,** as its own Go module (`github.com/syssam/velox2/contrib/otel`). The core module keeps no OpenTelemetry dependency. Core exposes vendor-neutral data; any other vendor's adapter can use it.
- **1.0 ships spans and metrics.** A slow-query log and log records are out of scope.
- **The SQL comment carries stable values only:** the application, the call site, and context tags the application lists. It never carries a trace ID or a tenant.
  - pgx caches prepared statements by SQL text.
  - PostgreSQL's `pg_stat_statements`, and MySQL's digest, group by text or shape.
  - A per-request value would defeat both.
- **Metric keys have bounded cardinality.** They are the database system, operation, table, entity and error type. The fingerprint is on every span; it is added to metrics only when the application asks.
- **`./example` is a runnable example module.** It shows velox2's API end to end and gains a tracing section when `contrib/otel` exists. CI runs it.

## Success criteria

1. **Spans:** every statement velox sends can appear as a span under the caller's trace. Inside `db.Tx` it nests under the transaction's span, even when the code passes its own context to a query.
2. **Fingerprints:** a fingerprint names a statement shape stably. It is the same in every process and service running the same velox version, and the same for every value of the statement's arguments.
3. **Call sites:** the Go call site of a statement is visible from the database's own tools (`pg_stat_activity`, the slow-query log) without breaking pgx's statement cache.
4. **Cost:**
   - With nothing configured, Core's budgets (Core §8) are unchanged.
   - With callers and comments on, velox's own added cost stays within §6's budget.
   - The cost of OpenTelemetry itself is the SDK's.
5. **Compile cost:** Core §3 still holds. Nothing is instantiated per entity for observability, and no velox type reachable from generated code holds `reflect.Type`.

## 1. Core additions

### 1.1 Fingerprint

`Statement.Fingerprint string` is set at render.

- **Format:** the first 8 bytes, as 16 lowercase hex characters, of the SHA-256 of the rendered SQL without any comment.
- **Why the SQL text is enough:** velox sends every value as a parameter, so the text is already the statement's normalized shape.
- **When it is computed:** once per render-cache entry, and stored with the cached text, so a cached statement costs nothing. An uncached render, such as a statement whose entity a `Rewrite` hook replaced, computes it each time.
- **The SQL comment** (§1.3) is excluded, so a fingerprint does not change with the call site.
- **Stability:** a fingerprint is stable for a given velox version and dialect. A release that changes rendering may change fingerprints, and its notes say so.

### 1.2 Call site

`velox.WithCallers()` on `Open` records, in `Statement.Caller`, the first stack frame outside velox's own packages and the application's registered wrapper packages.

- **Format:** `Caller{File, Line, Func}`. `File` is relative to the module root when it can be determined, and the base name otherwise.
- **How it is captured:** one `runtime.Callers` call per statement. Frames are resolved once per program counter and cached in a bounded map, so resolution costs nothing after the first time.
- **Wrapper packages:** `velox.WithCallerSkip(pkgPath ...string)` names packages to skip as well, for an application's own repository layer.
- **Default:** off. Without it, `Caller` is zero.

### 1.3 SQL comment

`velox.WithSQLComment(app string, tags ...string)` appends a sqlcommenter comment to every statement's text:

```sql
SELECT … FROM "invoices" WHERE … /*app='billing',caller='billing/invoice.go:42',route='GET%20%2Finvoices'*/
```

- **Keys:**
  - `app` is the given name.
  - `caller` is present when `WithCallers` is on.
  - Then each listed context tag (§1.4) that is set, in the order listed.
- **Encoding:** keys and values are URL-encoded, and values are quoted, as sqlcommenter specifies. Keys are sorted only as listed, never by map order.
- **Rendering:** the comment is rendered once per (statement shape, call site, tag values) and cached. That cache is bounded like the render cache (Core §5): 4,096 entries and 4 MiB, evicting the oldest.
- **Two copies of the text:** `Statement.SQL` is the text sent, comment included. The fingerprint is of the text without the comment.
- **Hooks:** a `Rewrite` hook sees no comment, because rendering comes after it. An `Around` hook sees the sent text.
- **Cache safety:** applications must list only tags whose values are stable per code path, such as a route template, never an ID. The documentation says so with the pgx cache reason.

### 1.4 Tags

`velox.WithTags(ctx, key, value, ...)` adds string tags to the context. Later calls add to, or replace, earlier ones.

- `Statement.Tags` holds them, set before the hooks run.
- Adapters read them for span attributes.
- The SQL comment includes only the keys `WithSQLComment` lists.

### 1.5 Results

`Statement.Result` is set when execution returns, before the `Around` hooks see `next`'s return:

```go
type Result struct {
    Rows     int64 // rows read
    Affected int64 // rows a write affected; -1 when the driver does not say
}
```

### 1.6 Transactions

`velox.WithTxAround(h TxAround)` adds a transaction hook:

```go
type TxAround func(ctx context.Context, tx *TxInfo, next func(context.Context) error) error

type TxInfo struct {
    ID        uint64             // unique within the process
    Isolation sql.IsolationLevel
    ReadOnly  bool
    Attempt   int                // 1 for the first try; RetrySerializable increments it
    Context   context.Context    // the context the hooks passed to next
}
```

- **What it wraps:** `TxAround` hooks wrap each `db.Tx` call, every attempt and the commit included. The first one given is the outermost. A savepoint (a nested `Tx`) runs inside the enclosing transaction's hooks and does not call them again.
- **Statements inside:** each gets `Statement.Tx` pointing to the transaction's `TxInfo`. An adapter can then parent a statement's span on `Tx.Context`'s span even when the application passed its own context to the query.
- **Rules as for `Around`:** a `TxAround` hook must call `next` once per attempt, or return an error of its own. Its errors are returned unchanged.

### 1.7 Statement fields, together

`Statement` gains `Fingerprint string`, `Caller Caller`, `Tags []Tag` (with `Tag{Key, Value string}`), `Result Result` and `Tx *TxInfo`.

## 2. contrib/otel

Module `github.com/syssam/velox2/contrib/otel`, package `veloxotel`. It requires `go.opentelemetry.io/otel` and its `trace` and `metric` APIs, but no SDK.

```go
db := velox.OpenPgx(pool, veloxotel.Options(veloxotel.Config{
    TracerProvider: tp,          // default otel.GetTracerProvider()
    MeterProvider:  mp,          // default otel.GetMeterProvider()
    App:            "billing",   // enables the SQL comment
    CommentTags:    []string{"route"},
})...)
```

`Options` returns the `velox.Option`s: `WithAround`, `WithTxAround`, `WithCallers`, and `WithSQLComment` when `App` is set. An application that wants only part of this can use `veloxotel.Around(cfg)` and `veloxotel.TxAround(cfg)` alone.

### 2.1 Spans

There is one span per statement, of kind client. It starts when `next` is called and ends when it returns, so it covers sending, every row read, and with `Iter` the loop body.

**Name:** `<db.operation.name> <db.collection.name>`, for example `SELECT invoices`. A `Count` is named `SELECT invoices`, with `velox.count=true`.

**Attributes:**

| Attribute | Value | When |
|---|---|---|
| `db.system.name` | `postgresql`, `mysql`, `sqlite` | always |
| `db.operation.name` | `SELECT`, `INSERT`, `UPDATE`, `DELETE` | always |
| `db.collection.name` | the table, schema-qualified when declared | always |
| `db.query.text` | `Statement.SQL`, placeholders only | unless `Config.OmitQueryText` |
| `db.response.returned_rows` | `Result.Rows` | reads |
| `velox.affected_rows` | `Result.Affected` | writes |
| `velox.entity` | the Go type | always |
| `velox.fingerprint` | §1.1 | always |
| `code.filepath`, `code.lineno`, `code.function` | `Statement.Caller` | when callers are on |
| `velox.tag.<key>` | each `Statement.Tags` entry | always |
| `velox.tenant_bypass`, `velox.policy_bypass` | the reasons | when set |
| `velox.tenant` | `Statement.Tenant`, formatted | only with `Config.RecordTenant` |

`SQL(ctx)` does not execute, so it makes no span.

**Errors:**
- The span's status is Error.
- `error.type` is a stable, low-cardinality value. In order:
  - `velox.denied`, `velox.no_tenant`, `velox.unbounded`, `velox.too_many_rows`, `velox.timeout`;
  - `constraint.unique`, `constraint.foreign_key`, `constraint.not_null`, `constraint.check`;
  - `context.canceled`, `context.deadline_exceeded`;
  - else `_OTHER`.
- The error is recorded as a span event, with its message.

**Parent:** the span's parent is the span in `Statement.Tx.Context` when the statement is in a transaction, and the span in the hook's context otherwise. The span's context is what the hook passes to `next`, so the driver's own instrumentation, if any, nests under it.

**Retries:** an `Around` hook that retries makes one span per attempt.

### 2.2 Transaction span

`TxAround` makes one span per `db.Tx` call, named `velox.tx`, with these attributes:
- `velox.tx.isolation` and `velox.tx.read_only`;
- `velox.tx.attempts`, the final count;
- `velox.tx.outcome`: `commit`, `rollback` (the function returned an error), or `error` (begin or commit failed, or the deadline passed).

Each retry adds a span event `retry` carrying the error type.

### 2.3 Metrics

The instruments follow OpenTelemetry's database semantic conventions where they exist:

| Instrument | Kind, unit | Attributes |
|---|---|---|
| `db.client.operation.duration` | histogram, s | `db.system.name`, `db.operation.name`, `db.collection.name`, `velox.entity`, `error.type` (when failed); `velox.fingerprint` only with `Config.MetricFingerprint` |
| `db.client.response.returned_rows` | histogram, {row} | as above, reads only |
| `velox.tx.duration` | histogram, s | `velox.tx.outcome`, `velox.tx.read_only`, `velox.tx.retried` (bool) |

Histogram boundaries are the conventions' recommended ones for durations (from 0.001 s to 10 s), and powers of 4 up to 65,536 for rows. Cardinality is bounded by entities × 4 operations × the error-type values. With `MetricFingerprint`, it is bounded by the render cache's 4,096 shapes per process.

### 2.4 Versions

`contrib/otel` tracks the OpenTelemetry Go API's stable `v1` line. Semantic-convention attribute names are written out as constants in the adapter, not imported from a `semconv` package version. That keeps the dependency to the stable API, and the names change only with a velox release that says so.

## 3. Example

`./example` is a module (`github.com/syssam/velox2/example`) with:
- an entity package owned by a "billing" team;
- generated code (`go tool velox-gen`);
- a `main` that walks through the API on in-memory SQLite: queries, relations, writes, guards, tenant and policy, transactions, deadlines and `SQL(ctx)`.

A golden-output test keeps it in step with the API, and CI runs it.

This sub-project adds a tracing section:
- it configures `contrib/otel` with an in-memory exporter;
- it prints the spans of a short transaction, with the span tree, the fingerprints, the call sites and the SQL comment sent;
- it prints the metric points recorded.

## 4. Errors

Observability never changes what a statement returns. A panic in the adapter is a bug, not handled. An `Around` or `TxAround` hook's errors follow the existing rules (Core §5, policy design §6).

## 5. Testing

- **Core:**
  - **Fingerprints:** stability across values, processes (a golden fingerprint per dialect for a fixed statement) and the comment.
  - **Call sites:** an application frame is found, and wrapper packages are skipped.
  - **Comments:** exact sqlcommenter text, encoding, and listed tags only.
  - **Caches:** commented-text cache bounds.
  - **Statement fields:** `Result` for reads and writes, and `Tx` on statements inside `db.Tx` and its savepoints.
  - **`TxAround`:** ordering, retries and errors.
  - **Budgets:** the tests unchanged with nothing configured, and a new budget test with callers and comments on.
- **`contrib/otel`,** with OpenTelemetry's in-memory span exporter and a manual metric reader on velox's fake executor:
  - span names and attributes for every operation;
  - error types;
  - the transaction span tree when the application passes its own context;
  - one span per retried attempt;
  - metric instruments and their attribute sets;
  - fingerprints excluded from metrics by default.
- **Real database:** PostgreSQL under pgx in `contrib/otel`'s tests, run by `scripts/ci-local.sh`. The commented statement uses pgx's statement cache: the same call site twice is prepared once, which pgx's statement-cache hooks or a `pg_prepared_statements` check confirm.
- **CI:** `scripts/ci-local.sh` and the GitHub workflow run `contrib/otel`'s and `example`'s tests, vet and lint.

## 6. Performance

- **Nothing configured:** Core's budgets (Core §8) are unchanged, and the fingerprint is computed into the render cache.
- **`WithCallers` and `WithSQLComment` on, cached statement:** velox adds at most 1 allocation and 300 ns per statement over the same statement without them. This is measured, and a budget test pins it.
- **`contrib/otel`:** its cost beyond OpenTelemetry's SDK is benchmarked and reported, not budgeted.

## 7. Out of scope

- Connection-pool metrics. pgx and database/sql have their own instrumentation.
- A slow-query log and log records.
- Trace IDs in SQL comments.
- Database-side tracing.
- Sampling decisions. They belong to the application's tracer provider.
