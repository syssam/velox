package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type OrderItem struct{ ent.Schema }

func (OrderItem) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (OrderItem) Fields() []ent.Field {
	return []ent.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Text("notes").Optional().Nillable(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
	}
}

func (OrderItem) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("releases", Release.Type).Annotations(entgql.RelayConnection()),
		edge.To("invoices", Invoice.Type).Annotations(entgql.RelayConnection()),
		edge.To("subscription_links", Subscription.Type).Annotations(entgql.RelayConnection()),
	}
}

func (OrderItem) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
