package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Review struct{ ent.Schema }

func (Review) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Review) Fields() []ent.Field {
	return []ent.Field{
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Bool("active").Default(false),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.Text("notes").Optional().Nillable(),
	}
}

func (Review) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("webhooks", Webhook.Type).Annotations(entgql.RelayConnection()),
		edge.To("tokens", Token.Type).Annotations(entgql.RelayConnection()),
		edge.To("feature_links", Feature.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Review) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
