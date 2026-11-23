package veloxgql

import (
	"context"
	"fmt"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// DBFromContext returns the DB an adapter put in ctx. It fails with ErrNoDB
// when there is none: the schema is served without an adapter
// (gqlgenx.NewExtension for gqlgen, graphqlgo.With for graphql-go), which
// puts it there; outside a request, veloxgql.Service puts it there.
func DBFromContext(ctx context.Context) (*velox.DB, error) {
	db := gqlcore.DB(ctx)
	if db == nil {
		return nil, fmt.Errorf("veloxgql: %w: serve the schema through gqlgenx.NewExtension or graphqlgo.With, which puts the DB in each request's context, or, outside a request, call veloxgql.Service", ErrNoDB)
	}
	return db, nil
}

// DB returns the DB an adapter put in ctx, for a resolver:
//
//	return veloxgql.DB(ctx).Query[veloxgen.Post]().Paginate(ctx, after, first, before, last, veloxgen.WithPostOrder(orderBy), veloxgen.WithPostFilter(where))
//
// It panics where DBFromContext fails, with DBFromContext's error: only a
// schema served without an adapter has no DB, a mistake in the program,
// which the server's recover reports as an internal error.
func DB(ctx context.Context) *velox.DB {
	db, err := DBFromContext(ctx)
	if err != nil {
		panic(err)
	}
	return db
}
