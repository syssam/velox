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

The rulings made while planning and building it (`docs/plans/2026-10-15-observability.md`, "Rulings this plan makes on the spec", and the task reviews) are written into the sections below.

Core gains these with no new dependency. A separate module, `contrib/otel`, turns them into OpenTelemetry spans and metrics. It plugs into Core's pipeline (Core §5) and into the policy design's statement fields (`2026-10-14-policy-design.md` §2.3), and nothing else.

## Decisions

Made by the author during design (2026-10-15):

- **The OpenTelemetry adapter lives in `./contrib/otel`,** as its own Go module (`github.com/syssam/velox2/contrib/otel`). The core module keeps no OpenTelemetry dependency. Core exposes vendor-neutral data; any other vendor's adapter can use it.
- **1.0 ships spans and metrics.** A slow-query log and log records are out of scope.
- **The SQL comment carries stable values only:** the application, the call site, and context tags the application lists. It never carries a trace ID or a tenant.
  - pgx caches prepared statements by SQL text, so a per-request value would defeat its cache.
  - `pg_stat_activity` shows the text, which a per-request value makes hard to read.
  - PostgreSQL's `pg_stat_statements` and MySQL's digest normalise comments away, so a comment does not split their statistics. (This design first said it would; the final review corrected it.)
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
- **When it is computed:** once per render-cache entry, and stored with the cached text, so a cached statement costs nothing. The render cache counts the fingerprint's bytes toward its 4 MiB bound. An uncached render, such as a statement whose entity a `Rewrite` hook replaced, or one past the cache's bound, computes it each time.
- **The SQL comment** (§1.3) is excluded, so a fingerprint does not change with the call site.
- **Stability:** a fingerprint is stable for a given velox version and dialect. A release that changes rendering may change fingerprints, and its notes say so.

### 1.2 Call site

`velox.WithCallers()` on `Open` records, in `Statement.Caller`, the first stack frame outside velox's own package and the application's registered wrapper packages.

- **Which frames are velox's:** only the root package `github.com/syssam/velox2`, whose path velox reads at run time from one of its own frames. Its sub-packages, such as `example` and `internal/testrepo`, are applications.
- **Format:** `Caller{File, Line, Func}`. `Func` is the function's full name, as `runtime.Frame.Function` gives it.
- **`File`:** relative to the root of the module that holds the frame's package: the package's directory within that module, then the file's base name.
  - The package path comes from the function name, with `%2e` unescaped and a trailing `_test` dropped (an external test package is in the directory of the one it tests). Package `main` is `BuildInfo.Path`.
  - The module is the longest module path in `debug.ReadBuildInfo` (the main module and every dependency) that holds the package.
  - Only the base name is read from the frame's file name, so `-trimpath` names (module path, `@version`) and full paths give the same `File`. A `//line` directive that names a file outside its package's directory is reported under the package's directory.
  - Failing all that, `File` is the base name.
- **How it is captured:** program counters are read with `runtime.Callers`, up to 64 frames. The first batch is as many frames as the last call site was down the stack (4 to 8; 4 at first), the next ones 8, so a statement usually costs one call that unwinds no more than it needs. The cache's read lock is taken once per batch. With nothing found within 64 frames, `Caller` is zero.
- **The cache:** each program counter's frames, inlined ones included, are resolved once and cached per DB, for up to 4,096 counters. Past that, a counter is resolved each time, as the render cache renders past its bound.
- **When:** only when something can read it. A DB with no hook and no SQL comment does not capture the call site, so `WithCallers` alone costs nothing.
- **Wrapper packages:** `velox.WithCallerSkip(pkgPath ...string)` names packages to skip as well, for an application's own repository layer. It implies `WithCallers`, since naming packages to skip means the application wants call sites.
- **Default:** off. Without it, `Caller` is zero.

### 1.3 SQL comment

`velox.WithSQLComment(app string, tags ...string)` appends a sqlcommenter comment to every statement's text:

```sql
SELECT … FROM "invoices" WHERE … /*app='billing',caller='billing%2Finvoice.go%3A42',route='GET%20%2Finvoices'*/
```

- **Keys:**
  - `app` is the given name.
  - `caller` is present when `WithCallers` is on.
  - Then each listed context tag (§1.4) that is set, in the order listed.
  - Keys are written in this order, never sorted.
- **Encoding:** as sqlcommenter specifies, keys and values are URL-encoded and values are quoted. Every byte outside the unreserved set `A–Z a–z 0–9 - . _ ~` is percent-encoded, with uppercase hex, so the call site reads `caller='billing%2Finvoice.go%3A42'`. No app name or tag value can then contain `'` or `*/`, so none can end the comment.
- **Rendering:** the text after the statement, ` /*…*/`, is rendered once per (call site, listed tags' values) and cached. It does not depend on the statement's shape, so the shape is not part of the key: that would only multiply the entries.
  - Joining it to the statement's cached text is the one allocation §6 allows.
  - The key encodes each part with its length, and an unset tag apart from an empty one, so no two call sites or tag lists share a key.
  - The cache holds at most 4,096 entries and 4 MiB of keys and texts, and evicts the oldest entry (FIFO) to make room. The render cache (Core §5), by contrast, stops growing at its bound.
- **Two copies of the text:** `Statement.SQL` is the text sent, comment included. The fingerprint is of the text without the comment.
- **Hooks:** a `Rewrite` hook sees no comment, because rendering comes after it. It may change `Caller` and `Tags`, and the comment is rendered from what it leaves. An `Around` hook sees the sent text.
- **`SQL(ctx)`** includes the comment, with the call site of the `SQL` call: it returns the text that would be sent from there.
- **Misuse:** `WithSQLComment` panics when it is called, in `Open`'s argument list, as `WithAround(nil)` does: with an empty `app`, or a listed tag name that is empty, repeated, `app` or `caller`.
- **Cache safety:** applications must list only tags whose values are stable per code path, such as a route template, never an ID. The documentation says so with the reasons: pgx caches prepared statements by their text, so a per-request value would prepare a statement per request, and it would make `pg_stat_activity` hard to read. `pg_stat_statements` and MySQL's digest normalise comments away, so their statistics are not split.
- **Call sites and pgx's statement cache:** with `WithCallers` on, each call site of a shape is a distinct statement text, so pgx prepares one statement per call site per connection. One shape called from many sites multiplies statements against pgx's per-connection cache, which holds 512 by default. The documentation (`WithSQLComment`, and `contrib/otel`'s `Config` and package doc) says to size `pgxpool.Config.ConnConfig.StatementCacheCapacity` for it, or to use `WithSQLComment` without callers.

### 1.4 Tags

`velox.WithTags(ctx, key, value, ...)` adds string tags to the context. Later calls add to, or replace, earlier ones: a key already set keeps its place and takes the new value, and a new key is appended. An odd number of strings panics when `WithTags` is called; none returns the context unchanged.

- `Statement.Tags` holds them, set before the hooks run. It is the context's slice, shared rather than copied, so tags cost nothing per statement. Where hooks run, velox clones it first, like the statement's other slices, so a `Rewrite` hook changes only its own statement's copy.
- Adapters read them for span attributes.
- The SQL comment includes only the keys `WithSQLComment` lists.

### 1.5 Results

`Statement.Result` is set when execution returns, before the `Around` hooks see `next`'s return:

```go
type Result struct {
    Rows     int64 // rows read
    Affected int64 // rows a write affected; -1 for a read or a failed statement
}
```

- **`Rows`** counts the rows read, an `INSERT … RETURNING`'s and a `Count`'s single row included. Rows are counted only when an `Around` hook exists or a row cap applies: otherwise nothing can read it, and counting would add an allocation.
- **`Affected`** is the driver's count for a write sent without `RETURNING`, the rows returned for one sent with it, and -1 for a read or a failed statement.
- **Each execution** resets `Result` first, so a retrying `Around` hook sees the last attempt's. It is also reset before the `Around` hooks run, so an outer hook sees `Affected: -1` and `Rows: 0` when an inner one refuses without calling `next`.

### 1.6 Transactions

`velox.WithTxAround(h TxAround)` adds a transaction hook:

```go
type TxAround func(ctx context.Context, tx *TxInfo, next func(context.Context) error) error

type TxInfo struct {
    ID        uint64             // unique within the process
    Isolation sql.IsolationLevel
    ReadOnly  bool
    Attempt   int                // 1 for the first try; RetrySerializable increments it
    Context   context.Context    // what the hooks passed to next; Tx's bounded context when there are none
    Retries   []TxRetry          // the attempts RetrySerializable retried, in order
    Outcome   TxOutcome          // how the last attempt ended; set before next returns
}

type TxRetry struct {
    Err error     // what the attempt failed with
    At  time.Time // when velox decided to retry
}

type TxOutcome uint8 // TxCommit, TxRollback or TxError; String gives "commit", "rollback", "error"
```

- **What it wraps:** a `TxAround` hook is called once per `db.Tx` call. velox runs the attempts inside `next`: `next` runs every `RetrySerializable` attempt and the commit, and `TxInfo.Attempt` changes before each attempt. The first hook given is the outermost. A savepoint (a nested `Tx`) runs inside the enclosing transaction's hooks and does not call them again.
  - Why: §2.2 asks for one span per `db.Tx` call, with the final attempt count and an event per retry. A hook called per attempt could not know which attempt is the last.
- **Retries:** velox appends a `TxRetry` before each retry. The retry events and `velox.tx.outcome` (§2.2) need what only velox knows; reading error text would be brittle.
- **Outcome:**
  - `fn`'s error is `TxRollback`, unless the transaction's context had ended (the deadline passed), which is `TxError`;
  - begin or commit failing, or the deadline passing before commit, is `TxError`;
  - a panic or `runtime.Goexit` in `fn` is `TxRollback`.
- **Statements inside:** each gets `Statement.Tx` pointing to the transaction's `TxInfo`. An adapter can then parent a statement's span on `Tx.Context`'s span even when the application passed its own context to the query.
- **Cost:** a `TxInfo` is made only when the DB has a hook of any kind (`Rewrite`, `Around` or `TxAround`); otherwise `Statement.Tx` is nil. Nothing else can read it, so a DB with nothing configured allocates nothing more per transaction.
- **Rules as for `Around`:** a `TxAround` hook must call `next` exactly once, or return an error of its own without calling it. Calling `next` a second time returns an error without running `fn`. Its errors are returned unchanged. If a hook recovers a panic from `next` and returns nil, `Tx` returns an error, since the transaction was rolled back; the outcome stays `TxRollback`. `WithTxAround(nil)` panics at `Open`.
- **velox's deadline:** past it, a transaction is never committed and its error matches `ErrTimeout`, even when a hook passed on a context without the deadline.

### 1.7 Statement fields, together

`Statement` gains `Fingerprint string`, `Caller Caller`, `Tags []Tag` (with `Tag{Key, Value string}`), `Result Result`, `Tx *TxInfo` and `Dialect Dialect`. An `Around` hook has only the statement, and §2.1's `db.system.name` needs the dialect.

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

The tracer and the meter are both named `github.com/syssam/velox2/contrib/otel`, with no version until velox tags releases. An error creating an instrument goes to `otel.Handle`, and the instrument the API returned is used.

The adapter never recovers a panic. Its spans are ended in deferred functions, so a panic in `fn` or in an `Iter` loop body ends them, with status Error and the description `panic`, and goes on unchanged.

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
| `code.file.path`, `code.line.number`, `code.function.name` | `Statement.Caller` | when callers are on |
| `velox.tag.<key>` | each `Statement.Tags` entry | always |
| `velox.tenant_bypass`, `velox.policy_bypass` | the reasons | when set |
| `velox.tenant` | `Statement.Tenant`, formatted | only with `Config.RecordTenant` |

The `code.*` names are those of the semantic-convention release whose `db.*` names this table uses; that release deprecated `code.filepath`, `code.lineno` and `code.function`.

`SQL(ctx)` does not execute, so it makes no span.

**Errors:**
- The span's status is Error.
- `error.type` is a stable, low-cardinality value. In order:
  - `velox.denied`, `velox.no_tenant`, `velox.unbounded`, `velox.too_many_rows`, `velox.timeout`;
  - `constraint.unique`, `constraint.foreign_key`, `constraint.not_null`, `constraint.check`;
  - `context.canceled`, `context.deadline_exceeded`;
  - else `_OTHER`.
- `velox.unbounded` covers `ErrUnbounded`, `ErrNoWhere` and `ErrLimitTooLarge`. Every other velox sentinel not listed is `_OTHER`.
- `velox.timeout` is matched by `errors.Is(err, velox.ErrTimeout)`, or by `context.DeadlineExceeded` while the hook's context has the cause `ErrTimeout` (a deadline velox set, reported by the driver as the context's).
- The error is recorded as a span event, with its message.
- A statement refused before execution (by a guard, tenant scoping, a policy or an entity hook) reaches no `Around` hook, so it makes no statement span and no metric point. It shows as the error of the transaction it ends.

**Parent:** the span's parent is the span in `Statement.Tx.Context` when the statement is in a transaction, and the span in the hook's context otherwise. The span's context is what the hook passes to `next`, so the driver's own instrumentation, if any, nests under it.

**Retries:** an `Around` hook that retries makes one span per attempt.

### 2.2 Transaction span

`TxAround` makes one span per `db.Tx` call, named `velox.tx`, of kind internal, with these attributes:
- `velox.tx.isolation`, `sql.IsolationLevel.String()` (for example `Default`), and `velox.tx.read_only`;
- `velox.tx.attempts`, the final count;
- `velox.tx.outcome`, `TxInfo.Outcome` (§1.6): `commit`, `rollback` (the function returned an error, or panicked), or `error` (begin or commit failed, the deadline passed, or an inner hook returned without calling `next`, which leaves velox no outcome).

Each retry adds a span event `retry` carrying the error type. The events are added after `next` returns, stamped with `TxRetry.At`.

A failed `db.Tx` gives the span the status Error and an `error.type`, with the mapping of §2.1.

### 2.3 Metrics

The instruments follow OpenTelemetry's database semantic conventions where they exist:

| Instrument | Kind, unit | Attributes |
|---|---|---|
| `db.client.operation.duration` | histogram, s | `db.system.name`, `db.operation.name`, `db.collection.name`, `velox.entity`, `error.type` (when failed); `velox.fingerprint` only with `Config.MetricFingerprint` |
| `db.client.response.returned_rows` | histogram, {row} | as above, for reads that succeeded only (so never `error.type`) |
| `velox.tx.duration` | histogram, s | `velox.tx.outcome`, `velox.tx.read_only`, `velox.tx.retried` (bool) |

A transaction whose function panics, or calls `runtime.Goexit`, still records its `velox.tx.duration` point, with the outcome `rollback`.

Histogram boundaries are the conventions' recommended ones for durations (from 0.001 s to 10 s), and powers of 4 up to 65,536 for rows. Cardinality is bounded by entities × 4 operations × the error-type values. With `MetricFingerprint`, it is bounded by the render cache's 4,096 shapes per process.

### 2.4 Versions

`contrib/otel` tracks the OpenTelemetry Go API's stable `v1` line, pinned at `v1.47.0`, the latest stable release on 2026-10-15. Semantic-convention attribute names are written out as constants in the adapter (`attrs.go`), not imported from a `semconv` package version. That keeps the dependency to the stable API, and the names change only with a velox release that says so.

The rule is about `contrib/otel`'s own imports: its package imports the OpenTelemetry API (`otel`, `attribute`, `codes`, `trace`, `metric`), velox and the standard library, and no SDK or `semconv` package. The API's own `trace` package imports a `semconv` package, which `contrib/otel` does not control, so `TestNoSDKDependency` checks the package's imports for `semconv` and its whole dependency graph for the SDK.

## 3. Example

`./example` is a module (`github.com/syssam/velox2/example`) with:
- an entity package owned by a "billing" team;
- generated code (`go tool velox-gen`);
- a `main` that walks through the API on in-memory SQLite: queries, relations, writes, guards, tenant and policy, transactions, deadlines and `SQL(ctx)`.

A golden-output test keeps it in step with the API, and CI runs it.

This sub-project adds a tracing example. The example module was split after this design was written into one directory per topic (`start`, `relations`, `tenant`, `policy`, `hooks`, `tx`, `tour`), each a `main` package with its own entity package, README and golden test, indexed by `example/README.md`. The tracing example is one more of them, `example/otel`:
- its entity package (`otel/model`) is the billing team's `Invoice`, with its generated file;
- it configures `contrib/otel` with the SDK's in-memory span exporter and a manual metric reader;
- a request span, carrying the `route` tag, pays an invoice in a transaction;
- it prints the span tree, with each statement's fingerprint, call site (`otel/main.go:<line>`) and the SQL comment sent, then the metric points recorded;
- it prints no span ID, time or duration, so its golden output is deterministic.

The example module requires `contrib/otel` (with a `replace` to `../contrib/otel`) and the OpenTelemetry SDK; only the otel example imports them.

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
- **`contrib/otel`,** with OpenTelemetry's in-memory span exporter and a manual metric reader, on a `database/sql` fake driver in its test files (velox's fake executor is in the root package's test files, which another module cannot import):
  - span names and attributes for every operation;
  - error types;
  - the transaction span tree when the application passes its own context;
  - one span per retried attempt;
  - metric instruments and their attribute sets;
  - fingerprints excluded from metrics by default.
- **Real database:** PostgreSQL under pgx in `contrib/otel`'s tests, run by `scripts/ci-local.sh` under `VELOX_TEST_POSTGRES`. The commented statement uses pgx's statement cache: `TestPgxPreparesACommentedStatementOnce` runs one call site three times, each with another tag the comment does not list, and finds one statement in `pg_prepared_statements`; a second call site makes two.
- **CI:** `scripts/ci-local.sh` and the GitHub workflow run `contrib/otel`'s and `example`'s tests, vet and lint.

## 6. Performance

- **Nothing configured:** Core's budgets (Core §8) are unchanged, and the fingerprint is computed into the render cache.
- **`WithCallers` and `WithSQLComment` on, cached statement:** velox adds at most 1 allocation per statement over the same statement without them, and aims at 300 ns; measured above it, see below.
  - The allocation is asserted: `TestObservedOneRowBudget` (7 allocations against 6).
  - The time is measured, not asserted, as Core §8 does for its own times, because times vary between machines and under `-race`. It is read from `BenchmarkOneRow` against `BenchmarkOneRowObserved`.
- **`contrib/otel`:** its cost beyond OpenTelemetry's SDK is benchmarked and reported, not budgeted: `BenchmarkAdapter` (a one-row read with `Options`, on an SDK tracer provider with a discarding exporter and a manual metric reader) against `BenchmarkBareSpan` (the same read, with an `Around` hook that only starts and ends one span).

Measured on 2026-10-15, darwin/arm64 (Apple M3 Max, 14 cores), against the fake driver. The machine was never quiet while this sub-project was built, so each row gives its load average (1 minute):

| Benchmarks | Run | Time | Added | Allocations | Load |
|---|---|---|---|---|---|
| `BenchmarkOneRow` / `BenchmarkOneRowObserved` | minimum of 600 alternating batches of 2,000 reads (while building the budget guards) | 0.69 / 1.03 µs | +340 ns | 6 / 7 | 20–45 |
| the same | medians of `-count 10` (while building the example) | 0.87 / 1.38 µs | +518 ns | 6 / 7 | 19–26 |
| the same | 10 interleaved pairs (while building the example) | medians 0.85 / 1.35 µs | median +478 ns (300 to 652) | 6 / 7 | 15–19 |
| `BenchmarkBareSpan` / `BenchmarkAdapter` | medians of `-count 10` (while building the example) | 3.9 / 9.0 µs | +5.1 µs | 25 / 41 | 19–21 |
| the same | 10 interleaved pairs (while building the example) | medians 3.2 / 9.2 µs | median +5.4 µs (4.3 to 8.2) | 25 / 41 | 15–19 |

- **The 300 ns aim is exceeded.** The medians put velox's added time at about 480 to 520 ns; the minimum, the figure least affected by the load, at 340 ns. A profile taken with that minimum split the 340 ns into the comment's join (about 80 ns) and one `runtime.Callers` (about 260 ns; unwinding costs about 60 ns a frame, the two skipped ones included). The first unwind is already sized to the last call site's depth (§1.2), so most of what remains is the unwinder's own cost. A quiet machine should re-measure before the number is cut further or the aim changed.
- **Runs at a load average of 39 to 52,** earlier the same day and while the transaction metrics were built, are not in the table: their medians moved by up to 4x between runs, and in one `-count 10` run the observed read's median was below the plain one's.
- **The allocations are stable** in every run: the observed read makes 1 more than the plain one, and the adapter 16 more than a bare span. The adapter's 16 are its metric attribute set (3), the span's attribute slice and start options, the SDK's handling of the extra attributes, and the call-site lookup.
- **`contrib/otel`'s time** is the SDK's as much as the adapter's: most of the difference is the SDK storing the span's attributes and recording two histogram points. For scale, a database round trip is typically 100 µs or more.

## 7. Out of scope

- Connection-pool metrics. pgx and database/sql have their own instrumentation.
- A slow-query log and log records.
- Trace IDs in SQL comments.
- Database-side tracing.
- Sampling decisions. They belong to the application's tracer provider.
