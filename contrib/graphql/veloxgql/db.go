package veloxgql

import (
	"context"
	"fmt"

	"github.com/syssam/velox"
)

// state is what the extension puts in an operation's context: the DB and
// the limits, as one value. A mutation's transaction replaces the DB and
// keeps the limits.
type state struct {
	db     *velox.DB
	limits Limits
}

// stateKey is the context key of the *state.
type stateKey struct{}

// withState returns ctx with db and limits.
func withState(ctx context.Context, db *velox.DB, limits Limits) context.Context {
	return context.WithValue(ctx, stateKey{}, &state{db: db, limits: limits})
}

// DBFromContext returns the DB the extension put in ctx. It fails with ErrNoDB
// when there is none: the schema is served without veloxgql.NewExtension, which
// puts it there.
func DBFromContext(ctx context.Context) (*velox.DB, error) {
	s, _ := ctx.Value(stateKey{}).(*state)
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("veloxgql: %w: serve the schema through veloxgql.NewExtension, which puts the DB in each request's context", ErrNoDB)
	}
	return s.db, nil
}

// DB returns the DB the extension put in ctx, for a resolver:
//
//	return veloxgql.DB(ctx).Query[gen.Post]().Paginate(ctx, after, first, before, last, gen.WithPostOrder(orderBy), gen.WithPostFilter(where))
//
// It panics where DBFromContext fails, with DBFromContext's error: only a
// schema served without veloxgql.NewExtension has no DB, a mistake in the
// program, which gqlgen's recover reports as an internal error.
func DB(ctx context.Context) *velox.DB {
	db, err := DBFromContext(ctx)
	if err != nil {
		panic(err)
	}
	return db
}
