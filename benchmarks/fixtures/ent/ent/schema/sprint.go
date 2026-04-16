package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Sprint struct{ ent.Schema }

func (Sprint) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Sprint) Fields() []ent.Field {
	return []ent.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Text("description").Optional().Nillable(),
		field.Bool("active").Default(false),
	}
}

func (Sprint) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("customers", Customer.Type).Annotations(entgql.RelayConnection()),
		edge.To("features", Feature.Type).Annotations(entgql.RelayConnection()),
		edge.To("plan_links", Plan.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Sprint) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
