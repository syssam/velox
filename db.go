package velox

import (
	"context"
	"database/sql"
	"fmt"
	"slices"

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
}

type config struct {
	rewrites []Rewrite
	arounds  []Around
	cache    renderCache
}

// Option configures a DB's pipeline.
type Option func(*config)

// WithRewrite adds a rewrite hook. Rewrite hooks run in the order given.
func WithRewrite(r Rewrite) Option { return func(c *config) { c.rewrites = append(c.rewrites, r) } }

// WithAround adds an around hook. The first one given is the outermost.
func WithAround(a Around) Option { return func(c *config) { c.arounds = append(c.arounds, a) } }

// Open returns a DB that runs statements through db, a *sql.DB the
// application opened with a driver for dialect d. For PostgreSQL, OpenPgx is
// faster.
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

// dbErr names the entity in an error from the database or the driver.
func dbErr(info *EntityInfo, err error) error { return fmt.Errorf("velox: %s: %w", info.Name, err) }

// hooked reports whether the pipeline has hooks: whether a statement must go
// through run, or may take the shortcut of plain.
func (db *DB) hooked() bool { return len(db.cfg.rewrites) > 0 || len(db.cfg.arounds) > 0 }

// plain runs a statement on a DB with no hooks: rendering and execution.
// Unlike run's, its st and read do not escape, so a caller's state and
// reading closure can stay on the stack.
func (db *DB) plain(ctx context.Context, st *Statement, read func(rows) error, res *result) error {
	st.SQL, st.Args = db.cfg.cache.render(db.d, st)
	return db.exec(ctx, st, read, res)
}

// run takes a statement through the pipeline: rewrite hooks, rendering,
// then the around hooks wrapping execution, which is sending the statement
// and reading its rows. read reads them; it is called once per execution (an
// around hook that retries runs it again) and must not close them: run does,
// before it returns, on every path. Hooks may keep the statement, and next
// (which holds read), so both escape; a DB with no hooks uses plain.
func (db *DB) run(ctx context.Context, st *Statement, read func(rows) error, res *result) error {
	if db.hooked() {
		// Hooks may edit the statement in place; give them copies so that
		// the query the statement came from stays as it was. With no hooks
		// nothing is copied.
		st.Where = slices.Clone(st.Where)
		st.OrderBy = slices.Clone(st.OrderBy)
		st.Columns = slices.Clone(st.Columns)
		st.Set = slices.Clone(st.Set)
		st.Returning = slices.Clone(st.Returning)
		if st.Rows != nil {
			rows := make([][]any, len(st.Rows))
			for i, r := range st.Rows {
				rows[i] = slices.Clone(r)
			}
			st.Rows = rows
		}
		if st.Conflict != nil {
			cf := *st.Conflict
			cf.Columns, cf.Update = slices.Clone(cf.Columns), slices.Clone(cf.Update)
			st.Conflict = &cf
		}
	}
	entity, op := st.Entity, st.Op
	for _, rw := range db.cfg.rewrites {
		if err := rw(ctx, st); err != nil {
			return err
		}
	}
	if st.Op != op {
		return fmt.Errorf("velox: %s: a Rewrite hook changed the statement's kind", entity.Name)
	}
	if len(db.cfg.rewrites) > 0 {
		if err := checkColumns(st); err != nil {
			return err
		}
	}
	if st.Entity == entity {
		st.SQL, st.Args = db.cfg.cache.render(db.d, st)
	} else {
		// A hook pointed the statement at its own EntityInfo, likely one
		// made for this statement alone; caching by it would fill the cache.
		st.SQL, st.Args = render(db.d, st)
	}
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
		}
		return bad(int(nd.col))
	}
	for _, p := range st.Where {
		if err := walk(p.n); err != nil {
			return err
		}
	}
	return nil
}

// exec sends a rendered statement, reads its rows with read and closes them;
// with a nil read it sends a statement that returns no rows and fills *res.
// Errors from the database, the driver or read name the entity.
func (db *DB) exec(ctx context.Context, st *Statement, read func(rows) error, res *result) (err error) {
	if read == nil {
		r, err := db.ex.exec(ctx, st.SQL, st.Args)
		if err != nil {
			return dbErr(st.Entity, err)
		}
		if res != nil {
			*res = r
		}
		return nil
	}
	r, err := db.ex.query(ctx, st.SQL, st.Args)
	if err != nil {
		return dbErr(st.Entity, err)
	}
	defer func() {
		// Deferred, so that the rows are closed even when the loop body of
		// an Iter, which runs inside read, panics. pgx reports some errors
		// only once the rows are closed.
		r.Close()
		if rerr := r.Err(); rerr != nil && err == nil {
			err = dbErr(st.Entity, rerr)
		}
	}()
	if err := read(r); err != nil {
		return dbErr(st.Entity, err)
	}
	return nil
}

// execWrite runs a statement that returns no rows through the pipeline.
func execWrite(ctx context.Context, db *DB, st *Statement) (result, error) {
	var res result
	var err error
	if db.hooked() {
		err = db.run(ctx, st, nil, &res)
	} else {
		err = db.plain(ctx, st, nil, &res)
	}
	return res, err
}
