package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Attachment struct{ velox.Schema }

func (Attachment) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Attachment) Fields() []velox.Field {
	return []velox.Field{
		field.Text("notes").Optional().Nillable(),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.String("url").MaxLen(500).Optional().Nillable().Annotations(graphql.OrderField("URL")),
		field.Bool("active").Default(false),
		field.Text("description").Optional().Nillable(),
	}
}

func (Attachment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("environments", Environment.Type).Annotations(graphql.RelayConnection()),
		edge.To("payments", Payment.Type).Annotations(graphql.RelayConnection()),
		edge.To("subscription_links", Subscription.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Attachment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
