package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Webhook struct{ ent.Schema }

func (Webhook) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Webhook) Fields() []ent.Field {
	return []ent.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Text("description").Optional().Nillable(),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
	}
}

func (Webhook) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("orderitems", OrderItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("warehouses", Warehouse.Type).Annotations(entgql.RelayConnection()),
		edge.To("coupon_links", Coupon.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Webhook) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
