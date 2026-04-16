package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Message struct{ ent.Schema }

func (Message) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Message) Fields() []ent.Field {
	return []ent.Field{
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Bool("active").Default(false),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
	}
}

func (Message) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("labels", Label.Type).Annotations(entgql.RelayConnection()),
		edge.To("releases", Release.Type).Annotations(entgql.RelayConnection()),
		edge.To("shipmentitem_links", ShipmentItem.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Message) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
