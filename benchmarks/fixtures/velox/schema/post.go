package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Post struct{ velox.Schema }

func (Post) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Post) Fields() []velox.Field {
	return []velox.Field{
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
	}
}

func (Post) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("coupons", Coupon.Type).Annotations(graphql.RelayConnection()),
		edge.To("shipmentitems", ShipmentItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("inventory_links", Inventory.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
