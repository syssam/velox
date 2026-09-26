package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Story struct{ velox.Schema }

func (Story) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Story) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Text("description").Optional().Nillable(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Text("notes").Optional().Nillable(),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
	}
}

func (Story) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("folders", Folder.Type).Annotations(graphql.RelayConnection()),
		edge.To("shipments", Shipment.Type).Annotations(graphql.RelayConnection()),
		edge.To("auditlog_links", AuditLog.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Story) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
