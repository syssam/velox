package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Epic struct{ ent.Schema }

func (Epic) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Epic) Fields() []ent.Field {
	return []ent.Field{
		field.Text("notes").Optional().Nillable(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
	}
}

func (Epic) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("warehouses", Warehouse.Type).Annotations(entgql.RelayConnection()),
		edge.To("customers", Customer.Type).Annotations(entgql.RelayConnection()),
		edge.To("task_links", Task.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Epic) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
