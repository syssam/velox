package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Product struct{ velox.Schema }

func (Product) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Product) Fields() []velox.Field {
	return []velox.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.Bool("active").Default(false),
		field.Text("notes").Optional().Nillable(),
	}
}

func (Product) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("labels", Label.Type).Annotations(graphql.RelayConnection()),
		edge.To("invoiceitems", InvoiceItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("warehouse_links", Warehouse.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Product) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
