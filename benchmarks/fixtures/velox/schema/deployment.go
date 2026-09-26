package schema

import (
	"github.com/syssam/velox"
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Deployment struct{ velox.Schema }

func (Deployment) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Deployment) Fields() []velox.Field {
	return []velox.Field{
		field.String("title").NotEmpty().MaxLen(200).Annotations(graphql.OrderField("TITLE")),
		field.JSON("metadata", map[string]any{}).Optional(),
		field.Text("notes").Optional().Nillable(),
		field.Text("description").Optional().Nillable(),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
	}
}

func (Deployment) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("invoices", Invoice.Type).Annotations(graphql.RelayConnection()),
		edge.To("releases", Release.Type).Annotations(graphql.RelayConnection()),
		edge.To("subscription_links", Subscription.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Deployment) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
