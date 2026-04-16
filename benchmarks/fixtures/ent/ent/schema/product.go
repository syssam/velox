package schema

import (
	"entgo.io/contrib/entgql"
	"entgo.io/ent"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

type Product struct{ ent.Schema }

func (Product) Mixin() []ent.Mixin { return []ent.Mixin{mixin.Time{}} }

func (Product) Fields() []ent.Field {
	return []ent.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("code").MaxLen(50).Annotations(entgql.OrderField("CODE")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(entgql.OrderField("TITLE")),
		field.Int("sort_order").Default(0).Annotations(entgql.OrderField("SORT_ORDER")),
		field.Bool("active").Default(false),
		field.Text("notes").Optional().Nillable(),
	}
}

func (Product) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("labels", Label.Type).Annotations(entgql.RelayConnection()),
		edge.To("invoiceitems", InvoiceItem.Type).Annotations(entgql.RelayConnection()),
		edge.To("warehouse_links", Warehouse.Type).Annotations(entgql.RelayConnection()),
	}
}

func (Product) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entgql.RelayConnection(),
		entgql.QueryField(),
		entgql.Mutations(entgql.MutationCreate(), entgql.MutationUpdate()),
	}
}
