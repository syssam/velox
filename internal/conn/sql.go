package conn

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"sync"
	"time"
)

type sqlConn interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// SQL runs statements through database/sql: through db outside a
// transaction, through a *sql.Tx inside one.
type SQL struct {
	c  sqlConn
	db *sql.DB // nil inside a transaction
	// cols makes Query return colRows: set when the driver is pgx's stdlib.
	cols bool
}

// NewSQL returns the SQL executor for db, outside a transaction.
func NewSQL(db *sql.DB) SQL { return NewSQLCols(db, PgxStdlib(db)) }

// NewSQLCols is NewSQL with cols, ColumnScanning's answer, given.
func NewSQLCols(db *sql.DB, cols bool) SQL { return SQL{c: db, db: db, cols: cols} }

// ColumnScanning reports whether e's rows are colRows.
func (e SQL) ColumnScanning() bool { return e.cols }

// RowsColumnScanning reports whether r are colRows.
func RowsColumnScanning(r Rows) bool {
	_, ok := r.(*colRows)
	return ok
}

// Query runs a statement that returns rows.
func (e SQL) Query(ctx context.Context, s string, args []any) (Rows, error) {
	r, err := e.c.QueryContext(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	if e.cols {
		return &colRows{Rows: r}, nil
	}
	return sqlRows{r}, nil
}

// Exec runs a statement that returns none.
func (e SQL) Exec(ctx context.Context, s string, args []any) (Result, error) {
	res, err := e.c.ExecContext(ctx, s, args...)
	if err != nil {
		return Result{}, err
	}
	// The statement ran: a driver that cannot count its rows leaves the
	// count unknown, -1, rather than failing a write that was made.
	n, err := res.RowsAffected()
	if err != nil {
		n = -1
	}
	id, idErr := res.LastInsertId()
	return Result{Affected: n, LastID: id, HasLastID: idErr == nil}, nil
}

// Begin starts a transaction, whose statements the returned Executor runs.
func (e SQL) Begin(ctx context.Context, o sql.TxOptions) (Executor, Tx, error) {
	tx, err := e.db.BeginTx(ctx, &o)
	if err != nil {
		return nil, nil, err
	}
	return SQL{c: tx, cols: e.cols}, sqlTx{tx}, nil
}

// BeginOnConn starts a transaction on a connection of its own, on which set
// runs first and which unset runs on after the transaction ends, however it
// ends; only then does the connection go back to the pool. It holds the
// connection because database/sql hands it back as soon as a transaction's
// context ends, with no way to reach it from the transaction.
func (e SQL) BeginOnConn(ctx context.Context, o sql.TxOptions, set, unset string) (Executor, Tx, error) {
	c, err := e.db.Conn(ctx)
	if err != nil {
		return nil, nil, err
	}
	if _, err := c.ExecContext(ctx, set); err != nil {
		_ = c.Close()
		return nil, nil, err
	}
	tx, err := c.BeginTx(ctx, &o)
	if err != nil {
		restoreConn(ctx, c, unset)
		return nil, nil, err
	}
	return SQL{c: tx, cols: e.cols}, &connTx{tx: tx, c: c, unset: unset}, nil
}

// connTx is a transaction of BeginOnConn.
type connTx struct {
	tx    *sql.Tx
	c     *sql.Conn
	unset string
	once  sync.Once
}

// Commit commits, and restores the connection. A commit that fails can leave
// the transaction open on it, which restoring rolls back.
func (t *connTx) Commit(ctx context.Context) error {
	err := t.tx.Commit()
	t.once.Do(func() {
		if err != nil {
			rctx, cancel := restoreContext(ctx)
			defer cancel()
			_, _ = t.c.ExecContext(rctx, "ROLLBACK")
		}
		restoreConn(ctx, t.c, t.unset)
	})
	return err
}

// Rollback rolls back, and restores the connection.
func (t *connTx) Rollback(ctx context.Context) error {
	err := t.tx.Rollback()
	t.once.Do(func() { restoreConn(ctx, t.c, t.unset) })
	return err
}

// restoreTimeout bounds what runs on a connection to restore it.
const restoreTimeout = 5 * time.Second

// restoreContext is ctx without its cancellation, bounded: the context of what
// must run once ctx may have ended.
func restoreContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), restoreTimeout)
}

// restoreConn runs unset on c and hands c back to the pool, or closes the
// connection when unset fails, as it must not reach another statement unset.
func restoreConn(ctx context.Context, c *sql.Conn, unset string) {
	rctx, cancel := restoreContext(ctx)
	defer cancel()
	if _, err := c.ExecContext(rctx, unset); err != nil {
		_ = c.Raw(func(any) error { return driver.ErrBadConn })
	}
	_ = c.Close()
}

type sqlTx struct{ tx *sql.Tx }

func (t sqlTx) Commit(context.Context) error   { return t.tx.Commit() }
func (t sqlTx) Rollback(context.Context) error { return t.tx.Rollback() }

type sqlRows struct{ *sql.Rows }

func (r sqlRows) Close() { _ = r.Rows.Close() }
