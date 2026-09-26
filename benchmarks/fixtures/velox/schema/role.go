package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Role struct{ velox.Schema }

func (Role) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Role) Fields() []velox.Field {
	return []velox.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Text("notes").Optional().Nillable(),
	}
}

func (Role) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("shipments", Shipment.Type).Annotations(graphql.RelayConnection()),
		edge.To("milestones", Milestone.Type).Annotations(graphql.RelayConnection()),
		edge.To("auditlog_links", AuditLog.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Role) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
