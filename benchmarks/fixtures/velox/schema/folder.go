package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Folder struct{ velox.Schema }

func (Folder) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Folder) Fields() []velox.Field {
	return []velox.Field{
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.Bool("active").Default(false),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Text("notes").Optional().Nillable(),
		field.Text("description").Optional().Nillable(),
	}
}

func (Folder) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("invoiceitems", InvoiceItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("shipmentitems", ShipmentItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("label_links", Label.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Folder) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
