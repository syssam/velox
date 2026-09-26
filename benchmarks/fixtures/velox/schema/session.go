package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Session struct{ velox.Schema }

func (Session) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Session) Fields() []velox.Field {
	return []velox.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.Bool("active").Default(false),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
	}
}

func (Session) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("coupons", Coupon.Type).Annotations(graphql.RelayConnection()),
		edge.To("environments", Environment.Type).Annotations(graphql.RelayConnection()),
		edge.To("team_links", Team.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Session) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
