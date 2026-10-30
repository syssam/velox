package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"math"
	"net"
	"slices"
	"sync/atomic"
	"time"

	"github.com/syssam/velox2/internal/conn"
)

// Conn is a connection pool that velox can run statements on: a DB's primary
// or one of its replicas. Make one with SQLConn or a driver package's Conn, such
// as veloxpgx.Conn; the zero Conn is none, and a DB given one panics.
type Conn struct {
	ex   Executor
	only Dialect // the one dialect the Conn is valid on, or 0 for the DB's
}

// SQLConn returns a Conn that runs statements through db, a *sql.DB the
// application opened with a driver for the DB's dialect. A nil db panics.
func SQLConn(db *sql.DB) Conn {
	if db == nil {
		panic("velox: SQLConn(nil)")
	}
	return Conn{ex: conn.NewSQL(db)}
}

// DriverConn returns a Conn on ex, valid only on dialect d. It is for velox's
// own driver packages, such as veloxpgx: nothing outside this module can
// implement ex.
func DriverConn(d Dialect, ex conn.Executor) Conn { return Conn{ex: ex, only: d} }

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
func WithReplicas(conns ...Conn) Option {
	for i, c := range conns {
		if c.ex == nil {
			panic(fmt.Sprintf("velox: WithReplicas: replica %d is the zero Conn; make it with SQLConn or a driver package's Conn", i))
		}
	}
	conns = slices.Clone(conns)
	return Option{func(c *Config) { c.Replicas = append(c.Replicas, conns...) }}
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

// WithShardFor sets the function that gives the name of a statement's shard,
// on a DB opened with OpenShards, from its context's tenant: the id given to
// WithTenant. It is called, with the statement's context, for each statement
// and Tx whose context has a tenant and no WithShard, so it should be fast; a
// SQL(ctx) dry run calls it too. Its error refuses the statement, which then
// matches both ErrNoShard and the error.
//
// One write can call it more than once: an InsertAll split into two
// statements calls it three times, for its transaction and for each
// statement, and a Tx calls it when it begins and for each statement in it.
//
// f is called concurrently, from the goroutine of every statement, so it must
// be safe for that, and fast: a map lookup or a hash, not a query.
//
// It is required with OpenShards; given to Open or OpenConn, it panics there.
// A nil f panics. WithShard names a statement's shard from its context instead.
func WithShardFor(f func(ctx context.Context, tenant any) (string, error)) Option {
	if f == nil {
		panic("velox: WithShardFor(nil)")
	}
	return Option{func(c *Config) { c.ShardFor = f }}
}

// WithShard returns a context whose statements, on a DB opened with
// OpenShards, run on the shard named name, whatever the context's tenant:
// for admin jobs and for data that belongs to no tenant. A later WithShard
// replaces it. On a DB opened otherwise it is ignored. Without it, the shard
// is the one WithShardFor gives for the context's tenant.
func WithShard(ctx context.Context, name string) context.Context {
	rc, _ := ctx.Value(RouteKey{}).(RouteCtx)
	rc.shard, rc.onShard = name, true
	return context.WithValue(ctx, RouteKey{}, rc)
}

// WithPrimaryReads returns a context whose reads go to the primary, on a DB
// with replicas: for a read that must see a write the replicas may not have
// yet.
func WithPrimaryReads(ctx context.Context) context.Context {
	rc, _ := ctx.Value(RouteKey{}).(RouteCtx)
	rc.Primary = true
	return context.WithValue(ctx, RouteKey{}, rc)
}

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
func WithSession(ctx context.Context) context.Context {
	rc, _ := ctx.Value(RouteKey{}).(RouteCtx)
	rc.Sess = new(session)
	return context.WithValue(ctx, RouteKey{}, rc)
}

// PinUntil pins ctx's session to the primary until t, if that is later than
// its pin now: an application that stored the time of a user's last write
// restarts the pin on the user's next request with it (PinnedUntil gives the
// time to store). It never lowers a pin, and does nothing on a context
// without a session. A t after 2262, past what PinnedUntil can return, pins
// until then: until the end of the session, in effect.
func PinUntil(ctx context.Context, t time.Time) {
	rc, _ := ctx.Value(RouteKey{}).(RouteCtx)
	if rc.Sess == nil || !t.After(time.Unix(0, 0)) {
		return // a time before 1970 is past: it pins nothing
	}
	u := int64(math.MaxInt64) // t.UnixNano() wraps after 2262
	if t.Before(time.Unix(0, math.MaxInt64)) {
		u = t.UnixNano()
	}
	rc.Sess.Raise(u)
}

// PinnedUntil returns the time until which ctx's session reads the primary:
// the latest of its writes' pin windows and PinUntil's times. It is zero on a
// context without a session, and on a session that has not been pinned.
func PinnedUntil(ctx context.Context) time.Time {
	rc, _ := ctx.Value(RouteKey{}).(RouteCtx)
	if rc.Sess == nil {
		return time.Time{}
	}
	if u := rc.Sess.until.Load(); u != 0 {
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
	return Option{func(c *Config) { c.PinWindow = d }}
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
	return Option{func(c *Config) { c.Backoff = d }}
}

// session is a context's record of when its reads may leave the primary
// again.
type session struct {
	until atomic.Int64 // the end of the pin, in Unix nanoseconds; 0: none
}

// raise moves the pin's end to t, unless it is later already.
func (s *session) Raise(t int64) {
	for {
		u := s.until.Load()
		if t <= u || s.until.CompareAndSwap(u, t) {
			return
		}
	}
}

// pinned reports whether the session's reads must go to the primary now.
func (s *session) Pinned() bool {
	u := s.until.Load()
	return u != 0 && time.Now().UnixNano() < u
}

// Route is where a statement runs. The route stage sets Statement.Route
// after the Rewrite hooks and before the Around hooks run. TxInfo.Route is a
// transaction's.
type Route struct {
	Shard   string // "" on an unsharded DB
	Replica int    // index into the replicas; -1 for the primary
	// Fallback is set when a replica failed and the read was retried on the
	// primary. It stays set for the statement's later executions, an Around
	// hook's retry, which run on the primary.
	Fallback bool
}

// RouteKey is the context key of the route marks. They share it, so that the
// route stage reads them all in one lookup.
type RouteKey struct{}

// RouteCtx is a context's route marks. Each function that sets one copies
// the context's RouteCtx, changes it, and stores the copy.
type RouteCtx struct {
	Sess    *session // WithSession's; nil: none
	shard   string   // WithShard's name, when onShard is set
	Primary bool     // WithPrimaryReads: reads go to the primary
	onShard bool     // WithShard: statements go to the shard named shard
}

// Router is the connections of a DB with replicas or shards, among which the
// route stage chooses. A DB with neither has none, and runs every statement
// on db.Ex.
type Router struct {
	One *ShardConn // Open's primary and WithReplicas's replicas; nil when sharded
	// shards are OpenShards's, by name, and shardFor its WithShardFor; nil
	// on a DB that is not sharded.
	Shards   map[string]*ShardConn
	ShardFor func(ctx context.Context, tenant any) (string, error)
}

// ShardConn is a primary and its replicas.
type ShardConn struct {
	Name     string // "" on an unsharded DB
	Primary  Executor
	Replicas []*replica
	next     atomic.Uint32 // the reads sent to replicas: the round-robin's turn
}

// replica is one of a shard's replicas.
type replica struct {
	ex Executor
	// downUntil is the time, in Unix nanoseconds, until which the replica is
	// in back-off and skipped; 0 when it never was.
	DownUntil atomic.Int64
}

// Target is the connection the route stage chose for a statement: exec sends
// it there.
type Target struct {
	Ex      Executor
	Sh      *ShardConn // the shard it is on; nil on a DB without a router and in a transaction
	Replica int        // index into sh.replicas; -1 for the primary
}

// NewRouter returns the router of an unsharded DB of dialect d on primary
// and replicas. A Conn valid only on another dialect panics.
func NewRouter(d Dialect, primary Executor, replicas []Conn) *Router {
	return &Router{One: NewShard(d, primary, replicas, "WithReplicas:")}
}

// NewShard returns a shard of a DB of dialect d on primary and replicas. A
// replica valid only on another dialect panics, in a message that
// starts with what.
func NewShard(d Dialect, primary Executor, replicas []Conn, what string) *ShardConn {
	sh := &ShardConn{Primary: primary}
	for i, c := range replicas {
		if c.only != 0 && c.only != d {
			panic(fmt.Sprintf("velox: %s replica %d is valid only on %s; the DB is %s", what, i, c.only, d))
		}
		sh.Replicas = append(sh.Replicas, &replica{ex: c.ex})
	}
	return sh
}

// shardOf returns the shard ctx names on a sharded DB, whose route marks are
// rc: WithShard's, else the one WithShardFor gives for ctx's tenant. It returns
// nil and no error when ctx names none, and an error wrapping ErrNoShard for a
// name that is not a shard and for WithShardFor's error. Only without an
// WithShard does it look the tenant up.
func (rt *Router) ShardOf(ctx context.Context, rc RouteCtx) (*ShardConn, error) {
	if rc.onShard {
		if sh := rt.Shards[rc.shard]; sh != nil {
			return sh, nil
		}
		return nil, fmt.Errorf("%w: WithShard names %q, which is not a shard of the DB", ErrNoShard, rc.shard)
	}
	id, _ := TenantFrom(ctx)
	if id == nil {
		return nil, nil
	}
	name, err := rt.ShardFor(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("%w: WithShardFor: %w", ErrNoShard, err)
	}
	if sh := rt.Shards[name]; sh != nil {
		return sh, nil
	}
	return nil, fmt.Errorf("%w: WithShardFor gave %q, which is not a shard of the DB", ErrNoShard, name)
}

// NoShard is the error, wrapping ErrNoShard, of a statement or Tx whose
// context ctx names no shard, outside a transaction.
func NoShard(ctx context.Context) error {
	if _, all := TenantFrom(ctx); all != "" {
		return fmt.Errorf("%w: WithAllTenants names no shard; name one with WithShard", ErrNoShard)
	}
	return fmt.Errorf("%w: the context has neither WithShard nor a tenant (WithTenant)", ErrNoShard)
}

// StmtErr names st's entity in err, which the route stage found.
func StmtErr(st *Statement, err error) error {
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
func (sh *ShardConn) Pick(peek bool) Target {
	var turn uint32
	if peek {
		turn = sh.next.Load()
	} else {
		turn = sh.next.Add(1) - 1
	}
	n := uint32(len(sh.Replicas))
	if i := turn % n; sh.Replicas[i].DownUntil.Load() == 0 {
		return Target{Ex: sh.Replicas[i].ex, Sh: sh, Replica: int(i)}
	}
	return sh.pickUp(turn, time.Now().UnixNano())
}

// pickUp is pick's path when the replica whose turn it is has been in
// back-off: that replica when its back-off has ended, else the
// (turn/n mod up)th of the up replicas, else the primary.
func (sh *ShardConn) pickUp(turn uint32, now int64) Target {
	n := uint32(len(sh.Replicas))
	if i := turn % n; sh.Replicas[i].Up(now) {
		return Target{Ex: sh.Replicas[i].ex, Sh: sh, Replica: int(i)}
	}
	var up uint32
	for _, r := range sh.Replicas {
		if r.Up(now) {
			up++
		}
	}
	if up == 0 {
		return Target{Ex: sh.Primary, Sh: sh, Replica: -1}
	}
	k := turn / n % up
	first := -1
	for i, r := range sh.Replicas {
		if !r.Up(now) {
			continue
		}
		if k == 0 {
			return Target{Ex: r.ex, Sh: sh, Replica: i}
		}
		if first < 0 {
			first = i
		}
		k--
	}
	if first >= 0 {
		// One went down between the loops: take the first still up.
		return Target{Ex: sh.Replicas[first].ex, Sh: sh, Replica: first}
	}
	return Target{Ex: sh.Primary, Sh: sh, Replica: -1}
}

// up reports whether r is out of back-off at now, in Unix nanoseconds. A
// back-off that has ended is cleared, which returns r to pick's path that
// does not read the clock.
func (r *replica) Up(now int64) bool {
	until := r.DownUntil.Load()
	if until == 0 {
		return true
	}
	if now < until {
		return false
	}
	r.DownUntil.CompareAndSwap(until, 0) // unless it failed again meanwhile
	return true
}

// down puts r in back-off for d from now.
func (r *replica) Down(d time.Duration) {
	r.DownUntil.Store(time.Now().Add(d).UnixNano())
}

// MysqlConnectError reports whether MySQL error number n is the server
// refusing or ending a connection, as PostgreSQL's connect errors are: the
// way the server says that it cannot take the client now, not a fault of the
// statement. The numbers are too many connections (1040), the user's
// connection limits (1203, 1226), access denied (1045), host blocked (1129),
// host not allowed (1130), shutdown in progress (1053) and connection killed
// (1927).
func MysqlConnectError(n int) bool {
	switch n {
	case 1040, 1203, 1226, 1045, 1129, 1130, 1053, 1927:
		return true
	}
	return false
}

// MysqlInvalidConn reports whether err's tree holds go-sql-driver/mysql's
// ErrInvalidConn, which the driver returns for a connection that broke. velox
// does not import the driver (see constraint).
func MysqlInvalidConn(err error) bool { return DriverSentinel(err, "invalid connection") }

func ConnExec(c Conn) Executor { return c.ex }

func ConnOnly(c Conn) Dialect { return c.only }

func NewConn(ex Executor, only Dialect) Conn { return Conn{ex: ex, only: only} }

// Failoverable reports whether err, from a read on a replica (the query call or
// its rows), is a connection error after which the read is retried on the
// primary: the replica was not reached, its connection is gone, or its server
// is shutting down or starting up. A query error (syntax, a constraint, a
// permission) is not. Neither is a context that ended, which says nothing about
// the replica: exec checks the statement's context before asking, and
// context.DeadlineExceeded, itself a net.Error, is ruled out here too. A
// net.Error that is a timeout counts only from a dial: a read that timed out
// may be a slow query, not a dead replica. A MySQL error that is the server
// refusing or ending the connection counts (MysqlConnectError), as a pgx
// ConnectError does.
func Failoverable(err error) bool {
	if Is(err, context.Canceled) || Is(err, context.DeadlineExceeded) {
		return false
	}
	if Is(err, driver.ErrBadConn) ||
		Is(err, sql.ErrConnDone) ||
		Is(err, net.ErrClosed) ||
		Is(err, io.EOF) ||
		Is(err, io.ErrUnexpectedEOF) ||
		DriverSentinel(err, PgConnClosed) ||
		safeToRetry(err) ||
		MysqlInvalidConn(err) {
		return true
	}
	if PgConnectError(err) {
		// Whatever the server said while connecting, such as too many
		// connections (53300) or a failed login: pgx wraps its PgError in
		// the ConnectError, so this comes before the PgError check.
		return true
	}
	if pg, ok := PgErrorOf(err); ok {
		// admin_shutdown, crash_shutdown, cannot_connect_now
		return pg.Code == "57P01" || pg.Code == "57P02" || pg.Code == "57P03"
	}
	if n, _, ok := MysqlError(err); ok {
		return MysqlConnectError(n)
	}
	ne, ok := As[net.Error](err)
	if !ok {
		return false
	}
	op, ok := As[*net.OpError](err)
	return !ne.Timeout() || ok && op.Op == "dial"
}

// safeToRetry is pgx's pgconn.SafeToRetry over as, so that a nil pointer in err's
// tree cannot panic it.
func safeToRetry(err error) bool {
	r, ok := As[interface {
		error
		SafeToRetry() bool
	}](err)
	return ok && r.SafeToRetry()
}

// Wrote records that a write reached the driver, whether or not it then
// failed, as its outcome is unknown: in a transaction, in the flag its commit
// reads; outside one, in the context's session. A DB without replicas keeps
// no record.
func (db *DB) Wrote(ctx context.Context) {
	if db.InTx {
		if db.txWrote != nil {
			db.txWrote.Store(true)
		}
		return
	}
	if db.Rt != nil {
		db.pinSession(ctx)
	}
}

// pinSession pins ctx's session, if it has one, for the DB's pin window from
// now.
func (db *DB) pinSession(ctx context.Context) {
	if rc, _ := ctx.Value(RouteKey{}).(RouteCtx); rc.Sess != nil {
		rc.Sess.Raise(time.Now().Add(db.Cfg.PinWindow).UnixNano())
	}
}

// shardForTx returns the shard a transaction begun with ctx runs on: on a
// sharded DB the one ctx names, on a DB with replicas its one shard, and nil
// on a DB without a router. Its error is Tx's.
func (db *DB) shardForTx(ctx context.Context) (*ShardConn, error) {
	sh, err := db.CtxShard(ctx)
	if err != nil {
		return nil, fmt.Errorf("velox: transaction: %w", err)
	}
	return sh, nil
}

// CtxShard is shardForTx's shard, and its error without the prefix.
func (db *DB) CtxShard(ctx context.Context) (*ShardConn, error) {
	switch {
	case db.Rt == nil:
		return nil, nil
	case db.Rt.One != nil:
		return db.Rt.One, nil
	}
	rc, _ := ctx.Value(RouteKey{}).(RouteCtx)
	sh, err := db.Rt.ShardOf(ctx, rc)
	if err == nil && sh == nil {
		err = NoShard(ctx)
	}
	return sh, err
}

// Route is the route stage: it chooses the connection st runs on, and
// records the choice in st.Route.
//
// Writes, statements in a transaction, reads whose context is marked with
// WithPrimaryReads and reads of a session within its pin window go to the
// primary. Exec's raw SQL is a write, and Raw's a read. A With load goes where
// its query went. Any other read goes to the next replica in turn, skipping one
// in back-off, or to the primary when every replica is. A dry run is routed as
// the read would be, but takes no replica's turn: it sends nothing. exec, not
// Route, falls a failed replica read over to the primary.
//
// On a sharded DB the statement's shard comes first: the one its context names
// (shardOf), the pinned target's for a With load, the transaction's in a
// transaction. A statement whose shard cannot be found fails, with nothing
// sent.
//
// On a DB without a router it is one branch: no context lookup.
func (db *DB) Route(ctx context.Context, st *Statement) (Target, error) {
	if db.Rt == nil || db.InTx && db.txShard == nil {
		// The DB's one connection, or the transaction's.
		st.Route = Route{Replica: -1}
		return Target{Ex: db.Ex, Replica: -1}, nil
	}
	if db.InTx {
		return db.routeTx(ctx, st)
	}
	sh := db.Rt.One
	var rc RouteCtx // read once, on a sharded DB here, else for a read below
	switch {
	case StmtPin(st).Ex != nil:
		sh = StmtPin(st).Sh // a With load: its query's shard
	case sh == nil:
		rc, _ = ctx.Value(RouteKey{}).(RouteCtx)
		var err error
		if sh, err = db.Rt.ShardOf(ctx, rc); err == nil && sh == nil {
			err = NoShard(ctx)
		}
		if err != nil {
			return Target{}, StmtErr(st, err)
		}
	}
	t := Target{Ex: sh.Primary, Sh: sh, Replica: -1}
	fallback := false
	switch {
	case !st.Op.Reads(): // a write, Exec's raw SQL included
	case StmtPin(st).Ex != nil:
		t = StmtPin(st)
		if t.Replica >= 0 && t.Sh.Replicas[t.Replica].DownUntil.Load() != 0 && !t.Sh.Replicas[t.Replica].Up(time.Now().UnixNano()) {
			// The replica has failed since the query read it, on an earlier
			// load: this one goes straight to the primary, as it would
			// have fallen over there.
			t, fallback = Target{Ex: t.Sh.Primary, Sh: t.Sh, Replica: -1}, true
		}
	case len(sh.Replicas) == 0: // a shard without replicas
	default:
		if db.Rt.One != nil {
			rc, _ = ctx.Value(RouteKey{}).(RouteCtx)
		}
		if !rc.Primary && (rc.Sess == nil || !rc.Sess.Pinned()) {
			t = sh.Pick(st.DryRun)
		}
	}
	st.Route = Route{Shard: t.Sh.Name, Replica: t.Replica, Fallback: fallback}
	return t, nil
}

// routeTx is route in a transaction on a sharded DB: the statement runs on the
// transaction's connection, unless its context names another shard, or names
// one that is not a shard, or WithShardFor fails. A context that names no shard
// is the transaction's.
func (db *DB) routeTx(ctx context.Context, st *Statement) (Target, error) {
	rc, _ := ctx.Value(RouteKey{}).(RouteCtx)
	sh, err := db.Rt.ShardOf(ctx, rc)
	if err != nil {
		return Target{}, StmtErr(st, err)
	}
	if sh != nil && sh != db.txShard {
		return Target{}, fmt.Errorf("velox: %s: statement for shard %q in a transaction on shard %q", st.Entity.Name, sh.Name, db.txShard.Name)
	}
	st.Route = Route{Shard: db.txShard.Name, Replica: -1}
	return Target{Ex: db.Ex, Replica: -1}, nil
}

// Broken reports whether err, from a read on t, is a connection error on a
// replica, with ctx still live: whether the read falls over. A context that
// ended says nothing about the replica.
func (db *DB) Broken(ctx context.Context, t Target, err error) bool {
	return t.Replica >= 0 && ctx.Err() == nil && Failoverable(err)
}

// FallBack records that st's read on replica t failed with a connection error:
// the replica is put in back-off for the DB's WithReplicaBackoff, and fallBack
// returns the shard's primary for exec to retry st on, with st.Route and st.ran
// saying so.
func (db *DB) FallBack(st *Statement, t Target) Target {
	t.Sh.Replicas[t.Replica].Down(db.Cfg.Backoff)
	p := Target{Ex: t.Sh.Primary, Sh: t.Sh, Replica: -1}
	st.Route = Route{Shard: t.Sh.Name, Replica: -1, Fallback: true}
	SetStmtRan(st, p)
	return p
}
