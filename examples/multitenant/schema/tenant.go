// Package schema defines the entity schemas for the multi-tenant example.
package schema

import (
	"context"
	"errors"

	"github.com/syssam/velox"
	"github.com/syssam/velox/privacy"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// TenantMixin gives an entity a tenant column and the write-side rules that
// keep it honest. Embed it in every tenant-scoped schema; declaring it once
// is what makes this tractable at a few hundred entities.
//
// Reads are deliberately NOT scoped here — see the package README. A Policy
// is process-global, so scoping reads through it narrows every read in the
// process, including the ones that guard an invariant. Those are scoped by
// registering an interceptor on a dedicated client instead.
type TenantMixin struct {
	mixin.Schema
}

// Fields of the TenantMixin.
func (TenantMixin) Fields() []velox.Field {
	return []velox.Field{
		// Immutable: a row never moves between tenants. The value is
		// stamped by the hook below, not supplied by the caller.
		field.String("tenant_id").
			Immutable().
			NotEmpty(),
	}
}

// Policy of the TenantMixin — the write side.
//
// Global scope is correct here: there is no such thing as a legitimate
// *silently* cross-tenant write. An internal job that must write across
// tenants says so by carrying the system role, and forgetting to is a loud
// denial rather than a silent escalation.
func (TenantMixin) Policy() velox.Policy {
	return privacy.Policy{
		Mutation: privacy.MutationPolicy{
			// No viewer at all is a bug, not a background job.
			privacy.DenyIfNoViewer(),
			// Internal jobs opt out explicitly, at the call site.
			privacy.HasRole(RoleSystem),
			// Everyone else: UPDATE and DELETE are constrained to their
			// own tenant, including bulk forms with no Where() of their own.
			privacy.TenantFilterRule("tenant_id"),
		},
	}
}

// Hooks of the TenantMixin.
//
// The tenant column is stamped from the viewer on create. This closes two
// holes at once: a create that omits the column would otherwise pass every
// rule and land a row with an empty tenant that no tenant can read, and a
// caller could otherwise plant a row in someone else's tenant by setting the
// field themselves. Overwriting unconditionally makes it unspoofable.
func (TenantMixin) Hooks() []velox.Hook {
	return []velox.Hook{
		func(next velox.Mutator) velox.Mutator {
			return velox.MutateFunc(func(ctx context.Context, m velox.Mutation) (velox.Value, error) {
				if !m.Op().Is(velox.OpCreate) {
					return next.Mutate(ctx, m)
				}
				viewer, _ := privacy.ViewerFromContext(ctx).(privacy.TenantIDer)
				if viewer == nil || viewer.TenantID() == "" {
					return nil, errors.New("multitenant: create requires a viewer carrying a tenant")
				}
				if err := m.SetField("tenant_id", viewer.TenantID()); err != nil {
					return nil, err
				}
				return next.Mutate(ctx, m)
			})
		},
	}
}
