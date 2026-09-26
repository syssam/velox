package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Warehouse struct{ velox.Schema }

func (Warehouse) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Warehouse) Fields() []velox.Field {
	return []velox.Field{
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.Text("notes").Optional().Nillable(),
		field.Bool("active").Default(false),
	}
}

func (Warehouse) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("attachments", Attachment.Type).Annotations(graphql.RelayConnection()),
		edge.To("products", Product.Type).Annotations(graphql.RelayConnection()),
		edge.To("label_links", Label.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Warehouse) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
