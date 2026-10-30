package velox

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"maps"
	"net"
	"slices"
	"time"

	"github.com/syssam/velox2/internal/engine"
)

// OpenShards returns a DB of dialect d whose statements each run on one of
// shards, chosen from the statement's context:
//
//  1. the shard WithShard names;
//  2. otherwise, the one WithShardFor's function returns for the context's
//     tenant (WithTenant); its error refuses the statement;
//  3. otherwise none: the statement fails with ErrNoShard, as it does under
//     WithAllTenants without WithShard.
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
// own Replicas. OpenShards panics on either mistake, on a d that is not
// Postgres, MySQL or SQLite, on no shards, on a shard named "" and on a shard
// whose Primary or a replica is the zero Conn, or a Conn valid only on another
// dialect.
func OpenShards(d Dialect, shards map[string]Shard, opts ...Option) *DB {
	if !engine.ValidDialect(d) {
		panic(fmt.Sprintf("velox: OpenShards: unknown %v; use velox.Postgres, velox.MySQL or velox.SQLite", d))
	}
	if len(shards) == 0 {
		panic("velox: OpenShards: no shards")
	}
	cfg := engine.NewConfig(opts)
	if cfg.ShardFor == nil {
		panic("velox: OpenShards: WithShardFor is required, to find a statement's shard from its tenant")
	}
	if len(cfg.Replicas) > 0 {
		panic("velox: OpenShards: WithReplicas is for Open and OpenConn; give each Shard its Replicas")
	}
	rt := &engine.Router{Shards: make(map[string]*engine.ShardConn, len(shards)), ShardFor: cfg.ShardFor}
	for _, name := range slices.Sorted(maps.Keys(shards)) {
		s := shards[name]
		if name == "" {
			panic("velox: OpenShards: a shard's name must not be empty")
		}
		what := fmt.Sprintf("OpenShards: shard %q's", name)
		switch {
		case engine.ConnExec(s.Primary) == nil:
			panic(fmt.Sprintf("velox: %s Primary is the zero Conn; make it with SQLConn or a driver package's Conn", what))
		case engine.ConnOnly(s.Primary) != 0 && engine.ConnOnly(s.Primary) != d:
			panic(fmt.Sprintf("velox: %s Primary is valid only on %s; the DB is %s", what, engine.ConnOnly(s.Primary), d))
		}
		for i, c := range s.Replicas {
			if engine.ConnExec(c) == nil {
				panic(fmt.Sprintf("velox: %s replica %d is the zero Conn; make it with SQLConn or a driver package's Conn", what, i))
			}
		}
		sh := engine.NewShard(d, engine.ConnExec(s.Primary), s.Replicas, what)
		sh.Name = name
		rt.Shards[name] = sh
	}
	return &DB{d: d, rt: rt, cfg: cfg}
}

// wrote records that a write reached the driver, whether or not it then
// failed, as its outcome is unknown: in a transaction, in the flag its commit
// reads; outside one, in the context's session. A DB without replicas keeps
// no record.
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
	if rc, _ := ctx.Value(engine.RouteKey{}).(engine.RouteCtx); rc.Sess != nil {
		rc.Sess.Raise(time.Now().Add(db.cfg.PinWindow).UnixNano())
	}
}

// shardForTx returns the shard a transaction begun with ctx runs on: on a
// sharded DB the one ctx names, on a DB with replicas its one shard, and nil
// on a DB without a router. Its error is Tx's.
func (db *DB) shardForTx(ctx context.Context) (*engine.ShardConn, error) {
	sh, err := db.ctxShard(ctx)
	if err != nil {
		return nil, fmt.Errorf("velox: transaction: %w", err)
	}
	return sh, nil
}

// ctxShard is shardForTx's shard, and its error without the prefix.
func (db *DB) ctxShard(ctx context.Context) (*engine.ShardConn, error) {
	switch {
	case db.rt == nil:
		return nil, nil
	case db.rt.One != nil:
		return db.rt.One, nil
	}
	rc, _ := ctx.Value(engine.RouteKey{}).(engine.RouteCtx)
	sh, err := db.rt.ShardOf(ctx, rc)
	if err == nil && sh == nil {
		err = engine.NoShard(ctx)
	}
	return sh, err
}

// route is the route stage: it chooses the connection st runs on, and
// records the choice in st.Route.
//
// Writes, statements in a transaction, reads whose context is marked with
// WithPrimaryReads and reads of a session within its pin window go to the
// primary. Exec's raw SQL is a write, and Raw's a read. A With load goes where
// its query went. Any other read goes to the next replica in turn, skipping one
// in back-off, or to the primary when every replica is. A dry run is routed as
// the read would be, but takes no replica's turn: it sends nothing. exec, not
// route, falls a failed replica read over to the primary.
//
// On a sharded DB the statement's shard comes first: the one its context names
// (shardOf), the pinned target's for a With load, the transaction's in a
// transaction. A statement whose shard cannot be found fails, with nothing
// sent.
//
// On a DB without a router it is one branch: no context lookup.
func (db *DB) route(ctx context.Context, st *Statement) (engine.Target, error) {
	if db.rt == nil || db.inTx && db.txShard == nil {
		// The DB's one connection, or the transaction's.
		st.Route = Route{Replica: -1}
		return engine.Target{Ex: db.ex, Replica: -1}, nil
	}
	if db.inTx {
		return db.routeTx(ctx, st)
	}
	sh := db.rt.One
	var rc engine.RouteCtx // read once, on a sharded DB here, else for a read below
	switch {
	case engine.StmtPin(st).Ex != nil:
		sh = engine.StmtPin(st).Sh // a With load: its query's shard
	case sh == nil:
		rc, _ = ctx.Value(engine.RouteKey{}).(engine.RouteCtx)
		var err error
		if sh, err = db.rt.ShardOf(ctx, rc); err == nil && sh == nil {
			err = engine.NoShard(ctx)
		}
		if err != nil {
			return engine.Target{}, engine.StmtErr(st, err)
		}
	}
	t := engine.Target{Ex: sh.Primary, Sh: sh, Replica: -1}
	fallback := false
	switch {
	case !st.Op.Reads(): // a write, Exec's raw SQL included
	case engine.StmtPin(st).Ex != nil:
		t = engine.StmtPin(st)
		if t.Replica >= 0 && t.Sh.Replicas[t.Replica].DownUntil.Load() != 0 && !t.Sh.Replicas[t.Replica].Up(time.Now().UnixNano()) {
			// The replica has failed since the query read it, on an earlier
			// load: this one goes straight to the primary, as it would
			// have fallen over there.
			t, fallback = engine.Target{Ex: t.Sh.Primary, Sh: t.Sh, Replica: -1}, true
		}
	case len(sh.Replicas) == 0: // a shard without replicas
	default:
		if db.rt.One != nil {
			rc, _ = ctx.Value(engine.RouteKey{}).(engine.RouteCtx)
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
func (db *DB) routeTx(ctx context.Context, st *Statement) (engine.Target, error) {
	rc, _ := ctx.Value(engine.RouteKey{}).(engine.RouteCtx)
	sh, err := db.rt.ShardOf(ctx, rc)
	if err != nil {
		return engine.Target{}, engine.StmtErr(st, err)
	}
	if sh != nil && sh != db.txShard {
		return engine.Target{}, fmt.Errorf("velox: %s: statement for shard %q in a transaction on shard %q", st.Entity.Name, sh.Name, db.txShard.Name)
	}
	st.Route = Route{Shard: db.txShard.Name, Replica: -1}
	return engine.Target{Ex: db.ex, Replica: -1}, nil
}

// broken reports whether err, from a read on t, is a connection error on a
// replica, with ctx still live: whether the read falls over. A context that
// ended says nothing about the replica.
func (db *DB) broken(ctx context.Context, t engine.Target, err error) bool {
	return t.Replica >= 0 && ctx.Err() == nil && failoverable(err)
}

// fallBack records that st's read on replica t failed with a connection error:
// the replica is put in back-off for the DB's WithReplicaBackoff, and fallBack
// returns the shard's primary for exec to retry st on, with st.Route and st.ran
// saying so.
func (db *DB) fallBack(st *Statement, t engine.Target) engine.Target {
	t.Sh.Replicas[t.Replica].Down(db.cfg.Backoff)
	p := engine.Target{Ex: t.Sh.Primary, Sh: t.Sh, Replica: -1}
	st.Route = Route{Shard: t.Sh.Name, Replica: -1, Fallback: true}
	engine.SetStmtRan(st, p)
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
// refusing or ending the connection counts (MysqlConnectError), as a pgx
// ConnectError does.
func failoverable(err error) bool {
	if engine.Is(err, context.Canceled) || engine.Is(err, context.DeadlineExceeded) {
		return false
	}
	if engine.Is(err, driver.ErrBadConn) ||
		engine.Is(err, sql.ErrConnDone) ||
		engine.Is(err, net.ErrClosed) ||
		engine.Is(err, io.EOF) ||
		engine.Is(err, io.ErrUnexpectedEOF) ||
		engine.DriverSentinel(err, engine.PgConnClosed) ||
		safeToRetry(err) ||
		engine.MysqlInvalidConn(err) {
		return true
	}
	if engine.PgConnectError(err) {
		// Whatever the server said while connecting, such as too many
		// connections (53300) or a failed login: pgx wraps its PgError in
		// the ConnectError, so this comes before the PgError check.
		return true
	}
	if pg, ok := engine.PgErrorOf(err); ok {
		// admin_shutdown, crash_shutdown, cannot_connect_now
		return pg.Code == "57P01" || pg.Code == "57P02" || pg.Code == "57P03"
	}
	if n, _, ok := engine.MysqlError(err); ok {
		return engine.MysqlConnectError(n)
	}
	ne, ok := as[net.Error](err)
	if !ok {
		return false
	}
	op, ok := as[*net.OpError](err)
	return !ne.Timeout() || ok && op.Op == "dial"
}

// safeToRetry is pgx's pgconn.SafeToRetry over as, so that a nil pointer in err's
// tree cannot panic it.
func safeToRetry(err error) bool {
	r, ok := as[interface {
		error
		SafeToRetry() bool
	}](err)
	return ok && r.SafeToRetry()
}
