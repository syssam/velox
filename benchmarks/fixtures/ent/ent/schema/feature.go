package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Feature struct{ ent.Schema }

func (Feature) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Feature) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.JSON("metadata", map[string]any{}).Optional(),
	}
}

func (Feature) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("sessions", Session.Type).Annotations(entgql.RelayConnection()),
		edge.To("messages", Message.Type).Annotations(entgql.RelayConnection()),
		edge.To("tag_links", Tag.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Feature) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
