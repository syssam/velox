package velox

import (
	"context"
	"database/sql"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// rows is what an executor returns for a query; *sql.Rows (wrapped) and
// pgx.Rows both provide it.
type rows interface {
	Next() bool
	Scan(dst ...any) error
	Err() error
	Close()
}

// result is what a statement that returns no rows reports.
type result struct {
	affected  int64
	lastID    int64
	hasLastID bool // the driver reports LastInsertId (MySQL, SQLite)
}

// txOptions are a transaction's options; TxOption sets them.
type txOptions struct {
	isolation sql.IsolationLevel
	readOnly  bool
	retries   int
	timeout   time.Duration // TxTimeout; 0: the DB's Tx default
}

// txControl ends a transaction.
type txControl interface {
	commit(ctx context.Context) error
	rollback(ctx context.Context) error
}

// executor runs rendered statements.
type executor interface {
	query(ctx context.Context, sql string, args []any) (rows, error)
	exec(ctx context.Context, sql string, args []any) (result, error)
	// begin starts a transaction and returns the executor that runs in it.
	begin(ctx context.Context, o txOptions) (executor, txControl, error)
}

type sqlConn interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// sqlExecutor runs statements through database/sql: through db outside a
// transaction, through a *sql.Tx inside one.
type sqlExecutor struct {
	c  sqlConn
	db *sql.DB // nil inside a transaction
	// cols makes query return colRows: set when the driver is pgx's stdlib.
	cols bool
}

// newSQLExecutor returns the executor for db, outside a transaction.
func newSQLExecutor(db *sql.DB) sqlExecutor {
	return sqlExecutor{c: db, db: db, cols: pgxStdlib(db)}
}

func (e sqlExecutor) query(ctx context.Context, s string, args []any) (rows, error) {
	r, err := e.c.QueryContext(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	if e.cols {
		return &colRows{Rows: r}, nil
	}
	return sqlRows{r}, nil
}

func (e sqlExecutor) exec(ctx context.Context, s string, args []any) (result, error) {
	res, err := e.c.ExecContext(ctx, s, args...)
	if err != nil {
		return result{}, err
	}
	// The statement ran: a driver that cannot count its rows leaves the
	// count unknown, -1, rather than failing a write that was made.
	n, err := res.RowsAffected()
	if err != nil {
		n = -1
	}
	id, idErr := res.LastInsertId()
	return result{affected: n, lastID: id, hasLastID: idErr == nil}, nil
}

func (e sqlExecutor) begin(ctx context.Context, o txOptions) (executor, txControl, error) {
	tx, err := e.db.BeginTx(ctx, &sql.TxOptions{Isolation: o.isolation, ReadOnly: o.readOnly})
	if err != nil {
		return nil, nil, err
	}
	return sqlExecutor{c: tx, cols: e.cols}, sqlTx{tx}, nil
}

type sqlTx struct{ tx *sql.Tx }

func (t sqlTx) commit(context.Context) error   { return t.tx.Commit() }
func (t sqlTx) rollback(context.Context) error { return t.tx.Rollback() }

type sqlRows struct{ *sql.Rows }

func (r sqlRows) Close() { _ = r.Rows.Close() }

type pgxConn interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type pgxBeginner interface {
	BeginTx(ctx context.Context, o pgx.TxOptions) (pgx.Tx, error)
}

// pgxExecutor runs statements through pgx's native protocol: through the
// pool outside a transaction, through a pgx.Tx inside one.
type pgxExecutor struct {
	c    pgxConn
	pool pgxBeginner // nil inside a transaction
}

func (e pgxExecutor) query(ctx context.Context, s string, args []any) (rows, error) {
	r, err := e.c.Query(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func (e pgxExecutor) exec(ctx context.Context, s string, args []any) (result, error) {
	tag, err := e.c.Exec(ctx, s, args...)
	if err != nil {
		return result{}, err
	}
	return result{affected: tag.RowsAffected()}, nil
}

func (e pgxExecutor) begin(ctx context.Context, o txOptions) (executor, txControl, error) {
	tx, err := e.pool.BeginTx(ctx, pgxTxOptions(o))
	if err != nil {
		return nil, nil, err
	}
	return pgxExecutor{c: tx}, pgxTx{tx}, nil
}

// pgxTxOptions is what BeginTx is given. The access mode is sent only for a
// read-only transaction: otherwise the server's (or the role's)
// default_transaction_read_only stays in force, as with database/sql.
func pgxTxOptions(o txOptions) pgx.TxOptions {
	opts := pgx.TxOptions{IsoLevel: pgxIsolation(o.isolation)}
	if o.readOnly {
		opts.AccessMode = pgx.ReadOnly
	}
	return opts
}

// pgxIsolation maps database/sql's isolation levels to pgx's; the default
// leaves the server's.
func pgxIsolation(l sql.IsolationLevel) pgx.TxIsoLevel {
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

type pgxTx struct{ tx pgx.Tx }

func (t pgxTx) commit(ctx context.Context) error   { return t.tx.Commit(ctx) }
func (t pgxTx) rollback(ctx context.Context) error { return t.tx.Rollback(ctx) }
