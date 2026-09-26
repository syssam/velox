package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Comment struct{ velox.Schema }

func (Comment) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Comment) Fields() []velox.Field {
	return []velox.Field{
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.Text("description").Optional().Nillable(),
		field.Bool("active").Default(false),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
	}
}

func (Comment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("warehouses", Warehouse.Type).Annotations(graphql.RelayConnection()),
		edge.To("reviews", Review.Type).Annotations(graphql.RelayConnection()),
		edge.To("coupon_links", Coupon.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Comment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
