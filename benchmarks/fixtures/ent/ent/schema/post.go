package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Post struct{ ent.Schema }

func (Post) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Post) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
	}
}

func (Post) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("coupons", Coupon.Type).Annotations(entgql.RelayConnection()),
		edge.To("shipmentitems", ShipmentItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("inventory_links", Inventory.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Post) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
