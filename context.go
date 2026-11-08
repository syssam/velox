package velox

import (
	"context"

	"github.com/syssam/velox/internal/engine"
	"github.com/syssam/velox/veloxtype"
)

// WithScopeValue returns ctx recording that the policies read value under
// name, so that RowScopeFromContext shows it, for logs and for a batcher of
// an application's own. A later call with the same name replaces it. The
// scope package calls it. The GraphQL batcher does not need it: it keys a
// batch by the SQL each caller's context reads, the policies' conditions
// included.
func WithScopeValue(ctx context.Context, name string, value any) context.Context {
	return engine.WithScopeValue(ctx, name, value)
}

// RowScope is what a context scopes statements by.
type RowScope = veloxtype.RowScope

// RowScopeFromContext returns ctx's row scope.
func RowScopeFromContext(ctx context.Context) RowScope { return engine.RowScopeFromContext(ctx) }

// IsDryRun reports whether ctx is that of a SQL(ctx) method: the statement
// being built will not be sent. The SQL(ctx) methods mark the context before
// anything runs, so everything handed that context sees it: the entity's
// hooks (Hooks), the Policy functions and the Rewrite hooks. A Rewrite hook
// can also read it from Statement.DryRun.
//
// A write made under such a context is refused: Exec of an Insert, InsertAll,
// Update or Delete, Save, an upsert, DB.Exec (raw SQL) and Tx fail with
// ErrDryRun before anything is sent, on any *DB, so a hook's audit write through
// Mutation.DB does not happen and needs no check. Reads run as normal.
func IsDryRun(ctx context.Context) bool { return engine.IsDryRun(ctx) }
