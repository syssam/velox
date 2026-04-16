package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Discount struct{ ent.Schema }

func (Discount) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Discount) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").MaxLen(255).Annotations(entgql.OrderField("EMAIL")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
	}
}

func (Discount) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("settings", Setting.Type).Annotations(entgql.RelayConnection()),
		edge.To("customers", Customer.Type).Annotations(entgql.RelayConnection()),
		edge.To("apikey_links", ApiKey.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Discount) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
