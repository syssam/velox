package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Payment struct{ velox.Schema }

func (Payment) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Payment) Fields() []velox.Field {
	return []velox.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
	}
}

func (Payment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("discounts", Discount.Type).Annotations(graphql.RelayConnection()),
		edge.To("customers", Customer.Type).Annotations(graphql.RelayConnection()),
		edge.To("token_links", Token.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Payment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
