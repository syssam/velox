package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type ApiKey struct{ ent.Schema }

func (ApiKey) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (ApiKey) Fields() []ent.Field {
	return []ent.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.Text("notes").Optional().Nillable(),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Text("description").Optional().Nillable(),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
	}
}

func (ApiKey) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("features", Feature.Type).Annotations(entgql.RelayConnection()),
		edge.To("bugs", Bug.Type).Annotations(entgql.RelayConnection()),
		edge.To("inventory_links", Inventory.Type).Annotations(entgql.RelayConnection()),
	}
}

func (ApiKey) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
