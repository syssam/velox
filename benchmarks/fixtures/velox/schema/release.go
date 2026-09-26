package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Release struct{ velox.Schema }

func (Release) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Release) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.Text("description").Optional().Nillable(),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
	}
}

func (Release) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("invoiceitems", InvoiceItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("comments", Comment.Type).Annotations(graphql.RelayConnection()),
		edge.To("message_links", Message.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Release) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
