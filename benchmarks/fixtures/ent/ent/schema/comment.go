package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Comment struct{ ent.Schema }

func (Comment) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Comment) Fields() []ent.Field {
	return []ent.Field{
		field.Float("amount").Default(0).Annotations(entgql.OrderField("AMOUNT")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("PRIORITY")),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Text("description").Optional().Nillable(),
		field.Bool("active").Default(false),
		field.String("name").NotEmpty().MaxLen(100).Annotations(entgql.OrderField("NAME")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
	}
}

func (Comment) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("warehouses", Warehouse.Type).Annotations(entgql.RelayConnection()),
		edge.To("reviews", Review.Type).Annotations(entgql.RelayConnection()),
		edge.To("coupon_links", Coupon.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Comment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
