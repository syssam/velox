// Package veloxotel traces velox statements with OpenTelemetry. It uses the
// OpenTelemetry API only: the application chooses and configures the SDK.
//
// Options returns the velox options that trace every statement, to give to
// velox.Open after any of the application's own:
//
//	db := velox.OpenPgx(pool, veloxotel.Options(veloxotel.Config{
//		TracerProvider: tp,        // default otel.GetTracerProvider()
//		App:            "billing", // enables the SQL comment
//		CommentTags:    []string{"route"},
//	})...)
//
// They are velox.WithCallers, velox.WithAround with a hook that makes one span
// per statement, and velox.WithSQLComment when Config.App is set. Around
// returns that hook alone, for an application that wants only the spans.
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
// A statement that velox refuses before it is sent (a guard, the tenant
// scope, a policy or a hook) reaches no Around hook, so it makes no
// statement span: the refusal is the error of the call, and of the
// transaction it ends.
//
// Config.CommentTags lists the tags the SQL comment carries. List only tags
// whose values are stable per code path, such as a route template, never an
// ID: pgx caches prepared statements by their text, so a per-request value
// would prepare a statement per request (see velox.WithSQLComment).
package veloxotel
