package gen

import (
	"context"

	"github.com/syssam/velox"
)

// createdBy returns a hook that fills a column from the context's user on each
// inserted row where it is empty. field says which column: it returns a
// pointer to it in a row of E. The hook is generic, so any entity with such a
// column lists it in its Hooks:
//
//	velox.On(createdBy(func(t *Task) *string { return &t.CreatedBy }), velox.OpInsert)
//
// This is what replaces a mixin's own hook: the CreatedBy mixin in the schema
// adds the column, and each entity that wants it filled lists this function.
func createdBy[E any](field func(*E) *string) velox.Hook[E] {
	return func(next velox.Mutator[E]) velox.Mutator[E] {
		return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
			for _, x := range m.Rows() {
				if p := field(x); *p == "" {
					*p = userFromContext(ctx)
				}
			}
			return next.Mutate(ctx, m)
		})
	}
}
