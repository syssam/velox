package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type InvoiceItem struct{ ent.Schema }

func (InvoiceItem) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (InvoiceItem) Fields() []ent.Field {
	return []ent.Field{
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.Bool("active").Default(false),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("quantity").Default(0).Annotations(entgql.OrderField("QUANTITY")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(entgql.OrderField("STATUS")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
	}
}

func (InvoiceItem) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("reviews", Review.Type).Annotations(entgql.RelayConnection()),
		edge.To("invoices", Invoice.Type).Annotations(entgql.RelayConnection()),
		edge.To("discount_links", Discount.Type).Annotations(entgql.RelayConnection()),
	}
}

func (InvoiceItem) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
