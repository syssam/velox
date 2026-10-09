package orm

import (
	"context"
	stdsql "database/sql"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

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
	sp := &savepointTx{txDriver: d, ctx: context.WithoutCancel(ctx), name: "vx_" + strconv.Itoa(*d.seq)}
	if err := d.t.Exec(ctx, "SAVEPOINT "+sp.name, []any{}, nil); err != nil {
		return nil, fmt.Errorf("orm: opening a savepoint: %w", err)
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
		return nil, fmt.Errorf("orm: starting a transaction: %w", err)
	}
	return &Tx{c: conn{drv: txDriver{t: t, name: db.c.drv.Dialect(), seq: new(int)}, ext: db.c.ext}, tx: t}, nil
}

// Commit commits the transaction.
func (tx *Tx) Commit() error {
	if tx.done {
		return ErrTxDone
	}
	tx.done = true
	return tx.tx.Commit()
}

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
		return fmt.Errorf("orm: committing the transaction: %w", err)
	}
	return nil
}

// Errors returned by the terminals of generated builders.
var (
	// ErrNotFound: First/Only/Save matched no row.
	ErrNotFound = errors.New("orm: not found")
	// ErrNotSingular: Only/Save matched more than one row.
	ErrNotSingular = errors.New("orm: more than one row matched")
	// ErrNoExecutor: a query or mutation was run that was not built from a
	// database handle or transaction (db.User.Query(nil), or a nil *orm.Tx).
	ErrNoExecutor = errors.New("orm: nothing to run on: build the query or mutation from a database handle or a transaction")
	// ErrNoEntity: the zero value of a builder was run. Builders come from
	// the generated package (db.User.Query(conn)).
	ErrNoEntity = errors.New("orm: a builder that was not made by the generated package has no entity")
	// ErrTxDone: the transaction was already committed or rolled back.
	ErrTxDone = errors.New("orm: transaction has already been committed or rolled back")
)

// ValidationError reports a value rejected before it reached the database.
type ValidationError struct {
	Entity string
	Field  string
	Err    error
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("orm: validating %s.%s: %v", e.Entity, e.Field, e.Err)
}

func (e *ValidationError) Unwrap() error { return e.Err }

// ConstraintKind is the kind of constraint a write was refused for.
type ConstraintKind uint8

// Constraint kinds.
const (
	ConstraintOther ConstraintKind = iota
	ConstraintUnique
	ConstraintForeignKey
	ConstraintCheck
)

// The kinds of a ConstraintError, for errors.Is: a handler that turns a
// duplicate into "already exists" tests errors.Is(err, orm.ErrUnique).
var (
	ErrUnique     = errors.New("orm: unique constraint violated")
	ErrForeignKey = errors.New("orm: foreign key constraint violated")
	ErrCheck      = errors.New("orm: check constraint violated")
)

// ConstraintError is a write the database refused for a constraint. Err is
// the driver's error.
type ConstraintError struct {
	Kind ConstraintKind
	// Constraint is what the database names as violated, when the driver
	// says: the constraint on Postgres (users_email_key), the columns on
	// SQLite (users.email), the key on MySQL. It is for messages and logs;
	// its form differs by database.
	Constraint string
	Err        error
}

func (e *ConstraintError) Error() string { return "orm: constraint failed: " + e.Err.Error() }

// Unwrap returns the driver's error.
func (e *ConstraintError) Unwrap() error { return e.Err }

// Is reports whether target is the sentinel of the error's kind.
func (e *ConstraintError) Is(target error) bool {
	switch target {
	case ErrUnique:
		return e.Kind == ConstraintUnique
	case ErrForeignKey:
		return e.Kind == ConstraintForeignKey
	case ErrCheck:
		return e.Kind == ConstraintCheck
	}
	return false
}

// IsRetryable reports whether err is a failure the database asks to be tried
// again: a serialization failure or a deadlock (SQLSTATE 40001 and 40P01 on
// Postgres, errors 1213 and 1205 on MySQL), or a database that was busy or
// locked (SQLite). The whole transaction is what is tried again, from its
// first statement, and only when running it twice is harmless; a constraint
// violation or a validation error is never retryable.
func IsRetryable(err error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		switch x := e.(type) {
		case interface{ SQLState() string }:
			if state := x.SQLState(); state == "40001" || state == "40P01" {
				return true
			}
		case interface{ Code() int }:
			// SQLite's primary result code: SQLITE_BUSY, SQLITE_LOCKED.
			if code := x.Code() & 0xff; code == 5 || code == 6 {
				return true
			}
		}
		// go-sql-driver/mysql carries the server's error number in a field.
		if rv := reflect.Indirect(reflect.ValueOf(e)); rv.Kind() == reflect.Struct {
			if f := rv.FieldByName("Number"); f.IsValid() && f.CanUint() && (f.Uint() == 1213 || f.Uint() == 1205) {
				return true
			}
		}
	}
	return false
}

var (
	sqliteConstraint = regexp.MustCompile(`constraint failed: ([^()]+?)\s*(?:\(|$)`)
	mysqlKey         = regexp.MustCompile(`for key '([^']+)'`)
)

// constraintOf names what a constraint error says was violated.
func constraintOf(err error) string {
	// lib/pq and pgx carry the name in a field of their error.
	for e := err; e != nil; e = errors.Unwrap(e) {
		rv := reflect.Indirect(reflect.ValueOf(e))
		if rv.Kind() != reflect.Struct {
			continue
		}
		for _, name := range [...]string{"Constraint", "ConstraintName"} {
			if f := rv.FieldByName(name); f.IsValid() && f.Kind() == reflect.String && f.String() != "" {
				return f.String()
			}
		}
	}
	msg := err.Error()
	if m := sqliteConstraint.FindStringSubmatch(msg); m != nil {
		return strings.TrimSpace(m[1])
	}
	if m := mysqlKey.FindStringSubmatch(msg); m != nil {
		return m[1]
	}
	return ""
}

// NotFound returns ErrNotFound for the named entity. Generated by-id helpers
// return it when the row is not there.
func NotFound(entity string) error { return fmt.Errorf("%w: %s", ErrNotFound, entity) }

// IsNotFound reports whether err is ErrNotFound.
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// IsConstraintError reports whether err is a constraint violation.
func IsConstraintError(err error) bool {
	var c *ConstraintError
	return errors.As(err, &c)
}
