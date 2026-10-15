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
// They are velox.WithCallers, velox.WithAround with a hook that makes one span
// per statement, velox.WithTxAround with a hook that makes one span per
// transaction, and velox.WithSQLComment when Config.App is set. Around and
// TxAround return those hooks alone, for an application that wants only part.
//
// A statement's span is of kind client, named for its operation and table
// ("SELECT invoices"), and covers sending it and reading every row; with
// Iter, the loop body too. It carries the fingerprint, the call site and the
// context's tags. An Around hook of the application's own that retries, given
// before these options, gets one span per attempt.
//
// Inside db.Tx, a statement's span is a child of the span on the
// transaction's context (the velox.tx span once the transaction hook is
// installed), whatever context the code passes to the query: a statement run
// on a request's context inside fn still nests under its transaction.
//
// The transaction's span, velox.tx, is of kind internal and covers every
// attempt RetrySerializable makes and the commit. It carries the isolation
// level, the read-only flag, the final attempt count and the outcome
// (commit, rollback or error), with a "retry" event per retried attempt. A
// savepoint (a nested Tx) makes no span of its own.
//
// A panic in the transaction's function, or in an Iter loop body, ends the
// open spans with the status Error and the description "panic", records the
// transaction's duration with the outcome rollback, and goes on: the adapter
// never recovers one. runtime.Goexit is reported the same way.
//
// The metrics are three histograms: db.client.operation.duration (seconds)
// and db.client.response.returned_rows (reads that succeeded) per
// statement, and velox.tx.duration (seconds) per transaction. Their attributes are bounded:
// the database system, operation, table, entity and error type for a
// statement, and the outcome, read-only flag and whether it retried for a
// transaction. Tags, the tenant, the SQL text and the call site are on spans
// only; the fingerprint is added to the statement metrics only with
// Config.MetricFingerprint.
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
// With Config.App set, the comment carries the call site too, since Options
// turns callers on: each call site of a shape is then a distinct text, and
// pgx prepares one statement per call site per connection. A shape called
// from many sites multiplies statements against pgx's per-connection cache,
// 512 by default. Size pgxpool.Config.ConnConfig's StatementCacheCapacity
// for it, or install Around and TxAround with velox.WithSQLComment and
// without velox.WithCallers (the spans then carry no call site).
package veloxotel
