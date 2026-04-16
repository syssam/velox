package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Setting struct{ ent.Schema }

func (Setting) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Setting) Fields() []ent.Field {
	return []ent.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
	}
}

func (Setting) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("sprints", Sprint.Type).Annotations(entgql.RelayConnection()),
		edge.To("permissions", Permission.Type).Annotations(entgql.RelayConnection()),
		edge.To("webhook_links", Webhook.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Setting) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
