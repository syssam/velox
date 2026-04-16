package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Invoice struct{ ent.Schema }

func (Invoice) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Invoice) Fields() []ent.Field {
	return []ent.Field{
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.Bool("active").Default(false),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Text("description").Optional().Nillable(),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
	}
}

func (Invoice) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("projects", Project.Type).Annotations(entgql.RelayConnection()),
		edge.To("attachments", Attachment.Type).Annotations(entgql.RelayConnection()),
		edge.To("shipmentitem_links", ShipmentItem.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Invoice) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
