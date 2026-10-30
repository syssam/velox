package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync/atomic"
	"time"
)

// DB is the root package's DB without what is generic: every statement's path
// through velox. The root's DB holds one by value, and adds the generic API.
type DB struct {
	D Dialect
	// Ex runs the statements of a DB without replicas, and of a transaction;
	// nil on a DB with replicas outside a transaction, whose Rt chooses.
	Ex   Executor
	Rt   *Router // WithReplicas's; nil without replicas
	Cfg  *Config
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
	txShard *ShardConn
	// txRoot is, in a transaction, its frame: the identity the mark on its
	// fn's ctx carries, which its savepoints share. Nil elsewhere.
	txRoot *txFrame
}

// Hooked reports whether the pipeline has hooks: whether a statement must go
// through run, or may take the shortcut of plain.
func (db *DB) Hooked() bool { return len(db.Cfg.Rewrites) > 0 || len(db.Cfg.Arounds) > 0 }

// outsideTx fails a statement, st, under a context marked by a transaction
// that has not ended and that db is not in (foreign), with ErrOutsideTx: one
// context lookup, and a pointer compare. In a transaction a savepoint broke
// (txFrame.broken), it fails st with that error.
func (db *DB) outsideTx(ctx context.Context, st *Statement) error {
	if db.Foreign(ctx) {
		return StmtErr(st, ErrOutsideTx)
	}
	if db.txRoot != nil && db.txRoot.broken != nil {
		return StmtErr(st, db.txRoot.broken)
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
		return StmtErr(st, ErrDryRun)
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
func (db *DB) Plain(ctx context.Context, st *Statement, read func(Rows) error, res *ExecResult) error {
	if err := db.dryWrite(ctx, st); err != nil {
		return err
	}
	if err := db.outsideTx(ctx, st); err != nil {
		return err
	}
	if err := ResolveStatement(st); err != nil {
		return err
	}
	if err := db.Guard(st); err != nil {
		return err
	}
	if err := db.scope(ctx, st); err != nil {
		return err
	}
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
		return db.exec(ctx, st, t, read, res)
	}
	// tctx is ctx itself when ctx's deadline comes first: then it is velox's
	// only on a context velox bounded, such as a transaction's fn's ctx.
	tctx, cancel := db.bound(ctx)
	if cancel != nil {
		defer cancel()
	}
	if err := db.exec(tctx, st, t, read, res); err != nil {
		if TimedOut(tctx) {
			return errors.Join(TimeoutError(st.Entity.Name), err)
		}
		return err
	}
	return nil
}

// Run takes a statement through the pipeline: prepare's stages, the route
// stage, the parameter limit (CheckParams), then the around hooks wrapping
// execution, which is sending the statement and reading its rows. read reads
// them; it is called once per execution (an around hook that retries runs it
// again) and must not close them: run does, before it returns, on every path.
// Hooks may keep the statement, and next (which holds read), so both escape;
// a statement with no hooks of either kind uses plain.
func (db *DB) Run(ctx context.Context, st *Statement, read func(Rows) error, res *ExecResult) error {
	if err := db.dryWrite(ctx, st); err != nil {
		return err
	}
	if err := db.Prepare(ctx, st); err != nil {
		return err
	}
	t, err := db.route(ctx, st)
	if err != nil {
		return err
	}
	// Before the around hooks, which never see a statement that cannot be
	// sent; exec checks again, for a hook that changes Args.
	if err := CheckParams(db.D, st); err != nil {
		return err
	}
	if db.timed() {
		// As in plain, tctx may be ctx, and its deadline velox's.
		tctx, cancel := db.bound(ctx)
		if cancel != nil {
			defer cancel()
		}
		if err := db.execute(tctx, st, t, read, res); err != nil {
			if TimedOut(tctx) {
				return errors.Join(TimeoutError(st.Entity.Name), err)
			}
			return err
		}
		return nil
	}
	return db.execute(ctx, st, t, read, res)
}

// Prepare takes a statement through every stage before execution, for run
// and for SQL(ctx): the transaction check (outsideTx), resolving the
// relations in its conditions (HasWhere leaves them to the statement), guard,
// observe, rewrite hooks and their checks, scope, the row cap, and rendering
// into st.SQL (comment included) and st.Args. Raw SQL takes only the
// transaction check and prepareRaw.
func (db *DB) Prepare(ctx context.Context, st *Statement) error {
	if err := db.outsideTx(ctx, st); err != nil {
		return err
	}
	if st.Op.Raw() {
		db.prepareRaw(ctx, st)
		return nil
	}
	if err := ResolveStatement(st); err != nil {
		return err
	}
	if err := db.Guard(st); err != nil {
		return err
	}
	db.observe(ctx, st)
	// The query's own Columns: the hooks get a copy of them.
	cols := st.Columns
	was := st.Each
	if db.Hooked() {
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
	bidi, pin := StmtBidi(st), StmtPin(st)
	// each is the Each the hooks get, a copy of velox's (was) when there are
	// hooks: a hook may not replace it or change it.
	each := st.Each
	returning := len(st.Returning) > 0
	for _, rw := range db.Cfg.Rewrites {
		if err := rw(ctx, st); err != nil {
			return NewStageError(entity.Name, "rewrite", err)
		}
	}
	SetStmtBidi(st, bidi)
	SetStmtPin(st, pin)
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
		return ErrAggregateChanged(entity.Name)
	}
	if (count || exists || agg != 0) && !slices.Equal(st.Columns, cols) {
		// The scan depends on them: one value, of the column's type.
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's Columns", entity.Name)
	}
	if st.Lock != lock || st.LockWait != wait {
		// The guard checked them.
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's lock", entity.Name)
	}
	if st.Each != each || (each != nil && !SameEach(each, was)) {
		// The load reads the rows by it: each owner's, in its order.
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's Each", entity.Name)
	}
	switch {
	case st.Entity == nil:
		return fmt.Errorf("velox: %s: a Rewrite hook set the statement's Entity to nil", entity.Name)
	case st.Entity != entity && (InfoKit(st.Entity) != InfoKit(entity) || len(st.Entity.Columns) != len(entity.Columns)):
		// A copy (info := *st.Entity) keeps the scan plan; anything else
		// would scan rows into the wrong fields.
		return fmt.Errorf("velox: %s: a Rewrite hook replaced the Entity with %s, which is not a copy of it", entity.Name, st.Entity.Name)
	case st.Entity.Tenant != entity.Tenant || InfoPolicy(st.Entity) != InfoPolicy(entity):
		// The scope reads both from the statement's Entity: a copy without
		// them would unscope the statement.
		return fmt.Errorf("velox: %s: a Rewrite hook replaced the Entity with a copy whose tenant column or policy differs", entity.Name)
	}
	if len(db.Cfg.Rewrites) > 0 {
		if err := CheckColumns(st); err != nil {
			return err
		}
		if err := CheckBidiRewrite(st, bidi); err != nil {
			return err
		}
		if cf := st.Conflict; cf != nil && len(cf.Where) > 0 {
			switch {
			case db.D == MySQL:
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
	if returning && len(st.Returning) == 0 && StmtUpsertPolicy(st) {
		return fmt.Errorf("velox: %s: a Rewrite hook dropped the RETURNING of an upsert whose update branch the policy conditions; velox reads the row back to find a row the policy hides", entity.Name)
	}
	db.capLimit(st)
	if st.Entity == entity {
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
	if err := db.Prepare(ctx, st); err != nil {
		return "", nil, err
	}
	if _, err := db.route(ctx, st); err != nil {
		return "", nil, err
	}
	if err := CheckParams(db.D, st); err != nil {
		return "", nil, err
	}
	return st.SQL, st.Args, nil
}

// execute runs a rendered, routed statement through the around hooks; t is
// where the route stage sent it.
func (db *DB) execute(ctx context.Context, st *Statement, t Target, read func(Rows) error, res *ExecResult) error {
	if len(db.Cfg.Arounds) == 0 {
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
	for i := len(db.Cfg.Arounds) - 1; i >= 0; i-- {
		a, inner := db.Cfg.Arounds[i], next
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
	if n, ok := EachBound(st); ok {
		return n
	}
	return db.Cfg.MaxRows
}

// capLimit lowers st's Limit to its row cap plus one, so that the database
// stops at the row that trips the cap instead of sending every row; exec's
// CountedRows still does the counting. It runs just before rendering: the
// guard and the Rewrite hooks have seen the caller's Limit. A per-owner read
// whose cap is its bound by construction needs no LIMIT, and gets none.
func (db *DB) capLimit(st *Statement) {
	n := db.rowCap(st)
	if st.Each != nil && st.MaxRows == 0 {
		if _, ok := EachBound(st); ok {
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
func (db *DB) exec(ctx context.Context, st *Statement, t Target, read func(Rows) error, res *ExecResult) error {
	st.Result = Result{Affected: -1}
	if err := CheckParams(db.D, st); err != nil {
		return err
	}
	if t.Replica >= 0 && st.Route.Fallback {
		// An Around hook retrying after a fallback: the statement runs where
		// its Route says, the primary.
		t = Target{Ex: t.Sh.Primary, Sh: t.Sh, Replica: -1}
	}
	SetStmtRan(st, t)
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
	if !st.Op.Reads() {
		// As for exec: a write that reached the database pins, whatever
		// came back. A write is not failed over, so it is sent only here.
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
	var counted *CountedRows
	if n > 0 || len(db.Cfg.Arounds) > 0 {
		counted = &CountedRows{Rows: r, Left: n, Capped: n > 0}
		r = counted
	}
	rerr, rowsErr := ReadRows(r, read)
	// The rows error counts for failover only when read returned nil: a
	// broken connection ends the rows with Next false, so read finishes
	// cleanly, while pgx also reports read's own scan error through
	// rows.Err(), where a Scanner's io.ErrUnexpectedEOF would look like one.
	if rerr == nil && rowsErr != nil && db.broken(ctx, t, rowsErr) {
		if StmtHanded(st) {
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
				*counted = CountedRows{Rows: pr, Left: n, Capped: n > 0}
				r = counted
			}
			again, againRows := ReadRows(r, read)
			if Is(again, ErrIterAgain) {
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
func ExecWrite(ctx context.Context, db *DB, st *Statement) (ExecResult, error) {
	var res ExecResult
	var err error
	if db.Hooked() {
		err = db.Run(ctx, st, nil, &res)
	} else {
		err = db.Plain(ctx, st, nil, &res)
	}
	return res, err
}
