// Package veloxpgx runs velox's statements on a pgx pool, through pgx's native
// protocol. It is a package of its own so that an application that does not use
// it does not link pgx.
//
//	pool, err := pgxpool.New(ctx, dsn)
//	...
//	db := veloxpgx.Open(pool)
//
// velox.Open with pgx's stdlib driver reaches the same database through
// database/sql, and needs no import of this package.
package veloxpgx

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syssam/velox"
	"github.com/syssam/velox/internal/conn"
	"github.com/syssam/velox/internal/engine"
)

// Open returns a DB for PostgreSQL that runs statements through pool, which
// makes about a quarter of the allocations of database/sql with pgx's stdlib
// driver for a 100-row query. A nil pool panics.
func Open(pool *pgxpool.Pool, opts ...velox.Option) *velox.DB {
	return velox.OpenConn(velox.Postgres, Conn(pool), opts...)
}

// Conn returns a velox.Conn that runs statements through pool, for a replica or
// a shard. It is valid only on a PostgreSQL DB; on another, the Open that is
// given it panics. A nil pool panics.
func Conn(pool *pgxpool.Pool) velox.Conn {
	if pool == nil {
		panic("veloxpgx: Conn(nil)")
	}
	return engine.DriverConn(velox.Postgres, executor{c: pool, pool: pool})
}

type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type beginner interface {
	BeginTx(ctx context.Context, o pgx.TxOptions) (pgx.Tx, error)
}

// executor runs statements through pgx's native protocol: through the pool
// outside a transaction, through a pgx.Tx inside one.
type executor struct {
	c    queryer
	pool beginner // nil inside a transaction
}

func (e executor) Query(ctx context.Context, s string, args []any) (conn.Rows, error) {
	r, err := e.c.Query(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (e executor) Exec(ctx context.Context, s string, args []any) (conn.Result, error) {
	tag, err := e.c.Exec(ctx, s, args...)
	if err != nil {
		return conn.Result{}, err
	}
	return conn.Result{Affected: tag.RowsAffected()}, nil
}

func (e executor) Begin(ctx context.Context, o sql.TxOptions) (conn.Executor, conn.Tx, error) {
	tx, err := e.pool.BeginTx(ctx, txOptions(o))
	if err != nil {
		return nil, nil, err
	}
	return executor{c: tx}, txControl{tx}, nil
}

// txOptions is what BeginTx is given. The access mode is sent only for a
// read-only transaction: otherwise the server's (or the role's)
// default_transaction_read_only stays in force, as with database/sql.
func txOptions(o sql.TxOptions) pgx.TxOptions {
	opts := pgx.TxOptions{IsoLevel: isolation(o.Isolation)}
	if o.ReadOnly {
		opts.AccessMode = pgx.ReadOnly
	}
	return opts
}

// isolation maps database/sql's isolation levels to pgx's; the default leaves
// the server's.
func isolation(l sql.IsolationLevel) pgx.TxIsoLevel {
	switch l {
	case sql.LevelReadUncommitted:
		return pgx.ReadUncommitted
	case sql.LevelReadCommitted:
		return pgx.ReadCommitted
	case sql.LevelRepeatableRead, sql.LevelSnapshot:
		return pgx.RepeatableRead
	case sql.LevelSerializable, sql.LevelLinearizable:
		return pgx.Serializable
	}
	return ""
}

type txControl struct{ tx pgx.Tx }

func (t txControl) Commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t txControl) Rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
