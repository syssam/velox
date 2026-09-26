package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type InvoiceItem struct{ velox.Schema }

func (InvoiceItem) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (InvoiceItem) Fields() []velox.Field {
	return []velox.Field{
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.Bool("active").Default(false),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
	}
}

func (InvoiceItem) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("reviews", Review.Type).Annotations(graphql.RelayConnection()),
		edge.To("invoices", Invoice.Type).Annotations(graphql.RelayConnection()),
		edge.To("discount_links", Discount.Type).Annotations(graphql.RelayConnection()),
	}
}

func (InvoiceItem) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
