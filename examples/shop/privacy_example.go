//go:build ignore

// This file contains example privacy policies for the shop application.
// Copy and modify these examples for your own use.

package main

import (
	"context"

	"example.com/shop/velox"
	"example.com/shop/velox/privacy"

	privacybase "github.com/syssam/velox/privacy"
)

// =============================================================================
// VIEWER CONTEXT
// =============================================================================

// Viewer represents the current user making the request.
// Implement this interface to provide user context to privacy rules.
type Viewer interface {
	// UserID returns the current user's ID
	UserID() string
	// TenantID returns the tenant for multi-tenancy
	TenantID() string
	// Roles returns the user's roles
	Roles() []string
	// HasRole checks if user has a specific role
	HasRole(role string) bool
	// IsAdmin checks if user is an admin
	IsAdmin() bool
}

// SimpleViewer is a basic implementation of the Viewer interface.
type SimpleViewer struct {
	ID        string
	Tenant    string
	UserRoles []string
}

func (v *SimpleViewer) UserID() string   { return v.ID }
func (v *SimpleViewer) TenantID() string { return v.Tenant }
func (v *SimpleViewer) Roles() []string  { return v.UserRoles }

func (v *SimpleViewer) HasRole(role string) bool {
	for _, r := range v.UserRoles {
		if r == role {
			return true
		}
	}
	return false
}

func (v *SimpleViewer) IsAdmin() bool {
	return v.HasRole("admin")
}

// ViewerFromContext extracts the Viewer from context.
func ViewerFromContext(ctx context.Context) (Viewer, bool) {
	v := privacybase.FromContext(ctx)
	if v == nil {
		return nil, false
	}
	viewer, ok := v.(Viewer)
	return viewer, ok
}

// WithViewer adds a Viewer to the context.
func WithViewer(ctx context.Context, v Viewer) context.Context {
	return privacybase.NewContext(ctx, v)
}

// =============================================================================
// PRIVACY RULES
// =============================================================================

// DenyIfNoViewer denies access if no viewer is set in context.
func DenyIfNoViewer() privacy.QueryMutationRule {
	return privacy.ContextQueryMutationRule(func(ctx context.Context) error {
		if _, ok := ViewerFromContext(ctx); !ok {
			return privacy.Deny
		}
		return privacy.Skip
	})
}

// AllowIfAdmin allows access for admin users.
func AllowIfAdmin() privacy.QueryMutationRule {
	return privacy.ContextQueryMutationRule(func(ctx context.Context) error {
		v, ok := ViewerFromContext(ctx)
		if ok && v.IsAdmin() {
			return privacy.Allow
		}
		return privacy.Skip
	})
}

// AllowIfRole allows access if user has any of the specified roles.
func AllowIfRole(roles ...string) privacy.QueryMutationRule {
	return privacy.ContextQueryMutationRule(func(ctx context.Context) error {
		v, ok := ViewerFromContext(ctx)
		if !ok {
			return privacy.Skip
		}
		for _, role := range roles {
			if v.HasRole(role) {
				return privacy.Allow
			}
		}
		return privacy.Skip
	})
}

// =============================================================================
// ENTITY-SPECIFIC POLICIES
// =============================================================================

// UserPolicy defines privacy rules for User entity.
// Add this to your schema/user.go file.
//
// Example:
//
//	func (User) Policy() velox.Policy {
//	    return UserPolicy()
//	}
func UserPolicy() velox.Policy {
	return velox.Policy{
		Query: velox.QueryPolicy{
			// Allow admins full access
			AllowIfAdmin(),
			// Users can only query their own data
			privacy.UserQueryRuleFunc(func(ctx context.Context, q *velox.UserQuery) error {
				v, ok := ViewerFromContext(ctx)
				if !ok {
					return privacy.Deny
				}
				// Filter to only user's own data
				q.Where(velox.UserIDEQ(v.UserID()))
				return privacy.Skip
			}),
			// Deny by default
			privacy.AlwaysDenyRule(),
		},
		Mutation: velox.MutationPolicy{
			// Require authentication
			DenyIfNoViewer(),
			// Allow admins
			AllowIfAdmin(),
			// Users can only update their own data
			privacy.UserMutationRuleFunc(func(ctx context.Context, m *velox.UserMutation) error {
				v, ok := ViewerFromContext(ctx)
				if !ok {
					return privacy.Deny
				}
				// Check if user is updating themselves
				id, exists := m.ID()
				if exists && id == v.UserID() {
					return privacy.Allow
				}
				return privacy.Skip
			}),
			// Deny by default
			privacy.AlwaysDenyRule(),
		},
	}
}

// OrderPolicy defines privacy rules for Order entity.
func OrderPolicy() velox.Policy {
	return velox.Policy{
		Query: velox.QueryPolicy{
			// Allow admins and managers
			AllowIfRole("admin", "manager"),
			// Customers can only see their own orders
			privacy.OrderQueryRuleFunc(func(ctx context.Context, q *velox.OrderQuery) error {
				v, ok := ViewerFromContext(ctx)
				if !ok {
					return privacy.Deny
				}
				// Filter to orders belonging to this user
				q.Where(velox.OrderHasCustomerWith(
					velox.CustomerUserIDEQ(v.UserID()),
				))
				return privacy.Skip
			}),
			privacy.AlwaysDenyRule(),
		},
		Mutation: velox.MutationPolicy{
			DenyIfNoViewer(),
			AllowIfRole("admin", "manager"),
			// Customers can create orders
			privacy.OnMutationOperation(
				privacy.OrderMutationRuleFunc(func(ctx context.Context, m *velox.OrderMutation) error {
					if m.Op().Is(velox.OpCreate) {
						return privacy.Allow
					}
					return privacy.Skip
				}),
				velox.OpCreate,
			),
			privacy.AlwaysDenyRule(),
		},
	}
}

// ProductPolicy defines privacy rules for Product entity.
func ProductPolicy() velox.Policy {
	return velox.Policy{
		Query: velox.QueryPolicy{
			// Products are publicly readable
			privacy.AlwaysAllowQueryRule(),
		},
		Mutation: velox.MutationPolicy{
			DenyIfNoViewer(),
			// Only admins and managers can modify products
			AllowIfRole("admin", "manager"),
			privacy.AlwaysDenyRule(),
		},
	}
}

// =============================================================================
// MULTI-TENANCY POLICY
// =============================================================================

// TenantIsolationRule ensures users can only access their tenant's data.
func TenantIsolationRule() privacy.QueryMutationRule {
	return privacy.ContextQueryMutationRule(func(ctx context.Context) error {
		v, ok := ViewerFromContext(ctx)
		if !ok {
			return privacy.Deny
		}
		if v.TenantID() == "" {
			return privacy.Deny
		}
		return privacy.Skip
	})
}

// TenantPolicy applies tenant isolation to an entity.
// The entity must have a "tenant_id" field.
func TenantPolicy() velox.Policy {
	return velox.Policy{
		Query: velox.QueryPolicy{
			TenantIsolationRule(),
			// Add filter for tenant_id
			privacy.QueryRuleFunc(func(ctx context.Context, q velox.Query) error {
				v, ok := ViewerFromContext(ctx)
				if !ok {
					return privacy.Skip
				}
				// This would need to be implemented per-entity
				// to add the appropriate WHERE clause
				_ = v.TenantID()
				return privacy.Skip
			}),
		},
		Mutation: velox.MutationPolicy{
			TenantIsolationRule(),
			// Verify tenant_id on mutations
			privacy.MutationRuleFunc(func(ctx context.Context, m velox.Mutation) error {
				v, ok := ViewerFromContext(ctx)
				if !ok {
					return privacy.Deny
				}
				// Check if mutation sets correct tenant_id
				if tenantID, exists := m.Field("tenant_id"); exists {
					if tenantID != v.TenantID() {
						return privacy.Denyf("cannot set tenant_id to %v", tenantID)
					}
				}
				return privacy.Skip
			}),
		},
	}
}

// =============================================================================
// COMBINING POLICIES
// =============================================================================

// CombinePolicies combines multiple policies into one.
func CombinePolicies(policies ...velox.Policy) velox.Policy {
	var queryRules velox.QueryPolicy
	var mutationRules velox.MutationPolicy

	for _, p := range policies {
		queryRules = append(queryRules, p.Query...)
		mutationRules = append(mutationRules, p.Mutation...)
	}

	return velox.Policy{
		Query:    queryRules,
		Mutation: mutationRules,
	}
}

// =============================================================================
// USAGE EXAMPLE
// =============================================================================

func exampleUsage(ctx context.Context, client *velox.Client) {
	// Create a viewer context
	viewer := &SimpleViewer{
		ID:        "user-123",
		Tenant:    "tenant-456",
		UserRoles: []string{"user", "customer"},
	}
	ctx = WithViewer(ctx, viewer)

	// Now all queries/mutations will be subject to privacy rules
	users, err := client.User.Query().All(ctx)
	if err != nil {
		// May fail if privacy rules deny access
		if privacy.IsPrivacyError(err) {
			// Handle privacy denial
		}
	}
	_ = users

	// Admin context
	adminViewer := &SimpleViewer{
		ID:        "admin-1",
		Tenant:    "tenant-456",
		UserRoles: []string{"admin"},
	}
	adminCtx := WithViewer(ctx, adminViewer)

	// Admin can access all data
	allUsers, err := client.User.Query().All(adminCtx)
	if err != nil {
		// Handle error
	}
	_ = allUsers
}

// Placeholder types for example compilation
type velox_placeholder struct{}

var _ = velox_placeholder{}

// Placeholder interfaces (these are in the actual generated code)
type QueryRuleFunc func(context.Context, velox.Query) error
type MutationRuleFunc func(context.Context, velox.Mutation) error
