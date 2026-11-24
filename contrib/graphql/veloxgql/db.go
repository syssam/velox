package veloxgql

import (
	"context"
	"fmt"

	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql/veloxgql/internal/gqlcore"
)

// DBFromContext returns the DB an adapter (gqlgenx.NewExtension,
// graphqlgo.With) or Service put in ctx. It fails with ErrNoDB when there is
// none. Generated code does not need it (decision 0173): a query carries
// its DB, and a row the DB that read it.
func DBFromContext(ctx context.Context) (*velox.DB, error) {
	db := gqlcore.DB(ctx)
	if db == nil {
		return nil, fmt.Errorf("veloxgql: %w: use the DB the resolver holds, or put one in the context with veloxgql.Service, gqlgenx.NewExtension or graphqlgo.With", ErrNoDB)
	}
	return db, nil
}

// DB returns the DB an adapter put in ctx, in a gqlgen mutation the
// mutation's transaction, for code outside a resolver that has only the
// context:
//
//	if err := veloxgql.DB(ctx).Insert(x).SetInput(input).Exec(ctx); err != nil {
//
// A resolver reads and writes on the DB its Resolver holds (r.DB): under
// gqlgenx's extension, in a mutation, a statement on it under the
// resolver's ctx runs in the mutation's transaction. The rows either
// returns carry the DB that read them, and their edges read on it.
//
// It panics where DBFromContext fails, with DBFromContext's error: a
// context nothing put a DB in. A server with no adapter has none, and its
// code uses the DB it holds.
func DB(ctx context.Context) *velox.DB {
	db, err := DBFromContext(ctx)
	if err != nil {
		panic(err)
	}
	return db
}
