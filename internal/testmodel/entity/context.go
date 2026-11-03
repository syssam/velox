// Package entity holds testmodel's entities, which testmodel re-exports, and
// the context helpers its schema's policies read.
package entity

import (
	"context"

	"github.com/syssam/velox/scope"
)

// Org is the organization the tests act for: the schema confines Invoice and
// Ticket to it (Ticket's Policy wraps the scope with its own rules).
var Org = scope.NewKey[int64]("org")

// userKey is the context key of the user the tests act as.
type userKey struct{}

// WithUser returns ctx acting as user; "admin" sees and changes every ticket.
func WithUser(ctx context.Context, user string) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}

// UserFromContext returns the user WithUser put in ctx, or "" if there is none.
func UserFromContext(ctx context.Context) string { u, _ := ctx.Value(userKey{}).(string); return u }
