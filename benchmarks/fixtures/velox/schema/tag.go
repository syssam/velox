package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Tag struct{ velox.Schema }

func (Tag) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Tag) Fields() []velox.Field {
	return []velox.Field{
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.Text("description").Optional().Nillable(),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
	}
}

func (Tag) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("orders", Order.Type).Annotations(graphql.RelayConnection()),
		edge.To("sessions", Session.Type).Annotations(graphql.RelayConnection()),
		edge.To("coupon_links", Coupon.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Tag) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
