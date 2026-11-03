// Package auth carries the user of a request, which authentication
// middleware would put in the context, and which the schema's policies read.
// It does not import gen, so that anything may import it.
package auth

import "context"

type userKey struct{}

// WithUser returns ctx acting as user. In a service, authentication middleware
// sets it. The user "admin" may read and change everything.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// UserFromContext returns the user WithUser put in ctx, or "" if there is none.
func UserFromContext(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }
