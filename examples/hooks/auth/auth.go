// Package auth is what the hooks example shares between entities: the user of
// a request, which authentication middleware would put in the context, and a
// generic hook that fills a column from it. It is written by hand and does not
// import gen, so gen's hooks can import it.
package auth

import (
	"context"

	"github.com/syssam/velox"
)

type userKey struct{}

// WithUser returns ctx acting as user, as authentication middleware would.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// UserFromContext returns the user WithUser put in ctx, or "" if there is none.
func UserFromContext(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }

// CreatedBy returns a hook that fills a column from the context's user on each
// inserted row where it is empty. field says which column: it returns a
// pointer to it in a row of E. The hook is generic, so any entity with such a
// column lists it in its Hooks:
//
//	velox.On(auth.CreatedBy(func(t *Task) *string { return &t.CreatedBy }), velox.OpInsert)
//
// This is what replaces a mixin's own hook: the CreatedBy mixin in the schema
// adds the column, and each entity that wants it lists this function.
func CreatedBy[E any](field func(*E) *string) velox.Hook[E] {
	return func(next velox.Mutator[E]) velox.Mutator[E] {
		return velox.MutateFunc[E](func(ctx context.Context, m *velox.Mutation[E]) error {
			for _, x := range m.Rows() {
				if p := field(x); *p == "" {
					*p = UserFromContext(ctx)
				}
			}
			return next.Mutate(ctx, m)
		})
	}
}
