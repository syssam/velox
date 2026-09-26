package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Message struct{ velox.Schema }

func (Message) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Message) Fields() []velox.Field {
	return []velox.Field{
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.Bool("active").Default(false),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
	}
}

func (Message) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("labels", Label.Type).Annotations(graphql.RelayConnection()),
		edge.To("releases", Release.Type).Annotations(graphql.RelayConnection()),
		edge.To("shipmentitem_links", ShipmentItem.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Message) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
