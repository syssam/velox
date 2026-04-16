package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Category struct{ ent.Schema }

func (Category) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Category) Fields() []ent.Field {
	return []ent.Field{
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.Text("description").Optional().Nillable(),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Text("notes").Optional().Nillable(),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
	}
}

func (Category) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("features", Feature.Type).Annotations(entgql.RelayConnection()),
		edge.To("products", Product.Type).Annotations(entgql.RelayConnection()),
		edge.To("deployment_links", Deployment.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Category) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
