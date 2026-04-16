package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Team struct{ ent.Schema }

func (Team) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Team) Fields() []ent.Field {
	return []ent.Field{
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
	}
}

func (Team) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("files", File.Type).Annotations(entgql.RelayConnection()),
		edge.To("customers", Customer.Type).Annotations(entgql.RelayConnection()),
		edge.To("attachment_links", Attachment.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Team) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
