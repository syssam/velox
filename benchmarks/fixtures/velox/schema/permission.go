package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Permission struct{ velox.Schema }

func (Permission) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Permission) Fields() []velox.Field {
	return []velox.Field{
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Text("description").Optional().Nillable(),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.Text("notes").Optional().Nillable(),
	}
}

func (Permission) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("invoiceitems", InvoiceItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("products", Product.Type).Annotations(graphql.RelayConnection()),
		edge.To("folder_links", Folder.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Permission) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
