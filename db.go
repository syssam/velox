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
	d    Dialect
	ex   executor
	cfg  *config
	inTx bool
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
func Open(d Dialect, db *sql.DB, opts ...Option) *DB { return newDB(d, sqlExecutor{db}, opts) }

// OpenPgx returns a DB for PostgreSQL that runs statements through pool with
// pgx's native protocol: about 0.4 times the time and allocations of
// database/sql for a 1,000-row result (design, Appendix A.5).
func OpenPgx(pool *pgxpool.Pool, opts ...Option) *DB { return newDB(Postgres, pgxExecutor{pool}, opts) }

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

// run takes a statement through the pipeline: rewrite hooks, rendering,
// around hooks, execution.
func (db *DB) run(ctx context.Context, st *Statement) (rows, error) {
	if len(db.cfg.rewrites) > 0 || len(db.cfg.arounds) > 0 {
		// Hooks may edit the statement in place; give them copies so that
		// the query the statement came from stays as it was. With no hooks
		// nothing is copied.
		st.Where = slices.Clone(st.Where)
		st.OrderBy = slices.Clone(st.OrderBy)
		st.Columns = slices.Clone(st.Columns)
	}
	if len(db.cfg.rewrites) > 0 {
		for _, rw := range db.cfg.rewrites {
			if err := rw(ctx, st); err != nil {
				return nil, err
			}
		}
	}
	st.SQL, st.Args = db.cfg.cache.render(db.d, st)
	if len(db.cfg.arounds) == 0 {
		r, err := db.ex.query(ctx, st.SQL, st.Args)
		if err != nil {
			return nil, dbErr(st.Entity, err)
		}
		return r, nil
	}
	var out rows
	called := false
	next := func(ctx context.Context) error {
		called = true
		r, err := db.ex.query(ctx, st.SQL, st.Args)
		if err != nil {
			return dbErr(st.Entity, err)
		}
		if out != nil {
			out.Close() // a hook called next twice; keep the latest rows
		}
		out = r
		return nil
	}
	for i := len(db.cfg.arounds) - 1; i >= 0; i-- {
		a, inner := db.cfg.arounds[i], next
		next = func(ctx context.Context) error { return a(ctx, st, inner) }
	}
	if err := next(ctx); err != nil {
		if out != nil {
			out.Close()
		}
		return nil, err
	}
	if out == nil {
		if called {
			return nil, fmt.Errorf("velox: %s: an Around hook discarded the error from next", st.Entity.Name)
		}
		return nil, fmt.Errorf("velox: %s: an Around hook returned without calling next", st.Entity.Name)
	}
	return out, nil
}
