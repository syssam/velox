// Package mixin provides common mixin implementations for Velox schemas.
//
// These mixins are OPTIONAL and provided as convenient starting points.
// Users are encouraged to create their own mixins tailored to their needs.
//
// Available mixins:
//   - CreateTime: Adds created_at timestamp field
//   - UpdateTime: Adds updated_at timestamp field
//   - Time: Combines CreateTime and UpdateTime
//   - ID: Adds UUID primary key with auto-generation
//   - SoftDelete: Adds deleted_at field for soft deletion
//   - TenantID: Adds tenant_id field for multi-tenancy
//   - TimeSoftDelete: Combines Time and SoftDelete
//
// Usage:
//
//	import "github.com/syssam/velox/contrib/mixin"
//
//	func (User) Mixin() []velox.Mixin {
//	    return []velox.Mixin{
//	        mixin.Time{},
//	        mixin.SoftDelete{},
//	    }
//	}
//
// Custom mixins:
//
// For project-specific needs, define your own mixins:
//
//	type AuditMixin struct {
//	    mixin.Schema
//	}
//
//	func (AuditMixin) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.String("created_by").Immutable(),
//	        field.String("updated_by"),
//	    }
//	}
package mixin

import (
	"time"

	"github.com/google/uuid"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// CreateTime adds created_at time field.
// The field is immutable and defaults to time.Now.
//
// Generated field:
//
//	created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
type CreateTime struct{ mixin.Schema }

// Fields of the create time mixin.
func (CreateTime) Fields() []velox.Field {
	return []velox.Field{
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
	}
}

// create time mixin must implement `Mixin` interface.
var _ velox.Mixin = (*CreateTime)(nil)

// UpdateTime adds updated_at time field.
// The field updates automatically on every mutation.
//
// Generated field:
//
//	updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
type UpdateTime struct{ mixin.Schema }

// Fields of the update time mixin.
func (UpdateTime) Fields() []velox.Field {
	return []velox.Field{
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// update time mixin must implement `Mixin` interface.
var _ velox.Mixin = (*UpdateTime)(nil)

// Time composes CreateTime and UpdateTime mixins.
// Provides both created_at and updated_at fields.
//
// This is the most common mixin for tracking entity timestamps.
type Time struct{ mixin.Schema }

// Fields of the time mixin.
func (Time) Fields() []velox.Field {
	return append(
		CreateTime{}.Fields(),
		UpdateTime{}.Fields()...,
	)
}

// time mixin must implement `Mixin` interface.
var _ velox.Mixin = (*Time)(nil)

// ID adds a UUID primary key field with auto-generation.
// Uses github.com/google/uuid for UUID generation.
//
// Generated field:
//
//	id UUID NOT NULL PRIMARY KEY DEFAULT gen_random_uuid()
//
// For custom ID types (e.g., Snowflake IDs), create your own mixin:
//
//	type SnowflakeID struct{ mixin.Schema }
//
//	func (SnowflakeID) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.Int64("id").Default(snowflake.Generate).Immutable(),
//	    }
//	}
type ID struct{ mixin.Schema }

// Fields of the ID mixin.
func (ID) Fields() []velox.Field {
	return []velox.Field{
		field.UUID("id", uuid.UUID{}).
			Default(uuid.New).
			Immutable(),
	}
}

// id mixin must implement `Mixin` interface.
var _ velox.Mixin = (*ID)(nil)

// SoftDelete adds a deleted_at field for soft deletion.
// Entities are not physically deleted but marked with a deletion timestamp.
//
// Use privacy policies or interceptors to filter out soft-deleted entities:
//
//	func (User) Policy() velox.Policy {
//	    return policy.Policy{
//	        Query: policy.QueryPolicy{
//	            privacy.FilterSoftDeleted("deleted_at"),
//	        },
//	    }
//	}
//
// Generated field:
//
//	deleted_at TIMESTAMP NULL
type SoftDelete struct{ mixin.Schema }

// Fields of the SoftDelete mixin.
func (SoftDelete) Fields() []velox.Field {
	return []velox.Field{
		field.Time("deleted_at").
			Optional().
			Nillable(),
	}
}

// soft delete mixin must implement `Mixin` interface.
var _ velox.Mixin = (*SoftDelete)(nil)

// TenantID adds a tenant_id field for multi-tenancy support.
// Combined with privacy policies, this enables row-level tenant isolation.
//
// The field is immutable to prevent accidental tenant data leakage.
//
// Usage with privacy policy:
//
//	func (User) Policy() velox.Policy {
//	    return policy.Policy{
//	        Query: policy.QueryPolicy{
//	            privacy.TenantRule("tenant_id"),
//	        },
//	    }
//	}
//
// For different naming conventions, create your own mixin:
//
//	type WorkspaceID struct{ mixin.Schema }
//
//	func (WorkspaceID) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.String("workspace_id").Immutable().NotEmpty(),
//	    }
//	}
type TenantID struct{ mixin.Schema }

// Fields of the TenantID mixin.
func (TenantID) Fields() []velox.Field {
	return []velox.Field{
		field.String("tenant_id").
			Immutable().
			NotEmpty(),
	}
}

// tenant id mixin must implement `Mixin` interface.
var _ velox.Mixin = (*TenantID)(nil)

// TimeSoftDelete composes Time and SoftDelete mixins.
// Provides created_at, updated_at, and deleted_at fields.
//
// This is useful for entities that need full audit trail with soft deletion.
type TimeSoftDelete struct{ mixin.Schema }

// Fields of the TimeSoftDelete mixin.
func (TimeSoftDelete) Fields() []velox.Field {
	return append(
		Time{}.Fields(),
		SoftDelete{}.Fields()...,
	)
}

// time soft delete mixin must implement `Mixin` interface.
var _ velox.Mixin = (*TimeSoftDelete)(nil)
