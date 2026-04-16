package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Environment struct{ ent.Schema }

func (Environment) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Environment) Fields() []ent.Field {
	return []ent.Field{
		field.Text("notes").Optional().Nillable(),
		field.Bool("active").Default(false),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
	}
}

func (Environment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("permissions", Permission.Type).Annotations(entgql.RelayConnection()),
		edge.To("shipments", Shipment.Type).Annotations(entgql.RelayConnection()),
		edge.To("category_links", Category.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Environment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
