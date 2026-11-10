// Package auth holds the hooks example's request user, which authentication
// middleware would put in the context. The schema's CreatedBy mixin fills
// created_by from it. It is written by hand and does not import veloxgen.
package auth

import "context"

type userKey struct{}

// WithUser returns ctx acting as user, as authentication middleware would.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// UserFromContext returns the user WithUser put in ctx, or "" if there is none.
func UserFromContext(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }
