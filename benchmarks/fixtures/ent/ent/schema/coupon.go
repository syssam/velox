package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Coupon struct{ ent.Schema }

func (Coupon) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Coupon) Fields() []ent.Field {
	return []ent.Field{
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Text("notes").Optional().Nillable(),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
	}
}

func (Coupon) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tags", Tag.Type).Annotations(entgql.RelayConnection()),
		edge.To("orders", Order.Type).Annotations(entgql.RelayConnection()),
		edge.To("review_links", Review.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Coupon) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
