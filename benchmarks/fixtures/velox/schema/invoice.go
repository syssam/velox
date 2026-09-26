package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Invoice struct{ velox.Schema }

func (Invoice) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Invoice) Fields() []velox.Field {
	return []velox.Field{
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.Bool("active").Default(false),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Text("description").Optional().Nillable(),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
	}
}

func (Invoice) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("projects", Project.Type).Annotations(graphql.RelayConnection()),
		edge.To("attachments", Attachment.Type).Annotations(graphql.RelayConnection()),
		edge.To("shipmentitem_links", ShipmentItem.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Invoice) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
