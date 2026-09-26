package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Setting struct{ velox.Schema }

func (Setting) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Setting) Fields() []velox.Field {
	return []velox.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
	}
}

func (Setting) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("sprints", Sprint.Type).Annotations(graphql.RelayConnection()),
		edge.To("permissions", Permission.Type).Annotations(graphql.RelayConnection()),
		edge.To("webhook_links", Webhook.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Setting) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
