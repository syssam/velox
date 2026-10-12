package orm

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/syssam/velox"
	"github.com/syssam/velox/dialect"
	"github.com/syssam/velox/dialect/sql"
)

// Executor is what every terminal of a generated builder runs on: a *DB or a
// *Tx. It is closed to other implementations because the executor carries
// the dialect, which a bare database/sql handle does not.
type Executor interface {
	conn() *conn
}

// conn is the state shared by a DB and the transactions started from it.
type conn struct {
	drv dialect.Driver
	ext *extensions
}

// extensions are the hooks and interceptors registered on a DB. A transaction
// shares those of the DB it was started from.
type extensions struct {
	hooks  []velox.Hook
	inters []velox.Interceptor
}

func (c *conn) hooks() []velox.Hook {
	if c.ext == nil {
		return nil
	}
	return c.ext.hooks
}

func (c *conn) inters() []velox.Interceptor {
	if c.ext == nil {
		return nil
	}
	return c.ext.inters
}

// DB is a database handle. It mentions no entity, so naming it in a
// signature costs the compiler nothing beyond this package.
type DB struct {
	c   conn
	raw *sql.Driver
}

// Open opens a database with the given dialect ("postgres", "mysql",
// "sqlite") and data source name.
func Open(dialectName, dsn string) (*DB, error) {
	drv, err := sql.Open(dialectName, dsn)
	if err != nil {
		return nil, err
	}
	return &DB{c: conn{drv: drv, ext: &extensions{}}, raw: drv}, nil
}

// OpenDB wraps an existing database/sql handle.
func OpenDB(dialectName string, db *stdsql.DB) *DB {
	drv := sql.OpenDB(dialectName, db)
	return &DB{c: conn{drv: drv, ext: &extensions{}}, raw: drv}
}

func (db *DB) conn() *conn {
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
func (db *DB) Use(hooks ...velox.Hook) {
	db.c.ext.hooks = append(db.c.ext.hooks, hooks...)
}

// Intercept adds interceptors that run around every query made through the
// handle and its transactions. Like Use, it is a start-up call.
//
// Interceptors shape queries; they reach no mutation. A rule that decides
// which rows a caller may touch belongs in the schema's Policy.
func (db *DB) Intercept(inters ...velox.Interceptor) {
	db.c.ext.inters = append(db.c.ext.inters, inters...)
}

// Tx is an open transaction. Pass it wherever a *DB is accepted to run inside it.
type Tx struct {
	c    conn
	tx   dialect.Tx
	done bool
	// after are the functions to run once the transaction is committed.
	after []func()
}

func (tx *Tx) conn() *conn {
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
	// savepoint makes Tx open one: see conn.atomic.
	savepoint bool
}

// atomic returns the driver for a mutation of several statements (a row and
// its join rows, an update and the keys it moves). On a database handle the
// SQL layer wraps them in a transaction of its own. Inside the caller's
// transaction there is none to open, so they are wrapped in a savepoint: when
// a later statement fails, the earlier ones are undone and the transaction
// goes on, instead of keeping half a mutation (and, on Postgres, instead of
// being aborted as a whole).
func (c *conn) atomic() dialect.Driver {
	if d, ok := c.drv.(txDriver); ok {
		d.savepoint = true
		return d
	}
	return c.drv
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
	return &Tx{c: conn{drv: txDriver{t: t, name: db.c.drv.Dialect(), seq: new(int)}, ext: db.c.ext}, tx: t}, nil
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

// Errors of the runtime's own. The ones an application tests after a query
// or a mutation are package velox's, whichever runtime ran it:
// velox.ErrNotFound, velox.ErrNotSingular, *velox.ValidationError,
// *velox.ConstraintError with velox.ErrUnique and its other kinds,
// *velox.NotLoadedError, and velox.IsNotFound and the rest for each.
var (
	// ErrNoExecutor: a query or mutation was run that was not built from a
	// database handle or transaction (db.User.Query(nil), or a nil *orm.Tx).
	ErrNoExecutor = errors.New("velox: nothing to run on: build the query or mutation from a database handle or a transaction")
	// ErrNoEntity: the zero value of a builder was run. Builders come from
	// the generated package (db.User.Query(conn)).
	ErrNoEntity = errors.New("velox: a builder that was not made by the generated package has no entity")
	// ErrTxDone: the transaction was already committed or rolled back.
	ErrTxDone = errors.New("velox: transaction has already been committed or rolled back")
)

// NotFound returns the error of a query or mutation of the entity that
// matched no row: a *velox.NotFoundError, which is velox.ErrNotFound and
// reads "velox: sales_order not found".
func (m *Meta) NotFound() error { return velox.NewNotFoundError(m.label()) }

// notSingular is the error of a read or write of one row that matched more.
func (m *Meta) notSingular() error { return velox.NewNotSingularError(m.label()) }

// invalid is the error of a value of a field or an edge refused before it
// reached the database.
func (m *Meta) invalid(name string, err error) error {
	return &velox.ValidationError{Entity: m.Name, Field: name, Name: name, Err: err}
}

// label is the entity as an error names it.
func (m *Meta) label() string { return snake(m.Name) }

// snake writes a name of the schema in lower case with underscores:
// SalesOrder is sales_order, HTTPLog is http_log.
func snake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if i > 0 && unicode.IsUpper(r) {
			prev, _ := utf8.DecodeLastRuneInString(s[:i])
			next, size := utf8.DecodeRuneInString(s[i+utf8.RuneLen(r):])
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && size > 0 && unicode.IsLower(next)) {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// Must returns v, and panics when err is not nil: for a test, and for the
// start of a program, where an error ends things either way.
//
//	u := orm.Must(db.User.Create(conn).Set(user.Name.Set("a")).Save(ctx))
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
//	id, err := orm.First(q.Limit(1).IDs(ctx))
func First[T any](vs []T, err error) (T, error) {
	if err != nil || len(vs) == 0 {
		var zero T
		if err == nil {
			err = velox.ErrNotFound
		}
		return zero, err
	}
	return vs[0], nil
}

// Apply calls each function on q and returns q: for the parts of a query
// that were collected before the query was built.
//
//	var parts []func(*entity.UserQuery)
//	if adultsOnly {
//		parts = append(parts, func(q *entity.UserQuery) { q.Where(user.Age.GTE(18)) })
//	}
//	us, err := orm.Apply(db.User.Query(conn), parts...).All(ctx)
func Apply[Q any](q Q, fns ...func(Q)) Q {
	for _, fn := range fns {
		if fn != nil {
			fn(q)
		}
	}
	return q
}

// Check panics when err is not nil: Must for a call that returns only an
// error.
func Check(err error) {
	if err != nil {
		panic(err)
	}
}
