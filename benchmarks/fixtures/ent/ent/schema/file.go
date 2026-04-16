package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type File struct{ ent.Schema }

func (File) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (File) Fields() []ent.Field {
	return []ent.Field{
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
	}
}

func (File) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("categorys", Category.Type).Annotations(entgql.RelayConnection()),
		edge.To("comments", Comment.Type).Annotations(entgql.RelayConnection()),
		edge.To("apikey_links", ApiKey.Type).Annotations(entgql.RelayConnection()),
	}
}

func (File) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
