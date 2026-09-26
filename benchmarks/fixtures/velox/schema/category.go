package schema

import (
	"github.com/syssam/velox/contrib/graphql"
	"github.com/syssam/velox"
	"github.com/syssam/velox/schema"
	"github.com/syssam/velox/schema/edge"
	"github.com/syssam/velox/schema/field"
	"github.com/syssam/velox/schema/mixin"
)

type Category struct{ velox.Schema }

func (Category) Mixin() []velox.Mixin { return []velox.Mixin{mixin.Time{}} }

func (Category) Fields() []velox.Field {
	return []velox.Field{
		field.Float("amount").Default(0).Annotations(graphql.OrderField("AMOUNT")),
		field.Int("sort_order").Default(0).Annotations(graphql.OrderField("SORT_ORDER")),
		field.Text("description").Optional().Nillable(),
		field.String("email").MaxLen(255).Annotations(graphql.OrderField("EMAIL")),
		field.Text("notes").Optional().Nillable(),
		field.String("name").NotEmpty().MaxLen(100).Annotations(graphql.OrderField("NAME")),
	}
}

func (Category) Edges() []velox.Edge {
	return []velox.Edge{
		edge.To("features", Feature.Type).Annotations(graphql.RelayConnection()),
		edge.To("products", Product.Type).Annotations(graphql.RelayConnection()),
		edge.To("deployment_links", Deployment.Type).Annotations(graphql.RelayConnection()),
	}
}

func (Category) Annotations() []schema.Annotation {
	return []schema.Annotation{
		graphql.RelayConnection(),
		graphql.QueryField(),
		graphql.Mutations(graphql.MutationCreate(), graphql.MutationUpdate()),
	}
}
