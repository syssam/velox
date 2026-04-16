package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Shipment struct{ ent.Schema }

func (Shipment) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Shipment) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Bool("active").Default(false),
		field.Text("description").Optional().Nillable(),
	}
}

func (Shipment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("files", File.Type).Annotations(entgql.RelayConnection()),
		edge.To("configs", AppConfig.Type).Annotations(entgql.RelayConnection()),
		edge.To("inventory_links", Inventory.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Shipment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
