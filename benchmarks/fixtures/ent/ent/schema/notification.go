package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Notification struct{ ent.Schema }

func (Notification) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Notification) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
	}
}

func (Notification) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("features", Feature.Type).Annotations(entgql.RelayConnection()),
		edge.To("orders", Order.Type).Annotations(entgql.RelayConnection()),
		edge.To("permission_links", Permission.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Notification) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
