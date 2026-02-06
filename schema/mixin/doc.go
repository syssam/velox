// Package mixin provides reusable schema components for Velox ORM.
//
// Mixins allow sharing common fields, edges, hooks, and policies across
// multiple entity schemas. This promotes code reuse and consistency.
//
// # Built-in Mixins
//
// The package provides several ready-to-use mixins:
//
//	// ID mixin: Adds auto-incrementing integer ID
//	mixin.ID{}
//
//	// Time mixin: Adds created_at and updated_at timestamps
//	mixin.Time{}
//
//	// SoftDelete mixin: Adds deleted_at for soft deletes
//	mixin.SoftDelete{}
//
//	// TenantID mixin: Adds tenant_id for multi-tenancy
//	mixin.TenantID{}
//
//	// TimeSoftDelete: Combines Time and SoftDelete
//	mixin.TimeSoftDelete{}
//
// # Using Mixins
//
// Mixins are applied to schemas via the Mixin() method:
//
//	type User struct{ velox.Schema }
//
//	func (User) Mixin() []velox.Mixin {
//	    return []velox.Mixin{
//	        mixin.ID{},
//	        mixin.Time{},
//	    }
//	}
//
// The resulting User entity will have:
//   - id (int64, auto-increment, primary key)
//   - created_at (time.Time, immutable)
//   - updated_at (time.Time, auto-updated)
//
// # Creating Custom Mixins
//
// Custom mixins implement the velox.Mixin interface:
//
//	type AuditMixin struct {
//	    velox.Mixin
//	}
//
//	func (AuditMixin) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.String("created_by"),
//	        field.String("updated_by").Optional(),
//	    }
//	}
//
//	func (AuditMixin) Hooks() []velox.Hook {
//	    return []velox.Hook{
//	        // Hook to set created_by/updated_by from context
//	    }
//	}
//
// # Mixin Order
//
// Mixins are applied in the order they are listed. Later mixins can
// override fields from earlier mixins if they have the same name.
//
//	func (User) Mixin() []velox.Mixin {
//	    return []velox.Mixin{
//	        BaseMixin{},      // Applied first
//	        AuditMixin{},     // Applied second
//	        TenantMixin{},    // Applied third
//	    }
//	}
//
// # Mixin Features
//
// Mixins can provide:
//
//   - Fields: Common fields shared across entities
//   - Edges: Common relationships
//   - Indexes: Common database indexes
//   - Hooks: Mutation hooks (before/after create, update, delete)
//   - Interceptors: Query interceptors
//   - Policy: Privacy/authorization rules
//   - Annotations: Custom annotations for generators
//
// # ID Mixin
//
// The ID mixin adds a standard integer primary key:
//
//	type ID struct{ velox.Mixin }
//
//	func (ID) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.Int64("id").
//	            Unique().
//	            Immutable(),
//	    }
//	}
//
// # Time Mixin
//
// The Time mixin adds timestamp tracking:
//
//	type Time struct{ velox.Mixin }
//
//	func (Time) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.Time("created_at").
//	            Default(time.Now).
//	            Immutable(),
//	        field.Time("updated_at").
//	            Default(time.Now).
//	            UpdateDefault(time.Now),
//	    }
//	}
//
// # SoftDelete Mixin
//
// The SoftDelete mixin enables soft deletion:
//
//	type SoftDelete struct{ velox.Mixin }
//
//	func (SoftDelete) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.Time("deleted_at").
//	            Optional().
//	            Nillable(),
//	    }
//	}
//
// # TenantID Mixin
//
// The TenantID mixin enables multi-tenant isolation:
//
//	type TenantID struct{ velox.Mixin }
//
//	func (TenantID) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.String("tenant_id").
//	            Immutable(),
//	    }
//	}
//
//	func (TenantID) Policy() velox.Policy {
//	    return policy.Policy(
//	        policy.Query(privacy.TenantRule("tenant_id")),
//	        policy.Mutation(privacy.TenantRule("tenant_id")),
//	    )
//	}
package mixin
