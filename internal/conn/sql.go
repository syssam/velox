package conn

import (
	"context"
	"database/sql"
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

func (e SQL) Begin(ctx context.Context, o sql.TxOptions) (Executor, Tx, error) {
	tx, err := e.db.BeginTx(ctx, &o)
	if err != nil {
		return nil, nil, err
	}
	return SQL{c: tx, cols: e.cols}, sqlTx{tx}, nil
}

type sqlTx struct{ tx *sql.Tx }

func (t sqlTx) Commit(context.Context) error   { return t.tx.Commit() }
func (t sqlTx) Rollback(context.Context) error { return t.tx.Rollback() }

type sqlRows struct{ *sql.Rows }

func (r sqlRows) Close() { _ = r.Rows.Close() }
