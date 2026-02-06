// Package privacy provides privacy layer types and rule implementations for Velox ORM.
//
// The privacy layer enables ORM-level authorization that evaluates before queries and
// mutations reach the database. This allows defining access control policies directly
// in the schema.
//
// # Core Concepts
//
// The privacy layer is built around three main concepts:
//
//   - Policy: A collection of rules that determine access to entities
//   - Rule: A function that returns Allow, Deny, or Skip decisions
//   - Viewer: An interface representing the current user/context
//
// # Defining Policies
//
// Policies are defined on schema types using the Policy() method:
//
//	func (User) Policy() velox.Policy {
//	    return policy.Policy(
//	        policy.Mutation(
//	            privacy.DenyIfNoViewer(),       // Require authentication
//	            privacy.HasRole("admin"),       // Allow admins
//	            privacy.IsOwner("user_id"),     // Allow owners
//	            privacy.AlwaysDenyRule(),       // Deny by default
//	        ),
//	        policy.Query(
//	            privacy.AlwaysAllowQueryRule(), // Allow all queries
//	        ),
//	    )
//	}
//
// # Rule Evaluation
//
// Rules are evaluated in order until one returns a final decision:
//
//   - Allow: Grants access and stops evaluation
//   - Deny: Denies access and stops evaluation
//   - Skip: Continues to the next rule
//
// If all rules return Skip, the default behavior is to deny access.
//
// # Built-in Rules
//
// The package provides several built-in rules:
//
//   - DenyIfNoViewer: Denies if no viewer is present in context
//   - AlwaysAllowRule: Always allows access
//   - AlwaysDenyRule: Always denies access
//   - HasRole: Allows if viewer has the specified role
//   - HasAnyRole: Allows if viewer has any of the specified roles
//   - IsOwner: Allows if viewer owns the entity
//   - TenantRule: Allows if viewer belongs to the same tenant
//
// # Rule Combinators
//
// Rules can be combined using logical operators:
//
//   - And(rules...): All rules must allow
//   - Or(rules...): Any rule must allow
//   - Not(rule): Inverts the rule's decision
//   - Chain(rules...): Chains rules in sequence
//
// # Viewer Interface
//
// The Viewer interface represents the authenticated user:
//
//	type Viewer interface {
//	    GetID() string       // Unique user identifier
//	    GetRoles() []string  // User's roles
//	    GetTenantID() string // Tenant ID for multi-tenancy
//	}
//
// A SimpleViewer implementation is provided for basic use cases:
//
//	viewer := &privacy.SimpleViewer{
//	    UserID:   "user-123",
//	    Roles:    []string{"admin", "user"},
//	    TenantID: "tenant-abc",
//	}
//
// # Context Integration
//
// The viewer is stored in context and retrieved during policy evaluation:
//
//	ctx := privacy.WithViewer(ctx, &privacy.SimpleViewer{
//	    UserID: "user-123",
//	    Roles:  []string{"user"},
//	})
//	users, err := client.User.Query().All(ctx)
//
// # Error Handling
//
// When access is denied, a DenyError is returned containing the reason:
//
//	if err != nil {
//	    if denyErr, ok := err.(*privacy.DenyError); ok {
//	        log.Printf("Access denied: %s", denyErr.Reason)
//	    }
//	}
package privacy
