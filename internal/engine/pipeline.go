package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/syssam/velox/internal/dberr"
	"io"
	"maps"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/syssam/velox/internal/conn"
)

// DB is the root package's DB without what is generic: every statement's path
// through velox. The root's DB holds one by value, and adds the generic API.
type DB struct {
	D Dialect
	// Ex runs the statements of a DB without replicas, and of a transaction;
	// nil on a DB with replicas outside a transaction, whose Rt chooses.
	Ex   executor
	Rt   *router // WithReplicas's; nil without replicas
	Cfg  *config
	InTx bool
	// txDeadline is, in a transaction, its deadline: velox's or the
	// caller's, whichever is earlier; zero when none. txCause is ErrTimeout
	// when the deadline is velox's, nil when it is the caller's.
	txDeadline time.Time
	txCause    error
	tx         *TxInfo // the transaction's, when hooks are configured
	// txWrote is, in a transaction on a DB with replicas, set by its first
	// successful write; the commit then pins the session of the context given
	// to Tx. Its savepoints share it. Nil elsewhere.
	txWrote *atomic.Bool
	// txShard is, in a transaction on a sharded DB, the shard it runs on;
	// nil elsewhere.
	txShard *shardConn
	// txRoot is, in a transaction, its frame: the identity the mark on its
	// fn's ctx carries, which its savepoints share. Nil elsewhere.
	txRoot *txFrame
	// txSelf is, in a transaction, the frame of this handle: the root's for
	// the transaction's own, a savepoint's for a nested Tx's. Nil elsewhere.
	txSelf *txFrame
	// pool is the *sql.DB of Open and Connect; nil on any other DB. owned says
	// Connect opened it, so that Close closes it. A field naming *sql.DB would
	// put database/sql's types in the export data of every package that uses a
	// DB, 30% more for an entity's package: see TestGenericTypesCompileSmall.
	pool  io.Closer
	owned bool
	// bypass is WithoutPolicy's reason: a DB whose statements skip every
	// entity's policy; "" on any other.
	bypass string
	// misuse fails every statement and Tx of the DB: WithoutPolicy's with
	// no reason. Nil on any other.
	misuse error
	// rec is Record's: a DB whose writes go to its sink; nil on any other.
	rec *Recording
	// batch is, on the handles of a recorded write's transaction or
	// savepoint, the write's Changes so far (batched); nil elsewhere.
	batch *batch
}

// Open is velox.Open's DB, opts given as a slice.
func Open(d Dialect, db *sql.DB, opts []Option) DB {
	if !validDialect(d) {
		panic(fmt.Sprintf("velox: Open: unknown %v; use velox.Postgres, velox.MySQL or velox.SQLite", d))
	}
	e := newDB(d, conn.NewSQL(db), opts)
	e.pool = db
	return e
}

// Connect is velox.Connect's DB, opts given as a slice.
func Connect(driver, dsn string, opts []Option) (DB, error) {
	d, ok := driverDialect(driver)
	if !ok {
		return DB{}, fmt.Errorf("velox: Connect: no dialect for driver %q; use velox.Open with the *sql.DB it opens", driver)
	}
	if d == MySQL {
		if err := checkMySQLDSN(dsn); err != nil {
			return DB{}, err
		}
	}
	open := sql.Open
	if d == SQLite {
		open = openSQLite
	}
	db, err := open(driver, dsn)
	if err != nil {
		return DB{}, fmt.Errorf("velox: Connect: %w", err)
	}
	if d == SQLite {
		db.SetMaxOpenConns(1)
	}
	e := Open(d, db, opts)
	e.owned = true
	return e, nil
}

// driverDialect is the dialect of a database/sql driver's usual name.
func driverDialect(driver string) (Dialect, bool) {
	switch driver {
	case "postgres", "pgx":
		return Postgres, true
	case "mysql":
		return MySQL, true
	case "sqlite", "sqlite3":
		return SQLite, true
	}
	return 0, false
}

// checkMySQLDSN refuses a DSN without the two parameters velox relies on. Its
// errors do not quote the DSN, which holds a password.
func checkMySQLDSN(dsn string) error {
	// As the driver reads it: the parameters follow the last '/', since the
	// password may hold a '/' or a '?'.
	_, query, _ := strings.Cut(dsn[strings.LastIndexByte(dsn, '/')+1:], "?")
	values, err := url.ParseQuery(query)
	if err != nil {
		return errors.New("velox: Connect: the DSN's parameters do not parse")
	}
	for _, name := range []string{"parseTime", "clientFoundRows"} {
		if on, err := strconv.ParseBool(values.Get(name)); err != nil || !on {
			return fmt.Errorf("velox: Connect: a MySQL DSN needs %s=true", name)
		}
	}
	return nil
}

// Pool is velox.Pool. Like the field, it is not a method of DB, for the export
// data's sake.
func Pool(db *DB) *sql.DB {
	if db.InTx {
		return nil
	}
	p, _ := db.pool.(*sql.DB)
	return p
}

// Close is velox.DB.Close.
func (db *DB) Close() error {
	switch {
	case db.InTx:
		return errors.New("velox: Close: called on a transaction's DB")
	case !db.owned:
		return nil
	}
	return db.pool.Close()
}

// OpenConn is velox.OpenConn's DB.
func OpenConn(d Dialect, c Conn, opts []Option) DB {
	if !validDialect(d) {
		panic(fmt.Sprintf("velox: OpenConn: unknown %v; use velox.Postgres, velox.MySQL or velox.SQLite", d))
	}
	switch {
	case ConnExec(c) == nil:
		panic("velox: OpenConn: the Conn is zero; make it with SQLConn or a driver package's Conn")
	case connOnly(c) != 0 && connOnly(c) != d:
		panic(fmt.Sprintf("velox: OpenConn: the Conn is valid only on %s; the DB is %s", connOnly(c), d))
	}
	return newDB(d, ConnExec(c), opts)
}

// dialectExecutor is ex with what dialect d needs of every statement.
func dialectExecutor(d Dialect, ex executor) executor {
	if d == SQLite {
		return conn.SQLiteTimes(ex)
	}
	return ex
}

// newDB returns an unsharded DB of dialect d on ex, its primary.
func newDB(d Dialect, ex executor, opts []Option) DB {
	cfg := newConfig(opts)
	if cfg.ShardFor != nil {
		panic("velox: WithShardFor is for OpenShards; Connect, Open and OpenConn open a DB on one shard")
	}
	if len(cfg.Replicas) > 0 {
		return DB{D: d, Rt: newRouter(d, ex, cfg.Replicas), Cfg: cfg}
	}
	return DB{D: d, Ex: dialectExecutor(d, ex), Cfg: cfg}
}

// OpenShards is velox.OpenShards's DB.
func OpenShards(d Dialect, shards map[string]Shard, opts []Option) DB {
	if !validDialect(d) {
		panic(fmt.Sprintf("velox: OpenShards: unknown %v; use velox.Postgres, velox.MySQL or velox.SQLite", d))
	}
	if len(shards) == 0 {
		panic("velox: OpenShards: no shards")
	}
	cfg := newConfig(opts)
	if cfg.ShardFor == nil {
		panic("velox: OpenShards: WithShardFor is required, to find a statement's shard from its context")
	}
	if len(cfg.Replicas) > 0 {
		panic("velox: OpenShards: WithReplicas is for Connect, Open and OpenConn; give each Shard its Replicas")
	}
	rt := &router{Shards: make(map[string]*shardConn, len(shards)), ShardFor: cfg.ShardFor}
	for _, name := range slices.Sorted(maps.Keys(shards)) {
		s := shards[name]
		if name == "" {
			panic("velox: OpenShards: a shard's name must not be empty")
		}
		what := fmt.Sprintf("OpenShards: shard %q's", name)
		switch {
		case ConnExec(s.Primary) == nil:
			panic(fmt.Sprintf("velox: %s Primary is the zero Conn; make it with SQLConn or a driver package's Conn", what))
		case connOnly(s.Primary) != 0 && connOnly(s.Primary) != d:
			panic(fmt.Sprintf("velox: %s Primary is valid only on %s; the DB is %s", what, connOnly(s.Primary), d))
		}
		for i, c := range s.Replicas {
			if ConnExec(c) == nil {
				panic(fmt.Sprintf("velox: %s replica %d is the zero Conn; make it with SQLConn or a driver package's Conn", what, i))
			}
		}
		sh := newShard(d, ConnExec(s.Primary), s.Replicas, what)
		sh.Name = name
		rt.Shards[name] = sh
	}
	return DB{D: d, Rt: rt, Cfg: cfg}
}

// Hooked reports whether the pipeline has hooks: whether a statement must go
// through run, or may take the shortcut of plain.
func (db *DB) Hooked() bool { return len(db.Cfg.Rewrites) > 0 || len(db.Cfg.Arounds) > 0 }

// outsideTx fails a statement, st, under a context marked by a transaction
// that has not ended and that db is not in (foreign), with ErrOutsideTx: one
// context lookup, and a pointer compare. In a transaction a savepoint broke
// (txFrame.broken), it fails st with that error.
func (db *DB) outsideTx(ctx context.Context, st *Statement) error {
	if db.misuse != nil {
		return stmtErr(st, db.misuse)
	}
	if db.Foreign(ctx) {
		return stmtErr(st, ErrOutsideTx)
	}
	if db.txRoot != nil && db.txRoot.broken != nil {
		return stmtErr(st, db.txRoot.broken)
	}
	return nil
}

// dryWrite fails a statement that writes, st, under a dry-run context
// (IsDryRun), with ErrDryRun: nothing is sent. It is made where a statement
// is sent, in plain and run, never in prepare, which SQL(ctx) renders
// through; so the statement being dry-run is rendered, and every other write
// a hook makes under its context is refused. Every write goes through plain
// or run, Raw included, so this is the one place the rule is kept. Reads
// pass without a context lookup.
func (db *DB) dryWrite(ctx context.Context, st *Statement) error {
	if !st.Op.Reads() && IsDryRun(ctx) {
		return stmtErr(st, ErrDryRun)
	}
	return nil
}

// observe sets what st's hooks and adapters read about it: its Dialect, its
// Tags, its transaction's TxInfo and, with WithCallers, its Caller.
func (db *DB) observe(ctx context.Context, st *Statement) {
	st.Dialect = db.D
	st.Tx = db.tx
	st.Tags = TagsFrom(ctx)
	if c := db.Cfg.Callers; c != nil {
		st.Caller = c.Capture()
	}
}

// Plain runs a statement on a DB with no hooks: the dry-run and transaction
// checks (dryWrite, then outsideTx, in run's order), resolving relations,
// guard, scope, rendering (the SQL comment included), routing and execution.
// Unlike run's, its st and read do not escape, so a caller's state and
// reading closure can stay on the stack.
func (db *DB) Plain(ctx context.Context, st *Statement, read func(Rows) error, res *execResult) error {
	if err := db.dryWrite(ctx, st); err != nil {
		return err
	}
	if err := db.outsideTx(ctx, st); err != nil {
		return err
	}
	if err := db.held(ctx, st); err != nil {
		return err
	}
	if err := ResolveStatement(st); err != nil {
		return err
	}
	if err := db.guard(st); err != nil {
		return err
	}
	if err := db.scope(ctx, st); err != nil {
		return err
	}
	keyReturning(db.D, st)
	db.capLimit(st)
	if db.Cfg.Comment != nil {
		db.observe(ctx, st) // the comment reads the call site and tags
	} else {
		st.Dialect = db.D
	}
	e, args, err := db.Cfg.Cache.Get(db.D, st)
	if err != nil {
		return err
	}
	st.SQL, st.Fingerprint, st.Args = e.Text, e.Fp, args
	if db.Cfg.Comment != nil {
		st.SQL += db.Cfg.Comment.Text(st)
	}
	t, err := db.route(ctx, st)
	if err != nil {
		return err
	}
	if !db.timed() {
		return endedErr(ctx, st.Entity.Name, db.exec(ctx, st, t, read, res))
	}
	// tctx is ctx itself when ctx's deadline comes first: then it is velox's
	// only on a context velox bounded, such as a transaction's fn's ctx.
	tctx, cancel := db.bound(ctx)
	if cancel != nil {
		defer cancel()
	}
	return endedErr(tctx, st.Entity.Name, db.exec(tctx, st, t, read, res))
}

// held refuses a statement that would wait for the pool's one connection
// that its own loop or Tx holds, under the context the caller gave, before
// velox bounds it; and notes an Iter's context for the hold (iterconn.go).
func (db *DB) held(ctx context.Context, st *Statement) error {
	if st.DryRun {
		return nil
	}
	if err := waitsForConn(ctx, db); err != nil {
		return stmtErr(st, err)
	}
	if st.iter {
		st.iterCtx = ctx
	}
	return nil
}

// Run takes a statement through the pipeline: prepare's stages, the route
// stage, the parameter limit (checkParams), then the around hooks wrapping
// execution, which is sending the statement and reading its rows. read reads
// them; it is called once per execution (an around hook that retries runs it
// again) and must not close them: run does, before it returns, on every path.
// Hooks may keep the statement, and next (which holds read), so both escape;
// a statement with no hooks of either kind uses plain.
func (db *DB) Run(ctx context.Context, st *Statement, read func(Rows) error, res *execResult) error {
	if err := db.dryWrite(ctx, st); err != nil {
		return err
	}
	if err := db.prepare(ctx, st); err != nil {
		return err
	}
	if err := db.held(ctx, st); err != nil {
		return err
	}
	t, err := db.route(ctx, st)
	if err != nil {
		return err
	}
	// Before the around hooks, which never see a statement that cannot be
	// sent; exec checks again, for a hook that changes Args.
	if err := checkParams(db, st); err != nil {
		return err
	}
	if db.timed() {
		// As in plain, tctx may be ctx, and its deadline velox's.
		tctx, cancel := db.bound(ctx)
		if cancel != nil {
			defer cancel()
		}
		return endedErr(tctx, st.Entity.Name, db.execute(tctx, st, t, read, res))
	}
	return endedErr(ctx, st.Entity.Name, db.execute(ctx, st, t, read, res))
}

// prepare takes a statement through every stage before execution, for run
// and for SQL(ctx): the transaction check (outsideTx), resolving the
// relations in its conditions (HasWith leaves them to the statement), guard,
// observe, rewrite hooks and their checks, scope, the row cap, and rendering
// into st.SQL (comment included) and st.Args. Raw SQL takes only the
// transaction check, in a transaction refuseTxEnd, and prepareRaw.
func (db *DB) prepare(ctx context.Context, st *Statement) error {
	if err := db.outsideTx(ctx, st); err != nil {
		return err
	}
	if st.Op.Raw() {
		if db.InTx {
			if err := refuseTxEnd(db.D, st); err != nil {
				return err
			}
		}
		db.prepareRaw(ctx, st)
		return nil
	}
	if err := ResolveStatement(st); err != nil {
		return err
	}
	if err := db.guard(st); err != nil {
		return err
	}
	db.observe(ctx, st)
	snap := takeRewriteSnapshot(st)
	if db.Hooked() {
		// Hooks may edit the statement in place; give them copies so that
		// the query the statement came from stays as it was. With no hooks
		// nothing is copied.
		cloneForHooks(st)
	}
	// The Each the hooks get: a copy of velox's when there are hooks. A hook
	// may not replace it or change it.
	snap.each = st.Each
	for _, rw := range db.Cfg.Rewrites {
		if err := rw(ctx, st); err != nil {
			return newStageError(snap.entity.Name, "rewrite", err)
		}
	}
	if err := snap.restoreAndCheck(st); err != nil {
		return err
	}
	if len(db.Cfg.Rewrites) > 0 {
		if err := db.checkRewritten(st, snap); err != nil {
			return err
		}
	}
	if err := db.scope(ctx, st); err != nil {
		return err
	}
	if snap.returning && len(st.Returning) == 0 && stmtUpsertPolicy(st) {
		return fmt.Errorf("velox: %s: a Rewrite hook dropped the RETURNING of an upsert whose update branch the policy conditions; velox reads the row back to find a row the policy hides", snap.entity.Name)
	}
	keyReturning(db.D, st)
	db.capLimit(st)
	if st.Entity == snap.entity {
		e, args, err := db.Cfg.Cache.Get(db.D, st)
		if err != nil {
			return err
		}
		st.SQL, st.Fingerprint, st.Args = e.Text, e.Fp, args
	} else {
		// A hook pointed the statement at its own EntityInfo, likely one
		// made for this statement alone; caching by it would fill the cache.
		text, args, err := RenderStmt(db.D, st)
		if err != nil {
			return err
		}
		st.SQL, st.Args = text, args
		st.Fingerprint = Fingerprint(st.SQL)
	}
	// After the fingerprint, which is of the text without the comment.
	if db.Cfg.Comment != nil {
		st.SQL += db.Cfg.Comment.Text(st)
	}
	return nil
}

// DryRun prepares and routes st, with DryRun set, and returns what run would
// send, or the error it would fail with before sending: the SQL(ctx) methods'
// work. It never executes st, so no Around hook runs.
func (db *DB) DryRun(ctx context.Context, st *Statement) (string, []any, error) {
	st.DryRun = true
	if err := db.prepare(ctx, st); err != nil {
		return "", nil, err
	}
	if _, err := db.route(ctx, st); err != nil {
		return "", nil, err
	}
	if err := checkParams(db, st); err != nil {
		return "", nil, err
	}
	return st.SQL, st.Args, nil
}

// execute runs a rendered, routed statement through the around hooks; t is
// where the route stage sent it.
func (db *DB) execute(ctx context.Context, st *Statement, t Target, read func(Rows) error, res *execResult) error {
	if len(db.Cfg.Arounds) == 0 {
		return db.exec(ctx, st, t, read, res)
	}
	// Reset here as well as in exec: a hook that returns before calling
	// next must leave the outer hooks no stale Result.
	st.Result = Result{Affected: -1}
	c := &aroundCall{db: db, st: st, t: t, read: read, res: res}
	// The outermost hook is called directly; each inner one is wrapped as
	// its outer hook's next. With one hook, that is c and its next alone.
	next := c.innermost
	arounds := db.Cfg.Arounds
	for i := len(arounds) - 1; i > 0; i-- {
		a, inner := arounds[i], next
		next = func(ctx context.Context) error { return a(ctx, st, inner) }
	}
	if err := arounds[0](ctx, st, next); err != nil {
		return err
	}
	if !c.called {
		return fmt.Errorf("velox: %s: an Around hook returned without calling next", st.Entity.Name)
	}
	if c.last != nil {
		return fmt.Errorf("velox: %s: an Around hook discarded the error from next", st.Entity.Name)
	}
	return nil
}

// aroundCall is one execute through the Around hooks: what the innermost
// next runs, and what it saw.
type aroundCall struct {
	db     *DB
	st     *Statement
	t      Target
	read   func(Rows) error
	res    *execResult
	called bool  // whether any hook called the innermost next
	last   error // what the innermost next last returned
}

// innermost is the innermost hook's next: it runs the statement.
func (c *aroundCall) innermost(ctx context.Context) error {
	c.called = true
	c.last = c.db.exec(ctx, c.st, c.t, c.read, c.res)
	return c.last
}

// rowCap is the most rows st may return: its own MaxRows, else, for a
// per-owner read bounded by construction, that bound (eachBound), else the
// DB's; 0 for no cap. Raw's read is capped too, Exec not; so is a per-owner
// count, by its owners, and a grouped aggregate, by its groups, but not Count
// or an aggregate of one row.
func (db *DB) rowCap(st *Statement) int {
	if !st.Op.Reads() || st.Exists || (st.GroupBy == 0 && ((st.Count && st.Each == nil) || st.Aggregate != 0)) {
		return 0
	}
	if st.MaxRows > 0 {
		return st.MaxRows
	}
	if n, ok := eachBound(st); ok {
		return n
	}
	return db.Cfg.MaxRows
}

// capLimit lowers st's Limit to its row cap plus one, so that the database
// stops at the row that trips the cap instead of sending every row; exec's
// countedRows still does the counting. It runs just before rendering: the
// guard and the Rewrite hooks have seen the caller's Limit. A per-owner read
// whose cap is its bound by construction needs no LIMIT, and gets none.
func (db *DB) capLimit(st *Statement) {
	n := db.rowCap(st)
	if st.Each != nil && st.MaxRows == 0 {
		if _, ok := eachBound(st); ok {
			return
		}
	}
	if n > 0 && n < math.MaxInt && (st.Limit == 0 || st.Limit > n) {
		st.Limit = n + 1
	}
}

// exec is send, and marks a transaction the server has rolled back. A broken
// transaction fails here as well as in outsideTx, as an Around hook may call next
// again after the statement that broke it.
func (db *DB) exec(ctx context.Context, st *Statement, t Target, read func(Rows) error, res *execResult) error {
	if db.txRoot != nil && db.txRoot.broken != nil {
		return stmtErr(st, db.txRoot.broken)
	}
	err := heldHint(ctx, db, db.send(ctx, st, t, read, res))
	if db.txRoot != nil {
		switch {
		case err != nil:
			db.noteServerRollback(ctx, err)
		case db.D == SQLite && st.Op.Raw(), db.D == Postgres && st.Op.Raw() && multiStatement(st.SQL):
			return db.noteRawEnd(ctx, st)
		}
	}
	return err
}

// send sends a rendered statement to t, the connection the route stage
// chose, reads its rows with read and closes them; with a nil read it sends a
// statement that returns no rows and fills *res. Either way it sets
// st.Result, and records where it ran in st.ran. Errors from the database,
// the driver or read name the entity.
//
// A read on a replica whose connection fails before any row reached the caller,
// whether the query call or the rows report it (the rows, only when read itself
// returned no error), is sent again, once, to the primary, and the replica is
// put in back-off. read runs again from the start, as it does when an Around
// hook retries. Once Iter has handed the caller a row, the replica's error is
// returned and only the back-off happens. Neither happens once ctx has ended.
func (db *DB) send(ctx context.Context, st *Statement, t Target, read func(Rows) error, res *execResult) error {
	st.Result = Result{Affected: -1}
	if err := checkParams(db, st); err != nil {
		return err
	}
	if t.Replica >= 0 && st.Route.Fallback {
		// An Around hook retrying after a fallback: the statement runs where
		// its Route says, the primary.
		t = Target{Ex: t.Sh.Primary, Sh: t.Sh, Replica: -1}
	}
	setStmtRan(st, t)
	if read == nil {
		r, err := t.Ex.Exec(ctx, st.SQL, st.Args)
		if !st.Op.Reads() {
			// It reached the database: whether it failed or not, its
			// outcome may be written.
			db.wrote(ctx)
		}
		if err != nil {
			return dbErr(st.Entity, err)
		}
		st.Result.Affected = r.Affected
		if res != nil {
			*res = r
		}
		return nil
	}
	r, err := t.Ex.Query(ctx, st.SQL, st.Args)
	if !st.Op.Reads() || (db.InTx && st.Op == OpRawQuery) {
		// As for exec: a write that reached the database pins, whatever
		// came back. A write is not failed over, so it is sent only here.
		// Raw SQL in a transaction may be a write, as velox cannot tell.
		db.wrote(ctx)
	}
	if err != nil {
		if !db.broken(ctx, t, err) {
			return dbErr(st.Entity, err)
		}
		t = db.fallBack(st, t)
		if r, err = t.Ex.Query(ctx, st.SQL, st.Args); err != nil {
			return dbErr(st.Entity, err)
		}
	}
	// Count the rows only when something reads the count: an Around hook
	// reads Result.Rows, and the cap needs it. Wrapping them allocates.
	n := db.rowCap(st)
	var counted *countedRows
	if n > 0 || len(db.Cfg.Arounds) > 0 {
		counted = &countedRows{Rows: r, Left: n, Capped: n > 0}
		r = counted
	}
	rerr, rowsErr := readHeld(db, st, r, read)
	// The rows error counts for failover only when read returned nil: a
	// broken connection ends the rows with Next false, so read finishes
	// cleanly, while pgx also reports read's own scan error through
	// rows.Err(), where a Scanner's io.ErrUnexpectedEOF would look like one.
	if rerr == nil && rowsErr != nil && db.broken(ctx, t, rowsErr) {
		if stmtHanded(st) {
			// Iter has handed the caller a row, which cannot be taken
			// back: the error is returned, and only the replica pays.
			t.Sh.Replicas[t.Replica].Down(db.Cfg.Backoff)
		} else {
			// The replica's connection broke before any row reached the
			// caller: read on the primary, from the start. read starts
			// anew, as for an Around hook's retry.
			t = db.fallBack(st, t)
			pr, err := t.Ex.Query(ctx, st.SQL, st.Args)
			if err != nil {
				return dbErr(st.Entity, err)
			}
			r = pr
			if counted != nil {
				*counted = countedRows{Rows: pr, Left: n, Capped: n > 0}
				r = counted
			}
			again, againRows := readRows(r, read)
			if dberr.Is(again, ErrIterAgain) {
				return dbErr(st.Entity, rowsErr) // the replica's error
			}
			rerr, rowsErr = again, againRows
		}
	}
	switch {
	case rerr != nil:
		err = dbErr(st.Entity, rerr)
	case counted != nil && counted.Over:
		err = fmt.Errorf("velox: %s: %w: more than %d; raise it with the query's MaxRows", st.Entity.Name, ErrTooManyRows, n)
	case rowsErr != nil:
		err = dbErr(st.Entity, rowsErr)
	}
	if counted != nil {
		st.Result.Rows = counted.N
		if !st.Op.Reads() && err == nil {
			// The rows a write returned are the rows it affected.
			st.Result.Affected = counted.N
		}
	}
	return err
}

// ExecWrite runs a statement that returns no rows through the pipeline: run
// when the DB or the statement's entity has hooks for it, else plain.
func ExecWrite(ctx context.Context, db *DB, st *Statement) (execResult, error) {
	var res execResult
	var err error
	if st.Op == OpUpdate {
		bumpOnce(st)
	}
	if db.Hooked() {
		err = db.Run(ctx, st, nil, &res)
	} else {
		err = db.Plain(ctx, st, nil, &res)
	}
	return res, err
}

// bumpOnce adds 1 to the Version column of st, an update, unless st already
// assigns it: writeCore did, or velox's own update of a bidirectional key,
// which no hook sees, did not.
func bumpOnce(st *Statement) {
	v, ok := st.Entity.VersionColumn()
	if !ok {
		return
	}
	for _, a := range st.Set {
		if a.Column == v {
			return
		}
	}
	st.Set = append(slices.Clip(st.Set), Assignment{Column: v, Op: AssignAdd, Value: int64(1)})
}
