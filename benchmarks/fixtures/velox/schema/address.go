package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Address struct{ velox.Schema }

func (Address) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Address) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Text("notes").Optional().Nillable(),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
	}
}

func (Address) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("deployments", Deployment.Type).Annotations(graphql.RelayConnection()),
		edge.To("shipmentitems", ShipmentItem.Type).Annotations(graphql.RelayConnection()),
		edge.To("user_links", User.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Address) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
