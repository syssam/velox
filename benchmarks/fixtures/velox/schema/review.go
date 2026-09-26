package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Review struct{ velox.Schema }

func (Review) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Review) Fields() []velox.Field {
	return []velox.Field{
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.Bool("active").Default(false),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.Enum("status").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("STATUS")),
		field.Text("notes").Optional().Nillable(),
	}
}

func (Review) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("webhooks", Webhook.Type).Annotations(graphql.RelayConnection()),
		edge.To("tokens", Token.Type).Annotations(graphql.RelayConnection()),
		edge.To("feature_links", Feature.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Review) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
