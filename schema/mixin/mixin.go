// Package mixin provides the base mixin implementation for Velox schemas.
//
// A mixin is a reusable set of fields, edges, indexes, hooks, and policies
// that can be embedded in multiple schema definitions.
//
// Core Components:
//
//   - Schema: Base mixin struct that all mixins should embed
//   - AnnotateFields: Adds annotations to mixin fields
//   - AnnotateEdges: Adds annotations to mixin edges
//
// Creating Custom Mixins:
//
// To create a custom mixin, embed Schema and override the methods you need:
//
//	type AuditMixin struct {
//	    mixin.Schema
//	}
//
//	func (AuditMixin) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.Time("created_at").Default(time.Now).Immutable(),
//	        field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
//	        field.String("created_by").Optional(),
//	        field.String("updated_by").Optional(),
//	    }
//	}
//
//	func (AuditMixin) Indexes() []velox.Index {
//	    return []velox.Index{
//	        index.Fields("created_at"),
//	    }
//	}
//
// Using Mixins:
//
//	func (User) Mixin() []velox.Mixin {
//	    return []velox.Mixin{
//	        AuditMixin{},
//	    }
//	}
//
// Common Mixins:
//
// For common patterns (timestamps, soft delete, tenant ID), see the
// contrib/mixin package which provides optional, ready-to-use mixins:
//
//	import "github.com/syssam/velox/contrib/mixin"
//
//	func (User) Mixin() []velox.Mixin {
//	    return []velox.Mixin{
//	        mixin.Time{},       // created_at, updated_at
//	        mixin.SoftDelete{}, // deleted_at
//	    }
//	}
package mixin

import (
	"time"

	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/field"
)

// Schema is the default implementation for the velox.Mixin interface.
// It should be embedded in all custom mixin definitions.
//
// Example:
//
//	type MyMixin struct {
//	    mixin.Schema
//	}
//
//	func (MyMixin) Fields() []velox.Field {
//	    return []velox.Field{
//	        field.String("custom_field"),
//	    }
//	}
type Schema struct{}

// Fields returns the fields of the mixin.
// Override this method to add custom fields.
func (Schema) Fields() []velox.Field { return nil }

// Edges returns the edges of the mixin.
// Override this method to add custom edges/relationships.
func (Schema) Edges() []velox.Edge { return nil }

// Indexes returns the indexes of the mixin.
// Override this method to add custom database indexes.
func (Schema) Indexes() []velox.Index { return nil }

// Hooks returns the hooks of the mixin.
// Override this method to add mutation lifecycle hooks.
func (Schema) Hooks() []velox.Hook { return nil }

// Interceptors returns the query interceptors of the mixin.
// Override this method to add query middleware.
func (Schema) Interceptors() []velox.Interceptor { return nil }

// Policy returns the privacy policy of the mixin.
// Override this method to add authorization rules.
func (Schema) Policy() velox.Policy { return nil }

// Annotations returns the annotations of the mixin.
// Override this method to add custom annotations for code generators.
func (Schema) Annotations() []schema.Annotation { return nil }

// schema mixin must implement `Mixin` interface.
var _ velox.Mixin = (*Schema)(nil)

// =============================================================================
// Built-in Mixins
// =============================================================================

// Time adds created_at and updated_at timestamp fields to a schema.
// created_at is set automatically on creation and is immutable.
// updated_at is set on creation and updated automatically on each update.
//
// Example:
//
//	func (User) Mixin() []velox.Mixin {
//	    return []velox.Mixin{
//	        mixin.Time{},
//	    }
//	}
type Time struct {
	Schema
}

// Fields returns the time tracking fields.
func (Time) Fields() []velox.Field {
	return []velox.Field{
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment("Timestamp when the entity was created"),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			Comment("Timestamp when the entity was last updated"),
	}
}

// CreateTime adds only created_at timestamp field to a schema.
// Useful when you only need creation tracking without update tracking.
type CreateTime struct {
	Schema
}

// Fields returns the created_at field.
func (CreateTime) Fields() []velox.Field {
	return []velox.Field{
		field.Time("created_at").
			Default(time.Now).
			Immutable().
			Comment("Timestamp when the entity was created"),
	}
}

// UpdateTime adds only updated_at timestamp field to a schema.
// Useful when you only need update tracking without creation tracking.
type UpdateTime struct {
	Schema
}

// Fields returns the updated_at field.
func (UpdateTime) Fields() []velox.Field {
	return []velox.Field{
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now).
			Comment("Timestamp when the entity was last updated"),
	}
}

// SoftDelete adds a deleted_at field for soft deletion support.
// When set, the entity is considered deleted but remains in the database.
//
// Example:
//
//	func (User) Mixin() []velox.Mixin {
//	    return []velox.Mixin{
//	        mixin.SoftDelete{},
//	    }
//	}
type SoftDelete struct {
	Schema
}

// Fields returns the soft delete field.
func (SoftDelete) Fields() []velox.Field {
	return []velox.Field{
		field.Time("deleted_at").
			Optional().
			Nillable().
			Comment("Timestamp when the entity was soft deleted (nil means not deleted)"),
	}
}

// TimeSoftDelete combines Time and SoftDelete mixins.
// Adds created_at, updated_at, and deleted_at fields.
type TimeSoftDelete struct {
	Schema
}

// Fields returns all timestamp and soft delete fields.
func (TimeSoftDelete) Fields() []velox.Field {
	return append(Time{}.Fields(), SoftDelete{}.Fields()...)
}

// AnnotateFields wraps a mixin and adds annotations to all its fields.
// This is useful for applying cross-cutting annotations like GraphQL directives.
//
// Example:
//
//	mixin.AnnotateFields(
//	    MyMixin{},
//	    graphql.Skip(graphql.SkipMutationInput),
//	)
func AnnotateFields(m velox.Mixin, annotations ...schema.Annotation) velox.Mixin {
	return fieldAnnotator{Mixin: m, annotations: annotations}
}

// AnnotateEdges wraps a mixin and adds annotations to all its edges.
// This is useful for applying cross-cutting annotations like GraphQL directives.
//
// Example:
//
//	mixin.AnnotateEdges(
//	    MyMixin{},
//	    graphql.Skip(graphql.SkipAll),
//	)
func AnnotateEdges(m velox.Mixin, annotations ...schema.Annotation) velox.Mixin {
	return edgeAnnotator{Mixin: m, annotations: annotations}
}

type fieldAnnotator struct {
	velox.Mixin
	annotations []schema.Annotation
}

func (a fieldAnnotator) Fields() []velox.Field {
	fields := a.Mixin.Fields()
	for i := range fields {
		desc := fields[i].Descriptor()
		desc.Annotations = append(desc.Annotations, a.annotations...)
	}
	return fields
}

type edgeAnnotator struct {
	velox.Mixin
	annotations []schema.Annotation
}

func (a edgeAnnotator) Edges() []velox.Edge {
	edges := a.Mixin.Edges()
	for i := range edges {
		desc := edges[i].Descriptor()
		desc.Annotations = append(desc.Annotations, a.annotations...)
	}
	return edges
}
