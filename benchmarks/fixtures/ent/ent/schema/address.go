package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Address struct{ ent.Schema }

func (Address) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Address) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Text("notes").Optional().Nillable(),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
	}
}

func (Address) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("deployments", Deployment.Type).Annotations(entgql.RelayConnection()),
		edge.To("shipmentitems", ShipmentItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("user_links", User.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Address) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
