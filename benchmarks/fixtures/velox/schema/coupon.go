package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Coupon struct{ velox.Schema }

func (Coupon) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Coupon) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.Text("notes").Optional().Nillable(),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
	}
}

func (Coupon) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("tags", Tag.Type).Annotations(graphql.RelayConnection()),
		edge.To("orders", Order.Type).Annotations(graphql.RelayConnection()),
		edge.To("review_links", Review.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Coupon) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
