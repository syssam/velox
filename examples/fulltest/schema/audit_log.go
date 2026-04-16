package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type AuditLog struct{ velox.Schema }

func (AuditLog) Mixin() []velox.Mixin {
	return []velox.Mixin{mixin.Time{}}
}

func (AuditLog) Fields() []velox.Field {
	return []velox.Field{
		field.String("action").
			NotEmpty().
			MaxLen(50).
			Immutable(),
		field.String("entity_type").
			NotEmpty().
			MaxLen(50).
			Immutable(),
		field.Int("entity_id").
			Immutable(),
		field.Text("payload").
			Optional().
			Nillable(),
		field.String("actor").
			Optional().
			Nillable().
			MaxLen(100),
	}
}

func (AuditLog) Edges() []velox.Edge {
	return []velox.Edge{
		edge.From("user", User.Type).
			Ref("audit_logs").
			Unique(),
	}
}

func (AuditLog) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.QueryField(),
		graphql.RelayConnection(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
