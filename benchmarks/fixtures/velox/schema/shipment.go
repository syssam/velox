package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Shipment struct{ velox.Schema }

func (Shipment) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Shipment) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Bool("active").Default(false),
		field.Text("description").Optional().Nillable(),
	}
}

func (Shipment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("files", File.Type).Annotations(graphql.RelayConnection()),
		edge.To("configs", AppConfig.Type).Annotations(graphql.RelayConnection()),
		edge.To("inventory_links", Inventory.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Shipment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
