package velox

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
)

// This file is the database handle of the code cmd/velox-gen generates: DB,
// Tx and Executor, which every query and mutation of that code is started
// on (db.User.Query(tx)). The handle mentions no entity, so naming it in a
// signature costs the compiler nothing beyond this package.

// Executor is what a query or a mutation runs on: a *DB, or a *Tx to run
// inside a transaction. A function that takes an Executor works on either.
type Executor interface {
	// State returns the state of the handle, or nil for a nil handle. It
	// is what the runtime that executes generated code works on; an
	// application has no use for it.
	State() *ExecutorState
}

// ExecutorState is the state of a handle: the driver its statements run on,
// and the hooks and interceptors of the database it belongs to. It is read by
// the runtime that executes generated code (package orm).
type ExecutorState struct {
	drv dialect.Driver
	ext *extensions
}

// Driver returns the driver one statement runs on.
func (s *ExecutorState) Driver() dialect.Driver { return s.drv }

// extensions are the hooks and interceptors registered on a DB. A transaction
// shares those of the DB it was started from.
type extensions struct {
	hooks  []Hook
	inters []Interceptor
}

// Hooks returns the hooks registered on the database (DB.Use).
func (s *ExecutorState) Hooks() []Hook {
	if s.ext == nil {
		return nil
	}
	return s.ext.hooks
}

// Interceptors returns the interceptors registered on the database
// (DB.Intercept).
func (s *ExecutorState) Interceptors() []Interceptor {
	if s.ext == nil {
		return nil
	}
	return s.ext.inters
}

// DB is a database handle. It mentions no entity, so naming it in a
// signature costs the compiler nothing beyond this package.
type DB struct {
	c   ExecutorState
	raw *sql.Driver
}

// Open opens a database with the given dialect ("postgres", "mysql",
// "sqlite") and data source name.
func Open(dialectName, dsn string) (*DB, error) {
	drv, err := sql.Open(dialectName, dsn)
	if err != nil {
		return nil, err
	}
	return &DB{c: ExecutorState{drv: drv, ext: &extensions{}}, raw: drv}, nil
}

// OpenDB wraps an existing database/sql handle.
func OpenDB(dialectName string, db *stdsql.DB) *DB {
	drv := sql.OpenDB(dialectName, db)
	return &DB{c: ExecutorState{drv: drv, ext: &extensions{}}, raw: drv}
}

// State implements Executor.
func (db *DB) State() *ExecutorState {
	if db == nil {
		return nil
	}
	return &db.c
}

// Close closes the database.
func (db *DB) Close() error { return db.raw.Close() }

// SQL returns the underlying database/sql handle.
func (db *DB) SQL() *stdsql.DB { return db.raw.DB() }

// Dialect returns the dialect name.
func (db *DB) Dialect() string { return db.c.drv.Dialect() }

// Driver returns the driver under the handle, for code that works below the
// ORM: the generated migrate package creates the tables through it.
func (db *DB) Driver() dialect.Driver { return db.raw }

// Use adds hooks that run around every mutation made through the handle and
// its transactions, outside the schema's own hooks. Register hooks before the
// handle is used concurrently; there is no lock on this path.
func (db *DB) Use(hooks ...Hook) {
	db.c.ext.hooks = append(db.c.ext.hooks, hooks...)
}

// Intercept adds interceptors that run around every query made through the
// handle and its transactions. Like Use, it is a start-up call.
//
// Interceptors shape queries; they reach no mutation. A rule that decides
// which rows a caller may touch belongs in the schema's Policy.
func (db *DB) Intercept(inters ...Interceptor) {
	db.c.ext.inters = append(db.c.ext.inters, inters...)
}

// Tx is an open transaction. Pass it wherever a *DB is accepted to run inside it.
type Tx struct {
	c    ExecutorState
	tx   dialect.Tx
	done bool
	// after are the functions to run once the transaction is committed.
	after []func()
}

// State implements Executor.
func (tx *Tx) State() *ExecutorState {
	if tx == nil {
		return nil
	}
	return &tx.c
}

// txDriver adapts a dialect.Tx to the dialect.Driver the SQL layer runs on.
type txDriver struct {
	t    dialect.Tx
	name string
	// seq numbers the savepoints of the transaction.
	seq *int
	// savepoint makes Tx open one: see ExecutorState.Atomic.
	savepoint bool
}

// Atomic returns the driver for a mutation of several statements (a row and
// its join rows, an update and the keys it moves). On a database handle the
// SQL layer wraps them in a transaction of its own. Inside the caller's
// transaction there is none to open, so they are wrapped in a savepoint: when
// a later statement fails, the earlier ones are undone and the transaction
// goes on, instead of keeping half a mutation (and, on Postgres, instead of
// being aborted as a whole).
func (s *ExecutorState) Atomic() dialect.Driver {
	if d, ok := s.drv.(txDriver); ok {
		d.savepoint = true
		return d
	}
	return s.drv
}

// savepointTx is the transaction the SQL layer sees for one mutation inside
// the caller's transaction.
type savepointTx struct {
	txDriver
	// ctx is the mutation's: Commit and Rollback of dialect.Tx take none.
	ctx  context.Context //nolint:containedctx // see above
	name string
}

func (s *savepointTx) Commit() error {
	return s.t.Exec(s.ctx, "RELEASE SAVEPOINT "+s.name, []any{}, nil)
}

func (s *savepointTx) Rollback() error {
	if err := s.t.Exec(s.ctx, "ROLLBACK TO SAVEPOINT "+s.name, []any{}, nil); err != nil {
		return err
	}
	return s.t.Exec(s.ctx, "RELEASE SAVEPOINT "+s.name, []any{}, nil)
}

func (d txDriver) Exec(ctx context.Context, query string, args, v any) error {
	return d.t.Exec(ctx, query, args, v)
}

func (d txDriver) Query(ctx context.Context, query string, args, v any) error {
	return d.t.Query(ctx, query, args, v)
}

func (d txDriver) Dialect() string { return d.name }
func (d txDriver) Close() error    { return nil }

// Tx lets the SQL layer wrap a multi-statement mutation (a row plus its join
// rows) while already inside a transaction. With savepoint it is one; without,
// it joins the transaction: its Commit and Rollback do nothing, and an error
// it reports reaches the caller, who rolls the real transaction back.
func (d txDriver) Tx(ctx context.Context) (dialect.Tx, error) {
	if !d.savepoint {
		return joinedTx{d}, nil
	}
	*d.seq++
	sp := &savepointTx{txDriver: d, ctx: context.WithoutCancel(ctx), name: "velox_" + strconv.Itoa(*d.seq)}
	if err := d.t.Exec(ctx, "SAVEPOINT "+sp.name, []any{}, nil); err != nil {
		return nil, fmt.Errorf("velox: opening a savepoint: %w", err)
	}
	return sp, nil
}

type joinedTx struct{ txDriver }

func (joinedTx) Commit() error   { return nil }
func (joinedTx) Rollback() error { return nil }

// Begin starts a transaction. The caller must Commit or Rollback it.
func (db *DB) Begin(ctx context.Context) (*Tx, error) { return db.BeginTx(ctx, nil) }

// BeginTx starts a transaction with options: its isolation level, or
// read-only. Nil options are the database's defaults.
func (db *DB) BeginTx(ctx context.Context, opts *stdsql.TxOptions) (*Tx, error) {
	t, err := db.raw.BeginTx(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("velox: starting a transaction: %w", err)
	}
	return &Tx{c: ExecutorState{drv: txDriver{t: t, name: db.c.drv.Dialect(), seq: new(int)}, ext: db.c.ext}, tx: t}, nil
}

// Driver returns the driver the transaction runs on, for code that works
// below the ORM inside the transaction: a lock statement, SQL of the
// caller's own.
func (tx *Tx) Driver() dialect.Driver { return tx.c.drv }

// Commit commits the transaction.
func (tx *Tx) Commit() error {
	if tx.done {
		return ErrTxDone
	}
	tx.done = true
	if err := tx.tx.Commit(); err != nil {
		return err
	}
	for _, fn := range tx.after {
		fn()
	}
	return nil
}

// AfterCommit registers fn to run once the transaction has been committed:
// not when it is rolled back, and not when the commit fails. It is for what
// must follow a change only when the change is certain, such as publishing
// an event. The functions run in the order they were registered, on the
// goroutine that commits, after Commit has succeeded and before it returns.
func (tx *Tx) AfterCommit(fn func()) { tx.after = append(tx.after, fn) }

// Rollback rolls the transaction back. After Commit or a previous Rollback it
// returns ErrTxDone and does nothing, so a deferred Rollback is safe.
func (tx *Tx) Rollback() error {
	if tx.done {
		return ErrTxDone
	}
	tx.done = true
	return tx.tx.Rollback()
}

// RunTx runs fn in a transaction: committed when fn returns nil, rolled back
// when it returns an error or panics.
func (db *DB) RunTx(ctx context.Context, fn func(tx *Tx) error) error {
	return db.RunTxOptions(ctx, nil, fn)
}

// RunTxOptions is RunTx with the options of BeginTx.
func (db *DB) RunTxOptions(ctx context.Context, opts *stdsql.TxOptions, fn func(tx *Tx) error) error {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return err
	}
	defer func() {
		if v := recover(); v != nil {
			_ = tx.Rollback()
			panic(v)
		}
	}()
	if err := fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, ErrTxDone) {
			return fmt.Errorf("%w (rolling back: %v)", err, rerr)
		}
		return err
	}
	if err := tx.Commit(); err != nil && !errors.Is(err, ErrTxDone) {
		return fmt.Errorf("velox: committing the transaction: %w", err)
	}
	return nil
}

// ErrNoExecutor is the error of a query or mutation that was run without a
// handle: one built from nil (db.User.Query(nil)), or from a nil *DB or *Tx.
var ErrNoExecutor = errors.New("velox: nothing to run on: build the query or mutation from a database handle or a transaction")

// ErrTxDone is returned by Commit and Rollback of a transaction that was
// already committed or rolled back.
var ErrTxDone = errors.New("velox: transaction has already been committed or rolled back")

type executorKey struct{}

// WithExecutor returns a context that carries ex, the database handle or the
// transaction of a request. A server sets it once per request; the methods a
// GraphQL engine calls on an entity to resolve its edges (u.Posts(ctx, ...))
// have nowhere else to take one from.
func WithExecutor(ctx context.Context, ex Executor) context.Context {
	return context.WithValue(ctx, executorKey{}, ex)
}

// ExecutorFrom returns the handle WithExecutor put in ctx, or nil: a query
// made from nil reports ErrNoExecutor when it runs.
func ExecutorFrom(ctx context.Context) Executor {
	ex, _ := ctx.Value(executorKey{}).(Executor)
	return ex
}

// TxFrom returns the transaction the context carries, or nil when it
// carries none or a handle that is not a transaction: for code that does
// something only inside a transaction (a check that must be rolled back
// with the write it follows).
func TxFrom(ctx context.Context) *Tx {
	tx, _ := ctx.Value(executorKey{}).(*Tx)
	return tx
}

// Must returns v, and panics when err is not nil: for a test, and for the
// start of a program, where an error ends things either way.
//
//	u := velox.Must(db.User.Create(conn).Set(user.Name.Set("a")).Save(ctx))
//
// The builders have no SaveX, GetX or ExecX of their own: each would be a
// method of every builder, and a package pays for the methods of every type
// it can reach.
func Must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// First returns the first of what a read of a list returned, or velox.ErrNotFound
// when it returned none: the id of the first row a query matches is
//
//	id, err := velox.First(q.Limit(1).IDs(ctx))
func First[T any](vs []T, err error) (T, error) {
	if err != nil || len(vs) == 0 {
		var zero T
		if err == nil {
			err = ErrNotFound
		}
		return zero, err
	}
	return vs[0], nil
}

// Check panics when err is not nil: Must for a call that returns only an
// error.
func Check(err error) {
	if err != nil {
		panic(err)
	}
}
