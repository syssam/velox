package velox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DB runs statements, inside or outside a transaction. It is the only type
// that does: generic methods cannot be in an interface, and one concrete type
// means every function taking a *DB works in a transaction unchanged.
type DB struct {
	d     Dialect
	ex    executor
	cfg   *config
	inTx  bool
	depth int // savepoint nesting inside a transaction
	// txDeadline is, in a transaction, its deadline: velox's or the
	// caller's, whichever is earlier; zero when none. txCause is ErrTimeout
	// when the deadline is velox's, nil when it is the caller's.
	txDeadline time.Time
	txCause    error
	tx         *TxInfo // the transaction's, when hooks are configured
}

type config struct {
	rewrites  []Rewrite
	arounds   []Around
	cache     renderCache
	maxLimit  int        // WithMaxLimit; 0: none
	maxRows   int        // WithMaxRows; 0: none
	deadlines Deadlines  // WithDeadlines
	callers   *callers   // WithCallers; nil: off
	comment   *commenter // WithSQLComment; nil: off
	txArounds []TxAround
}

// Option configures a DB's pipeline.
type Option func(*config)

// WithRewrite adds a rewrite hook. Rewrite hooks run in the order given. A
// nil hook panics.
func WithRewrite(r Rewrite) Option {
	if r == nil {
		panic("velox: WithRewrite(nil)")
	}
	return func(c *config) { c.rewrites = append(c.rewrites, r) }
}

// WithMaxLimit refuses, with ErrLimitTooLarge, a query whose Limit is over n.
// n must be at least 2, the limit One uses; a smaller n panics.
func WithMaxLimit(n int) Option {
	if n < 2 {
		panic(fmt.Sprintf("velox: WithMaxLimit(%d): the maximum must be at least 2, the limit One uses", n))
	}
	return func(c *config) { c.maxLimit = n }
}

// WithMaxRows fails, with ErrTooManyRows, a query that returns more than n
// rows: a relation load and Iter included, Count and an insert's RETURNING
// not. velox sends such a query with LIMIT n+1 (or the query's own Limit,
// when that is lower), so the database stops early: the extra row is the one
// that trips the cap. The cap counts per statement, so a With load that is
// split into chunks to stay under the parameter limit is counted chunk by
// chunk. A query's MaxRows overrides it, for the query and its With loads.
// n <= 0 sets no cap; n must otherwise be at least 2, the rows One reads, and
// n = 1 panics.
func WithMaxRows(n int) Option {
	if n == 1 {
		panic("velox: WithMaxRows(1): the cap must be at least 2, the rows One reads")
	}
	return func(c *config) { c.maxRows = max(n, 0) }
}

// WithAround adds an around hook. The first one given is the outermost. A nil
// hook panics.
func WithAround(a Around) Option {
	if a == nil {
		panic("velox: WithAround(nil)")
	}
	return func(c *config) { c.arounds = append(c.arounds, a) }
}

// Open returns a DB that runs statements through db, a *sql.DB the
// application opened with a driver for dialect d. For PostgreSQL, OpenPgx is
// faster.
//
// For MySQL, include parseTime=true and clientFoundRows=true in the DSN: the
// first scans DATETIME columns into time.Time, and clientFoundRows=true makes
// an UPDATE count every row it matched as affected, even one whose values did
// not change, which Save relies on. For SQLite, foreign keys are enforced
// only when the DSN asks for it (modernc.org/sqlite:
// _pragma=foreign_keys(1)).
func Open(d Dialect, db *sql.DB, opts ...Option) *DB {
	return newDB(d, sqlExecutor{c: db, db: db}, opts)
}

// OpenPgx returns a DB for PostgreSQL that runs statements through pool with
// pgx's native protocol: about 0.4 times the time and allocations of
// database/sql for a 1,000-row result (design, Appendix A.5).
func OpenPgx(pool *pgxpool.Pool, opts ...Option) *DB {
	return newDB(Postgres, pgxExecutor{c: pool, pool: pool}, opts)
}

func newDB(d Dialect, ex executor, opts []Option) *DB {
	cfg := &config{}
	for _, o := range opts {
		o(cfg)
	}
	return &DB{d: d, ex: ex, cfg: cfg}
}

// Dialect returns the DB's dialect.
func (db *DB) Dialect() Dialect { return db.d }

// dbErr names the entity in an error from the database or the driver, and
// turns a violated constraint into a *ConstraintError.
func dbErr(info *EntityInfo, err error) error {
	if errors.As(err, new(*ConstraintError)) {
		return err // already classified
	}
	if ce := constraint(info.Name, err); ce != nil {
		return ce
	}
	return fmt.Errorf("velox: %s: %w", info.Name, err)
}

// hooked reports whether the pipeline has hooks: whether a statement must go
// through run, or may take the shortcut of plain.
func (db *DB) hooked() bool { return len(db.cfg.rewrites) > 0 || len(db.cfg.arounds) > 0 }

// plain runs a statement on a DB with no hooks: guard, scope, rendering (the
// SQL comment included) and execution.
// Unlike run's, its st and read do not escape, so a caller's state and
// reading closure can stay on the stack.
func (db *DB) plain(ctx context.Context, st *Statement, read func(rows) error, res *result) error {
	if err := db.guard(st); err != nil {
		return err
	}
	if err := db.scope(ctx, st); err != nil {
		return err
	}
	db.capLimit(st)
	if db.cfg.comment != nil {
		db.observe(ctx, st) // the comment reads the call site and tags
	} else {
		st.Dialect = db.d
	}
	e, args := db.cfg.cache.get(db.d, st)
	st.SQL, st.Fingerprint, st.Args = e.text, e.fp, args
	if db.cfg.comment != nil {
		st.SQL += db.cfg.comment.text(st)
	}
	if !db.timed() {
		return db.exec(ctx, st, read, res)
	}
	tctx, cancel := db.bound(ctx)
	if cancel == nil {
		return db.exec(ctx, st, read, res)
	}
	defer cancel()
	if err := db.exec(tctx, st, read, res); err != nil {
		if timedOut(tctx) {
			return errTimeout(st.Entity.Name)
		}
		return err
	}
	return nil
}

// run takes a statement through the pipeline: prepare's stages, then the
// around hooks wrapping execution, which is sending the statement and
// reading its rows. read reads them; it is called once per execution (an
// around hook that retries runs it again) and must not close them: run does,
// before it returns, on every path. Hooks may keep the statement, and next
// (which holds read), so both escape; a statement with no hooks of either
// kind uses plain.
func (db *DB) run(ctx context.Context, st *Statement, read func(rows) error, res *result) error {
	if err := db.prepare(ctx, st); err != nil {
		return err
	}
	if db.timed() {
		if tctx, cancel := db.bound(ctx); cancel != nil {
			defer cancel()
			if err := db.execute(tctx, st, read, res); err != nil {
				if timedOut(tctx) {
					return errTimeout(st.Entity.Name)
				}
				return err
			}
			return nil
		}
	}
	return db.execute(ctx, st, read, res)
}

// prepare takes a statement through every stage before execution, for run
// and for SQL(ctx): guard, observe, the entity's BeforeUpdate or BeforeDelete
// hooks, rewrite hooks and their checks, scope, the row cap, and rendering
// into st.SQL (comment included) and st.Args.
func (db *DB) prepare(ctx context.Context, st *Statement) error {
	if err := db.guard(st); err != nil {
		return err
	}
	db.observe(ctx, st)
	if err := db.entityHooks(ctx, st); err != nil {
		return err
	}
	if db.hooked() {
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
	}
	entity, op := st.Entity, st.Op
	for _, rw := range db.cfg.rewrites {
		if err := rw(ctx, st); err != nil {
			return fmt.Errorf("velox: %s: rewrite: %w", entity.Name, err)
		}
	}
	if st.Op != op {
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's kind", entity.Name)
	}
	switch {
	case st.Entity == nil:
		return fmt.Errorf("velox: %s: a Rewrite hook set the statement's Entity to nil", entity.Name)
	case st.Entity != entity && (st.Entity.kit != entity.kit || len(st.Entity.Columns) != len(entity.Columns)):
		// A copy (info := *st.Entity) keeps the scan plan; anything else
		// would scan rows into the wrong fields.
		return fmt.Errorf("velox: %s: a Rewrite hook replaced the Entity with %s, which is not a copy of it", entity.Name, st.Entity.Name)
	case st.Entity.Tenant != entity.Tenant || st.Entity.policy != entity.policy:
		// The scope reads both from the statement's Entity: a copy without
		// them would unscope the statement.
		return fmt.Errorf("velox: %s: a Rewrite hook replaced the Entity with a copy whose tenant column or policy differs", entity.Name)
	}
	if len(db.cfg.rewrites) > 0 {
		if err := checkColumns(st); err != nil {
			return err
		}
		if cf := st.Conflict; cf != nil && len(cf.Where) > 0 {
			switch {
			case db.d == MySQL:
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
	db.capLimit(st)
	if st.Entity == entity {
		e, args := db.cfg.cache.get(db.d, st)
		st.SQL, st.Fingerprint, st.Args = e.text, e.fp, args
	} else {
		// A hook pointed the statement at its own EntityInfo, likely one
		// made for this statement alone; caching by it would fill the cache.
		st.SQL, st.Args = render(db.d, st)
		st.Fingerprint = fingerprint(st.SQL)
	}
	// After the fingerprint, which is of the text without the comment.
	if db.cfg.comment != nil {
		st.SQL += db.cfg.comment.text(st)
	}
	return nil
}

// observe sets what st's hooks and adapters read about it: its Dialect, its
// Tags, its transaction's TxInfo and, with WithCallers, its Caller.
func (db *DB) observe(ctx context.Context, st *Statement) {
	st.Dialect = db.d
	st.Tx = db.tx
	st.Tags = tagsOf(ctx)
	if c := db.cfg.callers; c != nil {
		st.Caller = c.capture()
	}
}

// dryRun prepares st, with DryRun set, and returns what run would send, or
// the error it would fail with before sending: the SQL(ctx) methods' work. It
// never executes st, so no Around hook runs.
func (db *DB) dryRun(ctx context.Context, st *Statement) (string, []any, error) {
	st.DryRun = true
	if err := db.prepare(ctx, st); err != nil {
		return "", nil, err
	}
	if err := checkParams(db.d, st); err != nil {
		return "", nil, err
	}
	return st.SQL, st.Args, nil
}

// mutationHooks returns the BeforeUpdate or BeforeDelete hooks of st's
// entity, and the stage that names their errors; none for other statements.
func mutationHooks(st *Statement) ([]func(context.Context, *Statement) error, string) {
	switch st.Op {
	case OpUpdate:
		return st.Entity.beforeUpdate, "before update"
	case OpDelete:
		return st.Entity.beforeDelete, "before delete"
	}
	return nil, ""
}

// entityHooks runs st's BeforeUpdate or BeforeDelete hooks, then the guard
// again: a hook may have added a condition. The hooks change st's slices
// only by copying or appending past their length, so the query st came from
// stays as it was.
func (db *DB) entityHooks(ctx context.Context, st *Statement) error {
	hooks, stage := mutationHooks(st)
	if len(hooks) == 0 {
		return nil
	}
	for _, h := range hooks {
		if err := h(ctx, st); err != nil {
			return &hookError{st.Entity.Name, stage, err}
		}
	}
	return db.guard(st)
}

// execute runs a rendered statement through the around hooks.
func (db *DB) execute(ctx context.Context, st *Statement, read func(rows) error, res *result) error {
	if len(db.cfg.arounds) == 0 {
		return db.exec(ctx, st, read, res)
	}
	called := false
	var last error // what the innermost next last returned
	next := func(ctx context.Context) error {
		called = true
		last = db.exec(ctx, st, read, res)
		return last
	}
	for i := len(db.cfg.arounds) - 1; i >= 0; i-- {
		a, inner := db.cfg.arounds[i], next
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

// checkColumns reports a column index in st, set by a rewrite hook, that
// st's entity does not have; rendering would panic on it. The generic API
// sets only valid ones.
func checkColumns(st *Statement) error {
	n := len(st.Entity.Columns)
	bad := func(c int) error {
		if c < 0 || c >= n {
			return fmt.Errorf("velox: %s: a Rewrite hook set column %d, the entity has %d columns", st.Entity.Name, c, n)
		}
		return nil
	}
	for _, c := range st.Columns {
		if err := bad(c); err != nil {
			return err
		}
	}
	for _, o := range st.OrderBy {
		if err := bad(o.Column); err != nil {
			return err
		}
	}
	for _, a := range st.Set {
		if err := bad(a.Column); err != nil {
			return err
		}
	}
	for _, c := range st.Returning {
		if err := bad(c); err != nil {
			return err
		}
	}
	if cf := st.Conflict; cf != nil {
		if len(cf.Columns) == 0 {
			return fmt.Errorf("velox: %s: a Rewrite hook left an upsert with no conflict columns", st.Entity.Name)
		}
		for _, c := range append(slices.Clip(cf.Columns), cf.Update...) {
			if err := bad(c); err != nil {
				return err
			}
		}
	}
	for _, r := range st.Rows {
		if len(r) != len(st.Columns) {
			return fmt.Errorf("velox: %s: a Rewrite hook made a row of %d values for %d columns", st.Entity.Name, len(r), len(st.Columns))
		}
	}
	var walk func(nd node) error
	walk = func(nd node) error {
		switch nd.op {
		case opAnd, opOr, opNot:
			for _, k := range nd.kids {
				if err := walk(k); err != nil {
					return err
				}
			}
			return nil
		case opExists:
			// Only the owner's column is the statement's entity's; the kids
			// are conditions on the target, made by HasWhere.
			return bad(nd.v.(link).ownerCol)
		case opInvalid:
			return nd.v.(error)
		}
		return bad(int(nd.col))
	}
	conds := func(ps []Predicate) error {
		for _, p := range ps {
			// walk does not enter an EXISTS's kids, so look for the zero
			// Predicate first.
			if hasZero(p.n) {
				return errZeroPredicate(st.Entity.Name)
			}
			if err := walk(p.n); err != nil {
				return err
			}
		}
		return nil
	}
	if err := conds(st.Where); err != nil {
		return err
	}
	if st.Conflict != nil {
		return conds(st.Conflict.Where)
	}
	return nil
}

// rowCap is the most rows st may return: its own MaxRows, else the DB's; 0
// for no cap.
func (db *DB) rowCap(st *Statement) int {
	if st.Op != OpSelect || st.Count {
		return 0
	}
	if st.MaxRows > 0 {
		return st.MaxRows
	}
	return db.cfg.maxRows
}

// capLimit lowers st's Limit to its row cap plus one, so that the database
// stops at the row that trips the cap instead of sending every row; exec's
// countedRows still does the counting. It runs just before rendering: the
// guard and the Rewrite hooks have seen the caller's Limit.
func (db *DB) capLimit(st *Statement) {
	if n := db.rowCap(st); n > 0 && n < math.MaxInt && (st.Limit == 0 || st.Limit > n) {
		st.Limit = n + 1
	}
}

// countedRows counts the rows read and, when capped, ends them after
// left of them and records that there were more.
type countedRows struct {
	rows
	n      int64
	left   int
	capped bool
	over   bool
}

func (c *countedRows) Next() bool {
	if !c.rows.Next() {
		return false
	}
	if c.capped {
		if c.left == 0 {
			c.over = true // the row that trips the cap is not counted
			return false
		}
		c.left--
	}
	c.n++
	return true
}

// exec sends a rendered statement, reads its rows with read and closes them;
// with a nil read it sends a statement that returns no rows and fills *res.
// Either way it sets st.Result. Errors from the database, the driver or read
// name the entity.
func (db *DB) exec(ctx context.Context, st *Statement, read func(rows) error, res *result) (err error) {
	st.Result = Result{Affected: -1}
	if err := checkParams(db.d, st); err != nil {
		return err
	}
	if read == nil {
		r, err := db.ex.exec(ctx, st.SQL, st.Args)
		if err != nil {
			return dbErr(st.Entity, err)
		}
		st.Result.Affected = r.affected
		if res != nil {
			*res = r
		}
		return nil
	}
	r, err := db.ex.query(ctx, st.SQL, st.Args)
	if err != nil {
		return dbErr(st.Entity, err)
	}
	// Count the rows only when something reads the count: an Around hook
	// reads Result.Rows, and the cap needs it. Wrapping them allocates.
	var counted *countedRows
	if n := db.rowCap(st); n > 0 || len(db.cfg.arounds) > 0 {
		counted = &countedRows{rows: r, left: n, capped: n > 0}
		r = counted
	}
	defer func() {
		// Deferred, so that the rows are closed even when the loop body of
		// an Iter, which runs inside read, panics. pgx reports some errors
		// only once the rows are closed.
		r.Close()
		if rerr := r.Err(); rerr != nil && err == nil {
			err = dbErr(st.Entity, rerr)
		}
		if counted != nil {
			st.Result.Rows = counted.n
			if st.Op != OpSelect && err == nil {
				// The rows a write returned are the rows it affected.
				st.Result.Affected = counted.n
			}
		}
	}()
	if err := read(r); err != nil {
		return dbErr(st.Entity, err)
	}
	if counted != nil && counted.over {
		return fmt.Errorf("velox: %s: %w: more than %d; raise it with the query's MaxRows", st.Entity.Name, ErrTooManyRows, db.rowCap(st))
	}
	return nil
}

// checkParams refuses a rendered statement with more parameters than
// dialect d allows.
func checkParams(d Dialect, st *Statement) error {
	if lim := maxParams[d]; len(st.Args) > lim {
		return fmt.Errorf("velox: %s: the statement has %d parameters; %s allows %d", st.Entity.Name, len(st.Args), d, lim)
	}
	return nil
}

// execWrite runs a statement that returns no rows through the pipeline: run
// when the DB or the statement's entity has hooks for it, else plain.
func execWrite(ctx context.Context, db *DB, st *Statement) (result, error) {
	var res result
	var err error
	if hooks, _ := mutationHooks(st); db.hooked() || len(hooks) > 0 {
		err = db.run(ctx, st, nil, &res)
	} else {
		err = db.plain(ctx, st, nil, &res)
	}
	return res, err
}
