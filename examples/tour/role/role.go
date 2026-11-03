// Package role carries the billing role of a request, which the schema's
// policies read. It does not import gen, so that anything may import it.
package role

import (
	"context"

	"github.com/syssam/velox"
)

// Role is what a user may do with billing data.
type Role string

// The billing roles.
const (
	Viewer     Role = "viewer"     // reads sent and paid invoices
	Accountant Role = "accountant" // reads and writes everything
)

type roleKey struct{}

// With returns ctx acting with role. In a service, authentication middleware
// sets it, next to the scope key's With.
func With(ctx context.Context, role Role) context.Context {
	return context.WithValue(ctx, roleKey{}, role)
}

// FromContext returns the role ctx acts with; "" when none.
func FromContext(ctx context.Context) Role {
	r, _ := ctx.Value(roleKey{}).(Role)
	return r
}

// RequireAccountant denies a write by anyone but an accountant.
func RequireAccountant(ctx context.Context) error {
	if FromContext(ctx) != Accountant {
		return velox.Deny("only accountants change billing data")
	}
	return nil
}
