package velox

import (
	"context"
	"database/sql"
	"time"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/veloxtype"
)

// Conn is a connection pool that velox can run statements on: a DB's primary
// or one of its replicas. Make one with SQLConn or a driver package's Conn, such
// as veloxpgx.Conn; the zero Conn is none, and a DB given one panics.
type Conn = engine.Conn

// SQLConn returns a Conn that runs statements through db, a *sql.DB the
// application opened with a driver for the DB's dialect. A nil db panics.
func SQLConn(db *sql.DB) Conn { return engine.SQLConn(db) }

// WithReplicas adds read replicas to a DB's primary, the connection Open or
// OpenConn was given. A read outside a transaction goes to a replica, each in
// turn, unless its context is marked with WithPrimaryReads; a query's With
// loads go to the replica the query read. Writes, and every statement in a
// transaction, go to the primary. Statement.Route records the choice.
//
// Give each replica's pool a connect timeout shorter than your request
// deadlines: pgx's connect_timeout, a MySQL DSN's timeout=. Neither driver
// sets one, so a replica that drops packets stalls the dial until the
// operating system gives up. A dial that times out falls over to the primary
// and puts the replica in back-off; a dial still hanging when the request's
// context ends is the context's error, and neither retries nor backs off.
//
// A zero Conn panics, and a Conn valid only on another dialect
// panics in Open.
func WithReplicas(conns ...Conn) Option { return engine.WithReplicas(conns...) }

// Shard is one shard of a DB opened with OpenShards: a primary and its read
// replicas, among which the shard's statements are routed as WithReplicas
// routes a DB's. As there, each replica's pool needs a connect timeout
// shorter than the request deadlines (pgx's connect_timeout, a MySQL DSN's
// timeout=): a dial that times out falls over to the shard's primary, but a
// dial still hanging when the request's context ends is the context's error,
// and neither retries nor backs the replica off.
//
// Its fields:
//
//	Primary Conn
//	Replicas []Conn
type Shard = engine.Shard

// WithShardFor sets the function that gives the name of a statement's shard,
// on a DB opened with OpenShards, from its context: it reads the value the
// application keyed its shards by (an organization, a region), as a policy
// does. It is called, with the statement's context, for each statement and Tx
// whose context has no WithShard, so it should be fast; a SQL(ctx) dry run
// calls it too. It returns "" for a context that names no shard, which refuses
// the statement with ErrNoShard. Its error refuses the statement too, which
// then matches both ErrNoShard and the error.
//
// One write can call it more than once: an InsertAll split into two
// statements calls it three times, for its transaction and for each
// statement, and a Tx calls it when it begins and for each statement in it.
//
// f is called concurrently, from the goroutine of every statement, so it must
// be safe for that, and fast: a map lookup or a hash, not a query.
//
// It is required with OpenShards; given to Connect, Open or OpenConn, it panics there.
// A nil f panics. WithShard names a statement's shard from its context instead.
func WithShardFor(f func(ctx context.Context) (string, error)) Option {
	return engine.WithShardFor(f)
}

// WithShard returns a context whose statements, on a DB opened with
// OpenShards, run on the shard named name, whatever the context says:
// for admin jobs and for data that belongs to no shard key. A later WithShard
// replaces it. On a DB opened otherwise it is ignored. Without it, the shard
// is the one WithShardFor gives for the context.
func WithShard(ctx context.Context, name string) context.Context { return engine.WithShard(ctx, name) }

// WithPrimaryReads returns a context whose reads go to the primary, on a DB
// with replicas: for a read that must see a write the replicas may not have
// yet.
func WithPrimaryReads(ctx context.Context) context.Context { return engine.WithPrimaryReads(ctx) }

// WithSession returns a context carrying a new session, typically one
// request's: after a write made through the context (or a context derived
// from it), the session's reads go to the primary for the DB's pin window
// (WithPinWindow), so that the session reads its own writes from a replica
// that may not have them yet. A write pins it once it has reached the
// database, even when it then fails: its outcome is unknown. A transaction
// that wrote counts as a write once it sent its COMMIT, at the commit, even
// when the COMMIT then failed; one that rolled back does not. Without a
// session in the context, nothing is pinned. A session is safe for
// concurrent use and lives only in its context; a context derived from a
// session's keeps it, and WithSession on one starts a new one.
func WithSession(ctx context.Context) context.Context { return engine.WithSession(ctx) }

// PinUntil pins ctx's session to the primary until t, if that is later than
// its pin now: an application that stored the time of a user's last write
// restarts the pin on the user's next request with it (PinnedUntil gives the
// time to store). It never lowers a pin, and does nothing on a context
// without a session. A t after 2262, past what PinnedUntil can return, pins
// until then: until the end of the session, in effect.
func PinUntil(ctx context.Context, t time.Time) { engine.PinUntil(ctx, t) }

// PinnedUntil returns the time until which ctx's session reads the primary:
// the latest of its writes' pin windows and PinUntil's times. It is zero on a
// context without a session, and on a session that has not been pinned.
func PinnedUntil(ctx context.Context) time.Time { return engine.PinnedUntil(ctx) }

// WithPinWindow sets how long after a write its session's reads go to the
// primary; the default is 2 seconds, sized to typical replica lag, so a DB
// whose replicas lag more needs a larger window. A negative d panics.
func WithPinWindow(d time.Duration) Option { return engine.WithPinWindow(d) }

// WithReplicaBackoff sets how long a replica is skipped after a read sent to
// it failed with a connection error; the default is 5 seconds. The read
// itself is retried once on the primary, with Statement.Route's Fallback set.
// A read whose context has ended is neither retried nor backs the replica
// off; see WithReplicas for the connect timeout that keeps a hung dial from
// ending that way.
// A negative d panics.
func WithReplicaBackoff(d time.Duration) Option { return engine.WithReplicaBackoff(d) }

// Route is where a statement runs.
type Route = veloxtype.Route
