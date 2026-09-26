package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Subscription struct{ velox.Schema }

func (Subscription) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Subscription) Fields() []velox.Field {
	return []velox.Field{
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
	}
}

func (Subscription) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("invoiceitems", InvoiceItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("orderitems", OrderItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("epic_links", Epic.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Subscription) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
