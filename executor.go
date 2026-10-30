package velox

import (
	"context"
	"database/sql"
	"time"

	"github.com/syssam/velox2/internal/conn"
)

// The driver-facing types are internal/conn's, so that velox's driver packages
// can implement them.
type (
	rows      = conn.Rows
	result    = conn.Result
	txControl = conn.Tx
	executor  = conn.Executor
)

// txOptions are a transaction's options; TxOption sets them.
type txOptions struct {
	isolation sql.IsolationLevel
	readOnly  bool
	retries   int
	timeout   time.Duration // TxTimeout; 0: the DB's Tx default
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
	// cols makes Query return colRows: set when the driver is pgx's stdlib.
	cols bool
}

// newSQLExecutor returns the executor for db, outside a transaction.
func newSQLExecutor(db *sql.DB) sqlExecutor {
	return sqlExecutor{c: db, db: db, cols: pgxStdlib(db)}
}

func (e sqlExecutor) Query(ctx context.Context, s string, args []any) (rows, error) {
	r, err := e.c.QueryContext(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	if e.cols {
		return &colRows{Rows: r}, nil
	}
	return sqlRows{r}, nil
}

func (e sqlExecutor) Exec(ctx context.Context, s string, args []any) (result, error) {
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
	return result{Affected: n, LastID: id, HasLastID: idErr == nil}, nil
}

func (e sqlExecutor) Begin(ctx context.Context, o sql.TxOptions) (executor, txControl, error) {
	tx, err := e.db.BeginTx(ctx, &o)
	if err != nil {
		return nil, nil, err
	}
	return sqlExecutor{c: tx, cols: e.cols}, sqlTx{tx}, nil
}

type sqlTx struct{ tx *sql.Tx }

func (t sqlTx) Commit(context.Context) error   { return t.tx.Commit() }
func (t sqlTx) Rollback(context.Context) error { return t.tx.Rollback() }

type sqlRows struct{ *sql.Rows }

func (r sqlRows) Close() { _ = r.Rows.Close() }
