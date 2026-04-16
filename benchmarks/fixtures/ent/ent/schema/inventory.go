package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Inventory struct{ ent.Schema }

func (Inventory) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Inventory) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(entgql.OrderField("URL")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(entgql.OrderField("SCORE")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
	}
}

func (Inventory) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("permissions", Permission.Type).Annotations(entgql.RelayConnection()),
		edge.To("products", Product.Type).Annotations(entgql.RelayConnection()),
		edge.To("file_links", File.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Inventory) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
