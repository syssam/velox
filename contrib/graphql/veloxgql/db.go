package veloxgql

import (
	"context"
	"fmt"

	"github.com/syssam/velox2"
)

// dbKey is the context key of the DB.
type dbKey struct{}

// WithDB returns ctx with db, which the generated code reads with DBFrom.
// The handler calls it for each request.
func WithDB(ctx context.Context, db *velox.DB) context.Context {
	return context.WithValue(ctx, dbKey{}, db)
}

// DBFrom returns the DB that WithDB put in ctx. It fails with ErrNoDB when
// there is none: the schema is served without veloxgql.Handler, which puts
// it there.
func DBFrom(ctx context.Context) (*velox.DB, error) {
	db, _ := ctx.Value(dbKey{}).(*velox.DB)
	if db == nil {
		return nil, fmt.Errorf("veloxgql: %w: serve the schema through veloxgql.Handler, which puts the DB in each request's context", ErrNoDB)
	}
	return db, nil
}
