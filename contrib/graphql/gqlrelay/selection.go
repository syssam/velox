package gqlrelay

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
)

// SelectedField is one field of a GraphQL selection as the engine executing
// the request sees it. Field collection reads selections only through it,
// so the eager loading, projection and count skipping it plans work under
// any engine that can describe what a query selected, not only gqlgen.
//
// An engine other than gqlgen supplies it with WithSelectionSource.
type SelectedField interface {
	// FieldName is the schema name of the field, not its alias.
	FieldName() string
	// Arguments returns the field's argument values with variables
	// substituted and defaults applied; an absent argument is left out.
	Arguments() map[string]any
	// Fields returns the fields selected beneath this one, one per
	// response key, with fragments applied. satisfies lists the object
	// type and the interfaces it implements when the field's type is
	// abstract; nil means every fragment applies.
	Fields(satisfies []string) []SelectedField
}

// SelectionSource returns the field being resolved in ctx, or false when ctx
// belongs to no resolver of the engine.
type SelectionSource func(ctx context.Context) (SelectedField, bool)

type selectionSourceKey struct{}

// WithSelectionSource returns a context whose field collection reads the
// selection from src instead of gqlgen's request context. An engine adapter
// installs it once per operation, so the generated CollectFields and
// Paginate methods project, eager-load and skip COUNT under that engine as
// they do under gqlgen.
func WithSelectionSource(ctx context.Context, src SelectionSource) context.Context {
	return context.WithValue(ctx, selectionSourceKey{}, src)
}

// defaultSource answers when the context carries no source of its own.
// gqlgenrelay sets it to gqlgen's request context when it is imported, which
// generated code does unless it was generated for another engine; this
// package imports no engine, so a server not built on gqlgen links none.
var defaultSource atomic.Pointer[SelectionSource]

// SetDefaultSelectionSource sets the source field collection reads when the
// context carries none (see WithSelectionSource). An engine adapter calls it
// from init; the last call wins.
func SetDefaultSelectionSource(src SelectionSource) {
	defaultSource.Store(&src)
}

// selectedField returns the field being resolved in ctx: from a source the
// context carries, which is explicit and so wins, or else from the default.
func selectedField(ctx context.Context) (SelectedField, bool) {
	src, installed := ctx.Value(selectionSourceKey{}).(SelectionSource)
	if installed && src != nil {
		if f, ok := src(ctx); ok {
			return f, true
		}
	}
	if def := defaultSource.Load(); def != nil && *def != nil {
		return (*def)(ctx)
	}
	if installed {
		// The engine's source has no field here: outside a resolver.
		return nil, false
	}
	warnNoSource.Do(func() {
		slog.Warn("velox: GraphQL field collection has no selection source, so it projects and eager-loads nothing: " +
			"code generated for gqlgen imports gqlgenrelay (regenerate), and another engine installs its own " +
			"(gqlrelay.WithSelectionSource; graphqlgo.Collect for graphql-go)")
	})
	return nil, false
}

// warnNoSource says once that collection is running blind. Without it, code
// generated before gqlgenrelay existed compiles, runs, and quietly goes
// back to a query per row.
var warnNoSource sync.Once

// SelectionCoveredByID reports whether the field being resolved in ctx
// selects nothing beyond __typename and id -- across every inline fragment,
// hence satisfies lists the interface and its implementor type names -- so a
// resolver can build the node from the foreign key it already holds instead
// of querying the target table. With no selection to read it reports false,
// and the caller queries.
func SelectionCoveredByID(ctx context.Context, satisfies ...string) bool {
	f, ok := selectedField(ctx)
	return ok && coveredByID(f, satisfies)
}
