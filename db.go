package velox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"unsafe"

	"github.com/syssam/velox2/internal/conn"
	"github.com/syssam/velox2/internal/engine"
)

// DB runs statements, inside or outside a transaction. It is the only type
// that does: generic methods cannot be in an interface, and one concrete type
// means every function taking a *DB works in a transaction unchanged.
//
// A DB from Open, OpenConn or OpenShards is safe for concurrent use by multiple
// goroutines. The *DB that Tx gives its fn is not: see Tx.
type DB struct {
	// e is the DB, without what is generic. It is the only field: a transaction
	// gives its fn the *DB of an *engine.DB, a pointer to the same memory.
	e engine.DB
}

// DB is the size of its engine.DB, which a *DB is cast from.
var _ [unsafe.Sizeof(DB{}) - unsafe.Sizeof(engine.DB{})]struct{} = [0]struct{}{}

// Open returns a DB that runs statements through db, a *sql.DB the
// application opened with a driver for dialect d. For PostgreSQL, veloxpgx.Open
// is faster.
//
// For MySQL, include parseTime=true and clientFoundRows=true in the DSN: the
// first scans DATETIME columns into time.Time, and clientFoundRows=true makes
// an UPDATE count every row it matched as affected, even one whose values did
// not change, which Save relies on. For SQLite, foreign keys are enforced
// only when the DSN asks for it (modernc.org/sqlite:
// _pragma=foreign_keys(1)). Open panics on a d that is not Postgres, MySQL or
// SQLite.
func Open(d Dialect, db *sql.DB, opts ...Option) *DB {
	if !engine.ValidDialect(d) {
		panic(fmt.Sprintf("velox: Open: unknown %v; use velox.Postgres, velox.MySQL or velox.SQLite", d))
	}
	return newDB(d, conn.NewSQL(db), opts)
}

// OpenConn returns a DB of dialect d whose primary is c. veloxpgx.Open is this
// with a veloxpgx.Conn. A zero Conn panics, as does a Conn that is valid only on
// another dialect.
func OpenConn(d Dialect, c Conn, opts ...Option) *DB {
	if !engine.ValidDialect(d) {
		panic(fmt.Sprintf("velox: OpenConn: unknown %v; use velox.Postgres, velox.MySQL or velox.SQLite", d))
	}
	switch {
	case engine.ConnExec(c) == nil:
		panic("velox: OpenConn: the Conn is zero; make it with SQLConn or a driver package's Conn")
	case engine.ConnOnly(c) != 0 && engine.ConnOnly(c) != d:
		panic(fmt.Sprintf("velox: OpenConn: the Conn is valid only on %s; the DB is %s", engine.ConnOnly(c), d))
	}
	return newDB(d, engine.ConnExec(c), opts)
}

// newDB returns an unsharded DB of dialect d on ex, its primary.
func newDB(d Dialect, ex engine.Executor, opts []Option) *DB {
	cfg := engine.NewConfig(opts)
	if cfg.ShardFor != nil {
		panic("velox: WithShardFor is for OpenShards; Open and OpenConn open a DB on one shard")
	}
	if len(cfg.Replicas) > 0 {
		return &DB{e: engine.DB{D: d, Rt: engine.NewRouter(d, ex, cfg.Replicas), Cfg: cfg}}
	}
	return &DB{e: engine.DB{D: d, Ex: ex, Cfg: cfg}}
}

// Dialect returns the DB's dialect.
func (db *DB) Dialect() Dialect { return db.e.D }

// Tx runs fn in a transaction. fn's error rolls it back and is returned; nil
// commits it; a panic in fn rolls it back and then panics again with the same
// value, and runtime.Goexit rolls it back and carries on. tx is a *DB like any
// other, so a function that takes a *DB works inside a transaction unchanged.
//
// fn's ctx is the context the transaction runs on: ctx, as the TxAround
// hooks passed it on, with the values they added. It carries the
// transaction's deadline, velox's or ctx's, whichever is earlier, unless a
// TxAround hook passed on a context without it; the deadline bounds every
// statement on tx either way.
//
// fn's ctx, and every context derived from it, is marked as the
// transaction's. Under the mark, a statement on any *DB other than tx and its
// savepoints fails before anything is sent, with ErrOutsideTx, as
// "velox: <Entity>: statement outside its transaction". That includes db
// itself, a DB from another Open, and another transaction's tx: such a
// statement was most likely meant for tx. A Tx on such a *DB fails the same
// way, as "velox: transaction: statement outside its transaction"; a nested
// transaction is tx.Tx.
//
// The mark refuses nothing else. A statement under a context without the
// mark runs as usual: one in a goroutine that fn starts with the ctx given to
// Tx, for example. So does a statement under WithoutTx(ctx), which removes
// the mark for work that must not join the transaction. Once the transaction
// has ended, committed or rolled back, its mark counts as none.
//
// Under a dry-run context (IsDryRun), such as the one SQL(ctx) gives a hook,
// Tx fails with ErrDryRun before BEGIN, nested or not: it is a write.
//
// WithDeadlines' Tx and MaxTx, and TxTimeout, bound the whole call. The
// transaction's deadline, velox's or ctx's, whichever is earlier, bounds every
// statement inside it, nested Tx included, whatever context the statement is
// given. A transaction whose ctx has ended (past either deadline, or canceled)
// is rolled back, never committed; its error matches ErrTimeout when velox's
// deadline passed, and context.DeadlineExceeded or context.Canceled otherwise.
//
// A rollback velox sends after fn fails, or once ctx has ended, is bounded to
// 5 seconds; database/sql's Tx.Rollback takes no context, so on a DB from Open
// that bound covers a savepoint's ROLLBACK TO only.
//
// WithTxAround's hooks wrap the whole call: every attempt and the commit.
//
// On a DB with replicas the transaction runs on the primary. On a DB opened
// with OpenShards it runs on the primary of the shard ctx names, as a
// statement's context names one; when ctx names none, Tx fails with
// ErrNoShard, as "velox: transaction: no shard for the statement: …", before
// anything is sent or any TxAround hook runs. Its statements run on that
// shard too, and one whose context names another fails.
//
// Inside a transaction, Tx makes a savepoint instead, so an error in a nested
// fn rolls back only that fn's work. When releasing the savepoint fails, the
// nested Tx returns that error and does not roll back to the savepoint.
// Whether the enclosing transaction can still commit is the database's call:
// PostgreSQL aborts it, so the outer COMMIT fails. A nested Tx (a savepoint)
// takes no options and returns an error when given any, and passes fn the ctx
// it is given, marked as the transaction's.
//
// tx is valid only inside fn: after Tx returns, statements on it fail (or, for
// a nested tx, run in the enclosing transaction). tx is not safe for
// concurrent use: run its statements from one goroutine at a time.
func (db *DB) Tx(ctx context.Context, fn func(ctx context.Context, tx *DB) error, opts ...TxOption) error {
	// The two function types differ only in a pointer to the same memory (see
	// DB). A closure over fn would escape, as the TxAround hooks keep it, and
	// cost every transaction an allocation.
	return db.e.Tx(ctx, *(*func(context.Context, *engine.DB) error)(unsafe.Pointer(&fn)), opts...)
}

// plain runs a statement on a DB with no hooks: the dry-run and transaction
// checks (dryWrite, then outsideTx, in run's order), resolving relations,
// guard, scope, rendering (the SQL comment included), routing and execution.
// Unlike run's, its st and read do not escape, so a caller's state and
// reading closure can stay on the stack.
func (db *DB) plain(ctx context.Context, st *Statement, read func(engine.Rows) error, res *engine.ExecResult) error {
	if err := db.e.DryWrite(ctx, st); err != nil {
		return err
	}
	if err := db.e.OutsideTx(ctx, st); err != nil {
		return err
	}
	if err := engine.ResolveStatement(st); err != nil {
		return err
	}
	if err := db.guard(st); err != nil {
		return err
	}
	if err := db.scope(ctx, st); err != nil {
		return err
	}
	db.capLimit(st)
	if db.e.Cfg.Comment != nil {
		db.e.Observe(ctx, st) // the comment reads the call site and tags
	} else {
		st.Dialect = db.e.D
	}
	e, args, err := db.e.Cfg.Cache.Get(db.e.D, st)
	if err != nil {
		return err
	}
	st.SQL, st.Fingerprint, st.Args = e.Text, e.Fp, args
	if db.e.Cfg.Comment != nil {
		st.SQL += db.e.Cfg.Comment.Text(st)
	}
	t, err := db.e.Route(ctx, st)
	if err != nil {
		return err
	}
	if !db.e.Timed() {
		return db.exec(ctx, st, t, read, res)
	}
	// tctx is ctx itself when ctx's deadline comes first: then it is velox's
	// only on a context velox bounded, such as a transaction's fn's ctx.
	tctx, cancel := db.e.Bound(ctx)
	if cancel != nil {
		defer cancel()
	}
	if err := db.exec(tctx, st, t, read, res); err != nil {
		if engine.TimedOut(tctx) {
			return errors.Join(engine.TimeoutError(st.Entity.Name), err)
		}
		return err
	}
	return nil
}

// run takes a statement through the pipeline: prepare's stages, the route
// stage, the parameter limit (CheckParams), then the around hooks wrapping
// execution, which is sending the statement and reading its rows. read reads
// them; it is called once per execution (an around hook that retries runs it
// again) and must not close them: run does, before it returns, on every path.
// Hooks may keep the statement, and next (which holds read), so both escape;
// a statement with no hooks of either kind uses plain.
func (db *DB) run(ctx context.Context, st *Statement, read func(engine.Rows) error, res *engine.ExecResult) error {
	if err := db.e.DryWrite(ctx, st); err != nil {
		return err
	}
	if err := db.prepare(ctx, st); err != nil {
		return err
	}
	t, err := db.e.Route(ctx, st)
	if err != nil {
		return err
	}
	// Before the around hooks, which never see a statement that cannot be
	// sent; exec checks again, for a hook that changes Args.
	if err := engine.CheckParams(db.e.D, st); err != nil {
		return err
	}
	if db.e.Timed() {
		// As in plain, tctx may be ctx, and its deadline velox's.
		tctx, cancel := db.e.Bound(ctx)
		if cancel != nil {
			defer cancel()
		}
		if err := db.execute(tctx, st, t, read, res); err != nil {
			if engine.TimedOut(tctx) {
				return errors.Join(engine.TimeoutError(st.Entity.Name), err)
			}
			return err
		}
		return nil
	}
	return db.execute(ctx, st, t, read, res)
}

// prepare takes a statement through every stage before execution, for run
// and for SQL(ctx): the transaction check (outsideTx), resolving the
// relations in its conditions (HasWhere leaves them to the statement), guard,
// observe, rewrite hooks and their checks, scope, the row cap, and rendering
// into st.SQL (comment included) and st.Args. Raw SQL takes only the
// transaction check and prepareRaw.
func (db *DB) prepare(ctx context.Context, st *Statement) error {
	if err := db.e.OutsideTx(ctx, st); err != nil {
		return err
	}
	if st.Op.Raw() {
		db.prepareRaw(ctx, st)
		return nil
	}
	if err := engine.ResolveStatement(st); err != nil {
		return err
	}
	if err := db.guard(st); err != nil {
		return err
	}
	db.e.Observe(ctx, st)
	// The query's own Columns: the hooks get a copy of them.
	cols := st.Columns
	was := st.Each
	if db.e.Hooked() {
		// Hooks may edit the statement in place; give them copies so that
		// the query the statement came from stays as it was. With no hooks
		// nothing is copied.
		st.Where = slices.Clone(st.Where)
		st.OrderBy = slices.Clone(st.OrderBy)
		st.Columns = slices.Clone(st.Columns)
		st.Set = slices.Clone(st.Set)
		st.Returning = slices.Clone(st.Returning)
		st.Tags = slices.Clone(st.Tags)
		if st.Rows != nil {
			rows := make([][]any, len(st.Rows))
			for i, r := range st.Rows {
				rows[i] = slices.Clone(r)
			}
			st.Rows = rows
		}
		if st.Conflict != nil {
			cf := *st.Conflict
			cf.Columns, cf.Update, cf.Where = slices.Clone(cf.Columns), slices.Clone(cf.Update), slices.Clone(cf.Where)
			st.Conflict = &cf
		}
		if st.Each != nil {
			ec := *st.Each
			ec.Keys = slices.Clone(ec.Keys)
			st.Each = &ec
		}
	}
	entity, op, writable, count, exists, agg, lock, wait, inTx := st.Entity, st.Op, st.Writable, st.Count, st.Exists, st.Aggregate, st.Lock, st.LockWait, st.InTx
	// velox's own marks, which no hook can set: a hook that replaces the
	// whole struct (*st = Statement{…}) zeroes them, and they are restored.
	bidi, pin := engine.StmtBidi(st), engine.StmtPin(st)
	// each is the Each the hooks get, a copy of velox's (was) when there are
	// hooks: a hook may not replace it or change it.
	each := st.Each
	returning := len(st.Returning) > 0
	for _, rw := range db.e.Cfg.Rewrites {
		if err := rw(ctx, st); err != nil {
			return engine.NewStageError(entity.Name, "rewrite", err)
		}
	}
	engine.SetStmtBidi(st, bidi)
	engine.SetStmtPin(st, pin)
	if st.Op != op {
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's kind", entity.Name)
	}
	if st.Writable != writable {
		// The scope reads it: without it, a Writable query drops the
		// Write policy.
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's Writable", entity.Name)
	}
	if st.InTx != inTx {
		// scopeInsert reads it: a hook that claimed a transaction would send
		// a policy-conditioned InsertAll without one.
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's InTx", entity.Name)
	}
	if st.Count != count {
		// The scan depends on it, and it renders before Exists and an
		// aggregate: a flip would read a count as a column, or the reverse.
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's Count", entity.Name)
	}
	if st.Exists != exists {
		// The scan depends on it: Exists reads no column.
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's Exists", entity.Name)
	}
	if st.Aggregate != agg {
		return engine.ErrAggregateChanged(entity.Name)
	}
	if (count || exists || agg != 0) && !slices.Equal(st.Columns, cols) {
		// The scan depends on them: one value, of the column's type.
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's Columns", entity.Name)
	}
	if st.Lock != lock || st.LockWait != wait {
		// The guard checked them.
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's lock", entity.Name)
	}
	if st.Each != each || (each != nil && !engine.SameEach(each, was)) {
		// The load reads the rows by it: each owner's, in its order.
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's Each", entity.Name)
	}
	switch {
	case st.Entity == nil:
		return fmt.Errorf("velox: %s: a Rewrite hook set the statement's Entity to nil", entity.Name)
	case st.Entity != entity && (engine.InfoKit(st.Entity) != engine.InfoKit(entity) || len(st.Entity.Columns) != len(entity.Columns)):
		// A copy (info := *st.Entity) keeps the scan plan; anything else
		// would scan rows into the wrong fields.
		return fmt.Errorf("velox: %s: a Rewrite hook replaced the Entity with %s, which is not a copy of it", entity.Name, st.Entity.Name)
	case st.Entity.Tenant != entity.Tenant || engine.InfoPolicy(st.Entity) != engine.InfoPolicy(entity):
		// The scope reads both from the statement's Entity: a copy without
		// them would unscope the statement.
		return fmt.Errorf("velox: %s: a Rewrite hook replaced the Entity with a copy whose tenant column or policy differs", entity.Name)
	}
	if len(db.e.Cfg.Rewrites) > 0 {
		if err := engine.CheckColumns(st); err != nil {
			return err
		}
		if err := engine.CheckBidiRewrite(st, bidi); err != nil {
			return err
		}
		if cf := st.Conflict; cf != nil && len(cf.Where) > 0 {
			switch {
			case db.e.D == MySQL:
				return fmt.Errorf("velox: %s: a Rewrite hook gave the upsert a condition; MySQL's ON DUPLICATE KEY UPDATE takes none", entity.Name)
			case cf.DoNothing || len(cf.Update) == 0:
				// Rendering would drop it: there is no update branch.
				return fmt.Errorf("velox: %s: a Rewrite hook gave the upsert a condition, but the upsert keeps the existing row and has no update branch to apply it to", entity.Name)
			}
		}
	}
	if err := db.scope(ctx, st); err != nil {
		return err
	}
	if returning && len(st.Returning) == 0 && engine.StmtUpsertPolicy(st) {
		return fmt.Errorf("velox: %s: a Rewrite hook dropped the RETURNING of an upsert whose update branch the policy conditions; velox reads the row back to find a row the policy hides", entity.Name)
	}
	db.capLimit(st)
	if st.Entity == entity {
		e, args, err := db.e.Cfg.Cache.Get(db.e.D, st)
		if err != nil {
			return err
		}
		st.SQL, st.Fingerprint, st.Args = e.Text, e.Fp, args
	} else {
		// A hook pointed the statement at its own EntityInfo, likely one
		// made for this statement alone; caching by it would fill the cache.
		text, args, err := engine.RenderStmt(db.e.D, st)
		if err != nil {
			return err
		}
		st.SQL, st.Args = text, args
		st.Fingerprint = engine.Fingerprint(st.SQL)
	}
	// After the fingerprint, which is of the text without the comment.
	if db.e.Cfg.Comment != nil {
		st.SQL += db.e.Cfg.Comment.Text(st)
	}
	return nil
}

// dryRun prepares and routes st, with DryRun set, and returns what run would
// send, or the error it would fail with before sending: the SQL(ctx) methods'
// work. It never executes st, so no Around hook runs.
func (db *DB) dryRun(ctx context.Context, st *Statement) (string, []any, error) {
	st.DryRun = true
	if err := db.prepare(ctx, st); err != nil {
		return "", nil, err
	}
	if _, err := db.e.Route(ctx, st); err != nil {
		return "", nil, err
	}
	if err := engine.CheckParams(db.e.D, st); err != nil {
		return "", nil, err
	}
	return st.SQL, st.Args, nil
}

// execute runs a rendered, routed statement through the around hooks; t is
// where the route stage sent it.
func (db *DB) execute(ctx context.Context, st *Statement, t engine.Target, read func(engine.Rows) error, res *engine.ExecResult) error {
	if len(db.e.Cfg.Arounds) == 0 {
		return db.exec(ctx, st, t, read, res)
	}
	// Reset here as well as in exec: a hook that returns before calling
	// next must leave the outer hooks no stale Result.
	st.Result = Result{Affected: -1}
	called := false
	var last error // what the innermost next last returned
	next := func(ctx context.Context) error {
		called = true
		last = db.exec(ctx, st, t, read, res)
		return last
	}
	for i := len(db.e.Cfg.Arounds) - 1; i >= 0; i-- {
		a, inner := db.e.Cfg.Arounds[i], next
		next = func(ctx context.Context) error { return a(ctx, st, inner) }
	}
	if err := next(ctx); err != nil {
		return err
	}
	if !called {
		return fmt.Errorf("velox: %s: an Around hook returned without calling next", st.Entity.Name)
	}
	if last != nil {
		return fmt.Errorf("velox: %s: an Around hook discarded the error from next", st.Entity.Name)
	}
	return nil
}

// rowCap is the most rows st may return: its own MaxRows, else, for a
// per-owner read bounded by construction, that bound (EachBound), else the
// DB's; 0 for no cap. Raw's read is capped too, Exec not; so is a per-owner
// count, by its owners, but not Count.
func (db *DB) rowCap(st *Statement) int {
	if !st.Op.Reads() || (st.Count && st.Each == nil) || st.Exists || st.Aggregate != 0 {
		return 0
	}
	if st.MaxRows > 0 {
		return st.MaxRows
	}
	if n, ok := engine.EachBound(st); ok {
		return n
	}
	return db.e.Cfg.MaxRows
}

// capLimit lowers st's Limit to its row cap plus one, so that the database
// stops at the row that trips the cap instead of sending every row; exec's
// CountedRows still does the counting. It runs just before rendering: the
// guard and the Rewrite hooks have seen the caller's Limit. A per-owner read
// whose cap is its bound by construction needs no LIMIT, and gets none.
func (db *DB) capLimit(st *Statement) {
	n := db.rowCap(st)
	if st.Each != nil && st.MaxRows == 0 {
		if _, ok := engine.EachBound(st); ok {
			return
		}
	}
	if n > 0 && n < math.MaxInt && (st.Limit == 0 || st.Limit > n) {
		st.Limit = n + 1
	}
}

// exec sends a rendered statement to t, the connection the route stage
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
func (db *DB) exec(ctx context.Context, st *Statement, t engine.Target, read func(engine.Rows) error, res *engine.ExecResult) error {
	st.Result = Result{Affected: -1}
	if err := engine.CheckParams(db.e.D, st); err != nil {
		return err
	}
	if t.Replica >= 0 && st.Route.Fallback {
		// An Around hook retrying after a fallback: the statement runs where
		// its Route says, the primary.
		t = engine.Target{Ex: t.Sh.Primary, Sh: t.Sh, Replica: -1}
	}
	engine.SetStmtRan(st, t)
	if read == nil {
		r, err := t.Ex.Exec(ctx, st.SQL, st.Args)
		if !st.Op.Reads() {
			// It reached the database: whether it failed or not, its
			// outcome may be written.
			db.e.Wrote(ctx)
		}
		if err != nil {
			return engine.DBErr(st.Entity, err)
		}
		st.Result.Affected = r.Affected
		if res != nil {
			*res = r
		}
		return nil
	}
	r, err := t.Ex.Query(ctx, st.SQL, st.Args)
	if !st.Op.Reads() {
		// As for exec: a write that reached the database pins, whatever
		// came back. A write is not failed over, so it is sent only here.
		db.e.Wrote(ctx)
	}
	if err != nil {
		if !db.e.Broken(ctx, t, err) {
			return engine.DBErr(st.Entity, err)
		}
		t = db.e.FallBack(st, t)
		if r, err = t.Ex.Query(ctx, st.SQL, st.Args); err != nil {
			return engine.DBErr(st.Entity, err)
		}
	}
	// Count the rows only when something reads the count: an Around hook
	// reads Result.Rows, and the cap needs it. Wrapping them allocates.
	n := db.rowCap(st)
	var counted *engine.CountedRows
	if n > 0 || len(db.e.Cfg.Arounds) > 0 {
		counted = &engine.CountedRows{Rows: r, Left: n, Capped: n > 0}
		r = counted
	}
	rerr, rowsErr := engine.ReadRows(r, read)
	// The rows error counts for failover only when read returned nil: a
	// broken connection ends the rows with Next false, so read finishes
	// cleanly, while pgx also reports read's own scan error through
	// rows.Err(), where a Scanner's io.ErrUnexpectedEOF would look like one.
	if rerr == nil && rowsErr != nil && db.e.Broken(ctx, t, rowsErr) {
		if engine.StmtHanded(st) {
			// Iter has handed the caller a row, which cannot be taken
			// back: the error is returned, and only the replica pays.
			t.Sh.Replicas[t.Replica].Down(db.e.Cfg.Backoff)
		} else {
			// The replica's connection broke before any row reached the
			// caller: read on the primary, from the start. read starts
			// anew, as for an Around hook's retry.
			t = db.e.FallBack(st, t)
			pr, err := t.Ex.Query(ctx, st.SQL, st.Args)
			if err != nil {
				return engine.DBErr(st.Entity, err)
			}
			r = pr
			if counted != nil {
				*counted = engine.CountedRows{Rows: pr, Left: n, Capped: n > 0}
				r = counted
			}
			again, againRows := engine.ReadRows(r, read)
			if engine.Is(again, engine.ErrIterAgain) {
				return engine.DBErr(st.Entity, rowsErr) // the replica's error
			}
			rerr, rowsErr = again, againRows
		}
	}
	switch {
	case rerr != nil:
		err = engine.DBErr(st.Entity, rerr)
	case counted != nil && counted.Over:
		err = fmt.Errorf("velox: %s: %w: more than %d; raise it with the query's MaxRows", st.Entity.Name, ErrTooManyRows, n)
	case rowsErr != nil:
		err = engine.DBErr(st.Entity, rowsErr)
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

// execWrite runs a statement that returns no rows through the pipeline: run
// when the DB or the statement's entity has hooks for it, else plain.
func execWrite(ctx context.Context, db *DB, st *Statement) (engine.ExecResult, error) {
	var res engine.ExecResult
	var err error
	if db.e.Hooked() {
		err = db.run(ctx, st, nil, &res)
	} else {
		err = db.plain(ctx, st, nil, &res)
	}
	return res, err
}
