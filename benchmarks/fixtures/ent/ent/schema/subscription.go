package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Subscription struct{ ent.Schema }

func (Subscription) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Subscription) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
	}
}

func (Subscription) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("invoiceitems", InvoiceItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("orderitems", OrderItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("epic_links", Epic.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Subscription) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
