package veloxgql

import (
	"context"
	"fmt"

	"github.com/syssam/velox2"
	"github.com/syssam/velox2/contrib/graphql/veloxgql/internal/shared"
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

// withState returns ctx with db and limits, and a new table of the rows
// the operation reads, whose edges batch (Batch).
func withState(ctx context.Context, db *velox.DB, limits Limits) context.Context {
	return context.WithValue(shared.WithTable(ctx), stateKey{}, &state{db: db, limits: limits})
}

// DBFrom returns the DB the extension put in ctx. It fails with ErrNoDB when
// there is none: the schema is served without veloxgql.NewExtension, which
// puts it there.
func DBFrom(ctx context.Context) (*velox.DB, error) {
	s, _ := ctx.Value(stateKey{}).(*state)
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("veloxgql: %w: serve the schema through veloxgql.NewExtension, which puts the DB in each request's context", ErrNoDB)
	}
	return s.db, nil
}
