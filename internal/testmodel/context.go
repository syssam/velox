package testmodel

import (
	"context"

	"github.com/syssam/velox/internal/testmodel/actor"
)

// Org is the organization the tests act for: the schema confines Invoice and
// Ticket to it (Ticket's Policy adds its own rules). It is actor.Org.
var Org = actor.Org

// WithUser returns ctx acting as user; "admin" sees and changes every ticket.
func WithUser(ctx context.Context, user string) context.Context { return actor.WithUser(ctx, user) }

// UserFromContext returns the user WithUser put in ctx, or "" if there is none.
func UserFromContext(ctx context.Context) string { return actor.UserFromContext(ctx) }
