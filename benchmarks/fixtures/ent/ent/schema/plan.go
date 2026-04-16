package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Plan struct{ ent.Schema }

func (Plan) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Plan) Fields() []ent.Field {
	return []ent.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Text("description").Optional().Nillable(),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
	}
}

func (Plan) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("sprints", Sprint.Type).Annotations(entgql.RelayConnection()),
		edge.To("labels", Label.Type).Annotations(entgql.RelayConnection()),
		edge.To("discount_links", Discount.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Plan) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
