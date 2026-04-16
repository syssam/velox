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
	// ID returns the viewer's unique identifier.
	ID() string
	// Roles returns the viewer's roles.
	Roles() []string
}

// TenantIDer is an optional interface that Viewer implementations can satisfy
// to support multi-tenancy. TenantRule and TenantQueryRule check for this
// interface via type assertion, so single-tenant applications don't need to
// implement it.
type TenantIDer interface {
	// TenantID returns the viewer's tenant identifier for multi-tenancy.
	TenantID() string
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

// SimpleViewer is a basic implementation of the Viewer and TenantIDer interfaces.
// Use this for testing or simple use cases where a full Viewer implementation
// is not needed.
type SimpleViewer struct {
	UserID     string
	UserRoles  []string
	UserTenant string
}

// Compile-time interface assertions.
var (
	_ Viewer     = (*SimpleViewer)(nil)
	_ TenantIDer = (*SimpleViewer)(nil)
)

// ID returns the user ID.
func (v *SimpleViewer) ID() string {
	return v.UserID
}

// Roles returns the user's roles.
func (v *SimpleViewer) Roles() []string {
	return v.UserRoles
}

// TenantID returns the tenant ID.
func (v *SimpleViewer) TenantID() string {
	return v.UserTenant
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
		if slices.Contains(viewer.Roles(), role) {
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
		viewerRoles := viewer.Roles()
		for _, role := range roles {
			if slices.Contains(viewerRoles, role) {
				return Allow
			}
		}
		return Skip
	})
}

// IsOwner returns a mutation rule that allows access if the viewer owns the entity.
// For creates, checks if the field value matches the viewer's ID.
// For updates/deletes, always skips — ownership cannot be verified without a DB query.
// Use FilterFunc with a WHERE predicate for update-time ownership checks.
//
// WARNING: This rule only protects create operations. For updates/deletes, the
// mutation's Field() returns the NEW value, which an attacker could set to their
// own ID. You MUST pair IsOwner with a FilterFunc that adds a WHERE predicate
// to verify ownership against the existing row:
//
//	privacy.FilterFunc(func(ctx context.Context, f privacy.Filter) error {
//	    viewer := privacy.ViewerFromContext(ctx)
//	    if viewer == nil { return privacy.Deny }
//	    f.WhereP(func(s *sql.Selector) {
//	        s.Where(sql.EQ(s.C("user_id"), viewer.ID()))
//	    })
//	    return privacy.Skip
//	})
//
// If the ownership field is not set on a create, this rule skips (does not deny).
// Use AlwaysDenyRule() as the final rule to catch this case.
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
		// For updates/deletes, we cannot verify ownership without a DB query.
		// The mutation's Field() returns the NEW value, which an attacker
		// could set to their own ID. Skip to defer to other rules
		// (typically a FilterFunc with a WHERE predicate).
		if m.Op().Is(velox.OpUpdate) || m.Op().Is(velox.OpUpdateOne) ||
			m.Op().Is(velox.OpDelete) || m.Op().Is(velox.OpDeleteOne) {
			return Skip
		}
		// For creates, check the value being set.
		// If the field is not set, skip to the next rule (AlwaysDenyRule will catch it).
		value, ok := m.Field(field)
		if !ok {
			return Skip
		}
		if stringifyValue(value) == viewer.ID() {
			return Allow
		}
		return Skip
	})
}

// IsOwnerOnCreate returns a mutation rule that allows access if the viewer
// owns the entity being created. Unlike IsOwner, this function is strict
// about type safety: unsupported field value types cause a Deny rather than
// a silent Skip, preventing security false-negatives.
//
// Supported value types: string, int, int64, uint, uint64, fmt.Stringer.
// Unsupported types (e.g., [16]byte, float64, struct) are denied.
//
// For updates/deletes, always skips — ownership cannot be verified without
// a DB query. Use FilterFunc with a WHERE predicate for those operations.
func IsOwnerOnCreate(field string) MutationRule {
	return MutationRuleFunc(func(ctx context.Context, m velox.Mutation) error {
		viewer := ViewerFromContext(ctx)
		if viewer == nil {
			return Skip
		}
		// For updates/deletes, skip (same reasoning as IsOwner).
		if m.Op().Is(velox.OpUpdate) || m.Op().Is(velox.OpUpdateOne) ||
			m.Op().Is(velox.OpDelete) || m.Op().Is(velox.OpDeleteOne) {
			return Skip
		}
		value, ok := m.Field(field)
		if !ok {
			return Skip
		}
		s, ok := ownerStringify(value)
		if !ok {
			return Denyf("privacy: unsupported owner field type %T", value)
		}
		if s == viewer.ID() {
			return Allow
		}
		return Skip
	})
}

// ownerStringify converts a value to string for ownership comparison.
// Returns (string, true) for supported types, ("", false) for unsupported.
func ownerStringify(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case int:
		return fmt.Sprintf("%d", v), true
	case int64:
		return fmt.Sprintf("%d", v), true
	case uint:
		return fmt.Sprintf("%d", v), true
	case uint64:
		return fmt.Sprintf("%d", v), true
	case fmt.Stringer:
		return v.String(), true
	default:
		return "", false
	}
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
	return QueryRuleFunc(func(ctx context.Context, _ velox.Query) error {
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
		// For updates/deletes, we cannot verify tenant ownership without a DB query.
		// The mutation's Field() returns the NEW value, which could be spoofed.
		// Skip to defer to other rules (typically a FilterFunc with a WHERE predicate).
		if m.Op().Is(velox.OpUpdate) || m.Op().Is(velox.OpUpdateOne) ||
			m.Op().Is(velox.OpDelete) || m.Op().Is(velox.OpDeleteOne) {
			return Skip
		}
		tv, ok := viewer.(TenantIDer)
		if !ok {
			return Skip
		}
		viewerTenant := tv.TenantID()
		if viewerTenant == "" {
			return Skip
		}
		value, ok := m.Field(field)
		if !ok {
			return Skip
		}
		if stringifyValue(value) == viewerTenant {
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
	return QueryRuleFunc(func(ctx context.Context, _ velox.Query) error {
		viewer := ViewerFromContext(ctx)
		if viewer == nil {
			return Denyf("privacy: viewer required for tenant-filtered query")
		}
		tv, ok := viewer.(TenantIDer)
		if !ok || tv.TenantID() == "" {
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

// QueryRuleFunc is a function adapter for QueryRule.
type QueryRuleFunc func(context.Context, velox.Query) error

// EvalQuery calls f(ctx, q).
func (f QueryRuleFunc) EvalQuery(ctx context.Context, q velox.Query) error {
	err := f(ctx, q)
	RecordTrace(ctx, "QueryRuleFunc", decisionString(err))
	return err
}

// stringifyValue converts a field value to its string representation for comparison.
func stringifyValue(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case int64:
		return fmt.Sprintf("%d", v)
	case int:
		return fmt.Sprintf("%d", v)
	case fmt.Stringer:
		return v.String()
	default:
		return fmt.Sprintf("%v", v)
	}
}
