package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Session struct{ ent.Schema }

func (Session) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.Bool("active").Default(false),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
	}
}

func (Session) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("coupons", Coupon.Type).Annotations(entgql.RelayConnection()),
		edge.To("environments", Environment.Type).Annotations(entgql.RelayConnection()),
		edge.To("team_links", Team.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Session) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
