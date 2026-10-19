package velox

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

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
// turn, unless its context is marked with Primary; a query's With loads go to
// the replica the query read. Writes, and every statement in a transaction,
// go to the primary. Statement.Route records the choice.
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
	return func(c *config) { c.replicas = append(c.replicas, conns...) }
}

// Primary returns a context whose reads go to the primary, on a DB with
// replicas: for a read that must see a write the replicas may not have yet.
func Primary(ctx context.Context) context.Context {
	rc, _ := ctx.Value(routeKey{}).(routeCtx)
	rc.primary = true
	return context.WithValue(ctx, routeKey{}, rc)
}

// Route is where a statement runs. The route stage sets Statement.Route
// before the Around hooks run.
type Route struct {
	Shard    string // "" on an unsharded DB
	Replica  int    // index into the replicas; -1 for the primary
	Fallback bool   // a replica failed and the read was retried on the primary
}

// routeKey is the context key of the route marks. They share it, so that the
// route stage reads them all in one lookup (Ruling 2).
type routeKey struct{}

// routeCtx is a context's route marks. Each function that sets one copies
// the context's routeCtx, changes it, and stores the copy.
type routeCtx struct {
	primary bool // Primary: reads go to the primary
}

// router is the connections of a DB with replicas, among which the route
// stage chooses. A DB without replicas has none, and runs every statement on
// db.ex.
type router struct {
	one *shard // Open's primary and WithReplicas's replicas
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
	sh      *shard // the shard it is on; nil on a DB without replicas and in a transaction
	replica int    // index into sh.replicas; -1 for the primary
}

// newRouter returns the router of a DB of dialect d on primary and replicas.
// A PgxConn on a DB of another dialect panics.
func newRouter(d Dialect, primary executor, replicas []Conn) *router {
	sh := &shard{primary: primary}
	for i, c := range replicas {
		if c.pgx && d != Postgres {
			panic(fmt.Sprintf("velox: WithReplicas: replica %d is a PgxConn, valid only for PostgreSQL; the DB is %s", i, d))
		}
		sh.replicas = append(sh.replicas, &replica{ex: c.ex})
	}
	return &router{one: sh}
}

// route is the route stage: it chooses the connection st runs on, and
// records the choice in st.Route.
//
// Writes, statements in a transaction and reads whose context is marked with
// Primary go to the primary. A With load goes where its query went (Ruling
// 3). Any other read goes to the next replica in turn, skipping one in
// back-off, or to the primary when every replica is. A dry run is routed as
// the read would be, but takes no replica's turn: it sends nothing.
//
// On a DB without replicas it is one branch: no context lookup.
func (db *DB) route(ctx context.Context, st *Statement) (target, error) {
	if db.rt == nil || db.inTx {
		// The DB's one connection, or the transaction's.
		st.Route = Route{Replica: -1}
		return target{ex: db.ex, replica: -1}, nil
	}
	sh := db.rt.one
	t := target{ex: sh.primary, sh: sh, replica: -1}
	switch {
	case st.Op != OpSelect:
	case db.pin.ex != nil:
		t = db.pin
	default:
		if rc, _ := ctx.Value(routeKey{}).(routeCtx); !rc.primary {
			t = sh.pick(st.DryRun)
		}
	}
	st.Route = Route{Shard: t.sh.name, Replica: t.replica}
	return t, nil
}

// pick returns the next replica in turn that is not in back-off, or the
// primary when every one is. With peek set it leaves the turn where it was.
func (sh *shard) pick(peek bool) target {
	var turn uint32
	if peek {
		turn = sh.next.Load()
	} else {
		turn = sh.next.Add(1) - 1
	}
	n := len(sh.replicas)
	first := int(turn % uint32(n))
	var now int64 // read only when a replica has been in back-off
	for k := range n {
		i := (first + k) % n
		r := sh.replicas[i]
		if until := r.downUntil.Load(); until != 0 {
			if now == 0 {
				now = time.Now().UnixNano()
			}
			if now < until {
				continue
			}
		}
		return target{ex: r.ex, sh: sh, replica: i}
	}
	return target{ex: sh.primary, sh: sh, replica: -1}
}
