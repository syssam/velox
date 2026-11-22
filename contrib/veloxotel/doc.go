// Package veloxotel traces and measures velox statements and transactions
// with OpenTelemetry. It uses the OpenTelemetry API only: the application
// chooses and configures the SDK.
//
// # At a glance
//
//	kind    name                              unit or kind  for each
//	span    {operation} {table}               client        statement execution
//	span    velox.tx                          internal      db.Tx, all its attempts
//	metric  db.client.operation.duration      s             statement execution
//	metric  db.client.response.returned_rows  {row}         successful read
//	metric  velox.tx.duration                 s             transaction
//	metric  velox.route.fallbacks             {fallback}    replica failover
//
// # Use
//
// Options returns the velox options that trace every statement. Give them to
// velox.Open after any of the application's own:
//
//	db := veloxpgx.Open(pool, veloxotel.Options(veloxotel.Config{
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
// The names follow OpenTelemetry's semantic conventions v1.43.0. The tracer's
// and meter's instrumentation scope is the module's path, with the release's
// schema URL and the module's version as the build records it: none when the
// module is the main one at "(devel)" or is replaced by a directory.
//
// # Statements
//
// A statement's span is of kind client, named for its operation and table
// ("SELECT invoices"), which db.query.summary repeats. It covers sending the
// statement and reading every row; with Iter, the loop body too, as the
// connection is held for it. It carries the SQL as db.query.text (velox's SQL
// is parameterized: no argument's value is in it), unless Config.OmitQueryText
// is set, the fingerprint, the call site and the context's tags. A read's span
// gets db.response.returned_rows, which the conventions make opt-in, only with
// Config.RecordReturnedRows.
//
// A statement's span is a child of the span on the statement's context.
// Inside db.Tx, fn's ctx carries the transaction's span, so a statement run
// on it nests under its transaction, and under a span the application starts
// inside fn on it.
//
// A statement that velox refuses before it is sent (a guard, a policy or a
// hook, as a scope is) reaches no Around hook, so it makes no statement span:
// the refusal is the error of the call, and of the transaction it ends.
//
// # Raw SQL
//
// velox.DB.Raw and Exec have neither an operation nor a table that velox
// knows, and velox does not parse them out of the SQL, as the semantic
// conventions advise. Their span has no db.operation.name, db.collection.name
// or db.query.summary, and is named as the conventions name a span without a
// summary: for Config.Target's (or TargetFor's) Namespace, else its Address
// and Port ("db.internal:5432"), else the database system ("postgresql").
// It carries velox.policy_bypass, the reason for a bypass.
//
// Its text is recorded only when it has arguments: the conventions say text
// that is not parameterized should not be collected by default, and velox
// cannot tell whether raw text without arguments holds literals.
//
// # Transactions
//
// The transaction's span, velox.tx, is of kind internal and covers every
// attempt TxRetrySerializable makes and the commit. It carries the isolation
// level, the read-only flag, the final attempt count and the outcome (commit,
// rollback or error), with a "retry" event per retried attempt. A savepoint
// (a nested Tx) makes no span of its own.
//
// Its start attributes also name where it runs, from velox.TxInfo.Route: the
// shard as velox.route.shard on a sharded DB, and the server, Config.Target's
// or TargetFor's for the shard's primary.
//
// # Panics
//
// A panic in the transaction's function, or in an Iter loop body, ends the
// open spans with the status Error and the description "panic", records the
// transaction's duration with the outcome rollback, and goes on: the adapter
// never recovers one. runtime.Goexit is reported the same way. Those spans
// get error.type "_OTHER", the conventions' fallback: a panic value's Go type
// is not a bounded set, and Goexit has no value.
//
// # Errors
//
// A failed statement's span and duration point, and a failed transaction's
// span, carry error.type and, when the driver gave one, its code as
// db.response.status_code (see
// velox.DriverCode): PostgreSQL's SQLSTATE, MySQL's error number or SQLite's
// extended result code. error.type is velox's own value, such as
// velox.denied or constraint.unique, where one fits, and otherwise the
// driver's code, or _OTHER when there is none. A retry event on the
// transaction's span gets the same. The code names the database's internals:
// it is for spans and metrics, not for a message a client sees.
//
// Such a span's status is Error, and its description is error.type. Its
// exception event has exception.type, the error's Go type, and no
// exception.message. No span holds an error's text: a driver's message can
// quote a row's values, as MySQL's "Duplicate entry 'alice@example.com'"
// does. The application logs the error it is returned when it wants the
// text.
//
// # Where it ran
//
// The adapter sees no connection string, so the application names the
// server: Config.Target gives every statement's db.namespace (PostgreSQL's
// "{database}|{schema}", MySQL's database), server.address and server.port,
// as start attributes and on its duration point.
//
// On a sharded or replicated DB, a statement's span also says which route ran
// it. velox.route.shard is a start attribute, so samplers and span processors
// see it. velox.route.replica (whether a replica ran the statement) and
// velox.route.fallback (a replica failed and the primary ran the read; only
// when true, and only on the span of the execution that fell over, not on an
// outer hook's retry of it) are set when the statement ends, because they can
// change while it runs.
//
// Config.TargetFor, when set, names the server per shard and replica from the
// statement's velox.Route. A route is final only once the statement ran, as a
// read on a failed replica falls over to the primary, so TargetFor is called
// then, once, and its attributes are set on the span before it ends and on
// the duration point: a read that fell over names only the primary. A
// transaction runs on its shard's primary, which is known when it starts, so
// TargetFor is called with its route then, once, for its span and its
// duration point. With the hosts of each
// shard's primary and replicas in two maps:
//
//	TargetFor: func(r velox.Route) veloxotel.Target {
//		t := veloxotel.Target{Namespace: "billing|public", Port: 5432}
//		switch {
//		case r.Replica >= 0: // replica r.Replica of shard r.Shard
//			t.Address = replicaHosts[r.Shard][r.Replica]
//		default: // -1: the primary; r.Shard is "" on an unsharded DB
//			t.Address = primaryHosts[r.Shard]
//		}
//		return t
//	},
//
// # Metrics
//
// Three histograms: db.client.operation.duration (seconds) and
// db.client.response.returned_rows (reads that succeeded) per statement, and
// velox.tx.duration (seconds) per transaction; and a counter,
// velox.route.fallbacks, of the reads that fell over to the primary, once per
// failover even when an outer hook retries the statement.
//
// A statement's duration is the database's time: with Iter it leaves out the
// loop body (velox.Result.LoopBody), which the span covers, also when the
// loop breaks early.
//
// Metric attributes are bounded. A statement has the database system,
// operation, table and summary (none for raw SQL), entity, the server
// Config.Target or TargetFor names, error type and status code and, on a
// sharded DB, shard. A transaction has the outcome, read-only flag, whether it
// retried, the server and, on a sharded DB, shard. The fallback counter has
// the shard alone. Tags, the scope values, the SQL text and the call site are
// on spans only; the fingerprint is added to the statement metrics only with
// Config.RecordMetricFingerprint.
//
// # Retrying hooks
//
// An Around hook of the application's own that retries goes before Options,
// as velox.WithAround makes the first one given the outermost:
//
//	opts := []velox.Option{velox.WithAround(retryOnDeadlock)} // the outer one
//	opts = append(opts, veloxotel.Options(cfg)...)            // inside it
//	db := veloxpgx.Open(pool, opts...)
//
// The adapter is then inside it and makes one span and one duration point per
// attempt. Given outside a retrying hook, it sees the whole retry as one
// statement, and an Iter that is run again after it yielded rows records a
// duration that still holds the first attempt's loop body.
//
// # SQL comment
//
// With Config.App set, velox.WithSQLComment appends a comment to each
// statement. Config.CommentTags lists the tags it carries. List only tags
// whose values are stable per code path, such as a route template, never an
// ID: pgx caches prepared statements by their text, so a per-request value
// would prepare a statement per request (see velox.WithSQLComment).
//
// With call sites recorded (the default), the comment carries the call site
// too: each call site of a shape is then a distinct text, and pgx prepares
// one statement per call site per connection. A shape called from many sites
// multiplies statements against pgx's per-connection cache, 512 by default.
// Size pgxpool.Config.ConnConfig's StatementCacheCapacity for it, or set
// Config.OmitCallers (the spans then carry no call site, and the comment is
// one text per shape).
//
// # Cost
//
// What every execution of a statement's shape shares (its table, summary and
// the attributes of a success's metric points) is built once per shape and
// cached, for up to 2,048 shapes, as are the tag keys' attribute names. A span
// that is not sampled gets its start attributes, which the sampler reads,
// and nothing more; its metric points are recorded as a sampled one's.
// Beyond those, the cost is the SDK's: storing the span's attributes and
// recording the points.
//
// # Not covered
//
// Connection-pool metrics (db.client.connection.*) are the driver's: with
// pgx, otelpgx's RecordStats reads a pgxpool.Pool's statistics; with
// database/sql, otelsql's RegisterDBStatsMetrics reads a sql.DB's. The adapter
// sees statements, not connections.
//
// The SQL comment carries no trace context (traceparent), as sqlcommenter's
// does: a trace ID is a per-request value, so pgx would prepare a statement
// per request. A trace finds its statements by the spans, and a statement's
// call site and tags are in its comment for the database's own views.
//
// Sampling is the tracer provider's. A statement's start attributes (its
// operation, table, entity, shard and Config.Target) are there for a sampler
// to decide on.
package veloxotel
