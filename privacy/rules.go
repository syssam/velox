package privacy

import (
	"context"
	"fmt"
	"slices"

	"github.com/syssam/velox"
)

// Viewer represents the authenticated user making a request.
// This interface should be implemented by application-specific user types.
type Viewer interface {
	// GetID returns the viewer's unique identifier.
	GetID() string
	// GetRoles returns the viewer's roles.
	GetRoles() []string
	// GetTenantID returns the viewer's tenant identifier for multi-tenancy.
	// Returns empty string if not applicable.
	GetTenantID() string
}

// viewerCtxKey is the context key for storing the viewer.
type viewerCtxKey struct{}

// WithViewer returns a new context with the viewer attached.
func WithViewer(ctx context.Context, viewer Viewer) context.Context {
	return context.WithValue(ctx, viewerCtxKey{}, viewer)
}

// ViewerFromContext retrieves the viewer from the context.
// Returns nil if no viewer is present.
func ViewerFromContext(ctx context.Context) Viewer {
	v, _ := ctx.Value(viewerCtxKey{}).(Viewer)
	return v
}

// SimpleViewer is a basic implementation of the Viewer interface.
// Use this for testing or simple use cases.
type SimpleViewer struct {
	UserID   string
	Roles    []string
	TenantID string
}

// GetID returns the user ID.
func (v *SimpleViewer) GetID() string {
	return v.UserID
}

// GetRoles returns the user's roles.
func (v *SimpleViewer) GetRoles() []string {
	return v.Roles
}

// GetTenantID returns the tenant ID.
func (v *SimpleViewer) GetTenantID() string {
	return v.TenantID
}

// DenyIfNoViewer returns a rule that denies access if no viewer is present in the context.
// This is typically used as the first rule in a policy to require authentication.
//
// Example:
//
//	policy.Mutation(
//	    privacy.DenyIfNoViewer(),
//	    privacy.HasRole("admin"),
//	    privacy.AlwaysDenyRule(),
//	)
func DenyIfNoViewer() QueryMutationRule {
	return ContextQueryMutationRule(func(ctx context.Context) error {
		if ViewerFromContext(ctx) == nil {
			return Denyf("privacy: viewer required")
		}
		return Skip
	})
}

// HasRole returns a rule that allows access if the viewer has the specified role.
// Skips if the viewer doesn't have the role (allows next rule to evaluate).
//
// Example:
//
//	policy.Mutation(
//	    privacy.DenyIfNoViewer(),
//	    privacy.HasRole("admin"),
//	    privacy.AlwaysDenyRule(),
//	)
func HasRole(role string) QueryMutationRule {
	return ContextQueryMutationRule(func(ctx context.Context) error {
		viewer := ViewerFromContext(ctx)
		if viewer == nil {
			return Skip
		}
		if slices.Contains(viewer.GetRoles(), role) {
			return Allow
		}
		return Skip
	})
}

// HasAnyRole returns a rule that allows access if the viewer has any of the specified roles.
// Skips if the viewer doesn't have any of the roles (allows next rule to evaluate).
//
// Example:
//
//	policy.Mutation(
//	    privacy.DenyIfNoViewer(),
//	    privacy.HasAnyRole("admin", "moderator"),
//	    privacy.AlwaysDenyRule(),
//	)
func HasAnyRole(roles ...string) QueryMutationRule {
	return ContextQueryMutationRule(func(ctx context.Context) error {
		viewer := ViewerFromContext(ctx)
		if viewer == nil {
			return Skip
		}
		viewerRoles := viewer.GetRoles()
		for _, role := range roles {
			if slices.Contains(viewerRoles, role) {
				return Allow
			}
		}
		return Skip
	})
}

// IsOwner returns a mutation rule that allows access if the viewer owns the entity.
// The rule checks if the mutation's field value matches the viewer's ID.
//
// Example:
//
//	policy.Mutation(
//	    privacy.DenyIfNoViewer(),
//	    privacy.IsOwner("user_id"),
//	    privacy.AlwaysDenyRule(),
//	)
func IsOwner(field string) MutationRule {
	return MutationRuleFunc(func(ctx context.Context, m velox.Mutation) error {
		viewer := ViewerFromContext(ctx)
		if viewer == nil {
			return Skip
		}
		// Check if the field value matches the viewer's ID
		value, ok := m.Field(field)
		if !ok {
			return Skip
		}
		// Handle different ID types
		var fieldID string
		switch v := value.(type) {
		case string:
			fieldID = v
		case int64:
			fieldID = fmt.Sprintf("%d", v)
		case int:
			fieldID = fmt.Sprintf("%d", v)
		default:
			fieldID = fmt.Sprintf("%v", v)
		}
		if fieldID == viewer.GetID() {
			return Allow
		}
		return Skip
	})
}

// OwnerQueryRule returns a query rule that filters queries to only return entities
// owned by the viewer. This is useful for implementing row-level security.
//
// Note: This rule only checks context; actual filtering must be done by the query.
// Use this as a guard that denies if the viewer is missing.
//
// Example:
//
//	policy.Query(
//	    privacy.OwnerQueryRule(),
//	)
func OwnerQueryRule() QueryRule {
	return queryRuleFunc(func(ctx context.Context, _ velox.Query) error {
		if ViewerFromContext(ctx) == nil {
			return Denyf("privacy: viewer required for owner-filtered query")
		}
		return Skip
	})
}

// TenantRule returns a mutation rule that allows access if the viewer's tenant
// matches the entity's tenant. Used for multi-tenant isolation.
//
// Example:
//
//	policy.Mutation(
//	    privacy.DenyIfNoViewer(),
//	    privacy.TenantRule("tenant_id"),
//	    privacy.AlwaysDenyRule(),
//	)
func TenantRule(field string) MutationRule {
	return MutationRuleFunc(func(ctx context.Context, m velox.Mutation) error {
		viewer := ViewerFromContext(ctx)
		if viewer == nil {
			return Skip
		}
		viewerTenant := viewer.GetTenantID()
		if viewerTenant == "" {
			return Skip
		}
		// Check if the field value matches the viewer's tenant
		value, ok := m.Field(field)
		if !ok {
			return Skip
		}
		// Handle different tenant ID types
		var fieldTenant string
		switch v := value.(type) {
		case string:
			fieldTenant = v
		default:
			fieldTenant = fmt.Sprintf("%v", v)
		}
		if fieldTenant == viewerTenant {
			return Allow
		}
		return Denyf("privacy: tenant mismatch")
	})
}

// TenantQueryRule returns a query rule that denies queries if no viewer
// or tenant is present. Use this as a guard for tenant-filtered queries.
//
// Example:
//
//	policy.Query(
//	    privacy.TenantQueryRule(),
//	)
func TenantQueryRule() QueryRule {
	return queryRuleFunc(func(ctx context.Context, _ velox.Query) error {
		viewer := ViewerFromContext(ctx)
		if viewer == nil {
			return Denyf("privacy: viewer required for tenant-filtered query")
		}
		if viewer.GetTenantID() == "" {
			return Denyf("privacy: tenant required")
		}
		return Skip
	})
}

// AllowMutationOperationRule returns a rule allowing specified mutation operation.
func AllowMutationOperationRule(op velox.Op) MutationRule {
	rule := MutationRuleFunc(func(_ context.Context, _ velox.Mutation) error {
		return Allow
	})
	return OnMutationOperation(rule, op)
}

// queryRuleFunc is a function adapter for QueryRule.
type queryRuleFunc func(context.Context, velox.Query) error

func (f queryRuleFunc) EvalQuery(ctx context.Context, q velox.Query) error {
	return f(ctx, q)
}
