package velox

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net"
	"reflect"
	"slices"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Conn is a connection pool that velox can run statements on: a DB's primary
// or one of its replicas. Make one with SQLConn or PgxConn; the zero Conn is
// none, and a DB given one panics.
type Conn struct {
	ex  executor
	pgx bool // pgx's native protocol, valid only for PostgreSQL
}

// SQLConn returns a Conn that runs statements through db, a *sql.DB the
// application opened with a driver for the DB's dialect. A nil db panics.
func SQLConn(db *sql.DB) Conn {
	if db == nil {
		panic("velox: SQLConn(nil)")
	}
	return Conn{ex: sqlExecutor{c: db, db: db}}
}

// PgxConn returns a Conn that runs statements through pool with pgx's native
// protocol. It is valid only on a PostgreSQL DB; on another, Open panics. A
// nil pool panics.
func PgxConn(pool *pgxpool.Pool) Conn {
	if pool == nil {
		panic("velox: PgxConn(nil)")
	}
	return Conn{ex: pgxExecutor{c: pool, pool: pool}, pgx: true}
}

// WithReplicas adds read replicas to a DB's primary, the connection Open or
// OpenPgx was given. A read outside a transaction goes to a replica, each in
// turn, unless its context is marked with ReadPrimary; a query's With loads go
// to the replica the query read. Writes, and every statement in a transaction,
// go to the primary. Statement.Route records the choice.
//
// Give each replica's pool a connect timeout shorter than your request
// deadlines: pgx's connect_timeout, a MySQL DSN's timeout=. Neither driver
// sets one, so a replica that drops packets stalls the dial until the
// operating system gives up. A dial that times out falls over to the primary
// and puts the replica in back-off; a dial still hanging when the request's
// context ends is the context's error, and neither retries nor backs off.
//
// A zero Conn panics, and a PgxConn on a DB whose dialect is not PostgreSQL
// panics in Open.
func WithReplicas(conns ...Conn) Option {
	for i, c := range conns {
		if c.ex == nil {
			panic(fmt.Sprintf("velox: WithReplicas: replica %d is the zero Conn; make it with SQLConn or PgxConn", i))
		}
	}
	conns = slices.Clone(conns)
	return Option{func(c *config) { c.replicas = append(c.replicas, conns...) }}
}

// Shard is one shard of a DB opened with OpenShards: a primary and its read
// replicas, among which the shard's statements are routed as WithReplicas
// routes a DB's. As there, each replica's pool needs a connect timeout
// shorter than the request deadlines (pgx's connect_timeout, a MySQL DSN's
// timeout=): a dial that times out falls over to the shard's primary, but a
// dial still hanging when the request's context ends is the context's error,
// and neither retries nor backs the replica off.
type Shard struct {
	Primary  Conn
	Replicas []Conn
}

// OpenShards returns a DB of dialect d whose statements each run on one of
// shards, chosen from the statement's context:
//
//  1. the shard OnShard names;
//  2. otherwise, the one WithShardFor's function returns for the context's
//     tenant (WithTenant); its error refuses the statement;
//  3. otherwise none: the statement fails with ErrNoShard, as it does under
//     AllTenants without OnShard.
//
// A name that is not one of shards fails with ErrNoShard too, naming it.
// Within its shard a statement is routed as on a DB with replicas: writes to
// the primary, reads to the replicas in turn, a failed replica read to the
// shard's primary. Statement.Route.Shard names the shard.
//
// Tx picks its shard from its context at begin, and every statement in the
// transaction runs there: one whose context names no shard as well, but one
// whose context names another fails, and nothing is sent.
//
// WithShardFor is required, and WithReplicas is refused: each Shard has its
// own Replicas. OpenShards panics on either mistake, on no shards, on a shard
// named "" and on a shard whose Primary or a replica is the zero Conn, or a
// PgxConn when d is not PostgreSQL.
func OpenShards(d Dialect, shards map[string]Shard, opts ...Option) *DB {
	if len(shards) == 0 {
		panic("velox: OpenShards: no shards")
	}
	cfg := newConfig(opts)
	if cfg.shardFor == nil {
		panic("velox: OpenShards: WithShardFor is required, to find a statement's shard from its tenant")
	}
	if len(cfg.replicas) > 0 {
		panic("velox: OpenShards: WithReplicas is for Open and OpenPgx; give each Shard its Replicas")
	}
	rt := &router{shards: make(map[string]*shard, len(shards)), shardFor: cfg.shardFor}
	for _, name := range slices.Sorted(maps.Keys(shards)) {
		s := shards[name]
		if name == "" {
			panic("velox: OpenShards: a shard's name must not be empty")
		}
		what := fmt.Sprintf("OpenShards: shard %q's", name)
		switch {
		case s.Primary.ex == nil:
			panic(fmt.Sprintf("velox: %s Primary is the zero Conn; make it with SQLConn or PgxConn", what))
		case s.Primary.pgx && d != Postgres:
			panic(fmt.Sprintf("velox: %s Primary is a PgxConn, valid only for PostgreSQL; the DB is %s", what, d))
		}
		for i, c := range s.Replicas {
			if c.ex == nil {
				panic(fmt.Sprintf("velox: %s replica %d is the zero Conn; make it with SQLConn or PgxConn", what, i))
			}
		}
		sh := newShard(d, s.Primary.ex, s.Replicas, what)
		sh.name = name
		rt.shards[name] = sh
	}
	return &DB{d: d, rt: rt, cfg: cfg}
}

// WithShardFor sets the function that gives the name of a statement's shard,
// on a DB opened with OpenShards, from its context's tenant: the id given to
// WithTenant. It is called, with the statement's context, for each statement
// and Tx whose context has a tenant and no OnShard, so it should be fast; a
// SQL(ctx) dry run calls it too. Its error refuses the statement, which then
// matches both ErrNoShard and the error.
//
// f is called concurrently, from the goroutine of every statement, so it must
// be safe for that, and fast: a map lookup or a hash, not a query.
//
// It is required with OpenShards; given to Open or OpenPgx, it panics there.
// A nil f panics.
func WithShardFor(f func(ctx context.Context, tenant any) (string, error)) Option {
	if f == nil {
		panic("velox: WithShardFor(nil)")
	}
	return Option{func(c *config) { c.shardFor = f }}
}

// OnShard returns a context whose statements, on a DB opened with
// OpenShards, run on the shard named name, whatever the context's tenant:
// for admin jobs and for data that belongs to no tenant. A later OnShard
// replaces it. On a DB opened otherwise it is ignored.
func OnShard(ctx context.Context, name string) context.Context {
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	rc.shard, rc.onShard = name, true
	return context.WithValue(ctx, routeKey{}, rc)
}

// ReadPrimary returns a context whose reads go to the primary, on a DB with
// replicas: for a read that must see a write the replicas may not have yet.
func ReadPrimary(ctx context.Context) context.Context {
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	rc.primary = true
	return context.WithValue(ctx, routeKey{}, rc)
}

// WithSession returns a context carrying a new session, typically one
// request's: after a write made through the context (or a context derived
// from it), the session's reads go to the primary for the DB's pin window
// (WithPinWindow), so that the session reads its own writes from a replica
// that may not have them yet. A transaction that wrote and committed counts as
// a write, at its commit. Without a session in the context, nothing is
// pinned. A session is safe for concurrent use and lives only in its context;
// a context derived from a session's keeps it, and WithSession on one starts
// a new one.
func WithSession(ctx context.Context) context.Context {
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	rc.sess = new(session)
	return context.WithValue(ctx, routeKey{}, rc)
}

// PinUntil pins ctx's session to the primary until t, if that is later than
// its pin now: an application that stored the time of a user's last write
// restarts the pin on the user's next request with it (PinnedUntil gives the
// time to store). It never lowers a pin, and does nothing on a context
// without a session. A t after 2262, past what PinnedUntil can return, pins
// until then: until the end of the session, in effect.
func PinUntil(ctx context.Context, t time.Time) {
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	if rc.sess == nil || !t.After(time.Unix(0, 0)) {
		return // a time before 1970 is past: it pins nothing
	}
	u := int64(math.MaxInt64) // t.UnixNano() wraps after 2262
	if t.Before(time.Unix(0, math.MaxInt64)) {
		u = t.UnixNano()
	}
	rc.sess.raise(u)
}

// PinnedUntil returns the time until which ctx's session reads the primary:
// the latest of its writes' pin windows and PinUntil's times. It is zero on a
// context without a session, and on a session that has not been pinned.
func PinnedUntil(ctx context.Context) time.Time {
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	if rc.sess == nil {
		return time.Time{}
	}
	if u := rc.sess.until.Load(); u != 0 {
		return time.Unix(0, u)
	}
	return time.Time{}
}

// WithPinWindow sets how long after a write its session's reads go to the
// primary; the default is 2 seconds, sized to typical replica lag, so a DB
// whose replicas lag more needs a larger window. A negative d panics.
func WithPinWindow(d time.Duration) Option {
	if d < 0 {
		panic(fmt.Sprintf("velox: WithPinWindow(%v): the window must not be negative", d))
	}
	return Option{func(c *config) { c.pinWindow = d }}
}

// WithReplicaBackoff sets how long a replica is skipped after a read sent to
// it failed with a connection error; the default is 5 seconds. The read
// itself is retried once on the primary, with Statement.Route's Fallback set.
// A read whose context has ended is neither retried nor backs the replica
// off; see WithReplicas for the connect timeout that keeps a hung dial from
// ending that way.
// A negative d panics.
func WithReplicaBackoff(d time.Duration) Option {
	if d < 0 {
		panic(fmt.Sprintf("velox: WithReplicaBackoff(%v): the back-off must not be negative", d))
	}
	return Option{func(c *config) { c.backoff = d }}
}

// session is a context's record of when its reads may leave the primary
// again.
type session struct {
	until atomic.Int64 // the end of the pin, in Unix nanoseconds; 0: none
}

// raise moves the pin's end to t, unless it is later already.
func (s *session) raise(t int64) {
	for {
		u := s.until.Load()
		if t <= u || s.until.CompareAndSwap(u, t) {
			return
		}
	}
}

// pinned reports whether the session's reads must go to the primary now.
func (s *session) pinned() bool {
	u := s.until.Load()
	return u != 0 && time.Now().UnixNano() < u
}

// wrote records that a statement wrote successfully: in a transaction, in the
// flag its commit reads; outside one, in the context's session. A DB without
// replicas keeps no record.
func (db *DB) wrote(ctx context.Context) {
	if db.inTx {
		if db.txWrote != nil {
			db.txWrote.Store(true)
		}
		return
	}
	if db.rt != nil {
		db.pinSession(ctx)
	}
}

// pinSession pins ctx's session, if it has one, for the DB's pin window from
// now.
func (db *DB) pinSession(ctx context.Context) {
	if rc, _ := ctx.Value(routeKey{}).(routeCtx); rc.sess != nil {
		rc.sess.raise(time.Now().Add(db.cfg.pinWindow).UnixNano())
	}
}

// Route is where a statement runs. The route stage sets Statement.Route
// after the Rewrite hooks and before the Around hooks run.
type Route struct {
	Shard    string // "" on an unsharded DB
	Replica  int    // index into the replicas; -1 for the primary
	Fallback bool   // a replica failed and the read was retried on the primary
}

// routeKey is the context key of the route marks. They share it, so that the
// route stage reads them all in one lookup.
type routeKey struct{}

// routeCtx is a context's route marks. Each function that sets one copies
// the context's routeCtx, changes it, and stores the copy.
type routeCtx struct {
	sess    *session // WithSession's; nil: none
	shard   string   // OnShard's name, when onShard is set
	primary bool     // ReadPrimary: reads go to the primary
	onShard bool     // OnShard: statements go to the shard named shard
}

// router is the connections of a DB with replicas or shards, among which the
// route stage chooses. A DB with neither has none, and runs every statement
// on db.ex.
type router struct {
	one *shard // Open's primary and WithReplicas's replicas; nil when sharded
	// shards are OpenShards's, by name, and shardFor its WithShardFor; nil
	// on a DB that is not sharded.
	shards   map[string]*shard
	shardFor func(ctx context.Context, tenant any) (string, error)
}

// shard is a primary and its replicas.
type shard struct {
	name     string // "" on an unsharded DB
	primary  executor
	replicas []*replica
	next     atomic.Uint32 // the reads sent to replicas: the round-robin's turn
}

// replica is one of a shard's replicas.
type replica struct {
	ex executor
	// downUntil is the time, in Unix nanoseconds, until which the replica is
	// in back-off and skipped; 0 when it never was.
	downUntil atomic.Int64
}

// target is the connection the route stage chose for a statement: exec sends
// it there.
type target struct {
	ex      executor
	sh      *shard // the shard it is on; nil on a DB without a router and in a transaction
	replica int    // index into sh.replicas; -1 for the primary
}

// newRouter returns the router of an unsharded DB of dialect d on primary
// and replicas. A PgxConn on a DB of another dialect panics.
func newRouter(d Dialect, primary executor, replicas []Conn) *router {
	return &router{one: newShard(d, primary, replicas, "WithReplicas:")}
}

// newShard returns a shard of a DB of dialect d on primary and replicas. A
// PgxConn replica on a DB of another dialect panics, in a message that
// starts with what.
func newShard(d Dialect, primary executor, replicas []Conn, what string) *shard {
	sh := &shard{primary: primary}
	for i, c := range replicas {
		if c.pgx && d != Postgres {
			panic(fmt.Sprintf("velox: %s replica %d is a PgxConn, valid only for PostgreSQL; the DB is %s", what, i, d))
		}
		sh.replicas = append(sh.replicas, &replica{ex: c.ex})
	}
	return sh
}

// shardOf returns the shard ctx names on a sharded DB, whose route marks are
// rc: OnShard's, else the one WithShardFor gives for ctx's tenant. It returns
// nil and no error when ctx names none, and an error wrapping ErrNoShard for a
// name that is not a shard and for WithShardFor's error. Only without an
// OnShard does it look the tenant up.
func (rt *router) shardOf(ctx context.Context, rc routeCtx) (*shard, error) {
	if rc.onShard {
		if sh := rt.shards[rc.shard]; sh != nil {
			return sh, nil
		}
		return nil, fmt.Errorf("%w: OnShard names %q, which is not a shard of the DB", ErrNoShard, rc.shard)
	}
	id, _ := tenantOf(ctx)
	if id == nil {
		return nil, nil
	}
	name, err := rt.shardFor(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: WithShardFor: %w", ErrNoShard, err)
	}
	if sh := rt.shards[name]; sh != nil {
		return sh, nil
	}
	return nil, fmt.Errorf("%w: WithShardFor gave %q, which is not a shard of the DB", ErrNoShard, name)
}

// noShard is the error, wrapping ErrNoShard, of a statement or Tx whose
// context ctx names no shard, outside a transaction.
func noShard(ctx context.Context) error {
	if _, all := tenantOf(ctx); all != "" {
		return fmt.Errorf("%w: AllTenants names no shard; name one with OnShard", ErrNoShard)
	}
	return fmt.Errorf("%w: the context has neither OnShard nor a tenant (WithTenant)", ErrNoShard)
}

// shardForTx returns the shard a transaction begun with ctx runs on: on a
// sharded DB the one ctx names, on a DB with replicas its one shard, and nil
// on a DB without a router. Its error is Tx's.
func (db *DB) shardForTx(ctx context.Context) (*shard, error) {
	switch {
	case db.rt == nil:
		return nil, nil
	case db.rt.one != nil:
		return db.rt.one, nil
	}
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	sh, err := db.rt.shardOf(ctx, rc)
	if err == nil && sh == nil {
		err = noShard(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("velox: transaction: %w", err)
	}
	return sh, nil
}

// route is the route stage: it chooses the connection st runs on, and
// records the choice in st.Route.
//
// Writes, statements in a transaction, reads whose context is marked with
// ReadPrimary and reads of a session within its pin window go to the primary.
// Exec's raw SQL is a write, and Raw's a read. A With load goes where its
// query went. Any other read goes to the next replica in turn, skipping one
// in back-off, or to the primary when every replica is. A dry run is routed
// as the read would be, but takes no replica's turn: it sends nothing. exec,
// not route, falls a failed replica read over to the primary.
//
// On a sharded DB the statement's shard comes first: the one its context names
// (shardOf), the pinned target's for a With load, the transaction's in a
// transaction. A statement whose shard cannot be found fails, with nothing
// sent.
//
// On a DB without a router it is one branch: no context lookup.
func (db *DB) route(ctx context.Context, st *Statement) (target, error) {
	if db.rt == nil || db.inTx && db.txShard == nil {
		// The DB's one connection, or the transaction's.
		st.Route = Route{Replica: -1}
		return target{ex: db.ex, replica: -1}, nil
	}
	if db.inTx {
		return db.routeTx(ctx, st)
	}
	sh := db.rt.one
	var rc routeCtx // read once, on a sharded DB here, else for a read below
	switch {
	case db.pin.ex != nil:
		sh = db.pin.sh // a With load: its query's shard
	case sh == nil:
		rc, _ = ctx.Value(routeKey{}).(routeCtx)
		var err error
		if sh, err = db.rt.shardOf(ctx, rc); err == nil && sh == nil {
			err = noShard(ctx)
		}
		if err != nil {
			return target{}, stmtErr(st, err)
		}
	}
	t := target{ex: sh.primary, sh: sh, replica: -1}
	fallback := false
	switch {
	case !st.Op.Reads(): // a write, Exec's raw SQL included
	case db.pin.ex != nil:
		t = db.pin
		if t.replica >= 0 && t.sh.replicas[t.replica].downUntil.Load() != 0 && !t.sh.replicas[t.replica].up(time.Now().UnixNano()) {
			// The replica has failed since the query read it, on an earlier
			// load: this one goes straight to the primary, as it would
			// have fallen over there.
			t, fallback = target{ex: t.sh.primary, sh: t.sh, replica: -1}, true
		}
	case len(sh.replicas) == 0: // a shard without replicas
	default:
		if db.rt.one != nil {
			rc, _ = ctx.Value(routeKey{}).(routeCtx)
		}
		if !rc.primary && (rc.sess == nil || !rc.sess.pinned()) {
			t = sh.pick(st.DryRun)
		}
	}
	st.Route = Route{Shard: t.sh.name, Replica: t.replica, Fallback: fallback}
	return t, nil
}

// routeTx is route in a transaction on a sharded DB: the statement runs on the
// transaction's connection, unless its context names another shard, or names
// one that is not a shard, or WithShardFor fails. A context that names no shard
// is the transaction's.
func (db *DB) routeTx(ctx context.Context, st *Statement) (target, error) {
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	sh, err := db.rt.shardOf(ctx, rc)
	if err != nil {
		return target{}, stmtErr(st, err)
	}
	if sh != nil && sh != db.txShard {
		return target{}, fmt.Errorf("velox: %s: statement for shard %q in a transaction on shard %q", st.Entity.Name, sh.name, db.txShard.name)
	}
	st.Route = Route{Shard: db.txShard.name, Replica: -1}
	return target{ex: db.ex, replica: -1}, nil
}

// stmtErr names st's entity in err, which the route stage found.
func stmtErr(st *Statement, err error) error {
	return fmt.Errorf("velox: %s: %w", st.Entity.Name, err)
}

// pick returns the next replica in turn that is not in back-off, or the
// primary when every one is. With peek set it leaves the turn where it was.
//
// When the replica whose turn it is is in back-off, the turn goes to one of
// the others that are up, chosen by the turn too, so that the down replica's
// reads are spread evenly over them rather than all falling to its
// neighbour. Only that path reads the clock: with every replica up, pick is
// one atomic add and one load.
func (sh *shard) pick(peek bool) target {
	var turn uint32
	if peek {
		turn = sh.next.Load()
	} else {
		turn = sh.next.Add(1) - 1
	}
	n := uint32(len(sh.replicas))
	if i := turn % n; sh.replicas[i].downUntil.Load() == 0 {
		return target{ex: sh.replicas[i].ex, sh: sh, replica: int(i)}
	}
	return sh.pickUp(turn, time.Now().UnixNano())
}

// pickUp is pick's path when the replica whose turn it is has been in
// back-off: that replica when its back-off has ended, else the
// (turn/n mod up)th of the up replicas, else the primary.
func (sh *shard) pickUp(turn uint32, now int64) target {
	n := uint32(len(sh.replicas))
	if i := turn % n; sh.replicas[i].up(now) {
		return target{ex: sh.replicas[i].ex, sh: sh, replica: int(i)}
	}
	var up uint32
	for _, r := range sh.replicas {
		if r.up(now) {
			up++
		}
	}
	if up == 0 {
		return target{ex: sh.primary, sh: sh, replica: -1}
	}
	k := turn / n % up
	first := -1
	for i, r := range sh.replicas {
		if !r.up(now) {
			continue
		}
		if k == 0 {
			return target{ex: r.ex, sh: sh, replica: i}
		}
		if first < 0 {
			first = i
		}
		k--
	}
	if first >= 0 {
		// One went down between the loops: take the first still up.
		return target{ex: sh.replicas[first].ex, sh: sh, replica: first}
	}
	return target{ex: sh.primary, sh: sh, replica: -1}
}

// up reports whether r is out of back-off at now, in Unix nanoseconds. A
// back-off that has ended is cleared, which returns r to pick's path that
// does not read the clock.
func (r *replica) up(now int64) bool {
	until := r.downUntil.Load()
	if until == 0 {
		return true
	}
	if now < until {
		return false
	}
	r.downUntil.CompareAndSwap(until, 0) // unless it failed again meanwhile
	return true
}

// down puts r in back-off for d from now.
func (r *replica) down(d time.Duration) {
	r.downUntil.Store(time.Now().Add(d).UnixNano())
}

// broken reports whether err, from a read on t, is a connection error on a
// replica, with ctx still live: whether the read falls over. A context that
// ended says nothing about the replica.
func (db *DB) broken(ctx context.Context, t target, err error) bool {
	return t.replica >= 0 && ctx.Err() == nil && failoverable(err)
}

// fallBack records that st's read on replica t failed with a connection error:
// the replica is put in back-off for the DB's WithReplicaBackoff, and fallBack
// returns the shard's primary for exec to retry st on, with st.Route and st.ran
// saying so.
func (db *DB) fallBack(st *Statement, t target) target {
	t.sh.replicas[t.replica].down(db.cfg.backoff)
	p := target{ex: t.sh.primary, sh: t.sh, replica: -1}
	st.Route = Route{Shard: t.sh.name, Replica: -1, Fallback: true}
	st.ran = p
	return p
}

// failoverable reports whether err, from a read on a replica (the query call or
// its rows), is a connection error after which the read is retried on the
// primary: the replica was not reached, its connection is gone, or its server
// is shutting down or starting up. A query error (syntax, a constraint, a
// permission) is not. Neither is a context that ended, which says nothing about
// the replica: exec checks the statement's context before asking, and
// context.DeadlineExceeded, itself a net.Error, is ruled out here too. A
// net.Error that is a timeout counts only from a dial: a read that timed out
// may be a slow query, not a dead replica. A MySQL error that is the server
// refusing or ending the connection counts (mysqlConnectError), as a pgx
// ConnectError does.
func failoverable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, pgconn.ErrConnClosed) ||
		pgconn.SafeToRetry(err) ||
		mysqlInvalidConn(err) {
		return true
	}
	var ce *pgconn.ConnectError
	if errors.As(err, &ce) {
		// Whatever the server said while connecting, such as too many
		// connections (53300) or a failed login: pgx wraps its PgError in
		// the ConnectError, so this comes before the PgError check.
		return true
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		// admin_shutdown, crash_shutdown, cannot_connect_now
		return pg.Code == "57P01" || pg.Code == "57P02" || pg.Code == "57P03"
	}
	if n, _, ok := mysqlError(err); ok {
		return mysqlConnectError(n)
	}
	var ne net.Error
	if !errors.As(err, &ne) {
		return false
	}
	var op *net.OpError
	return !ne.Timeout() || errors.As(err, &op) && op.Op == "dial"
}

// mysqlConnectError reports whether MySQL error number n is the server
// refusing or ending a connection, as PostgreSQL's connect errors are: the
// way the server says that it cannot take the client now, not a fault of the
// statement. The numbers are too many connections (1040), the user's
// connection limits (1203, 1226), access denied (1045), host blocked (1129),
// host not allowed (1130), shutdown in progress (1053) and connection killed
// (1927).
func mysqlConnectError(n int) bool {
	switch n {
	case 1040, 1203, 1226, 1045, 1129, 1130, 1053, 1927:
		return true
	}
	return false
}

// mysqlInvalidConn reports whether err's tree holds go-sql-driver/mysql's
// ErrInvalidConn, which the driver returns for a connection that broke. velox
// does not import the driver (see constraint), so the sentinel, an
// errors.New, is recognized by its type and text.
func mysqlInvalidConn(err error) bool {
	for err != nil {
		if t := reflect.TypeOf(err); t.Kind() == reflect.Pointer && t.Elem().PkgPath() == "errors" && t.Elem().Name() == "errorString" && err.Error() == "invalid connection" {
			return true
		}
		switch u := err.(type) {
		case interface{ Unwrap() error }:
			err = u.Unwrap()
		case interface{ Unwrap() []error }:
			for _, e := range u.Unwrap() {
				if mysqlInvalidConn(e) {
					return true
				}
			}
			return false
		default:
			return false
		}
	}
	return false
}
