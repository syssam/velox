package velox

import (
	"context"
	"database/sql"
	"unsafe"

	"github.com/syssam/velox/internal/engine"
)

// DB runs statements, inside or outside a transaction. It is the only type
// that does: generic methods cannot be in an interface, and one concrete type
// means every function taking a *DB works in a transaction unchanged.
//
// A DB from Connect, Open, OpenConn or OpenShards is safe for concurrent use by multiple
// goroutines. The *DB that Tx gives its fn is not: see Tx.
type DB struct {
	// e is the DB, without what is generic. It is the only field: a transaction
	// gives its fn the *DB of an *engine.DB, a pointer to the same memory.
	e engine.DB
}

// DB is the size of its engine.DB, which a *DB is cast from.
var _ [unsafe.Sizeof(DB{}) - unsafe.Sizeof(engine.DB{})]struct{} = [0]struct{}{}

// Connect opens a database and returns a DB on it. Close it when done. The
// usual way to start:
//
//	db, err := velox.Connect("sqlite", "app.db?_pragma=foreign_keys(1)")
//	if err != nil {
//		return err
//	}
//	defer db.Close()
//
// driver is a database/sql driver's name, which the application imports as
// always: "postgres" or "pgx", "mysql", "sqlite" or "sqlite3". Any other name
// returns an error; use Open for it. Like sql.Open, Connect does not reach the
// database, so a wrong address fails on the first statement.
//
// A MySQL dsn must set parseTime=true and clientFoundRows=true, for the reasons
// Open gives; Connect returns an error when it does not. On SQLite, the pool
// has one connection, on a file too: each connection to ":memory:" is its own
// database, and SQLite has one writer, so that on several connections concurrent
// writes fail with SQLITE_BUSY rather than take turns. With one, they take
// turns, and a statement in the body of a loop over Iter, which holds the
// connection, fails (see Query.Iter). Raise it with
// velox.Pool(db).SetMaxOpenConns on a file that takes concurrent reads, with a
// busy_timeout in the dsn. Foreign keys are enforced only when the dsn asks, as
// Open says. For PostgreSQL, veloxpgx.Open is faster.
func Connect(driver, dsn string, opts ...Option) (*DB, error) {
	e, err := engine.Connect(driver, dsn, opts)
	if err != nil {
		return nil, err
	}
	return &DB{e: e}, nil
}

// Pool returns the *sql.DB under db, a DB from Connect or Open, for its pool
// settings and for what takes a *sql.DB, such as migrate.Up. It returns nil for a
// DB from OpenConn or OpenShards, and for a transaction's DB, whose statements
// must not leave the transaction.
//
// It is a function, not a method: a method of DB mentioning *sql.DB would make
// every package that uses a DB larger to compile.
func Pool(db *DB) *sql.DB { return engine.Pool(&db.e) }

// Close closes the pool of a DB from Connect. It does nothing for any other DB,
// whose pool belongs to whoever opened it, as do the Conns of WithReplicas. On a
// transaction's DB it returns an error.
func (db *DB) Close() error { return db.e.Close() }

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
// SQLite. Connect does the opening too, for an application that does not need a
// *sql.DB of its own.
func Open(d Dialect, db *sql.DB, opts ...Option) *DB { return &DB{e: engine.Open(d, db, opts)} }

// OpenConn returns a DB of dialect d whose primary is c. veloxpgx.Open is this
// with a veloxpgx.Conn. A zero Conn panics, as does a Conn that is valid only on
// another dialect.
func OpenConn(d Dialect, c Conn, opts ...Option) *DB { return &DB{e: engine.OpenConn(d, c, opts)} }

// OpenShards returns a DB of dialect d whose statements each run on one of
// shards, chosen from the statement's context:
//
//  1. the shard WithShard names;
//  2. otherwise, the one WithShardFor's function returns for the context's
//     tenant (WithTenant); its error refuses the statement;
//  3. otherwise none: the statement fails with ErrNoShard, as it does under
//     WithAllTenants without WithShard.
//
// A name that is not one of shards fails with ErrNoShard too, naming it.
// Within its shard a statement is routed as on a DB with replicas: writes to
// the primary, reads to the replicas in turn, a failed replica read to the
// shard's primary. Statement.Route.Shard names the shard.
//
// Tx picks its shard from its context at begin, and every statement in the
// transaction runs there: one whose context names no shard as well, but one
// whose context names another fails, and nothing is sent.
//
// WithShardFor is required, and WithReplicas is refused: each Shard has its
// own Replicas. OpenShards panics on either mistake, on a d that is not
// Postgres, MySQL or SQLite, on no shards, on a shard named "" and on a shard
// whose Primary or a replica is the zero Conn, or a Conn valid only on another
// dialect.
func OpenShards(d Dialect, shards map[string]Shard, opts ...Option) *DB {
	return &DB{e: engine.OpenShards(d, shards, opts)}
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
// On MySQL, a deadlock (1213) rolls the whole transaction back, as does a lock
// wait timeout (1205) under innodb_rollback_on_timeout, which velox asks the
// server after one. Should fn swallow the error, its later statements fail
// unsent and its nil returns the error: nothing runs outside the transaction.
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
// nested Tx rolls back to the savepoint, on a context without ctx's
// cancellation, and returns the error, so an error always means the nested
// work is undone. When that rollback fails too, or a rollback after fn's error
// does, the transaction is broken: its statements fail, and the outer fn's nil
// rolls it back with an error. A nested Tx (a savepoint) takes no options and returns an error when given any, and passes fn the ctx
// it is given, marked as the transaction's.
//
// tx is valid only inside fn: after Tx returns, statements on it fail (or, for
// a nested tx, run in the enclosing transaction). tx is not safe for
// concurrent use: run its statements from one goroutine at a time.
func (db *DB) Tx(ctx context.Context, fn func(ctx context.Context, tx *DB) error, opts ...TxOption) error {
	// tx points to its DB's only field, so it is that DB (see DB).
	return db.e.Tx(ctx, func(ctx context.Context, tx *engine.DB) error {
		return fn(ctx, (*DB)(unsafe.Pointer(tx)))
	}, opts...)
}
