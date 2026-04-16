package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type AppConfig struct{ ent.Schema }

func (AppConfig) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (AppConfig) Fields() []ent.Field {
	return []ent.Field{
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Text("description").Optional().Nillable(),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
	}
}

func (AppConfig) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("messages", Message.Type).Annotations(entgql.RelayConnection()),
		edge.To("invoices", Invoice.Type).Annotations(entgql.RelayConnection()),
		edge.To("webhook_links", Webhook.Type).Annotations(entgql.RelayConnection()),
	}
}

func (AppConfig) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
