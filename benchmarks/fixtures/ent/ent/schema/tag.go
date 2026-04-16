package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Tag struct{ ent.Schema }

func (Tag) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Tag) Fields() []ent.Field {
	return []ent.Field{
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.Text("description").Optional().Nillable(),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
	}
}

func (Tag) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("orders", Order.Type).Annotations(entgql.RelayConnection()),
		edge.To("sessions", Session.Type).Annotations(entgql.RelayConnection()),
		edge.To("coupon_links", Coupon.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Tag) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
