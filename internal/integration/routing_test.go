package integration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/testmodel"
	"github.com/syssam/velox/veloxpgx"
)

// The routing tests stand a second and a third database in for a replica and
// a second shard (routing spec §9): velox_replica and velox_shard2, made
// beside the CI database for each test and dropped after it. They prove where
// a statement went by reading each database directly, with database/sql and
// no velox. They do not test replication: the replica is a different database
// whose rows differ, so that a read shows which one it reached.

// routingDriver is one way to reach a server: PostgreSQL through pgx's native
// protocol, PostgreSQL through database/sql, or MySQL.
type routingDriver struct {
	name string
	d    velox.Dialect
	pgx  bool
	base string // the CI database's DSN; the others are the same server
	// primary is the database that DSN names; the replica and the second
	// shard are made beside it.
	primary string
	skip    string // why the driver's tests do not run; "" when they do
}

// routingDrivers lists the three drivers, each with its reason to skip when
// the environment does not name its server.
func routingDrivers(t *testing.T) []routingDriver {
	t.Helper()
	pg, my, admin := os.Getenv("VELOX_TEST_POSTGRES"), os.Getenv("VELOX_TEST_MYSQL"), os.Getenv("VELOX_TEST_MYSQL_ADMIN")
	var pgSkip, mySkip string
	if pg == "" {
		pgSkip = "VELOX_TEST_POSTGRES is unset"
	}
	switch {
	case my == "":
		mySkip = "VELOX_TEST_MYSQL is unset"
	case admin == "":
		mySkip = "VELOX_TEST_MYSQL_ADMIN is unset: it is the root DSN that creates the databases the routing tests use"
	}
	var pgName, myName string
	if pg != "" {
		u, err := url.Parse(pg)
		if err != nil {
			t.Fatal(err)
		}
		pgName = strings.TrimPrefix(u.Path, "/")
	}
	if my != "" {
		cfg, err := mysql.ParseDSN(my)
		if err != nil {
			t.Fatal(err)
		}
		myName = cfg.DBName
	}
	return []routingDriver{
		{name: "postgres-pgx", d: velox.Postgres, pgx: true, base: pg, primary: pgName, skip: pgSkip},
		{name: "postgres-sql", d: velox.Postgres, base: pg, primary: pgName, skip: pgSkip},
		{name: "mysql", d: velox.MySQL, base: my, primary: myName, skip: mySkip},
	}
}

// eachRouting runs f on every driver whose server the environment names, with
// the databases made.
func eachRouting(t *testing.T, f func(t *testing.T, r routingDriver)) {
	for _, r := range routingDrivers(t) {
		t.Run(r.name, func(t *testing.T) {
			if r.skip != "" {
				t.Skip(r.skip)
			}
			r.databases(t)
			f(t, r)
		})
	}
}

const (
	replicaDB = "velox_replica"
	shard2DB  = "velox_shard2"
)

// dsn returns the DSN of database db on the CI server, or of the same
// database on addr (host:port) when that is not "".
func (r routingDriver) dsn(t *testing.T, db, addr string) string {
	t.Helper()
	if r.d == velox.MySQL {
		cfg, err := mysql.ParseDSN(r.base)
		if err != nil {
			t.Fatal(err)
		}
		cfg.DBName = db
		if addr != "" {
			cfg.Addr = addr
		}
		return cfg.FormatDSN()
	}
	u, err := url.Parse(r.base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + db
	if addr != "" {
		u.Host = addr
	}
	return u.String()
}

// open returns a database/sql handle on dsn, closed when t ends.
func (r routingDriver) open(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	name := "pgx"
	if r.d == velox.MySQL {
		name = "mysql"
	}
	db, err := sql.Open(name, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// direct returns a handle on database db for reading it back without velox.
func (r routingDriver) direct(t *testing.T, db string) *sql.DB {
	t.Helper()
	return r.open(t, r.dsn(t, db, ""))
}

// admin returns a handle with the right to make databases and to end other
// sessions: PostgreSQL's velox is its image's superuser; MySQL needs the root
// DSN of VELOX_TEST_MYSQL_ADMIN.
func (r routingDriver) admin(t *testing.T) *sql.DB {
	t.Helper()
	if r.d == velox.MySQL {
		return r.open(t, os.Getenv("VELOX_TEST_MYSQL_ADMIN"))
	}
	return r.open(t, r.dsn(t, r.primary, ""))
}

// handle is a pool on one database: a Conn to give velox, and a way to open
// the pool as a DB's primary.
type handle struct {
	conn velox.Conn
	open func(opts ...velox.Option) *velox.DB
}

// handle returns the pool for database db, or for db on addr (host:port) when
// that is not "": through pgx for a pgx driver, else through database/sql.
func (r routingDriver) handle(t *testing.T, db, addr string) handle {
	t.Helper()
	dsn := r.dsn(t, db, addr)
	if r.pgx {
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pool.Close)
		return handle{veloxpgx.Conn(pool), func(opts ...velox.Option) *velox.DB {
			return veloxpgx.Open(pool, opts...).WithoutPolicy("routing test")
		}}
	}
	sdb := r.open(t, dsn)
	return handle{velox.SQLConn(sdb), func(opts ...velox.Option) *velox.DB {
		return velox.Open(r.d, sdb, opts...).WithoutPolicy("routing test")
	}}
}

// databases drops and makes velox_replica and velox_shard2, gives each, and
// the CI database, empty invoices, items and tickets tables, and drops the two
// databases again when t ends. Dropping first means a run that died leaves
// nothing that breaks the next.
func (r routingDriver) databases(t *testing.T) {
	t.Helper()
	admin := r.admin(t)
	dropSuffix, grants := "", func(string) []string { return nil }
	if r.d == velox.Postgres {
		dropSuffix = " WITH (FORCE)" // sessions left by an earlier run
	} else {
		cfg, err := mysql.ParseDSN(r.base)
		if err != nil {
			t.Fatal(err)
		}
		// The CI user may not touch a database it did not make: the root
		// connection grants it these.
		grants = func(db string) []string { return []string{"GRANT ALL ON " + db + ".* TO '" + cfg.User + "'@'%'"} }
	}
	drop := func() {
		for _, db := range []string{replicaDB, shard2DB} {
			q := "DROP DATABASE IF EXISTS " + db + dropSuffix
			if _, err := admin.ExecContext(ctx, q); err != nil {
				t.Errorf("%s: %v", q, err)
			}
		}
	}
	drop()
	t.Cleanup(drop) // registered first, so it runs after the pools are closed
	for _, db := range []string{replicaDB, shard2DB} {
		for _, q := range append([]string{"CREATE DATABASE " + db}, grants(db)...) {
			if _, err := admin.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
	ddl := []string{pgDDL, pgItems, pgTickets}
	if r.d == velox.MySQL {
		ddl = []string{mysqlDDL, mysqlItems, mysqlTickets}
	}
	for _, db := range []string{r.primary, replicaDB, shard2DB} {
		d := r.direct(t, db)
		for _, table := range []string{"tickets", "items", "invoices"} {
			if _, err := d.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
				t.Fatal(err)
			}
		}
		for _, q := range ddl {
			if _, err := d.ExecContext(ctx, q); err != nil {
				t.Fatalf("%s: %v", db, err)
			}
		}
		_ = d.Close() // no session of ours is left on the database
	}
}

// seed writes an invoice into d, a database read directly.
func (r routingDriver) seed(t *testing.T, d *sql.DB, id, org int64, number string) {
	t.Helper()
	q := rebind(r.d, "INSERT INTO invoices (created_at, updated_at, id, org_id, number, total, paid, note, data) VALUES (?, ?, ?, ?, ?, 1, ?, '', ?)")
	if _, err := d.ExecContext(ctx, q, t0, t0, id, org, number, false, []byte("d")); err != nil {
		t.Fatal(err)
	}
}

// invoiceNumbers returns the numbers of the invoices in d, a database read
// directly, in id order.
func invoiceNumbers(t *testing.T, d *sql.DB) []string {
	t.Helper()
	rows, err := d.QueryContext(ctx, "SELECT number FROM invoices ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// readNumbers returns the numbers of the invoices db reads under c, in id
// order.
func readNumbers(t *testing.T, c context.Context, db *velox.DB) []string {
	t.Helper()
	got, err := db.Query[testmodel.Invoice]().OrderBy(I.ID.Asc()).AllowUnbounded().All(c)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(got))
	for i, inv := range got {
		out[i] = inv.Number
	}
	return out
}

// routeLog records the Route of each statement, read in an Around hook after
// next, where the route stage and a failover have both had their say.
type routeLog struct {
	mu     sync.Mutex
	routes []velox.Route
}

func (l *routeLog) option() velox.Option {
	return velox.WithAround(func(ctx context.Context, st *velox.Statement, next func(context.Context) error) error {
		err := next(ctx)
		l.mu.Lock()
		l.routes = append(l.routes, st.Route)
		l.mu.Unlock()
		return err
	})
}

// last returns the Route of the latest statement.
func (l *routeLog) last(t *testing.T) velox.Route {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.routes) == 0 {
		t.Fatal("no statement reached the Around hook")
	}
	return l.routes[len(l.routes)-1]
}

func wantNumbers(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s: %v, want %v", what, got, want)
	}
}

func wantRoute(t *testing.T, what string, got, want velox.Route) {
	t.Helper()
	if got != want {
		t.Errorf("%s: route %+v, want %+v", what, got, want)
	}
}

func insertInvoice(t *testing.T, c context.Context, db *velox.DB, org int64, number string) {
	t.Helper()
	inv := newInvoice(org, number)
	if err := db.Insert(&inv).Exec(c); err != nil {
		t.Fatal(err)
	}
}

// TestRoutingSendsWhereItSays: a write reaches the primary database only; a
// read reaches the replica database, except within a session's pin window or
// under Primary; and a sharded DB puts each org's rows in its own
// database. Each claim is checked by reading the databases themselves.
func TestRoutingSendsWhereItSays(t *testing.T) {
	eachRouting(t, func(t *testing.T, r routingDriver) {
		primary, replica, shard2 := r.direct(t, r.primary), r.direct(t, replicaDB), r.direct(t, shard2DB)
		r.seed(t, primary, 1, 1, "PRIMARY-SEED")
		r.seed(t, replica, 1, 1, "REPLICA-SEED")
		var log routeLog
		const window = time.Second
		db := r.handle(t, r.primary, "").open(
			velox.WithReplicas(r.handle(t, replicaDB, "").conn), velox.WithPinWindow(window), log.option())

		insertInvoice(t, ctx, db, 1, "WRITTEN")
		wantRoute(t, "the write", log.last(t), velox.Route{Replica: -1})
		wantNumbers(t, "primary after a write", invoiceNumbers(t, primary), []string{"PRIMARY-SEED", "WRITTEN"})
		wantNumbers(t, "replica after a write", invoiceNumbers(t, replica), []string{"REPLICA-SEED"})
		wantNumbers(t, "shard2 after a write", invoiceNumbers(t, shard2), nil)

		// With no session, the read goes to the replica: it finds the row only
		// the replica has, and not the primary's.
		wantNumbers(t, "read with no session", readNumbers(t, ctx, db), []string{"REPLICA-SEED"})
		wantRoute(t, "read with no session", log.last(t), velox.Route{Replica: 0})

		wantNumbers(t, "read under Primary", readNumbers(t, velox.WithPrimaryReads(ctx), db), []string{"PRIMARY-SEED", "WRITTEN"})
		wantRoute(t, "read under Primary", log.last(t), velox.Route{Replica: -1})

		// Within a session, a read after a write reads the primary, the write
		// included; once the window has passed, the replica again.
		sctx := velox.WithSession(ctx)
		wantNumbers(t, "read in a session before its write", readNumbers(t, sctx, db), []string{"REPLICA-SEED"})
		// The pin starts inside insertInvoice, so the guard below measures
		// from before it; the sleep after the window measures from after it,
		// which is the later, and so the safe, side.
		before := time.Now()
		insertInvoice(t, sctx, db, 1, "IN-SESSION")
		wrote := time.Now()
		wantNumbers(t, "read in a session after its write", readNumbers(t, sctx, db), []string{"PRIMARY-SEED", "WRITTEN", "IN-SESSION"})
		wantRoute(t, "read in the pin window", log.last(t), velox.Route{Replica: -1})
		wantNumbers(t, "read with no session in the window", readNumbers(t, ctx, db), []string{"REPLICA-SEED"})
		// The in-window reads must have finished inside the window, or their
		// checks prove nothing about it.
		if took := time.Since(before); took >= window {
			t.Fatalf("the runner took %v for the in-window reads, past the %v window", took, window)
		}
		time.Sleep(time.Until(wrote.Add(window + 100*time.Millisecond)))
		wantNumbers(t, "read after the window", readNumbers(t, sctx, db), []string{"REPLICA-SEED"})
		wantRoute(t, "read after the window", log.last(t), velox.Route{Replica: 0})
	})
}

// TestRoutingShardsByOrg: a sharded DB writes org 1 to the CI database
// and org 2 to velox_shard2, reads each org from its own, and refuses a
// statement whose context names no shard, sending nothing.
func TestRoutingShardsByOrg(t *testing.T) {
	eachRouting(t, func(t *testing.T, r routingDriver) {
		one, two := r.direct(t, r.primary), r.direct(t, shard2DB)
		var log routeLog
		db := velox.OpenShards(r.d, map[string]velox.Shard{
			"one": {Primary: r.handle(t, r.primary, "").conn},
			"two": {Primary: r.handle(t, shard2DB, "").conn},
		}, velox.WithShardFor(func(ctx context.Context) (string, error) {
			org, _ := testmodel.Org.From(ctx)
			switch org {
			case 1:
				return "one", nil
			case 2:
				return "two", nil
			}
			return "", fmt.Errorf("org %v has no shard", org)
		}), log.option())
		bg := context.Background()
		t1, t2 := testmodel.Org.With(bg, int64(1)), testmodel.Org.With(bg, int64(2))

		insertInvoice(t, t1, db, 1, "ORG-1")
		wantRoute(t, "org 1's write", log.last(t), velox.Route{Shard: "one", Replica: -1})
		insertInvoice(t, t2, db, 2, "ORG-2")
		wantRoute(t, "org 2's write", log.last(t), velox.Route{Shard: "two", Replica: -1})
		wantNumbers(t, "shard one", invoiceNumbers(t, one), []string{"ORG-1"})
		wantNumbers(t, "shard two", invoiceNumbers(t, two), []string{"ORG-2"})

		wantNumbers(t, "org 1's read", readNumbers(t, t1, db), []string{"ORG-1"})
		wantNumbers(t, "org 2's read", readNumbers(t, t2, db), []string{"ORG-2"})
		wantRoute(t, "org 2's read", log.last(t), velox.Route{Shard: "two", Replica: -1})
		free := db.WithoutPolicy("routing test")
		wantNumbers(t, "WithShard", readNumbers(t, velox.WithShard(ctx, "two"), free), []string{"ORG-2"})

		_, err := free.Query[testmodel.Invoice]().AllowUnbounded().All(ctx)
		if !errors.Is(err, velox.ErrNoShard) {
			t.Errorf("a read under WithoutPolicy without WithShard: %v, want ErrNoShard", err)
		}
		refused := newInvoice(1, "REFUSED")
		if err := free.Insert(&refused).Exec(ctx); !errors.Is(err, velox.ErrNoShard) {
			t.Errorf("a write under WithoutPolicy without WithShard: %v, want ErrNoShard", err)
		}
		wantNumbers(t, "shard one after the refusal", invoiceNumbers(t, one), []string{"ORG-1"})
		wantNumbers(t, "shard two after the refusal", invoiceNumbers(t, two), []string{"ORG-2"})
	})
}

// TestFailoverToPrimary: a replica on a closed port falls over: the read
// returns the primary's rows, and an Around hook sees Route.Fallback. The
// replica is then in back-off, and the next read goes straight to the primary.
func TestFailoverToPrimary(t *testing.T) {
	eachRouting(t, func(t *testing.T, r routingDriver) {
		r.seed(t, r.direct(t, r.primary), 1, 1, "PRIMARY-SEED")
		r.seed(t, r.direct(t, replicaDB), 1, 1, "REPLICA-SEED")
		var log routeLog
		db := r.handle(t, r.primary, "").open(velox.WithReplicas(r.handle(t, replicaDB, "127.0.0.1:1").conn), log.option())

		wantNumbers(t, "read from a dead replica", readNumbers(t, ctx, db), []string{"PRIMARY-SEED"})
		wantRoute(t, "read from a dead replica", log.last(t), velox.Route{Replica: -1, Fallback: true})
		wantNumbers(t, "read in back-off", readNumbers(t, ctx, db), []string{"PRIMARY-SEED"})
		wantRoute(t, "read in back-off", log.last(t), velox.Route{Replica: -1})
	})
}

// TestFailoverAfterBackendTerminated: the server ends the replica's pooled
// connections, as a restarted or failed-over replica does to a warm pool, and
// the next read still succeeds. What it reaches depends on the driver, which
// the test observes (the drivers' errors are in the task report):
//   - pgx's pool does not notice until it sends, and gets SQLSTATE 57P01
//     (admin_shutdown): the read falls over to the primary, strictly.
//   - database/sql with either driver finds the connection dead before
//     sending (ErrBadConn) and reconnects to the replica itself, so no error
//     escapes (the driver heals the connection). That is all the test claims
//     for them: it reads the replica's rows with Route {Replica: 0}, or, if
//     the driver sends and fails first, falls over; the rows must match the
//     route.
//
// TestFailoverWhenReplicaGoesAway makes the fall over certain for every
// driver.
func TestFailoverAfterBackendTerminated(t *testing.T) {
	eachRouting(t, func(t *testing.T, r routingDriver) {
		r.seed(t, r.direct(t, r.primary), 1, 1, "PRIMARY-SEED")
		seeder := r.direct(t, replicaDB)
		r.seed(t, seeder, 1, 1, "REPLICA-SEED")
		_ = seeder.Close() // so the sessions counted below are velox's pool's
		var log routeLog
		db := r.handle(t, r.primary, "").open(velox.WithReplicas(r.handle(t, replicaDB, "").conn), log.option())

		wantNumbers(t, "the warming read", readNumbers(t, ctx, db), []string{"REPLICA-SEED"})
		wantRoute(t, "the warming read", log.last(t), velox.Route{Replica: 0})
		if n := r.endSessions(t, replicaDB); n == 0 {
			t.Fatal("velox's pool holds no session on the replica database to end: it was not warm")
		}

		got := readNumbers(t, ctx, db)
		route := log.last(t)
		switch {
		case route.Fallback:
			wantNumbers(t, "fallen over", got, []string{"PRIMARY-SEED"})
			wantRoute(t, "fallen over", route, velox.Route{Replica: -1, Fallback: true})
		case r.pgx:
			t.Errorf("pgx read after the backends ended: %v on route %+v, want the primary's rows with Fallback", got, route)
		default:
			t.Logf("%s: no error escaped; the driver healed the connection", r.name)
			wantNumbers(t, "healed", got, []string{"REPLICA-SEED"})
			wantRoute(t, "healed", route, velox.Route{Replica: 0})
		}
	})
}

// TestFailoverWhenReplicaGoesAway: the replica is reached through a proxy
// that is stopped after the pool is warm, which ends its connections and
// refuses new ones, as a replica that went down does. Every driver's pool
// fails, and the read falls over to the primary.
func TestFailoverWhenReplicaGoesAway(t *testing.T) {
	eachRouting(t, func(t *testing.T, r routingDriver) {
		r.seed(t, r.direct(t, r.primary), 1, 1, "PRIMARY-SEED")
		r.seed(t, r.direct(t, replicaDB), 1, 1, "REPLICA-SEED")
		addr, stop := tcpProxy(t, r.serverAddr(t))
		var log routeLog
		db := r.handle(t, r.primary, "").open(velox.WithReplicas(r.handle(t, replicaDB, addr).conn), log.option())

		wantNumbers(t, "the warming read", readNumbers(t, ctx, db), []string{"REPLICA-SEED"})
		wantRoute(t, "the warming read", log.last(t), velox.Route{Replica: 0})
		stop()

		wantNumbers(t, "read after the replica went away", readNumbers(t, ctx, db), []string{"PRIMARY-SEED"})
		wantRoute(t, "read after the replica went away", log.last(t), velox.Route{Replica: -1, Fallback: true})
	})
}

// serverAddr returns the host:port of the CI server.
func (r routingDriver) serverAddr(t *testing.T) string {
	t.Helper()
	if r.d == velox.MySQL {
		cfg, err := mysql.ParseDSN(r.base)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.Addr
	}
	u, err := url.Parse(r.base)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

// tcpProxy listens on a free local port and forwards each connection to
// addr. The returned stop closes the listener and every connection through
// it, and waits for the forwarding to end; t's cleanup calls it too.
func tcpProxy(t *testing.T, addr string) (listen string, stop func()) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
		wg    sync.WaitGroup
	)
	wg.Go(func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			up, err := net.Dial("tcp", addr)
			if err != nil {
				_ = c.Close()
				continue
			}
			mu.Lock()
			conns = append(conns, c, up)
			mu.Unlock()
			wg.Go(func() { _, _ = io.Copy(up, c); _ = up.Close() })
			wg.Go(func() { _, _ = io.Copy(c, up); _ = c.Close() })
		}
	})
	var once sync.Once
	stop = func() {
		once.Do(func() {
			_ = l.Close()
			mu.Lock()
			for _, c := range conns {
				_ = c.Close()
			}
			mu.Unlock()
			wg.Wait()
		})
	}
	t.Cleanup(stop)
	return l.Addr().String(), stop
}

// endSessions ends every server session on database db, and returns how many.
func (r routingDriver) endSessions(t *testing.T, db string) int {
	t.Helper()
	admin := r.admin(t)
	if r.d == velox.Postgres {
		rows, err := admin.QueryContext(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()", db)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		n := 0
		for rows.Next() {
			n++
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		// pg_terminate_backend only signals: wait until the sessions are gone.
		var left int
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if err := admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()", db).Scan(&left); err != nil {
				t.Fatal(err)
			}
			if left == 0 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d sessions on %s still open 5s after pg_terminate_backend", left, db)
			}
		}
		return n
	}
	rows, err := admin.QueryContext(ctx, "SELECT id FROM information_schema.processlist WHERE db = ?", db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var idsToKill []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		idsToKill = append(idsToKill, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, id := range idsToKill {
		if _, err := admin.ExecContext(ctx, fmt.Sprintf("KILL %d", id)); err != nil {
			t.Fatal(err)
		}
	}
	return len(idsToKill)
}
