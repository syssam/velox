package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Task struct{ velox.Schema }

func (Task) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Task) Fields() []velox.Field {
	return []velox.Field{
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Bool("active").Default(false),
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.Float("score").Default(0).Optional().Nillable().Annotations(graphql.OrderField("SCORE")),
	}
}

func (Task) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("tokens", Token.Type).Annotations(graphql.RelayConnection()),
		edge.To("sessions", Session.Type).Annotations(graphql.RelayConnection()),
		edge.To("webhook_links", Webhook.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Task) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
