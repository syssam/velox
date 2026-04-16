package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Customer struct{ ent.Schema }

func (Customer) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Customer) Fields() []ent.Field {
	return []ent.Field{
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
	}
}

func (Customer) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("teams", Team.Type).Annotations(entgql.RelayConnection()),
		edge.To("warehouses", Warehouse.Type).Annotations(entgql.RelayConnection()),
		edge.To("task_links", Task.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Customer) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
