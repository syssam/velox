package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Task struct{ ent.Schema }

func (Task) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Task) Fields() []ent.Field {
	return []ent.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Bool("active").Default(false),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
	}
}

func (Task) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("tokens", Token.Type).Annotations(entgql.RelayConnection()),
		edge.To("sessions", Session.Type).Annotations(entgql.RelayConnection()),
		edge.To("webhook_links", Webhook.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Task) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
