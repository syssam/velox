package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Payment struct{ ent.Schema }

func (Payment) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Payment) Fields() []ent.Field {
	return []ent.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
	}
}

func (Payment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("discounts", Discount.Type).Annotations(entgql.RelayConnection()),
		edge.To("customers", Customer.Type).Annotations(entgql.RelayConnection()),
		edge.To("token_links", Token.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Payment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
