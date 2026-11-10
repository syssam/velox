// Package conn is what velox runs statements through. It is internal so that
// velox's driver packages can implement it without it being an API: nothing
// outside this module can name these types, so they can change in any release.
package conn

import (
	"context"
	"database/sql"
)

// Rows is what an Executor returns for a query; *sql.Rows (wrapped) and
// pgx.Rows both provide it.
type Rows interface {
	Next() bool
	Scan(dst ...any) error
	Err() error
	Close()
}

// Result is what a statement that returns no rows reports.
type Result struct {
	Affected  int64
	LastID    int64
	HasLastID bool // the driver reports LastInsertId (MySQL, SQLite)
}

// Tx ends a transaction.
type Tx interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// Adopter is an Executor that can run on a transaction its caller began on a
// *sql.DB: SQL can, and so can what wraps it. A driver package's own executor,
// veloxpgx's, cannot: its statements go through a pgx connection, not a *sql.Tx.
type Adopter interface {
	// Adopt returns the Executor that runs statements on tx, and false when
	// the Executor cannot.
	Adopt(tx *sql.Tx) (Executor, bool)
}

// Executor runs rendered statements.
type Executor interface {
	Query(ctx context.Context, sql string, args []any) (Rows, error)
	Exec(ctx context.Context, sql string, args []any) (Result, error)
	// Begin starts a transaction and returns the Executor that runs in it.
	Begin(ctx context.Context, o sql.TxOptions) (Executor, Tx, error)
}
