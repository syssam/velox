package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Notification struct{ velox.Schema }

func (Notification) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Notification) Fields() []velox.Field {
	return []velox.Field{
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
	}
}

func (Notification) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("features", Feature.Type).Annotations(graphql.RelayConnection()),
		edge.To("orders", Order.Type).Annotations(graphql.RelayConnection()),
		edge.To("permission_links", Permission.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Notification) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
