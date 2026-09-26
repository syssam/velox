package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Bug struct{ velox.Schema }

func (Bug) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Bug) Fields() []velox.Field {
	return []velox.Field{
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.Text("notes").Optional().Nillable(),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
	}
}

func (Bug) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("tokens", Token.Type).Annotations(graphql.RelayConnection()),
		edge.To("releases", Release.Type).Annotations(graphql.RelayConnection()),
		edge.To("invoiceitem_links", InvoiceItem.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Bug) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
