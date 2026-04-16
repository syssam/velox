package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type ShipmentItem struct{ ent.Schema }

func (ShipmentItem) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (ShipmentItem) Fields() []ent.Field {
	return []ent.Field{
		field.Text("description").Optional().Nillable(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Bool("active").Default(false),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
	}
}

func (ShipmentItem) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("comments", Comment.Type).Annotations(entgql.RelayConnection()),
		edge.To("plans", Plan.Type).Annotations(entgql.RelayConnection()),
		edge.To("milestone_links", Milestone.Type).Annotations(entgql.RelayConnection()),
	}
}

func (ShipmentItem) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
