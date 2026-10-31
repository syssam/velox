// Package gen holds the tour's entities. The *_velox.go files are written by
// go tool velox-gen; the other files are written by hand: this one carries the
// billing role of a request, and invoice.go and line.go the policies and the
// hook, which velox-gen binds into the entities' metadata.
package gen

import (
	"context"

	"github.com/syssam/velox"
)

// Role is what a user may do with billing data.
type Role string

// The billing roles.
const (
	RoleViewer     Role = "viewer"     // reads sent and paid invoices
	RoleAccountant Role = "accountant" // reads and writes everything
)

type roleKey struct{}

// WithRole returns ctx acting with role. In a service, authentication
// middleware sets it, next to velox.WithTenant.
func WithRole(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey{}, role)
}

// RoleFromContext returns the role ctx acts with; "" when none.
func RoleFromContext(ctx context.Context) Role {
	r, _ := ctx.Value(roleKey{}).(Role)
	return r
}

// requireAccountant denies a write by anyone but an accountant.
func requireAccountant(ctx context.Context) error {
	if RoleFromContext(ctx) != RoleAccountant {
		return velox.Deny("only accountants change billing data")
	}
	return nil
}
