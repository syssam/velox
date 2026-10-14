package velox

import (
	"context"
	"database/sql"

	"github.com/jackc/pgx/v5"
)

// rows is what an executor returns; *sql.Rows (wrapped) and pgx.Rows both
// provide it.
type rows interface {
	Next() bool
	Scan(dst ...any) error
	Err() error
	Close()
}

// executor runs rendered statements.
type executor interface {
	query(ctx context.Context, sql string, args []any) (rows, error)
}

type sqlQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// sqlExecutor runs statements through database/sql.
type sqlExecutor struct{ q sqlQueryer }

func (e sqlExecutor) query(ctx context.Context, s string, args []any) (rows, error) {
	r, err := e.q.QueryContext(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return sqlRows{r}, nil
}

type sqlRows struct{ *sql.Rows }

func (r sqlRows) Close() { _ = r.Rows.Close() }

type pgxQueryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// pgxExecutor runs statements through pgx's native protocol.
type pgxExecutor struct{ q pgxQueryer }

func (e pgxExecutor) query(ctx context.Context, s string, args []any) (rows, error) {
	r, err := e.q.Query(ctx, s, args...)
	if err != nil {
		return nil, err
	}
	return r, nil
}
