// Package veloxotel traces and measures velox statements and transactions
// with OpenTelemetry. It uses the OpenTelemetry API only: the application
// chooses and configures the SDK.
//
// Options returns the velox options that trace every statement, to give to
// velox.Open after any of the application's own:
//
//	db := velox.OpenPgx(pool, veloxotel.Options(veloxotel.Config{
//		TracerProvider: tp,        // default otel.GetTracerProvider()
//		MeterProvider:  mp,        // default otel.GetMeterProvider()
//		App:            "billing", // enables the SQL comment
//		CommentTags:    []string{"route"},
//	})...)
//
// They are velox.WithCallers (unless Config.OmitCallers is set),
// velox.WithAround with a hook that makes one span per statement,
// velox.WithTxAround with a hook that makes one span per transaction, and
// velox.WithSQLComment when Config.App is set. An application that wants
// velox.WithCallerSkip passes it itself after Options; it turns call sites on.
//
// The names follow OpenTelemetry's semantic conventions v1.43.0. The
// tracer's and meter's instrumentation scope is the module's path, with the
// release's schema URL and the module's version as the build records it:
// none when the module is the main one at "(devel)" or is replaced by a
// directory.
//
// A statement's span is of kind client, named for its operation and table
// ("SELECT invoices"), which db.query.summary repeats, and covers sending it
// and reading every row; with Iter, the loop body too, as the connection is
// held for it. It carries the SQL as
// db.query.text (velox's SQL is parameterized: no argument's value is in
// it), unless Config.OmitQueryText is set. Raw SQL (velox.DB.Raw and Exec)
// has neither an operation nor a table that velox knows: velox does not
// parse them out of the SQL, as the semantic conventions advise, so its span
// is named for the database system ("postgresql") and has no
// db.operation.name, db.collection.name or db.query.summary. Its reason is
// velox.tenant_bypass and velox.policy_bypass. Its text is recorded only
// when it has arguments: the conventions say text that is not
// parameterized should not be collected by default, and velox cannot tell
// whether raw text without arguments holds literals. A read's span gets
// db.response.returned_rows, which the conventions make opt-in, only with
// Config.RecordReturnedRows. A span carries the fingerprint, the call site
// and the context's tags. An Around hook of the application's own that
// retries, given before these options, gets one span per attempt.
//
// A statement's span is a child of the span on the statement's context.
// Inside db.Tx, fn's ctx carries the transaction's span (velox.tx, once the
// transaction hook is installed), so a statement run on it nests under its
// transaction, and under a span the application starts inside fn on it.
//
// The transaction's span, velox.tx, is of kind internal and covers every
// attempt TxRetrySerializable makes and the commit. It carries the isolation
// level, the read-only flag, the final attempt count and the outcome
// (commit, rollback or error), with a "retry" event per retried attempt. A
// savepoint (a nested Tx) makes no span of its own.
//
// A panic in the transaction's function, or in an Iter loop body, ends the
// open spans with the status Error and the description "panic", records the
// transaction's duration with the outcome rollback, and goes on: the adapter
// never recovers one. runtime.Goexit is reported the same way.
//
// A statement's span says where it ran. velox.route.shard, on a sharded DB,
// is a start attribute, so samplers and span processors see it.
// velox.route.replica (whether a replica ran the statement) and
// velox.route.fallback (a replica failed and the primary ran the read; only
// when true) are set when the statement ends, because they can change while
// it runs.
//
// The metrics are three histograms: db.client.operation.duration (seconds)
// and db.client.response.returned_rows (reads that succeeded) per statement,
// and velox.tx.duration (seconds) per transaction; and a counter,
// velox.route.fallbacks, of the reads that fell over to the primary, once per
// failover even when an outer hook retries the statement. A statement's
// duration is the database's time: with Iter it leaves out the loop body
// (velox.Result.LoopBody), which the span covers, also when the loop breaks
// early. Their attributes are bounded: the database system, operation, table
// and summary (none for raw SQL), entity, error type and, on a sharded DB,
// shard for a statement; the outcome, read-only flag and whether it retried
// for a transaction; and the shard alone for the fallback counter. Tags, the
// tenant, the SQL text and the call site are on spans only; the fingerprint
// is added to the statement metrics only with
// Config.RecordMetricFingerprint.
//
// A statement that velox refuses before it is sent (a guard, the tenant
// scope, a policy or a hook) reaches no Around hook, so it makes no
// statement span: the refusal is the error of the call, and of the
// transaction it ends.
//
// Config.CommentTags lists the tags the SQL comment carries. List only tags
// whose values are stable per code path, such as a route template, never an
// ID: pgx caches prepared statements by their text, so a per-request value
// would prepare a statement per request (see velox.WithSQLComment).
//
// With Config.App set and call sites recorded (the default), the comment
// carries the call site too: each call site of a shape is then a distinct
// text, and
// pgx prepares one statement per call site per connection. A shape called
// from many sites multiplies statements against pgx's per-connection cache,
// 512 by default. Size pgxpool.Config.ConnConfig's StatementCacheCapacity
// for it, or set Config.OmitCallers (the spans then carry no call
// site, and the comment is one text per shape).
package veloxotel
