package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Token struct{ ent.Schema }

func (Token) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Token) Fields() []ent.Field {
	return []ent.Field{
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Text("notes").Optional().Nillable(),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
	}
}

func (Token) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("environments", Environment.Type).Annotations(entgql.RelayConnection()),
		edge.To("permissions", Permission.Type).Annotations(entgql.RelayConnection()),
		edge.To("file_links", File.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Token) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
