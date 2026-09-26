package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Webhook struct{ velox.Schema }

func (Webhook) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Webhook) Fields() []velox.Field {
	return []velox.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Text("description").Optional().Nillable(),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
	}
}

func (Webhook) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("orderitems", OrderItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("warehouses", Warehouse.Type).Annotations(graphql.RelayConnection()),
		edge.To("coupon_links", Coupon.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Webhook) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
