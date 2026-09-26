package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type AppConfig struct{ velox.Schema }

func (AppConfig) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (AppConfig) Fields() []velox.Field {
	return []velox.Field{
		field.String("code").MaxLen(50).Annotations(graphql.OrderField("CODE")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Text("description").Optional().Nillable(),
		field.Int("quantity").Default(0).Annotations(graphql.OrderField("QUANTITY")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
		field.Enum("priority").Values("low", "medium", "high").Default("medium").Annotations(graphql.OrderField("PRIORITY")),
	}
}

func (AppConfig) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("messages", Message.Type).Annotations(graphql.RelayConnection()),
		edge.To("invoices", Invoice.Type).Annotations(graphql.RelayConnection()),
		edge.To("webhook_links", Webhook.Type).Annotations(graphql.RelayConnection()),
	}
}

func (AppConfig) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
