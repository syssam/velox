package gqlrelay

import (
	"context"

	"github.com/99designs/gqlgen/graphql"
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

// selectedField returns the field being resolved in ctx: from a source the
// context carries, which is explicit and so wins, or else from gqlgen.
func selectedField(ctx context.Context) (SelectedField, bool) {
	if src, ok := ctx.Value(selectionSourceKey{}).(SelectionSource); ok && src != nil {
		if f, ok := src(ctx); ok {
			return f, true
		}
	}
	fc := graphql.GetFieldContext(ctx)
	if fc == nil || !graphql.HasOperationContext(ctx) {
		return nil, false
	}
	return gqlgenField{oc: graphql.GetOperationContext(ctx), f: fc.Field}, true
}

// gqlgenField is SelectedField over gqlgen's collected fields.
type gqlgenField struct {
	oc *graphql.OperationContext
	f  graphql.CollectedField
}

func (g gqlgenField) FieldName() string { return g.f.Name }

func (g gqlgenField) Arguments() map[string]any {
	if g.f.Field == nil {
		return nil
	}
	return g.f.ArgumentMap(g.oc.Variables)
}

func (g gqlgenField) Fields(satisfies []string) []SelectedField {
	collected := graphql.CollectFields(g.oc, g.f.Selections, satisfies)
	out := make([]SelectedField, len(collected))
	for i, f := range collected {
		out[i] = gqlgenField{oc: g.oc, f: f}
	}
	return out
}
