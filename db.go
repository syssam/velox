package velox

import (
	"context"
	"database/sql"
	"fmt"
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
