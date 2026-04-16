package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Attachment struct{ ent.Schema }

func (Attachment) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Attachment) Fields() []ent.Field {
	return []ent.Field{
		field.Text("notes").Optional().Nillable(),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.Bool("active").Default(false),
		field.Text("description").Optional().Nillable(),
	}
}

func (Attachment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("environments", Environment.Type).Annotations(entgql.RelayConnection()),
		edge.To("payments", Payment.Type).Annotations(entgql.RelayConnection()),
		edge.To("subscription_links", Subscription.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Attachment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
