package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Order struct{ ent.Schema }

func (Order) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Order) Fields() []ent.Field {
	return []ent.Field{
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.Text("description").Optional().Nillable(),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.Bool("active").Default(false),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
	}
}

func (Order) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("storys", Story.Type).Annotations(entgql.RelayConnection()),
		edge.To("configs", AppConfig.Type).Annotations(entgql.RelayConnection()),
		edge.To("post_links", Post.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Order) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
