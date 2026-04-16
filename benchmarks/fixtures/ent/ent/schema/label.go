package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Label struct{ ent.Schema }

func (Label) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Label) Fields() []ent.Field {
	return []ent.Field{
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.Bool("active").Default(false),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
	}
}

func (Label) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("projects", Project.Type).Annotations(entgql.RelayConnection()),
		edge.To("comments", Comment.Type).Annotations(entgql.RelayConnection()),
		edge.To("post_links", Post.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Label) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
