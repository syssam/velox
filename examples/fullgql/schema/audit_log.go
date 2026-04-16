package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

// AuditLog holds the schema definition for the AuditLog entity.
// It is an append-only entity for tracking changes.
type AuditLog struct {
	velox.Schema
}

// Mixin of the AuditLog.
func (AuditLog) Mixin() []velox.Mixin {
	return []velox.Mixin{
		mixin.Time{},
	}
}

// Fields of the AuditLog.
func (AuditLog) Fields() []velox.Field {
	return []velox.Field{
		field.String("action").
			NotEmpty().
			MaxLen(50).
			Immutable().
			Annotations(
				graphql.WhereInput(),
				graphql.OrderField("ACTION"),
			),
		field.String("entity_type").
			NotEmpty().
			MaxLen(50).
			Immutable().
			Annotations(
				graphql.WhereInput(),
			),
		field.Int("entity_id").
			Immutable(),
		field.Text("payload").
			Optional().
			Nillable().
			Comment("JSON payload stored as text"),
		field.String("actor").
			Optional().
			Nillable().
			MaxLen(100).
			Annotations(
				graphql.WhereInput(),
			),
	}
}

// Edges of the AuditLog.
func (AuditLog) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("user", User.Type).
			Ref("audit_logs").
			Unique(),
	}
}

// Annotations of the AuditLog.
func (AuditLog) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
	}
}
